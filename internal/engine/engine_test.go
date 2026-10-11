//go:build docker

package engine

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcp "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"mtx/internal/graph"
	"mtx/internal/harness"
	"mtx/internal/llm"
	"mtx/internal/store"
	"mtx/internal/tools"
)

// scriptProv 脚本化 provider：按会话 system 中的精确角色标记路由脚本。
// 注意：worker prompt 含"规划者"字样（"交给规划者"），必须用带引号的完整标记区分。
type scriptProv struct {
	mu          sync.Mutex
	scripts     map[string][][]llm.Event // key: "worker" | "planner" | "goals"
	callIdx     map[string]int
	reqs        []llm.Request
	workerCalls int
}

func newScriptProv() *scriptProv {
	return &scriptProv{scripts: map[string][][]llm.Event{}, callIdx: map[string]int{}}
}

const (
	// 注意：prompt 常量用 ASCII 双引号包裹角色名（"执行者"），标记必须一致。
	markPlanner = `规划者"（planner）`
	markWorker  = `执行者"（worker`
	markGoals   = "目标分解器"
)

func (p *scriptProv) Stream(_ context.Context, req llm.Request) (<-chan llm.Event, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reqs = append(p.reqs, req)
	key := "planner"
	switch {
	case strings.Contains(req.System, markGoals):
		key = "goals"
	case strings.Contains(req.System, markPlanner):
		key = "planner"
	case strings.Contains(req.System, markWorker):
		key = "worker"
		p.workerCalls++
	}
	idx := p.callIdx[key]
	p.callIdx[key]++
	script := p.scripts[key]
	if idx >= len(script) {
		ch := make(chan llm.Event, 1)
		ch <- &llm.StreamError{Err: errExhausted{}}
		close(ch)
		return ch, nil
	}
	evs := script[idx]
	ch := make(chan llm.Event, len(evs))
	for _, e := range evs {
		ch <- e
	}
	close(ch)
	return ch, nil
}

type errExhausted struct{}

func (errExhausted) Error() string { return "script exhausted" }

func toolCall(id, name, args string) []llm.Event {
	return []llm.Event{
		llm.ToolUseStart{ID: id, Name: name},
		llm.ToolInputJSON{ID: id, Fragment: args},
		llamMessageDelta(),
	}
}

func llamMessageDelta() llm.MessageDelta { return llm.MessageDelta{StopReason: "tool_use"} }

func llamkText(s string) []llm.Event {
	return []llm.Event{llamTextDelta(s), llamMessageDeltaEnd()}
}

func llamTextDelta(s string) llm.TextDelta { return llm.TextDelta{S: s} }

func llamMessageDeltaEnd() llm.MessageDelta { return llm.MessageDelta{StopReason: "end_turn"} }

var _ = json.Marshal

