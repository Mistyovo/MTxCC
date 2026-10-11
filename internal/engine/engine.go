// Package engine 实现 planner/worker 双循环编排（spec §5）：
// worker CAS 领意图执行；意图完成经 notify 唤醒 planner（debounce 合并）；
// 失败三模态分类 + 死亡账本 + steer/kill 过程控制 + Settlement 收尾由 harness 承担。
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"mtx/internal/agents"
	"mtx/internal/graph"
	"mtx/internal/harness"
	"mtx/internal/llm"
	"mtx/internal/store"
	"mtx/internal/tools"
)

// Deps 是引擎依赖。
type Deps struct {
	Store      *store.Store
	Graph      *graph.GraphStore
	Pool       llm.Provider
	Bash       tools.BashExec
	Traffic    []tools.Tool
	Perms      harness.PermissionChecker
	Transcript harness.TranscriptStore
	Model      string
	Workers    int
	Debounce   time.Duration // planner 唤醒合并窗口（默认 800ms；测试调小）
	MaxTokens  int           // profile.max_tokens 透传（C3）
	Workroot   string        // worker/expert 文件根（沙箱 /workspace 或本地 dataDir）
	ActivityFn func(intentID int64, role, kind, payload string) // 事件落库回调，可 nil
}

// Engine 是任务编排引擎。
type Engine struct {
	Deps      Deps
	MaxTurns  int           // worker 每意图预算（0=默认 40；测试调小）
	RetryDelay time.Duration // model_error 重试退避（默认 3s）

	mu       sync.Mutex
	steer    map[int64][]string
	cancels  map[int64]context.CancelFunc
	notify   chan int64
	doneCh   chan struct{}
	expID    int64
	taskID   int64
}

// NewEngine 建引擎。
func NewEngine(d Deps) *Engine {
	if d.Workers <= 0 {
		d.Workers = 3
	}
	if d.Debounce <= 0 {
		d.Debounce = 800 * time.Millisecond
	}
	return &Engine{
		Deps:    d,
		steer:   map[int64][]string{},
		cancels: map[int64]context.CancelFunc{},
		notify:  make(chan int64, 64),
		doneCh:  make(chan struct{}),
	}
}

// Steer 给在跑意图的下一次工具调用前注入纠偏（不 kill 不丢进展，spec §5.3）。
func (e *Engine) Steer(_ context.Context, intentID int64, msg string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.steer[intentID] = append(e.steer[intentID], msg)
	return nil
}

// Kill 止损单个意图（per-work cancel）。
func (e *Engine) Kill(_ context.Context, intentID int64) error {
	e.mu.Lock()
	cancel := e.cancels[intentID]
	e.mu.Unlock()
	if cancel != nil {
		cancel()
		return nil
	}
	return fmt.Errorf("意图 %d 不在运行中", intentID)
}

// steerWrap 把 steer 队列包装成 PermissionChecker（每次工具调用前消费一条）。
type steerHook struct {
	engine   *Engine
	intentID int64
	inner    harness.PermissionChecker
}

func (h *steerHook) PreToolUse(ctx context.Context, tool string, input json.RawMessage) (json.RawMessage, error) {
	h.engine.mu.Lock()
	queue := h.engine.steer[h.intentID]
	if len(queue) > 0 {
		msg := queue[0]
		h.engine.steer[h.intentID] = queue[1:]
		h.engine.mu.Unlock()
		// 纠偏以错误结果形式回给模型（阻塞本次调用，不 kill）。
		return nil, fmt.Errorf("【纠偏】%s（该调用已拦截，请按新指示调整后再继续）", msg)
	}
	h.engine.mu.Unlock()
	if h.inner != nil {
		return h.inner.PreToolUse(ctx, tool, input)
	}
	return input, nil
}

func (h *steerHook) PostToolUse(ctx context.Context, tool string, input, result string) {
	if h.inner != nil {
		h.inner.PostToolUse(ctx, tool, input, result)
	}
}

