package agents

import (
	"testing"

	"mtx/internal/graph"
)

// TestAssembleMaxTokensSet 所有角色装配必须带 MaxTokens>0
//（线上 bug：planner 漏传 → max_tokens=1 → 空轮死锁，M0-C3 同源漏网）。
func TestAssembleMaxTokensSet(t *testing.T) {
	deps := CommonDeps{MaxTokens: 8192}
	ec := ExecContext{ExpID: 1}
	if p := AssemblePlanner(deps, ec); p.MaxTokens <= 0 {
		t.Fatalf("planner MaxTokens = %d, want > 0", p.MaxTokens)
	}
	if w := AssembleWorker(deps, ec, nodeTitle("x")); w.MaxTokens <= 0 {
		t.Fatalf("worker MaxTokens = %d", w.MaxTokens)
	}
	if g := AssembleGoals(deps, ec); g.MaxTokens <= 0 {
		t.Fatalf("goals MaxTokens = %d", g.MaxTokens)
	}
	// deps 未设时也要有默认值（防 max_tokens=1 回归）。
	empty := CommonDeps{}
	if p := AssemblePlanner(empty, ec); p.MaxTokens <= 0 {
		t.Fatalf("默认 MaxTokens 缺失: %d", p.MaxTokens)
	}
}

// nodeTitle 构造意图节点（测试用）。
func nodeTitle(title string) graph.Node { return graph.Node{Title: title} }
