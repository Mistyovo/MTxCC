package roe

import (
	"net"
	"testing"
	"time"
)

var baseRules = Rules{
	Mode:       "enforce",
	InScope:    []string{"10.0.0.0/8", "192.168.1.0/24", "*.example.com"},
	OutOfScope: []string{"10.255.0.1"},
	ForbiddenTLDs: []string{"gov", "mil"},
	ForbiddenCmdPatterns: []string{`hydra.*-L.*rockyou`},
	MaxConcurrent: 8,
	MinDelayMS:  100,
}

// TestEvaluateTarget 表格驱动（≥14 例）：out_of_scope 优先、禁 TLD、CIDR 数学、
// 通配域、尾点归一、私网白名单语义、默认拒。
func TestEvaluateTarget(t *testing.T) {
	cases := []struct {
		host string
		want bool
		desc string
	}{
		{"10.0.0.5", true, "CIDR 包含 10.0.0.5 ∈ 10/8"},
		{"10.255.0.1", false, "显式 out_of_scope 优先于 in_scope"},
		{"192.168.1.77", true, "另一网段包含"},
		{"192.168.2.1", false, "网段外默认拒"},
		{"app.example.com", true, "通配域包含"},
		{"example.com", false, "裸根不在 *.example.com（白名单须显式）"},
		{"www.example.com.", true, "尾点 FQDN 归一后判定"},
		{"evil.gov", false, "禁 TLD"},
		{"x.gov.cn", false, "禁 TLD（多级后缀）"},
		{"army.mil", false, "禁 TLD mil"},
		{"172.16.0.1", false, "未授权私网默认拒"},
		{"8.8.8.8", false, "公网未授权默认拒"},
		{"10.0.0.256", false, "非法 IP 视为未授权"},
		{"not_a_host", false, "非 IP 非域名默认拒"},
	}
	for _, c := range cases {
		d := EvaluateTarget(c.host, baseRules)
		if d.Allow != c.want {
			t.Errorf("%s: EvaluateTarget(%q) = %v (reason %q), want %v",
				c.desc, c.host, d.Allow, d.Reason, c.want)
		}
	}
}

// TestEvaluateCommand 覆盖命令层：禁令正则、目标提取（curl URL/nmap IP/dig 域名）、
// 无目标 fail-closed。
func TestEvaluateCommand(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
		desc string
	}{
		{"curl http://10.0.0.5/", true, "URL 内 IP 提取"},
		{"nmap -sV 10.0.0.9", true, "nmap 目标位"},
		{"dig app.example.com", true, "dig 域名"},
		{"curl https://evil.gov/x", false, "URL 提取后禁 TLD"},
		{"nmap 10.255.0.1", false, "nmap out_of_scope"},
		{"hydra -L /usr/share/wordlists/rockyou.txt -p x ssh://10.0.0.5", false, "禁令正则优先"},
		{"IP=1.2.3.4; nmap $IP", false, "变量间接提取不到目标 → fail-closed"},
		{"nmap $TARGET", false, "纯变量目标 → fail-closed"},
		{"echo hello", true, "无目标无害命令"},
		{"ls -la", true, "无目标无害命令 2"},
		{"nc -zv app.example.com 443", true, "nc 域名目标"},
		{"curl http://172.16.0.9:8080/", false, "URL 私网未授权"},
		{"sqlmap -u http://10.0.0.5/?id=1 --batch", true, "sqlmap URL"},
		{"ssh admin@10.0.0.5", true, "user@host 形态"},
	}
	for _, c := range cases {
		d := EvaluateCommand(c.cmd, baseRules)
		if d.Allow != c.want {
			t.Errorf("%s: EvaluateCommand(%q) = %v (reason %q), want %v",
				c.desc, c.cmd, d.Allow, d.Reason, c.want)
		}
	}
}

// TestParseRules 验证 JSON 字段与 spec §7.1 逐字段一致。
func TestParseRules(t *testing.T) {
	r, err := ParseRules([]byte(`{
	  "mode": "enforce",
	  "in_scope": ["10.0.0.0/8"],
	  "out_of_scope": ["10.9.9.9"],
	  "forbidden_tlds": ["gov"],
	  "forbidden_command_patterns": ["fork"],
	  "max_concurrent_connections": 4,
	  "min_inter_request_delay_ms": 250,
	  "windows": [{"authorized": ["2026-10-12T09:00/18:00"]}]
	}`))
	if err != nil {
		t.Fatalf("ParseRules: %v", err)
	}
	if r.Mode != "enforce" || len(r.InScope) != 1 || r.OutOfScope[0] != "10.9.9.9" ||
		r.ForbiddenTLDs[0] != "gov" || r.MaxConcurrent != 4 || r.MinDelayMS != 250 || len(r.Windows) != 1 {
		t.Fatalf("解析字段不符: %+v", r)
	}
	if _, err := ParseRules([]byte(`{bad`)); err == nil {
		t.Fatal("坏 JSON 应报错")
	}
}

// TestTimeWindow 验证授权时间窗外拒绝（注入 clock）。
func TestTimeWindow(t *testing.T) {
	r := baseRules
	r.Windows = []Window{{Authorized: []string{"2099-01-01T00:00/01:00"}}}
	Now = func() time.Time { return time.Date(2099, 1, 1, 12, 0, 0, 0, time.UTC) }
	defer func() { Now = time.Now }()

	if d := EvaluateCommand("curl http://10.0.0.5/", r); d.Allow {
		t.Fatalf("时间窗外应拒: %+v", d)
	}
	if d := EvaluateTarget("10.0.0.5", r); d.Allow {
		t.Fatalf("目标评估也应受窗口影响: %+v", d)
	}
}

// TestResolveNet 验证 DNS 解析后逐 IP 判定（注入 resolver）。
func TestResolveNet(t *testing.T) {
	resolver := func(host string) ([]net.IP, error) {
		if host == "app.example.com" {
			return []net.IP{net.ParseIP("10.0.0.5")}, nil
		}
		return []net.IP{net.ParseIP("8.8.8.8")}, nil
	}
	ips, err := ResolveNet(resolver, "app.example.com")
	if err != nil || len(ips) != 1 || ips[0].String() != "10.0.0.5" {
		t.Fatalf("ResolveNet = %v, %v", ips, err)
	}
	// 域名在白名单（通配）→ 允许；若靠解析 IP 判定也应允许。
	if d := EvaluateTarget("app.example.com", baseRules); !d.Allow {
		t.Fatalf("通配域应允许: %+v", d)
	}
}
