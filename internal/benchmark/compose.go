package benchmark

import (
	"fmt"
	"os/exec"
	"strings"
)

// CmdExec 是命令执行抽象（测试注入 fakeExec；生产 exec.Cmd）。
type CmdExec interface {
	Run(cmd *exec.Cmd) (string, error)
}

// realExec 生产执行器。
type realExec struct{}

// Run 见 CmdExec。
func (realExec) Run(cmd *exec.Cmd) (string, error) {
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%v: %s", err, string(out))
	}
	return string(out), nil
}

// ComposeExecutor 封装题目 docker compose 生命周期（XBOW autobuild 一致形态）。
type ComposeExecutor struct {
	Dir  string // 题目目录（含 docker-compose.yml）
	Exec CmdExec
}

// NewComposeExecutor 建执行器。
func NewComposeExecutor(dir string) *ComposeExecutor {
	return &ComposeExecutor{Dir: dir, Exec: realExec{}}
}

// buildCmd 构建镜像（FLAG 注入——与官方 `make build FLAG=` 一致）。
func (e *ComposeExecutor) buildCmd(flag string) *exec.Cmd {
	cmd := exec.Command("docker", "compose", "build", "--build-arg", "flag="+flag)
	cmd.Dir = e.Dir
	return cmd
}

// upCmd 启动并等待 healthcheck（官方形态）。
func (e *ComposeExecutor) upCmd() *exec.Cmd {
	cmd := exec.Command("docker", "compose", "up", "-d", "--wait")
	cmd.Dir = e.Dir
	return cmd
}

// downCmd 停止并清理。
func (e *ComposeExecutor) downCmd() *exec.Cmd {
	cmd := exec.Command("docker", "compose", "down", "--remove-orphans")
	cmd.Dir = e.Dir
	return cmd
}

// portCmd 探测映射端口（compose port <service> 80）。
func (e *ComposeExecutor) portCmd() *exec.Cmd {
	cmd := exec.Command("docker", "compose", "ps", "--format", "json")
	cmd.Dir = e.Dir
	return cmd
}

// Build 见 buildCmd 的执行版。
func (e *ComposeExecutor) Build(flag string) (string, error) {
	return e.Exec.Run(e.buildCmd(flag))
}

// Up 见 upCmd 的执行版。
func (e *ComposeExecutor) Up() (string, error) {
	return e.Exec.Run(e.upCmd())
}

// Down 见 downCmd 的执行版。
func (e *ComposeExecutor) Down() (string, error) {
	return e.Exec.Run(e.downCmd())
}

// Port 探测首个服务的宿主映射端口。
func (e *ComposeExecutor) Port() (string, error) {
	out, err := e.Exec.Run(e.portCmd())
	if err != nil {
		return "", err
	}
	// 解析 JSON 数组（compose ps --format json）找 PublishedPort。
	// 宽松提取 127.0.0.1:PORT 或 0.0.0.0:PORT 形态。
	for _, seg := range strings.FieldsFunc(out, func(r rune) bool { return r == '"' || r == ' ' || r == '\n' }) {
		if strings.HasPrefix(seg, "0.0.0.0:") || strings.HasPrefix(seg, "127.0.0.1:") {
			parts := strings.Split(seg, ":")
			if len(parts) == 2 && parts[1] != "0" {
				return parts[1], nil
			}
		}
	}
	return "", fmt.Errorf("未找到映射端口: %s", out)
}

// ParsePort 从 compose ps 输出提取端口（纯函数，测试用）。
func ParsePort(psOutput string) string {
	for _, seg := range strings.FieldsFunc(psOutput, func(r rune) bool { return r == '"' || r == ' ' || r == '\n' || r == ',' || r == '{' }) {
		if strings.HasPrefix(seg, "0.0.0.0:") || strings.HasPrefix(seg, "127.0.0.1:") {
			parts := strings.Split(seg, ":")
			if len(parts) == 2 && parts[1] != "0" {
				return parts[1]
			}
		}
	}
	return ""
}
