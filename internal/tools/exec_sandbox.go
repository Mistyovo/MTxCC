package tools

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"time"

	"mtx/internal/sandbox"
	"mtx/internal/tools/tmux"
)

// SandboxExec 把 BashExec 落到任务容器：
//   - 一次性命令：docker exec <id> <shell> -c cmd
//   - 会话命令：宿主 tmux 驱动容器内交互 shell（docker exec -i <id> <shell>），
//     环境/工作目录跨命令持久（Decepticon 模式，spec §6.1）
type SandboxExec struct {
	inst    *sandbox.Instance
	workdir string
	proxy   *ProxyEnv
	mgr     *tmux.Manager

	shellOnce sync.Once
	shell     string // "bash" 或 "sh"（镜像无 bash 时）
}

// NewSandboxExec 建沙箱执行器；proxy 可为 nil。
func NewSandboxExec(inst *sandbox.Instance, workdir string, proxy *ProxyEnv) BashExec {
	s := &SandboxExec{inst: inst, workdir: workdir, proxy: proxy}
	s.mgr = tmux.NewManager(func(session string) []string {
		return []string{"docker", "exec", "-i", "-w", workdir, inst.ID(), s.detectShell()}
	})
	return s
}

// detectShell 探测容器内可用 shell（bash 优先，busybox 类回退 sh），缓存一次。
func (s *SandboxExec) detectShell() string {
	s.shellOnce.Do(func() {
		s.shell = "sh"
		if err := exec.Command("docker", "exec", s.inst.ID(), "which", "bash").Run(); err == nil {
			s.shell = "bash"
		}
	})
	return s.shell
}

// Run 见 BashExec。
func (s *SandboxExec) Run(ctx context.Context, p BashParams) (BashResult, error) {
	if p.TimeoutMS <= 0 {
		p.TimeoutMS = 120_000
	}
	if p.TimeoutMS > 600_000 {
		p.TimeoutMS = 600_000
	}
	timeout := time.Duration(p.TimeoutMS) * time.Millisecond

	if p.Session != "" || p.IsInput {
		return s.runInSession(ctx, p, timeout)
	}

	shell := s.detectShell()
	flag := "-c"
	if shell == "bash" {
		flag = "-lc"
	}
	argv := []string{"exec"}
	// docker exec 不继承宿主环境：代理/CA 变量逐个 -e 透传。
	for _, kv := range s.proxy.Environ() {
		argv = append(argv, "-e", kv)
	}
	argv = append(argv, "-w", s.workdir, s.inst.ID(), shell, flag, p.Command)
	cmd := exec.CommandContext(ctx, "docker", argv...)
	cmd.Env = append(os.Environ(), s.proxy.Environ()...)
	var buf syncBuffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	if ctx.Err() != nil && code == -1 {
		return BashResult{Output: SanitizeOutput(buf.Bytes()) + "\n[timeout]", ExitCode: -1}, nil
	}
	return BashResult{Output: SanitizeOutput(buf.Bytes()), ExitCode: code}, nil
}

// runInSession 在宿主 tmux 驱动的容器交互 shell 中执行（持久状态 / is_input 语义）。
func (s *SandboxExec) runInSession(ctx context.Context, p BashParams, timeout time.Duration) (BashResult, error) {
	if p.IsInput {
		if err := s.mgr.SendInput(p.Session, p.Command); err != nil {
			return BashResult{}, err
		}
		return BashResult{Output: "[input sent]"}, nil
	}
	res, err := s.mgr.Exec(ctx, p.Session, p.Command, timeout)
	if err != nil {
		return BashResult{}, err
	}
	out := res.Output
	if res.InteractivePrompt {
		out += "\n[等待输入：用 is_input=true 发送内容]"
	}
	return BashResult{Output: SanitizeOutput([]byte(out)), ExitCode: res.ExitCode}, nil
}

// syncBuffer 是简单并发安全缓冲。
type syncBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf...)
}
