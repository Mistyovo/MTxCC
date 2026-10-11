package roe

import "testing"

// TestExtractHardening 审查 C2d：命令替换/输入文件/十六进制 IP 不得逃逸目标提取。
func TestExtractHardening(t *testing.T) {
	r := Rules{Mode: "enforce", InScope: []string{"10.0.0.0/8"}}
	cases := []struct {
		cmd  string
		want bool // false = 应被拒
	}{
		{"nmap -iL hosts.txt", false},    // 输入文件形态 → 疑似目标 fail-closed
		{"nmap $(cat hosts.txt)", false}, // 命令替换 → fail-closed
		{"ping 0x0a000101", false},       // 十六进制 IP → fail-closed（无法判定）
		{"curl http://10.0.0.5/", true},  // 合法目标
		{"nmap -sV 10.0.0.9", true},      // 合法目标
		{"echo backup", true},            // 无目标无害
	}
	for _, c := range cases {
		if d := EvaluateCommand(c.cmd, r); d.Allow != c.want {
			t.Errorf("EvaluateCommand(%q) = %v (reason %q), want %v", c.cmd, d.Allow, d.Reason, c.want)
		}
	}
}
