package guard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"mtx/internal/roe"
)

func mustJ(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, _ := json.Marshal(v)
	return b
}

func bashInput(t *testing.T, cmd string) json.RawMessage {
	return mustJ(t, map[string]any{"command": cmd})
}

func newTestGuard(t *testing.T, rules []Rule, roeRules roe.Rules) *Guard {
	t.Helper()
	src := NewMemorySource(rules...)
	g := NewGuard(src, func(_ int64) roe.Rules { return roeRules }, nil)
	g.Now = func() time.Time { return time.Now() } // 可覆写
	return g
}

// TestDenyRule 验证 deny 规则命中返回带平台前缀的错误。
func TestDenyRule(t *testing.T) {
	g := newTestGuard(t, []Rule{{Priority: 10, ToolNamePat: "bash", InputPat: "shutdown", Match: "string", Action: "deny"}}, roe.Rules{Mode: "audit"})
	_, err := g.PreToolUse(context.Background(), "bash", bashInput(t, "shutdown -h now"))
	if err == nil {
		t.Fatal("deny 规则应拒绝")
	}
	if !strings.Contains(err.Error(), "【MTX 平台管控·非目标防御】") {
		t.Fatalf("错误缺平台前缀: %v", err)
	}
}

// TestCatastrophicFirst 验证灾难正则最先拦截。
func TestCatastrophicFirst(t *testing.T) {
	g := newTestGuard(t, []Rule{{Priority: 1, ToolNamePat: "bash", InputPat: "rm", Match: "string", Action: "allow"}}, roe.Rules{Mode: "audit"})
	if _, err := g.PreToolUse(context.Background(), "bash", bashInput(t, "rm -rf /")); err == nil {
		t.Fatal("灾难命令即使有 allow 规则也应被拒")
	}
}

// TestAskTimeoutDefaultsDeny 验证 ask 超时默认拒绝（1s TTL 注入加速）。
func TestAskTimeoutDefaultsDeny(t *testing.T) {
	g := newTestGuard(t, []Rule{{Priority: 5, ToolNamePat: "bash", InputPat: "nmap", Match: "string", Action: "ask", TTLSecs: 1}}, roe.Rules{Mode: "audit"})
	start := time.Now()
	_, err := g.PreToolUse(context.Background(), "bash", bashInput(t, "nmap -sV 10.0.0.5"))
	if err == nil {
		t.Fatal("ask 超时应拒绝")
	}
	if !strings.Contains(err.Error(), "默认拒绝") {
		t.Fatalf("错误应说明默认拒绝: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Fatalf("过早拒绝（%v），应等到 TTL", elapsed)
	}
}

// TestAskDecideModify 验证人工 modify：替换参数后放行，PreToolUse 返回替换后 input。
func TestAskDecideModify(t *testing.T) {
	g := newTestGuard(t, []Rule{{Priority: 5, ToolNamePat: "bash", InputPat: "nmap", Match: "string", Action: "ask", TTLSecs: 30}}, roe.Rules{Mode: "audit"})

	// 异步决策：创建 pending 后立刻改参放行。
	g.OnPending = func(id int64) {
		go func() {
			repl := mustJ(t, map[string]any{"command": "nmap -sV 10.0.0.99"})
			_ = g.Decide(context.Background(), id, "modify", repl, "admin")
		}()
	}

	out, err := g.PreToolUse(context.Background(), "bash", bashInput(t, "nmap -sV 10.0.0.5"))
	if err != nil {
		t.Fatalf("modify 后应放行: %v", err)
	}
	var p struct{ Command string }
	_ = json.Unmarshal(out, &p)
	if p.Command != "nmap -sV 10.0.0.99" {
		t.Fatalf("参数未被替换: %s", out)
	}
}

// TestAskDecideAllowAndDeny 验证 allow/deny 决策路径。
func TestAskDecideAllowAndDeny(t *testing.T) {
	g := newTestGuard(t, []Rule{{Priority: 5, ToolNamePat: "bash", InputPat: "nmap", Match: "string", Action: "ask", TTLSecs: 30}}, roe.Rules{Mode: "audit"})

	g.OnPending = func(id int64) { go func() { _ = g.Decide(context.Background(), id, "allow", nil, "admin") }() }
	if _, err := g.PreToolUse(context.Background(), "bash", bashInput(t, "nmap -sV 10.0.0.5")); err != nil {
		t.Fatalf("allow 后应放行: %v", err)
	}

	g.OnPending = func(id int64) { go func() { _ = g.Decide(context.Background(), id, "deny", nil, "admin") }() }
	if _, err := g.PreToolUse(context.Background(), "bash", bashInput(t, "nmap -sV 10.0.0.5")); err == nil {
		t.Fatal("deny 后应拒绝")
	}
}

// TestRoEEnforceOverridesDefaultAllow 验证 RoE enforce 拒绝优先于"无规则放行"默认。
func TestRoEEnforceOverridesDefaultAllow(t *testing.T) {
	r := roe.Rules{Mode: "enforce", InScope: []string{"10.0.0.0/8"}}
	g := newTestGuard(t, nil, r)
	if _, err := g.PreToolUse(context.Background(), "bash", bashInput(t, "curl http://172.16.1.1/")); err == nil {
		t.Fatal("RoE enforce 范围外应拒（即使无 intercept 规则）")
	}
	if _, err := g.PreToolUse(context.Background(), "bash", bashInput(t, "curl http://10.0.0.5/")); err != nil {
		t.Fatalf("范围内应放行: %v", err)
	}
}

// TestPostToolUseBlockedCount 验证 PostToolUse 对 waf/blocked 类结果的计数。
func TestPostToolUseBlockedCount(t *testing.T) {
	g := newTestGuard(t, nil, roe.Rules{Mode: "audit"})
	g.PostToolUse(context.Background(), "bash", `{"command":"x"}`, "HTTP 403 forbidden (WAF)")
	g.PostToolUse(context.Background(), "bash", `{"command":"x"}`, "200 OK")
	if n := g.BlockedCount(); n != 1 {
		t.Fatalf("BlockedCount = %d, want 1", n)
	}
}
