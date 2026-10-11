//go:build tmux

package tmux

import (
	"context"
	"strings"
	"testing"
	"time"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m := NewManager(nil)
	t.Cleanup(func() {
		for _, s := range m.Sessions() {
			_ = m.killSession(s)
		}
	})
	return m
}

// TestExecSimple 验证基本执行：输出与 exit code。
// 令其失败的生产改动：完成标记检测失效、输出截取错位。
func TestExecSimple(t *testing.T) {
	m := newTestManager(t)
	res, err := m.Exec(context.Background(), "t_simple", `echo -n ok_123`, 5*time.Second)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !strings.Contains(res.Output, "ok_123") {
		t.Fatalf("输出 = %q", res.Output)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
	if res.InteractivePrompt {
		t.Fatal("不应误报交互提示")
	}
}

// TestExecExitCode 验证非零退出码透传。
func TestExecExitCode(t *testing.T) {
	m := newTestManager(t)
	res, err := m.Exec(context.Background(), "t_exit", `exit 7`, 5*time.Second)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 7 {
		t.Fatalf("exit = %d, want 7", res.ExitCode)
	}
}

// TestInteractivePromptDetectedAndResumed 验证交互挂起检测 + is_input 续话：
// read -p 等密码 → 超时返回 InteractivePrompt → SendInput 注入后下条命令恢复正常。
func TestInteractivePromptDetectedAndResumed(t *testing.T) {
	m := newTestManager(t)
	res, err := m.Exec(context.Background(), "t_inter", `read -p "Password: " pw; echo got=$pw`, 2*time.Second)
	if err == nil && !res.InteractivePrompt {
		t.Fatalf("应检测到交互提示: res=%+v err=%v", res, err)
	}
	if !LooksLikeInteractivePrompt("Password: ") {
		t.Fatal("LooksLikeInteractivePrompt(Password: ) 应为 true")
	}
	if err := m.SendInput("t_inter", "s3cret"); err != nil {
		t.Fatalf("SendInput: %v", err)
	}
	// 注入后程序应消化输入并结束；再执行一条验证会话可用。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res2, err := m.Exec(context.Background(), "t_inter", `echo alive`, 3*time.Second)
		if err == nil && res2.ExitCode == 0 && strings.Contains(res2.Output, "alive") {
			return // 会话恢复正常
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("SendInput 后会话未恢复")
}

// TestSessionStatePersists 验证同名会话复用：环境变量跨命令保留。
func TestSessionStatePersists(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.Exec(context.Background(), "t_state", `export MTXV=hello`, 5*time.Second); err != nil {
		t.Fatalf("第一次 Exec: %v", err)
	}
	res, err := m.Exec(context.Background(), "t_state", `echo $MTXV`, 5*time.Second)
	if err != nil {
		t.Fatalf("第二次 Exec: %v", err)
	}
	if !strings.Contains(res.Output, "hello") {
		t.Fatalf("会话状态丢失: %q", res.Output)
	}
	if !containsStr(m.Sessions(), "t_state") {
		t.Fatalf("Sessions 应含 t_state: %v", m.Sessions())
	}
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
