package verify

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRecipeFromEvidence 从 finding evidence 自动提取 verification recipe（实测闭环）：
// evidence 含 {kind:"replay", url, expect_body} 条目 → 生成重放 recipe。
func TestRecipeFromEvidence(t *testing.T) {
	evidence := `[
		{"kind":"tool_output","ref":"activity#5","excerpt":"uid=0(root)"},
		{"kind":"replay","url":"http://x/vuln","expect_body":"PROOF_MARKER","expect_status":200}
	]`
	recipes := RecipesFromEvidence(evidence)
	if len(recipes) != 1 {
		t.Fatalf("recipes = %d, want 1", len(recipes))
	}
	r := recipes[0]
	if r.Kind != "replay" || r.Steps[0].URL != "http://x/vuln" || r.Steps[0].ExpectBody != "PROOF_MARKER" {
		t.Fatalf("recipe = %+v", r)
	}
	// 无 replay 条目 → 空。
	if got := RecipesFromEvidence(`[{"kind":"tool_output"}]`); len(got) != 0 {
		t.Fatalf("无 replay 条目应空: %v", got)
	}
}

// TestAutoVerifyFinding 全自动验证流：evidence 带 replay → oracle 跑 → verified/candidate。
func TestAutoVerifyFinding(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("PROOF_MARKER root"))
	}))
	defer ts.Close()

	evidence := `[{"kind":"replay","url":"` + ts.URL + `","expect_body":"PROOF_MARKER"}]`
	v := AutoVerify(NoopSleeper{}, evidence)
	if v != Verified {
		t.Fatalf("命中特征应 verified, got %v", v)
	}
	// 特征不匹配 → candidate（永不 refuted）。
	evidence2 := `[{"kind":"replay","url":"` + ts.URL + `","expect_body":"NOT_PRESENT"}]`
	if v := AutoVerify(NoopSleeper{}, evidence2); v != Candidate {
		t.Fatalf("未命中应 candidate, got %v", v)
	}
	// 无 recipe → candidate（不武断）。
	if v := AutoVerify(NoopSleeper{}, `[]`); v != Candidate {
		t.Fatalf("无 recipe 应 candidate, got %v", v)
	}
	_ = strings.TrimSpace("")
}