// Run 起双循环直到任务终态。
func (e *Engine) Run(ctx context.Context, taskID int64) error {
	e.taskID = taskID

	var expID, timeoutSecs int64
	if err := e.Deps.Store.QueryRow(ctx,
		`SELECT e.id, t.timeout_seconds FROM tasks t JOIN explorations e ON e.task_id=t.id
		 WHERE t.id=$1`, taskID).Scan(&expID, &timeoutSecs); err != nil {
		return fmt.Errorf("加载任务: %w", err)
	}
	e.expID = expID
	if timeoutSecs <= 0 {
		timeoutSecs = 7200
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	// deadline/终态协调器：任一路径判定完成后 cancel。
	finish := func(status string) {
		_, _ = e.Deps.Store.Exec(context.WithoutCancel(ctx),
			`UPDATE tasks SET status=$1, finished_at=now() WHERE id=$2`, status, taskID)
		cancel()
	}

	// worker 循环。
	for i := 0; i < e.Deps.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.workerLoop(ctx)
		}()
	}

	// planner 循环。
	wg.Add(1)
	go func() {
		defer wg.Done()
		e.plannerLoop(ctx, finish)
	}()

	// 空转守护：没有 open/running 意图且 planner 无事可做 → 检查终态。
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if e.checkTaskComplete(ctx) {
					status := "done"
					if ctx.Err() != nil {
						status = "timeout"
					}
					finish(status)
					return
				}
			}
		}
	}()

	wg.Wait()
	select {
	case <-e.doneCh:
	default:
		close(e.doneCh)
	}
	return nil
}

// workerLoop 自旋领取并执行意图。
func (e *Engine) workerLoop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		intent, ok := e.claimNext(ctx)
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-time.After(250 * time.Millisecond):
			}
			continue
		}
		e.runIntent(ctx, intent)
	}
}

// claimNext 从 frontier 逐条 CAS 领取。
func (e *Engine) claimNext(ctx context.Context) (graph.Node, bool) {
	list, err := e.Deps.Graph.Frontier(ctx, e.expID, 20)
	if err != nil || len(list) == 0 {
		return graph.Node{}, false
	}
	owner := fmt.Sprintf("w-%d", time.Now().UnixNano()%1e6)
	for _, n := range list {
		ok, err := e.Deps.Store.ClaimIntent(ctx, e.expID, n.ID, owner)
		if err == nil && ok {
			return n, true
		}
	}
	return graph.Node{}, false
}

// runIntent 执行单条意图（含 model_error 重试与失败分类）。
func (e *Engine) runIntent(ctx context.Context, intent graph.Node) {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.mu.Lock()
	e.cancels[intent.ID] = cancel
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.cancels, intent.ID)
		e.mu.Unlock()
	}()

	ec := agents.ExecContext{TaskID: e.taskID, ExpID: e.expID, IntentID: intent.ID, Role: "worker"}
	deps := agents.CommonDeps{
		Graph:      e.Deps.Graph,
		Bash:       e.Deps.Bash,
		TrafficTools: e.Deps.Traffic,
		Perms:      &steerHook{engine: e, intentID: intent.ID, inner: e.Deps.Perms},
		Provider:   e.Deps.Pool,
		Transcript: e.Deps.Transcript,
		Model:      e.Deps.Model,
		MaxTurns:   e.MaxTurns,
		MaxTokens:  e.Deps.MaxTokens,
		Workroot:   e.Deps.Workroot,
	}
	opts := agents.AssembleWorker(deps, ec, intent)
	opts.Provider = e.Deps.Pool
	opts.Transcript = e.Deps.Transcript
	opts.Model = e.Deps.Model
	if e.Deps.ActivityFn != nil {
		af := e.Deps.ActivityFn
		intentID := intent.ID
		opts.OnToolCall = func(tool, input, result string, isErr bool) {
			af(intentID, "worker", "tool_use",
				fmt.Sprintf(`{"tool":%q,"input":%s,"result":%.400q,"is_error":%v}`, tool, input, result, isErr))
		}
	}

	retryDelay := e.RetryDelay
	if retryDelay <= 0 {
		retryDelay = 3 * time.Second
	}
	var res harness.QueryResult
	var qerr error
	for attempt := 0; attempt < 3; attempt++ {
		sess := harness.NewSession(opts, fmt.Sprintf("t%d-w-i%d", e.taskID, intent.ID))
		res, qerr = sess.Query(wctx, opts.System, workerUserMsg(intent))
		if qerr == nil && res.Reason != harness.ReasonModelError {
			break
		}
		if attempt < 2 { // model_error 退避重试 ≤2 次
			select {
			case <-wctx.Done():
				// 被取消（Kill/超时）：同样走 finalize，防意图永卡 running（审查 I5）。
				e.finalizeIntent(ctx, intent, harness.QueryResult{Reason: harness.ReasonAborted}, nil)
				return
			case <-time.After(retryDelay):
			}
		}
	}

	e.finalizeIntent(ctx, intent, res, qerr)
	select {
	case e.notify <- intent.ID:
	default:
	}
}

