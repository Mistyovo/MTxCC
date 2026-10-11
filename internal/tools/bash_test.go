package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func runBash(t *testing.T, b Tool, cmd string) (string, int) {
	t.Helper()
	out, err := b.Call(context.Background(), mustJSON(t, map[string]any{"command": cmd}))
	if err != nil {
		return out, -1
	}
	var r struct {
		Output   string `json:"output"`
		ExitCode int    `json:"exit_code"`
	}
	_ = json.Unmarshal([]byte(out), &r)
	return r.Output, r.ExitCode
}

// TestBashLocalExec 验证 LocalExec：echo 输出与 exit code、超时字段默认值。
// 令其失败的生产改动：JSON 信封字段错、exit code 丢失。
func TestBashLocalExec(t *testing.T) {
	b := NewBash(NewLocalExec(t.TempDir(), nil))

	out, code := runBash(t, b, `echo -n hello`)
	if !strings.Contains(out, "hello") || code != 0 {
		t.Fatalf("echo = (%q, %d)", out, code)
	}

	_, code = runBash(t, b, `exit 3`)
	if code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
}

// TestBashCatastrophicRejected 验证灾难命令在执行前被拒（权限链前置）。
func TestBashCatastrophicRejected(t *testing.T) {
	b := NewBash(NewLocalExec(t.TempDir(), nil))
	out, err := b.Call(context.Background(), mustJSON(t, map[string]any{"command": "rm -rf /"}))
	if err == nil && !strings.Contains(out, "灾难") {
		t.Fatalf("灾难命令应被拒: out=%q err=%v", out, err)
	}
}

// TestSanitizeOutput 验证清洗管线：ANSI 剥离 + 重复行压缩（前5+后5+省略计数）。
func TestSanitizeOutput(t *testing.T) {
	// ANSI 颜色序列。
	if got := SanitizeOutput([]byte("\x1b[31m红\x1b[0m")); got != "红" {
		t.Fatalf("ANSI 剥离 = %q", got)
	}
	// nmap 式重复行：15 行相同 → 前 5 + 省略计数 + 后 5。
	lines := make([]string, 15)
	for i := range lines {
		lines[i] = "PORT STATE"
	}
	got := SanitizeOutput([]byte(strings.Join(lines, "\n")))
	if strings.Count(got, "PORT STATE") != 10 {
		t.Fatalf("重复行压缩后应剩 10 行（5+5），实际 %d：%q", strings.Count(got, "PORT STATE"), got)
	}
	if !strings.Contains(got, "省略 5 行") {
		t.Fatalf("缺省略计数: %q", got)
	}
}

// TestCaptureOffload 验证输出三级治理的截断层：>15K 返回 head2K+tail1K+卸载指针。
func TestCaptureOffload(t *testing.T) {
	big := strings.Repeat("A", 30000)
	head, offload := Capture(big, 15000)
	if !strings.Contains(head, "[offloaded to .scratch/") {
		t.Fatalf("缺卸载指针: %q", head)
	}
	if offload == "" || !strings.HasPrefix(offload, big[:100]) {
		t.Fatalf("卸载正文丢失")
	}
	// 小输出原样返回。
	h2, o2 := Capture("short", 15000)
	if h2 != "short" || o2 != "" {
		t.Fatalf("小输出被误卸载: (%q,%q)", h2, o2)
	}
}