func setupEngine(t *testing.T, prov llm.Provider) (*Engine, *graph.GraphStore, *store.Store, int64, int64) {
	t.Helper()
	ctx := context.Background()
	pg, err := tcp.Run(ctx, "postgres:16-alpine",
		tcp.WithDatabase("mtx"), tcp.WithUsername("mtx"), tcp.WithPassword("mtx"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	dsn, _ := pg.ConnectionString(ctx, "sslmode=disable")
	s, _ := store.Open(ctx, dsn)
	t.Cleanup(s.Close)
	g := graph.NewGraphStore(s)

	var taskID, expID int64
	_ = s.QueryRow(ctx, `INSERT INTO tasks (title,target_text,status,created_at)
		VALUES ('t','10.0.0.5','created',now()) RETURNING id`).Scan(&taskID)
	_ = s.QueryRow(ctx, `INSERT INTO explorations (task_id,created_at)
		VALUES ($1,now()) RETURNING id`, taskID).Scan(&expID)
	_ = s.QueryRow(ctx, `INSERT INTO exploration_nodes (exploration_id,kind,state,title,created_at,updated_at)
		VALUES ($1,'goal','open','拿到立足点',now(),now())`, expID).Scan(new(int64))
	if _, err := g.AddIntent(ctx, expID, nil, "探测 Web 入口", 5, nil); err != nil {
		t.Fatal(err)
	}

	e := NewEngine(Deps{
		Store: s, Graph: g, Pool: prov,
		Bash:       tools.NewLocalExec(t.TempDir(), nil),
		Transcript: harness.NewFileTranscript(t.TempDir()),
		Model:      "test", Workers: 2, Debounce: 10 * time.Millisecond,
	})
	return e, g, s, taskID, expID
}

func waitFor(t *testing.T, ctx context.Context, s *store.Store, expID int64,
	kind, want string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var state string
		_ = s.QueryRow(ctx, `SELECT state FROM exploration_nodes
			WHERE exploration_id=$1 AND kind=$2 ORDER BY id LIMIT 1`, expID, kind).Scan(&state)
		if state == want {
			return state
		}
		time.Sleep(80 * time.Millisecond)
	}
	return ""
}

// TestEngineIntentDoneWakesPlanner 意图完成 → planner 被唤醒、prove_goal 生效、任务 done。
func TestEngineIntentDoneWakesPlanner(t *testing.T) {
	prov := newScriptProv()
	prov.scripts["worker"] = [][]llm.Event{
		toolCall("t1", "record_fact", `{"title":"发现登录页","body":"http://10.0.0.5/login"}`),
		llamkText("意图完成：登录页已登记"),
	}
	plannerTurn := append(toolCall("p1", "prove_goal", `{"goal_id":1,"state":"met","reason":"立足点已取得"}`),
		llamMessageDeltaEnd())
	prov.scripts["planner"] = [][]llm.Event{plannerTurn}

	e, _, s, taskID, _ := setupEngine(t, prov)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() { _ = e.Run(ctx, taskID) }()

	deadline := time.Now().Add(20 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		_ = s.QueryRow(ctx, `SELECT status FROM tasks WHERE id=$1`, taskID).Scan(&status)
		if status == "done" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if status != "done" {
		t.Fatalf("任务终态 = %q, want done", status)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	for _, r := range prov.reqs {
		if !strings.Contains(r.System, markPlanner) {
			continue
		}
		for _, m := range r.Messages {
			for _, b := range m.Blocks {
				if strings.Contains(b.Text, "唤醒原因") {
					return
				}
			}
		}
	}
	t.Fatal("planner 唤醒输入缺 TriggerEvent")
}

// TestEngineWanderingBlockedAndTried bash 空转耗尽预算且零写回 → blocked + tried_ledger。
func TestEngineWanderingBlockedAndTried(t *testing.T) {
	prov := newScriptProv()
	prov.scripts["worker"] = [][]llm.Event{
		toolCall("w1", "bash", `{"command":"echo 想想"}`),
		toolCall("w2", "bash", `{"command":"echo 再想想"}`),
		llamkText("（收尾）无内容可写回"), // Settlement 轮：干净收尾 → 终态 MaxTurns
	}
	prov.scripts["planner"] = [][]llm.Event{llamkText("等待")}

	e, _, s, taskID, expID := setupEngine(t, prov)
	e.MaxTurns = 2
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() { _ = e.Run(ctx, taskID) }()

	if waitFor(t, ctx, s, expID, "intent", "blocked", 20*time.Second) == "" {
		t.Fatal("意图未进入 blocked（WANDERING 分类未生效）")
	}
	var n int
	_ = s.QueryRow(ctx, `SELECT count(*) FROM tried_ledger WHERE exploration_id=$1`, expID).Scan(&n)
	if n == 0 {
		t.Fatal("WANDERING 应登记 tried_ledger")
	}
}

// TestEngineSteerInjectsBeforeNextCall steer 消息在下一工具调用前注入为错误结果。
func TestEngineSteerInjectsBeforeNextCall(t *testing.T) {
	prov := newScriptProv()
	prov.scripts["worker"] = [][]llm.Event{
		// 轮1 慢命令（拉长 running 窗口并让 steer 在其期间入队）；轮2 再调一次 bash 供钩子消费注入；轮3 收尾。
		toolCall("s1", "bash", `{"command":"sleep 2","timeout_ms":5000}`),
		toolCall("s2", "bash", `{"command":"echo done"}`),
		llamkText("收到纠偏，停止"),
	}
	prov.scripts["planner"] = [][]llm.Event{llamkText("等待")}

	e, _, s, taskID, expID := setupEngine(t, prov)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() { _ = e.Run(ctx, taskID) }()

	var intentID int64
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_ = s.QueryRow(ctx, `SELECT id FROM exploration_nodes
			WHERE exploration_id=$1 AND kind='intent' AND state='running' LIMIT 1`, expID).Scan(&intentID)
		if intentID > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if intentID == 0 {
		t.Fatal("意图未进入 running")
	}
	if err := e.Steer(ctx, intentID, "立即停止扫描，改测 SSH"); err != nil {
		t.Fatalf("Steer: %v", err)
	}

	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		prov.mu.Lock()
		done := false
		for _, r := range prov.reqs {
			if !strings.Contains(r.System, markWorker) {
				continue
			}
			for _, m := range r.Messages {
				for _, b := range m.Blocks {
					if b.Type == llm.BlockToolResult && strings.Contains(b.Text, "纠偏") {
						done = true
					}
				}
			}
		}
		prov.mu.Unlock()
		if done {
			return
		}
		time.Sleep(80 * time.Millisecond)
	}
	t.Fatal("未观察到 steer 注入")
}

// TestEngineModelErrorRetriesThenBlocks model_error 重试 2 次后 blocked（共 3 个 worker 会话）。
func TestEngineModelErrorRetriesThenBlocks(t *testing.T) {
	prov := newScriptProv()
	errTurn := []llm.Event{&llm.StreamError{Err: errBoom{}}}
	prov.scripts["worker"] = [][]llm.Event{errTurn, errTurn, errTurn, errTurn, errTurn, errTurn}
	prov.scripts["planner"] = [][]llm.Event{llamkText("等")}

	e, _, s, taskID, expID := setupEngine(t, prov)
	e.RetryDelay = time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() { _ = e.Run(ctx, taskID) }()

	if waitFor(t, ctx, s, expID, "intent", "blocked", 20*time.Second) == "" {
		t.Fatal("model_error 未走到 blocked")
	}
	prov.mu.Lock()
	calls := prov.workerCalls
	prov.mu.Unlock()
	if calls != 3 {
		t.Fatalf("worker 会话数 = %d, want 3（原 1 + 重试 2）", calls)
	}
}

type errBoom struct{}

func (errBoom) Error() string { return "model 500" }
