package agents

import (
	"time"

	"mtx/internal/graph"
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
	RunSeconds   int
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

	system, _ := renderWorkerPrompt(WorkerPromptConst, WorkerPromptConst, map[string]any{
		"intent_title": intent.Title,
	})

	runSeconds := deps.RunSeconds
	if runSeconds <= 0 {
		runSeconds = 1200
	}
	maxTurns := deps.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 40
	}

	return harness.Options{
		Tools:               reg,
		Perms:               deps.Perms,
		System:              system,
		MaxTurns:            maxTurns,
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
	maxTurns := deps.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 12 // planner 每次唤醒预算小
	}
	return harness.Options{
		Tools:    reg,
		Perms:    nil, // planner 不执行目标流量（无 bash）
		System:   PlannerPromptConst,
		MaxTurns: maxTurns,
	}
}

// AssembleGoals 装配目标分解器（一次性）。
func AssembleGoals(deps CommonDeps, ec ExecContext) harness.Options {
	reg := tools.NewRegistry()
	for _, t := range DomainTools(deps.Graph, ec) {
		reg.Register(t)
	}
	return harness.Options{
		Tools:    reg,
		System:   GoalsPromptConst,
		MaxTurns: 8,
	}
}

// workspaceRoot 是 worker 文件工具根（沙箱工作区；M0 用任务数据目录约定）。
func workspaceRoot(ec ExecContext) string {
	return "/workspace"
}
