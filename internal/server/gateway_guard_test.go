package server

import "testing"

// TestGatewayGuardHostPort 防误伤判定必须比 host:port（同 host 不同端口是不同服务）。
func TestGatewayGuardHostPort(t *testing.T) {
	cases := []struct {
		target, gateway string
		wantBlock       bool
	}{
		{"http://127.0.0.1:4280/vuln", "http://127.0.0.1:11434", false}, // 同 host 不同端口 → 放行（DVWA vs llm）
		{"http://127.0.0.1:11434/v1/models", "http://127.0.0.1:11434", true}, // 真网关 → 拦
		{"http://10.0.0.5/", "http://127.0.0.1:11434", false},
		{"http://172.17.0.1:4280/", "https://open.bigmodel.cn/api/anthropic", false},
		{"http://open.bigmodel.cn/", "https://open.bigmodel.cn:8443/api", false}, // 不同端口放行
		{"http://open.bigmodel.cn/", "https://open.bigmodel.cn/api", false},      // 80 vs 443 不同服务 → 放行
		{"https://open.bigmodel.cn/api/v1", "https://open.bigmodel.cn/api", true}, // 同 authority（443）→ 拦
	}
	for _, c := range cases {
		if got := isGatewayTarget(c.target, c.gateway); got != c.wantBlock {
			t.Errorf("isGatewayTarget(%q, %q) = %v, want %v", c.target, c.gateway, got, c.wantBlock)
		}
	}
}
