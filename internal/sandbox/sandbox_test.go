//go:build docker

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/client"
)

func newManager(t *testing.T) *Manager {
	t.Helper()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return NewManager(cli)
}

func busySpec(t *testing.T) (Spec, string) {
	t.Helper()
	dir := t.TempDir()
	return Spec{
		Image:     "busybox:latest",
		Name:      "mtx-test-" + filepath.Base(dir),
		WorkdirVol: dir,
	}, dir
}

// TestEnsureHardened 验证容器加固项：cap_drop ALL + 白名单、pids_limit、只读 rootfs、
// 工作区卷可写；并验证幂等。
func TestEnsureHardened(t *testing.T) {
	m := newManager(t)
	spec, _ := busySpec(t)
	ctx := context.Background()

	inst, err := m.Ensure(ctx, spec)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), inst.ID()) })

	ci, err := m.Inspect(ctx, inst.ID())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	hc := ci.HostConfig
	if len(hc.CapDrop) == 0 || hc.CapDrop[0] != "ALL" {
		t.Fatalf("CapDrop = %v, want [ALL]", hc.CapDrop)
	}
	if len(hc.CapAdd) == 0 || (hc.CapAdd[0] != "NET_RAW" && hc.CapAdd[0] != "CAP_NET_RAW") {
		t.Fatalf("CapAdd = %v, want 至少 NET_RAW", hc.CapAdd)
	}
	if *hc.PidsLimit != 2048 {
		t.Fatalf("PidsLimit = %d, want 2048", *hc.PidsLimit)
	}
	if !hc.ReadonlyRootfs {
		t.Fatal("ReadonlyRootfs 应为 true")
	}
	// 禁挂 docker.sock。
	for _, mnt := range hc.Binds {
		if filepath.Base(filepath.Dir(mnt)) == "docker.sock" || filepath.Base(mnt) == "docker.sock" {
			t.Fatalf("禁止挂载 docker.sock: %v", mnt)
		}
	}

	// 幂等：再次 Ensure 不重建（同 ID）。
	inst2, err := m.Ensure(ctx, spec)
	if err != nil {
		t.Fatalf("Ensure 二次: %v", err)
	}
	if inst2.ID() != inst.ID() {
		t.Fatalf("幂等失败: %s vs %s", inst2.ID(), inst.ID())
	}
}

// TestWorkdirWritable 验证只读 rootfs 下工作区卷可写。
func TestWorkdirWritable(t *testing.T) {
	m := newManager(t)
	spec, dir := busySpec(t)
	ctx := context.Background()

	inst, err := m.Ensure(ctx, spec)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), inst.ID()) })

	if err := m.ExecSimple(ctx, inst.ID(), "sh -c 'echo hi > /workspace/probe.txt'"); err != nil {
		t.Fatalf("工作区写入: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "probe.txt")); err != nil || string(b) != "hi\n" && string(b) != "hi" {
		t.Fatalf("宿主侧应可见: %q %v", string(b), err)
	}
}

// TestAbortMarker 验证 .abort 紧急停机标记（gated 工具调用前检查，spec §7.3）。
func TestAbortMarker(t *testing.T) {
	m := newManager(t)
	spec, dir := busySpec(t)
	ctx := context.Background()

	inst, err := m.Ensure(ctx, spec)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), inst.ID()) })

	if inst.TouchAbort() {
		t.Fatal("初始不应有 abort 标记")
	}
	if err := os.WriteFile(filepath.Join(dir, ".abort"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if inst.TouchAbort() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal(".abort 标记应可见")
}

// TestInjectEgressIntegration 集成验证（需 nft 权限；无权限跳过）：
// busybox + egress gw 容器（network_mode: container: 共享 netns）。
func TestInjectEgressIntegration(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()
	spec, _ := busySpec(t)
	inst, err := m.Ensure(ctx, spec)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), inst.ID()) })

	if err := InjectEgress(ctx, m, inst, sampleRules()); err != nil {
		t.Skipf("egress 注入不可用（无 nft 权限环境按计划允许跳过）: %v", err)
	}
	// 注入成功即结构正确；连通性正反向断言在 CI 特权 runner 跑（计划说明）。
}
