package agents

import (
	"context"
	"strings"
	"testing"

	"mtx/internal/llm"
	"mtx/internal/tools"
)

// TestVerifierHasRetestGate submit_verdict 的 hasRetest=false 时 confirmed 拒（Review Focus 5）。
func TestVerifierHasRetestGate(t *testing.T) {
	verdictTool := newVerifierVerdictTool()

	if err := verdictTool.validate(VerdictIn{Confirmed: true, HasRetest: false}); err == nil {
		t.Fatal("hasRetest=false 的 confirmed 应被拒")
	}
	if err := verdictTool.validate(VerdictIn{Confirmed: true, HasRetest: true}); err != nil {
		t.Fatalf("hasRetest=true 应放行: %v", err)
	}
	if err := verdictTool.validate(VerdictIn{Confirmed: false, HasRetest: false}); err != nil {
		t.Fatalf("否定结论无需 retest: %v", err)
	}
}

// TestVerifierToolSurface verifier 装配：只读 + submit_verdict 单出口、无写回工具。
func TestVerifierToolSurface(t *testing.T) {
	reg := VerifierToolRegistry()
	names := map[string]bool{}
	for _, t := range reg.List() {
		names[t.Name()] = true
	}
	if !names["submit_verdict"] {
		t.Fatal("缺 submit_verdict 单出口")
	}
	for _, banned := range []string{"add_intent", "report_finding", "record_fact", "expert_http"} {
		if names[banned] {
			t.Fatalf("verifier 不应有副作用工具 %s", banned)
		}
	}
}

// TestVerifierRealLoop 真跑一轮：假 LLM 调 bash 复测 + submit_verdict(hasRetest=true)。
func TestVerifierRealLoop(t *testing.T) {
	prov := &fakeProvider{script: [][]llm.Event{
		expertTurn("v1", "bash", `{"command":"echo id=0(root)"}`),
		[]llm.Event{
			llm.ToolUseStart{ID: "v2", Name: "submit_verdict"},
			llm.ToolInputJSON{ID: "v2", Fragment: `{"confirmed":true,"has_retest":true,"notes":"复测成功"}`},
			llamEnd(),
		},
		expertText("验证完成"),
	}}
	out := runVerifierSession(prov, tools.NewLocalExec(t.TempDir(), nil), "RCE /api/exec")
	if !strings.Contains(out, "验证完成") {
		t.Fatalf("verifier 结果: %q", out)
	}
	_ = context.Background()
}
