package tmux

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// exitMarkerRE 匹配完成标记（命令尾自动追加的 echo 结果行）。
var exitMarkerRE = regexp.MustCompile(`__MTX_EXIT_(-?\d+)__`)

// TmuxResult 是一次会话内命令执行的结果。
type TmuxResult struct {
	Output            string
	ExitCode          int
	InteractivePrompt bool // 超时且尾部像交互提示（应改用 SendInput 续话）
	PaneDead          bool // shell 已退出（ExitCode 来自 pane_dead_status）
}

// Manager 管理一批命名持久会话。targetCmd 为会话包装命令（如 docker exec；
// nil 表示本地 bash），由执行后端注入（Task 16）。
type Manager struct {
	targetCmd func(session string) []string
	mu        sync.Mutex
	created   map[string]bool
}

// NewManager 建管理器。
func NewManager(targetCmd func(session string) []string) *Manager {
	return &Manager{targetCmd: targetCmd, created: map[string]bool{}}
}

// sanitizeSession 名只留安全字符并加统一前缀。
func sanitizeSession(name string) string {
	var sb strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			sb.WriteRune(r)
		}
	}
	if sb.Len() == 0 {
		return "mtx_s0"
	}
	return "mtx_" + sb.String()
}

// Exec 在会话内执行命令：确保会话存活 → 发送（命令+完成标记）→
// 轮询标记 / pane 死亡（remain-on-exit 保尸读取退出码）→ 超时则判交互提示。
func (m *Manager) Exec(ctx context.Context, session, cmd string, timeout time.Duration) (TmuxResult, error) {
	sess := sanitizeSession(session)
	if err := m.ensureSession(sess); err != nil {
		return TmuxResult{}, err
	}

	wrapped := fmt.Sprintf("%s; __MTX_EXIT_$?__", cmd)
	if err := m.send(sess, wrapped); err != nil {
		return TmuxResult{}, err
	}

	deadline := time.Now().Add(timeout)
	delay := 40 * time.Millisecond
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return TmuxResult{Output: m.capture(sess)}, ctx.Err()
		case <-time.After(delay):
		}
		if delay < 800*time.Millisecond {
			delay *= 2 // 几何退避
		}
		pane := m.capture(sess)
		if mm := exitMarkerRE.FindStringSubmatch(pane); mm != nil {
			var code int
			_, _ = fmt.Sscanf(mm[1], "%d", &code)
			return TmuxResult{Output: extractOutput(pane), ExitCode: code}, nil
		}
		if dead, code := m.paneStatus(sess); dead {
			return TmuxResult{Output: extractOutput(pane), ExitCode: code, PaneDead: true}, nil
		}
	}
	last := m.capture(sess)
	return TmuxResult{Output: last, InteractivePrompt: LooksLikeInteractivePrompt(last)}, nil
}

// SendInput 向挂起的交互程序发送一行输入（is_input 语义）。
func (m *Manager) SendInput(session, s string) error {
	return m.send(sanitizeSession(session), s)
}

// Sessions 列出本管理器创建的会话名（去前缀）。
func (m *Manager) Sessions() []string {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return nil
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(line, "mtx_") {
			names = append(names, strings.TrimPrefix(line, "mtx_"))
		}
	}
	return names
}

// ensureSession 保证会话存在且 pane 存活（死 pane 先 respawn）。
func (m *Manager) ensureSession(sess string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	exists := exec.Command("tmux", "has-session", "-t", sess).Run() == nil
	if !exists {
		argv := []string{"new-session", "-d", "-s", sess}
		if m.targetCmd != nil {
			argv = append(argv, m.targetCmd(sess)...)
		} else {
			argv = append(argv, "bash")
		}
		if out, err := exec.Command("tmux", argv...).CombinedOutput(); err != nil {
			return fmt.Errorf("建会话 %s: %v: %s", sess, err, strings.TrimSpace(string(out)))
		}
		// shell 退出后 pane 保尸，退出码可查询。
		_ = exec.Command("tmux", "set-option", "-t", sess, "remain-on-exit", "on").Run()
		return nil
	}
	if dead, _ := m.paneStatus(sess); dead {
		_ = exec.Command("tmux", "respawn-pane", "-k", "-t", sess).Run()
		_ = exec.Command("tmux", "set-option", "-t", sess, "remain-on-exit", "on").Run()
	}
	return nil
}

func (m *Manager) send(sess, line string) error {
	if err := exec.Command("tmux", "send-keys", "-t", sess, "-l", line).Run(); err != nil {
		return err
	}
	return exec.Command("tmux", "send-keys", "-t", sess, "Enter").Run()
}

func (m *Manager) capture(sess string) string {
	out, err := exec.Command("tmux", "capture-pane", "-p", "-t", sess, "-S", "-3000").Output()
	if err != nil {
		return ""
	}
	return stripOSC(string(out))
}

// paneStatus 返回 (是否死亡, 退出码)。
func (m *Manager) paneStatus(sess string) (bool, int) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", sess,
		"#{pane_dead} #{pane_dead_status}").Output()
	if err != nil {
		return false, 0
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) >= 1 && fields[0] == "1" {
		code := 0
		if len(fields) >= 2 {
			_, _ = fmt.Sscanf(fields[1], "%d", &code)
		}
		return true, code
	}
	return false, 0
}

func (m *Manager) killSession(name string) error {
	return exec.Command("tmux", "kill-session", "-t", sanitizeSession(name)).Run()
}

// extractOutput 取"命令回显行（含 __MTX_EXIT_$?__ 字面）之后、标记结果行之前"的输出主体。
func extractOutput(pane string) string {
	lines := strings.Split(pane, "\n")
	echoIdx, markIdx := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "__MTX_EXIT_$?__") && echoIdx < 0 {
			echoIdx = i
		}
		if exitMarkerRE.MatchString(l) {
			markIdx = i
		}
	}
	var body []string
	switch {
	case echoIdx >= 0 && markIdx > echoIdx:
		body = lines[echoIdx+1 : markIdx]
	case markIdx >= 0:
		body = lines[:markIdx]
	default:
		body = lines
	}
	// 去尾部空行与提示符残行。
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	return strings.Join(body, "\n")
}

// stripOSC 清掉 OSC/CSI 控制噪声（capture-pane 偶发）。
func stripOSC(s string) string {
	re := regexp.MustCompile(`\x1b\][^\x07]*\x07|\x1b\[[0-9;?]*[a-zA-Z]`)
	return re.ReplaceAllString(s, "")
}
