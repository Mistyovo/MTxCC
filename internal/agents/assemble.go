package agents

import (
	"os"
	"time"

	"mtx/internal/graph"
	"mtx/internal/skills"
	"mtx/internal/harness"
	"mtx/internal/tools"
)

// CommonDeps 是装配各角色共享的依赖。
type CommonDeps struct {
	Graph        *graph.GraphStore
	Bash         tools.BashExec        // 沙箱或本地执行器
	TrafficTools []tools.Tool          // 可为 nil
	Perms        harness.PermissionChecker // 审批门，可为 nil
	Provider     interface{}           // llm Provider（引擎接线时断言）
	Transcript   harness.TranscriptStore
	Model        string
	MaxTurns     int
	MaxTokens    int // 单回复输出上限（profile.max_tokens 透传）
	CtxWindowK   int // 上下文窗口（千 token，压缩阈值）
	RunSeconds   int
	Workroot     string // worker/expert 文件工具根目录
	SkillsDir    string // 技能库目录（空则不启用）
	SteerFn      func(intentID int64, msg string) error // planner 纠偏在跑 worker（I6）
	KillFn       func(intentID int64) error             // planner 止损意图（I6）
}

// AssembleWorker 装配执行者（意图进 system 不可压缩；工具面=领域写回+bash+文件+traffic；
// 刻意不给检索类工具——跨意图复盘是规划者职责；Settlement 开启）。
func AssembleWorker(deps CommonDeps, ec ExecContext, intent graph.Node) harness.Options {
	reg := tools.NewRegistry()
	for _, t := range DomainTools(deps.Graph, ec) {
		switch t.Name() {
		case "search_traces", "get_worker_trace", "add_intent":
			continue // 越权工具：规划者专属 / worker 不产新方向
		}
		reg.Register(t)
	}
	reg.Register(tools.NewBash(deps.Bash))
	root := workspaceRoot(ec)
	reg.Register(tools.NewRead(root))
	reg.Register(tools.NewWrite(root))
	reg.Register(tools.NewList(root))
	for _, t := range deps.TrafficTools {
		reg.Register(t)
	}
	for _, t := range ExpertTools(deps, ec) {
		reg.Register(t) // 专家委托（深度 1，Review Focus 1）
	}
	if deps.SkillsDir != "" {
		reg.Register(NewLoadSkillTool(deps.SkillsDir))
	}

	system, _ := renderWorkerPrompt(WorkerPromptConst, WorkerPromptConst, map[string]any{
		"intent_title": intent.Title,
	})
	if deps.SkillsDir != "" {
		system += skills.CatalogDirPrompt(deps.SkillsDir) // 目录常驻（渐进披露）
	}

	runSeconds := deps.RunSeconds
	if runSeconds <= 0 {
		runSeconds = 1200
	}
	maxTurns := deps.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 40
	}

	maxTokens := deps.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	return harness.Options{
		Tools:               reg,
		Perms:               deps.Perms,
		System:              system,
		MaxTurns:            maxTurns,
		MaxTokens:           maxTokens,
		Compactor:           harness.NewCompactor(nil, deps.CtxWindowK),
		MaxDuration:         time.Duration(runSeconds) * time.Second,
		Settlement:          true,
		SettlementKeepTools: []string{"record_fact", "insert_assets", "report_finding", "prove_goal"},
	}
}

// AssemblePlanner 装配规划者（图查询+派意图+判目标+过程控制原语）。
func AssemblePlanner(deps CommonDeps, ec ExecContext) harness.Options {
	reg := tools.NewRegistry()
	for _, t := range DomainTools(deps.Graph, ec) {
		reg.Register(t) // planner 全量：含检索/意图派发
	}
	for _, t := range SteerTools(deps, ec) {
		reg.Register(t) // 过程控制原语（I6：steer_work/kill_work）
	}
	maxTurns := deps.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 12 // planner 每次唤醒预算小
	}
	return harness.Options{
		Tools:     reg,
		Perms:     nil, // planner 不执行目标流量（无 bash）
		System:    PlannerPromptConst,
		MaxTurns:  maxTurns,
		MaxTokens: orDefault(deps.MaxTokens, 8192),
	}
}

// AssembleGoals 装配目标分解器（一次性）。
func AssembleGoals(deps CommonDeps, ec ExecContext) harness.Options {
	reg := tools.NewRegistry()
	for _, t := range DomainTools(deps.Graph, ec) {
		reg.Register(t)
	}
	return harness.Options{
		Tools:     reg,
		System:    GoalsPromptConst,
		MaxTurns:  8,
		MaxTokens: orDefault(deps.MaxTokens, 8192),
	}
}

// workspaceRoot 是 worker 文件工具根（沙箱 /workspace；本地模式走 DataDir）。
func workspaceRoot(ec ExecContext) string {
	if root := os.Getenv("MTX_WORKROOT"); root != "" {
		return root
	}
	return "/workspace"
}
