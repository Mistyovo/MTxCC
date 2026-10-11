//go:build docker

package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/client"

	"mtx/internal/sandbox"
)

func newDockerClient() (*client.Client, error) {
	return client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
}

func shortTag() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000)
}

func newSandboxInst(t *testing.T) *sandbox.Instance {
	t.Helper()
	cli, err := newDockerClient()
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	m := sandbox.NewManager(cli)
	dir := t.TempDir()
	inst, err := m.Ensure(context.Background(), sandbox.Spec{
		Image: "busybox:latest", Name: "mtx-t-bash-" + shortTag(), WorkdirVol: dir,
	})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), inst.ID()) })
	return inst
}

// TestSandboxExecEcho 验证容器内执行往返（docker exec -w）。
func TestSandboxExecEcho(t *testing.T) {
	inst := newSandboxInst(t)
	exec := NewSandboxExec(inst, "/workspace", nil)
	out, err := exec.Run(context.Background(), BashParams{Command: `echo -n sb_ok`})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.Output, "sb_ok") || out.ExitCode != 0 {
		t.Fatalf("输出 = (%q, %d)", out.Output, out.ExitCode)
	}
}

// TestProxyEnvInject 验证 proxyEnv 六生态 CA 环境变量注入容器。
func TestProxyEnvInject(t *testing.T) {
	inst := newSandboxInst(t)
	proxy := &ProxyEnv{Addr: "http://172.17.0.1:8788", CACertPath: "/ca/mitm.pem", Enabled: true}
	exec := NewSandboxExec(inst, "/workspace", proxy)

	out, err := exec.Run(context.Background(), BashParams{Command: `env`})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{"HTTPS_PROXY", "SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO", "NODE_EXTRA_CA_CERTS"} {
		if !strings.Contains(out.Output, want) {
			t.Fatalf("env 缺 %s: %q", want, out.Output)
		}
	}

	// 关闭时不注入。
	proxyOff := &ProxyEnv{Addr: "x", Enabled: false}
	if got := proxyOff.Environ(); len(got) != 0 {
		t.Fatalf("关闭时应为空切片: %v", got)
	}
}

// TestSandboxTmuxPersistent 验证 tmux 包装在容器内持久会话（环境跨命令保留）。
func TestSandboxTmuxPersistent(t *testing.T) {
	inst := newSandboxInst(t)
	exec := NewSandboxExec(inst, "/workspace", nil)

	if _, err := exec.Run(context.Background(), BashParams{Command: `export SBXV=kept`, Session: "w1"}); err != nil {
		t.Fatalf("第一条: %v", err)
	}
	out, err := exec.Run(context.Background(), BashParams{Command: `echo $SBXV`, Session: "w1"})
	if err != nil {
		t.Fatalf("第二条: %v", err)
	}
	if !strings.Contains(out.Output, "kept") {
		t.Fatalf("会话状态丢失: %q", out.Output)
	}
}


