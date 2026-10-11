package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// BashParams 是 bash 工具参数（spec §6.1）。
type BashParams struct {
	Command    string `json:"command"`
	TimeoutMS  int    `json:"timeout_ms"`
	Session    string `json:"session"`
	IsInput    bool   `json:"is_input"`
	Background bool   `json:"background"`
}

// BashResult 是 bash 执行结果。
type BashResult struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
	Offloaded string `json:"offloaded,omitempty"`
}

// BashExec 是执行后端抽象：Task 16 提供沙箱实现（docker exec + tmux），此处为本地实现。
type BashExec interface {
	Run(ctx context.Context, p BashParams) (BashResult, error)
}

// LocalExec 在本机 bash -c 执行（M0 开发/测试用；生产路径为沙箱执行）。
type LocalExec struct {
	workdir string
	env     []string
}

// NewLocalExec 建本地执行器；env 可为 nil。
func NewLocalExec(workdir string, env []string) *LocalExec {
	return &LocalExec{workdir: workdir, env: env}
}

// Run 见 BashExec。
func (l *LocalExec) Run(ctx context.Context, p BashParams) (BashResult, error) {
	if p.TimeoutMS <= 0 {
		p.TimeoutMS = 120_000
	}
	if p.TimeoutMS > 600_000 {
		p.TimeoutMS = 600_000
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(p.TimeoutMS)*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(cctx, "bash", "-c", p.Command)
	cmd.Dir = l.workdir
	if l.env != nil {
		cmd.Env = append(os.Environ(), l.env...)
	}
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	if cctx.Err() != nil && code == -1 {
		return BashResult{Output: SanitizeOutput(out) + "\n[timeout]", ExitCode: -1}, nil
	}
	return BashResult{Output: SanitizeOutput(out), ExitCode: code}, nil
}

type bashTool struct {
	exec BashExec
}

// NewBash 创建 bash 工具（灾难正则前置于执行；输出经 Capture 截断）。
// 注：审批门由 harness 层 PermissionChecker 统一拦截（tools 包不得反向依赖 harness）。
func NewBash(exec BashExec) Tool { return &bashTool{exec: exec} }

func (t *bashTool) Name() string { return "bash" }

func (t *bashTool) Description() string {
	return `在工作区执行一条 bash 命令。参数：command（必填，单条或 && 连接的小组合）、
timeout_ms（默认 120000，上限 600000）、session（复用持久会话名，留空一次性执行）、
is_input（true=向前一条挂起的交互程序发送输入而非新命令）、background（true=后台运行，
立即返回，用 session 查看进度）。
长输出会自动截断为头尾并给出 .scratch/ 路径，可用 read/grep 取回完整内容。`
}

func (t *bashTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"command":    map[string]any{"type": "string"},
		"timeout_ms": map[string]any{"type": "integer"},
		"session":    map[string]any{"type": "string"},
		"is_input":   map[string]any{"type": "boolean"},
		"background": map[string]any{"type": "boolean"},
	}, "required": []string{"command"}}
}

func (t *bashTool) Flags() Flags { return Flags{Concurrent: false} }

func (t *bashTool) CheckPerms(_ context.Context, input json.RawMessage) error {
	var p BashParams
	if err := json.Unmarshal(input, &p); err != nil {
		return err
	}
	if CatastrophicBash(p.Command) {
		return fmt.Errorf("灾难命令被无条件拒绝（rm -rf / fork 炸弹 / mkfs / dd 写盘设备）")
	}
	return nil
}

func (t *bashTool) Call(ctx context.Context, input json.RawMessage) (string, error) {
	var p BashParams
	if err := json.Unmarshal(input, &p); err != nil {
		return "", err
	}
	if err := t.CheckPerms(ctx, input); err != nil {
		return "", err
	}
	res, err := t.exec.Run(ctx, p)
	if err != nil {
		return "", err
	}
	head, offload := Capture(res.Output, 15000)
	res.Output = head
	if offload != "" {
		res.Offloaded = fmt.Sprintf(".scratch/%d", len(offload))
	}
	b, _ := json.Marshal(res)
	return string(b), nil
}
