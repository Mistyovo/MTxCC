package roe_test

import (
	"net"
	"strings"
	"testing"

	"mtx/internal/roe"
	"mtx/internal/sandbox"
)

// 三条绕过向量的端到端断言（计划 Review Focus 1）：
// 解析层与 egress 编译层必须至少其一拦截。

var scopeRules = roe.Rules{
	Mode:       "enforce",
	InScope:    []string{"10.0.0.0/8"},
	OutOfScope: []string{"10.66.0.0/16"},
}

// 向量 1：变量间接（IP=…; nmap $IP）→ 解析层 fail-closed 或提取判定拒绝。
func TestBypassVariableIndirection(t *testing.T) {
	d := roe.EvaluateCommand("IP=8.8.8.8; nmap $IP", scopeRules)
	if d.Allow {
		t.Fatalf("变量间接 8.8.8.8 应被拒: %+v", d)
	}
	// 赋值内联提取的合法目标仍应放行。
	if d := roe.EvaluateCommand("IP=10.0.0.9; nmap $IP", scopeRules); !d.Allow {
		t.Fatalf("scope 内赋值目标误拒: %+v", d)
	}
}

// 向量 2：域名解析到 out-of-scope IP → egress 规则应包含解析出的 IP（deny 段）。
func TestBypassDNSResolutionToOutOfScope(t *testing.T) {
	resolved := map[string][]net.IP{
		"app.example.com": {net.ParseIP("10.66.1.5")}, // 域名在 scope（通配此处简化）但解析到禁区
	}
	nft := sandbox.CompileNft(scopeRules, resolved)
	// deny 优先：10.66.0.0/16 段的 reject 必须先于 10.0.0.0/8 allow，
	// 即解析出的 10.66.1.5 落在 deny 段内（网络层兜底）。
	idxDeny := strings.Index(nft, "10.66.0.0/16")
	idxAllow := strings.Index(nft, "10.0.0.0/8")
	if idxDeny < 0 || idxAllow < 0 || idxDeny > idxAllow {
		t.Fatalf("deny 段必须先于 allow 段:\n%s", nft)
	}
}

// 向量 3：尾点 FQDN（host.）→ 解析层归一后按原域判定。
func TestBypassTrailingDotFQDN(t *testing.T) {
	r := scopeRules
	r.InScope = append(r.InScope, "*.example.com")

	if d := roe.EvaluateCommand("curl http://app.example.com./x", r); !d.Allow {
		t.Fatalf("尾点归一后应命中通配 scope: %+v", d)
	}
	if d := roe.EvaluateCommand("curl http://evil.gov./x", r); d.Allow {
		t.Fatalf("尾点归一后禁 TLD 仍应拒: %+v", d)
	}
}
