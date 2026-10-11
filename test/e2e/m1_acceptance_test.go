//go:build docker

package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"mtx/internal/agents"
	"mtx/internal/llm"
	"mtx/internal/skills"
	"mtx/internal/tools"
	"mtx/internal/verify"
	"mtx/internal/vault"
)

// textProv 恒回一段文本的 provider（expert 无工具轮场景）。
type textProv struct {
	mu  sync.Mutex
	sys []string
}

func (p *textProv) Stream(_ context.Context, req llm.Request) (<-chan llm.Event, error) {
	p.mu.Lock()
	p.sys = append(p.sys, req.System)
	p.mu.Unlock()
	ch := make(chan llm.Event, 2)
	ch <- llm.TextDelta{S: "EXPERT_DONE: 可达性确认"}
	ch <- llm.MessageDelta{StopReason: "end_turn"}
	close(ch)
	return ch, nil
}

// TestM1Capabilities M1 装配级验收：技能目录 + expert 委托 + oracle 两态 + vault 掩码。
func TestM1Capabilities(t *testing.T) {
	ctx := context.Background()

	// 1) 技能目录 ≥ 20。
	if n := len(skills.Catalog("../../skills")); n < 20 {
		t.Fatalf("技能数 = %d, want >= 20", n)
	}

	// 2) expert 委托真跑。
	prov := &textProv{}
	deps := agents.CommonDeps{
		Provider: prov, Model: "m", MaxTokens: 4096,
		Bash:     tools.NewLocalExec(t.TempDir(), nil),
		Workroot: t.TempDir(),
	}
	et := agents.ExpertTools(deps, agents.ExecContext{Depth: 0})
	var httpExpert tools.Tool
	for _, tl := range et {
		if tl.Name() == "expert_http" {
			httpExpert = tl
		}
	}
	if httpExpert == nil {
		t.Fatal("expert_http 缺失")
	}
	out, err := httpExpert.Call(ctx, []byte(`{"objective":"探测可达性"}`))
	if err != nil || !strings.Contains(out, "EXPERT_DONE") {
		t.Fatalf("expert: %q %v", out, err)
	}

	// 3) oracle 两态（verified / candidate，永不 refuted）。
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("PROOF_MARKER ok"))
	}))
	defer ts.Close()
	o := verify.NewOracle(verify.RealSleeper())
	v, _ := o.Run(verify.Recipe{Kind: "replay", Steps: []verify.Req{
		{Method: "GET", URL: ts.URL, ExpectBody: "PROOF_MARKER"},
	}})
	if v != verify.Verified {
		t.Fatalf("命中特征应 verified, got %v", v)
	}
	v2, _ := o.Run(verify.Recipe{Kind: "replay", Steps: []verify.Req{
		{Method: "GET", URL: ts.URL, ExpectBody: "不存在的特征"},
	}})
	if v2 != verify.Candidate {
		t.Fatalf("未命中应 candidate（非 refuted）, got %v", v2)
	}

	// 4) vault 双向掩码（Review Focus 3）。
	vt, err := vault.New("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	if err := vt.Put(1, "password", "root", "secret-value-123"); err != nil {
		t.Fatal(err)
	}
	sent := vt.Expand(1, "curl -u admin:${CRED:root} http://10.0.0.1/")
	if !strings.Contains(sent, "secret-value-123") {
		t.Fatal("Expand 未还原")
	}
	masked := vt.Mask(1, "login ok secret-value-123 logged in")
	if strings.Contains(masked, "secret-value-123") || !strings.Contains(masked, "${CRED:root}") {
		t.Fatalf("Mask 泄漏或未替换: %q", masked)
	}
}
