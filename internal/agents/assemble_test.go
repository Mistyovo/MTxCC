//go:build docker

package agents

import (
	"strings"
	"testing"

	"mtx/internal/graph"
	"mtx/internal/tools"
)

// TestSeedPromptsIdempotent 验证播种幂等：两遍执行行数不变。
func mapsKeys(m map[string]bool) map[string]struct{} {
	out := map[string]struct{}{}
	for k := range m {
		out[k] = struct{}{}
	}
	return out
}

func TestSeedPromptsIdempotent(t *testing.T) {
	ctx, g, _, _ := newDomainTools(t)
	s := g.DB()

	if err := SeedPrompts(ctx, s); err != nil {
		t.Fatalf("第一遍: %v", err)
	}
	var n1 int
	_ = s.QueryRow(ctx, `SELECT count(*) FROM agent_prompts`).Scan(&n1)
	if n1 == 0 {
		t.Fatal("播种后应有 prompt 行")
	}
	if err := SeedPrompts(ctx, s); err != nil {
		t.Fatalf("第二遍: %v", err)
	}
	var n2 int
	_ = s.QueryRow(ctx, `SELECT count(*) FROM agent_prompts`).Scan(&n2)
	if n1 != n2 {
		t.Fatalf("幂等失败: %d vs %d", n1, n2)
	}
}

// TestAssembleWorkerToolSurface 验证 worker 工具面：
// 含 bash/read/write/list + 写回六件套；不含 planner 专属的 search_traces 等越权工具。
func TestAssembleWorkerToolSurface(t *testing.T) {
	_, g, _, ec := newDomainTools(t)
	deps := CommonDeps{
		Graph:       g,
		Bash:        tools.NewLocalExec(t.TempDir(), nil),
		TrafficTools: nil,
		MaxTurns:    40,
	}
	opts := AssembleWorker(deps, ec, graph.Node{ID: 1, Title: "探测 Web 入口"})
	names := map[string]bool{}
	for _, tl := range opts.Tools.List() {
		names[tl.Name()] = true
	}
	for _, want := range []string{"bash", "read", "write", "list", "record_fact", "insert_assets", "report_finding", "prove_goal", "expert_http", "expert_write_poc"} {
		if !names[want] {
			t.Errorf("worker 缺工具 %s（现有 %v）", want, names)
		}
	}
	for _, banned := range []string{"search_traces", "get_worker_trace", "add_intent"} {
		if names[banned] {
			t.Errorf("worker 不应有越权工具 %s", banned)
		}
	}
	if !opts.Settlement {
		t.Error("worker 必须开启 Settlement 收尾")
	}
	if !strings.Contains(opts.System, "探测 Web 入口") {
		t.Error("意图应注入 system（不可压缩不变量）")
	}
}

// TestRenderPromptFallback 验证模板渲染失败逐级回退代码常量。
func TestRenderPromptFallback(t *testing.T) {
	// DB 模板里放一个引用不存在变量的模板 → 渲染失败 → 回退常量版本。
	got, usedFallback := renderWorkerPrompt(brokenWorkerTemplate, WorkerPromptConst, map[string]any{"intent_title": "x"})
	if !usedFallback {
		t.Fatal("坏模板应触发回退")
	}
	if !strings.Contains(got, "执行者") {
		t.Fatalf("回退内容应为代码常量: %.80s", got)
	}

	// 好模板正常渲染。
	good := "意图：{{.intent_title}}"
	got, usedFallback = renderWorkerPrompt(good, WorkerPromptConst, map[string]any{"intent_title": "扫端口"})
	if usedFallback || !strings.Contains(got, "扫端口") {
		t.Fatalf("好模板渲染异常: %q fallback=%v", got, usedFallback)
	}
}


// nodeTitle 构造意图节点（测试用）。
func nodeTitle(title string) graph.Node { return graph.Node{Title: title} }