// finalizeIntent 按失败三模态落意图终态。
func (e *Engine) finalizeIntent(ctx context.Context, intent graph.Node, res harness.QueryResult, qerr error) {
	state := "done"
	switch {
	case ctx.Err() != nil:
		state = "stopped"
	case qerr != nil || res.Reason == harness.ReasonModelError:
		state = "blocked" // INFRA：重试耗尽仍失败
	case res.Reason == harness.ReasonMaxTurns || res.Reason == harness.ReasonTimeout:
		if !e.intentProduced(ctx, intent.ID) {
			// WANDERING：耗尽预算且零写回 → 死账登记，禁止原样重派。
			state = "blocked"
			_ = e.Deps.Graph.MarkTried(ctx, e.expID, "intent", intent.Title, "wandering")
		} else {
			state = "exhausted" // 有产出但撞预算：planner 读 trace 决定续派
		}
	}
	_, _ = e.Deps.Store.Exec(context.WithoutCancel(ctx),
		`UPDATE exploration_nodes SET state=$1, updated_at=now() WHERE id=$2`, state, intent.ID)
	_, _ = e.Deps.Store.Exec(context.WithoutCancel(ctx),
		`INSERT INTO activity (task_id, node_id, agent_role, kind, payload)
		 VALUES ($1,$2,'worker','result',$3::jsonb)`,
		e.taskID, intent.ID,
		fmt.Sprintf(`{"reason":"%s","state":"%s"}`, res.Reason, state))
}

// intentProduced：该意图是否写回过 fact/finding（yields 边存在）。
func (e *Engine) intentProduced(ctx context.Context, intentID int64) bool {
	var n int
	if err := e.Deps.Store.QueryRow(ctx,
		`SELECT count(*) FROM exploration_edges WHERE src=$1 AND kind='yields'`, intentID).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// plannerLoop 事件驱动唤醒（debounce 合并风暴）。
func (e *Engine) plannerLoop(ctx context.Context, finish func(string)) {
	heartbeat := 300 * time.Second
	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			e.planOnce(ctx, finish, "心跳巡检")
		case <-e.notify:
			// debounce：合并窗口内的完成事件。
			timer := time.NewTimer(e.Deps.Debounce)
		collect:
			for {
				select {
				case <-e.notify:
				case <-timer.C:
					break collect
				case <-ctx.Done():
					timer.Stop()
					return
				}
			}
			e.planOnce(ctx, finish, "意图完成")
		}
	}
}

// planOnce 唤醒规划者：态势预取 + TriggerEvent 注入 user。
func (e *Engine) planOnce(ctx context.Context, finish func(string), trigger string) {
	if ctx.Err() != nil {
		return
	}
	ov, err := e.Deps.Graph.GraphOverview(ctx, e.expID)
	if err != nil {
		return
	}
	dead := map[string]int{}
	for k, n := range ov.DeadVectors {
		dead[k.VectorClass+"@"+k.TargetKey] = n
	}

	// 硬底线：无 open goal 且无 open/running 意图 → 任务完成判定交给守护循环。
	user := fmt.Sprintf("【唤醒原因：%s】\n态势：goals=%+v intents=%+v\n最近事实：%s\n死亡向量（禁止原样重派）：%v",
		trigger, ov.Goals, ov.Intents, strings.Join(ov.RecentFacts, "；"), dead)

	ec := agents.ExecContext{TaskID: e.taskID, ExpID: e.expID, Role: "planner"}
	deps := agents.CommonDeps{
		Graph: e.Deps.Graph, Transcript: e.Deps.Transcript, Model: e.Deps.Model,
		MaxTurns: 12,
	}
	deps.MaxTurns = 12
	opts := agents.AssemblePlanner(deps, ec)
	opts.Provider = e.Deps.Pool
	opts.Transcript = e.Deps.Transcript
	opts.Model = e.Deps.Model

	sess := harness.NewSession(opts, fmt.Sprintf("t%d-planner", e.taskID))
	_, _ = sess.Query(ctx, opts.System, user)
}

// checkTaskComplete：全部 goal 终态且无 open/running 意图 → done。
func (e *Engine) checkTaskComplete(ctx context.Context) bool {
	var openGoals, openIntents, totalGoals int
	err := e.Deps.Store.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE kind='goal' AND state='open'),
		count(*) FILTER (WHERE kind='intent' AND state IN ('open','running')),
		count(*) FILTER (WHERE kind='goal')
		FROM exploration_nodes WHERE exploration_id=$1`, e.expID).
		Scan(&openGoals, &openIntents, &totalGoals)
	if err != nil || totalGoals == 0 {
		return false
	}
	return openGoals == 0 && openIntents == 0
}

// workerUserMsg 是 worker 首条 user 输入。
func workerUserMsg(intent graph.Node) string {
	return fmt.Sprintf("开始执行意图：%s", intent.Title)
}

