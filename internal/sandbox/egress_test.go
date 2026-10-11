package sandbox

import (
	"net"
	"strings"
	"testing"

	"mtx/internal/roe"
)

func sampleRules() roe.Rules {
	return roe.Rules{
		Mode:       "enforce",
		InScope:    []string{"10.0.0.0/8", "app.example.com"},
		OutOfScope: []string{"10.66.0.0/16"},
	}
}

// TestCompileNft 验证 nftables 规则文本：
// 默认 drop 新连接、out_of_scope 先于 allow、scope allow 行、DNS 例外、云元数据禁。
func TestCompileNft(t *testing.T) {
	resolved := map[string][]net.IP{
		"app.example.com": {net.ParseIP("10.1.2.3")},
	}
	txt := CompileNft(sampleRules(), resolved)

	if !strings.Contains(txt, "policy drop") && !strings.Contains(txt, "default drop") && !strings.Contains(txt, "reject") {
		t.Fatalf("缺默认拒绝策略:\n%s", txt)
	}
	// deny 优先：10.66.0.0/16 的 reject 行必须出现在 10.0.0.0/8 的 accept 行之前。
	idxDeny := strings.Index(txt, "10.66.0.0/16")
	idxAllow := strings.Index(txt, "10.0.0.0/8")
	if idxDeny < 0 || idxAllow < 0 {
		t.Fatalf("缺 out_of_scope/scope 行:\n%s", txt)
	}
	if idxDeny > idxAllow {
		t.Fatalf("deny 必须先于 allow:\n%s", txt)
	}
	if !strings.Contains(txt, "10.1.2.3") {
		t.Fatalf("DNS 解析出的 IP 应进 allow:\n%s", txt)
	}
	// DNS 例外（仅网关 DNS）与云元数据禁。
	if !strings.Contains(txt, "53") {
		t.Fatalf("缺 DNS 例外:\n%s", txt)
	}
	for _, meta := range []string{"169.254.169.254"} {
		if !strings.Contains(txt, meta) {
			t.Fatalf("缺云元数据封禁 %s:\n%s", meta, txt)
		}
	}
}

// TestCompileDnsmasq 验证 DNS 白名单生成：scope 域名 server= 转发、其余拒绝。
func TestCompileDnsmasq(t *testing.T) {
	txt := CompileDnsmasq(sampleRules(), "10.0.0.53")
	if !strings.Contains(txt, "server=/app.example.com/10.0.0.53") {
		t.Fatalf("缺域名白名单转发:\n%s", txt)
	}
	if !strings.Contains(txt, "address=/#/") && !strings.Contains(txt, "server=/#/") {
		t.Fatalf("缺默认拒绝解析:\n%s", txt)
	}
}
