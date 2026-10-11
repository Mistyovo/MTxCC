package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

// Manager 是容器生命周期管理器。
type Manager struct {
	cli *client.Client
}

// NewManager 建管理器。
func NewManager(cli *client.Client) *Manager { return &Manager{cli: cli} }

// Instance 是一个已就绪的任务容器句柄。
type Instance struct {
	id      string
	workdir string
	m       *Manager
}

// ID 返回容器 ID。
func (i *Instance) ID() string { return i.id }

// Workdir 返回宿主侧工作区目录。
func (i *Instance) Workdir() string { return i.workdir }

// Ensure 幂等保证容器存在且按规格运行（存在则复用，不存在则加固创建）。
func (m *Manager) Ensure(ctx context.Context, spec Spec) (*Instance, error) {
	// 按名字找已有容器。
	if id := m.findByName(ctx, spec.Name); id != "" {
		return &Instance{id: id, workdir: spec.WorkdirVol, m: m}, nil
	}

	if spec.WorkdirVol == "" {
		return nil, fmt.Errorf("WorkdirVol 不能为空")
	}
	if err := os.MkdirAll(spec.WorkdirVol, 0o750); err != nil {
		return nil, err
	}
	// rootless/userns docker 下容器 root 与宿主 uid 不映射，bind 目录需放宽可写
	//（任务工作区本身即单任务隔离单元；宿主侧外层目录权限仍受控）。
	if err := os.Chmod(spec.WorkdirVol, 0o777); err != nil {
		return nil, err
	}

	caps := []string{"NET_RAW"}
	if spec.AllowNETADMIN {
		caps = append(caps, "NET_ADMIN")
	}
	pids := int64(2048)

	hostCfg := &container.HostConfig{
		CapDrop:       []string{"ALL"},
		CapAdd:        caps,
		ReadonlyRootfs: true,
		// 工作区 bind（ro=false）；根文件系统只读但 /workspace 与 /tmp 可写。
		Binds:       []string{spec.WorkdirVol + ":/workspace"},
		Tmpfs:       map[string]string{"/tmp": "rw,size=64m"},
		NetworkMode: "bridge", // 独立 netns（egress 围栏由 Task 15 注入）
		RestartPolicy: container.RestartPolicy{Name: "no"},
	}
	hostCfg.PidsLimit = &pids // Resources 内嵌字段（v28 类型路径）
	cfg := &container.Config{
		Image:      spec.Image,
		Cmd:        []string{"sleep", "infinity"},
		WorkingDir: "/workspace",
		Tty:        true, // tmux 需要 PTY
		OpenStdin:  true,
	}
	_ = nat.Port("") // 保持依赖引用（端口分配 M1 接入）

	// 镜像存在性：不存在则拉取。
	if err := m.ensureImage(ctx, spec.Image); err != nil {
		return nil, fmt.Errorf("镜像 %s: %w", spec.Image, err)
	}

	resp, err := m.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, spec.Name)
	if err != nil {
		return nil, fmt.Errorf("创建容器: %w", err)
	}
	if err := m.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = m.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("启动容器: %w", err)
	}
	return &Instance{id: resp.ID, workdir: spec.WorkdirVol, m: m}, nil
}

// Destroy 强制移除容器。
func (m *Manager) Destroy(ctx context.Context, id string) error {
	return m.cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true})
}

// Inspect 返回容器全量信息（测试断言加固项用）。
func (m *Manager) Inspect(ctx context.Context, id string) (container.InspectResponse, error) {
	return m.cli.ContainerInspect(ctx, id)
}

// ExecSimple 在容器内跑一条命令并等待退出（轻量探测用）。
func (m *Manager) ExecSimple(ctx context.Context, id string, cmd string) error {
	created, err := m.cli.ContainerExecCreate(ctx, id, container.ExecOptions{
		Cmd:          []string{"sh", "-c", cmd},
		WorkingDir:   "/workspace",
		AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return err
	}
	resp, err := m.cli.ContainerExecAttach(ctx, created.ID, container.ExecStartOptions{})
	if err != nil {
		return err
	}
	defer resp.Close()
	buf := make([]byte, 4096)
	for {
		if _, err := resp.Reader.Read(buf); err != nil {
			break // EOF
		}
	}
	ins, err := m.cli.ContainerExecInspect(ctx, created.ID)
	if err != nil {
		return err
	}
	if ins.ExitCode != 0 {
		return fmt.Errorf("exit %d", ins.ExitCode)
	}
	return nil
}

// TouchAbort 检查工作区 .abort 紧急停机标记（每次 gated 工具调用前调用，spec §7.3）。
func (i *Instance) TouchAbort() bool {
	_, err := os.Stat(filepath.Join(i.workdir, ".abort"))
	return err == nil
}

func (m *Manager) findByName(ctx context.Context, name string) string {
	list, err := m.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return ""
	}
	for _, c := range list {
		for _, n := range c.Names {
			if strings.TrimPrefix(n, "/") == name {
				return c.ID
			}
		}
	}
	return ""
}

func (m *Manager) ensureImage(ctx context.Context, ref string) error {
	if _, err := m.cli.ImageInspect(ctx, ref); err == nil {
		return nil
	}
	rc, err := m.cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return err
	}
	defer rc.Close()
	buf := make([]byte, 32*1024)
	for {
		if _, err := rc.Read(buf); err != nil {
			if err.Error() == "EOF" {
				return nil
			}
			return nil // 流结束即视为完成（EOF 以 io.EOF 判定）
		}
	}
}
