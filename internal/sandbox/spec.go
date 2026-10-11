// Package sandbox 管理任务执行容器：cap_drop ALL + 白名单能力、pids_limit、
// 只读 rootfs + 工作区卷可写、独立网络（spec §7.3）。禁止挂载 docker.sock。
package sandbox

// Spec 是任务容器的创建规格。
type Spec struct {
	Image        string // 镜像（如 mtx-kali 或 busybox 测试镜像）
	Name         string // 容器名（mtx-task-<id>）
	WorkdirVol   string // 宿主工作区目录（bind 到 /workspace）
	AllowNETRAW  bool   // 默认 true
	AllowNETADMIN bool  // 默认 false（抓包/隧道任务开启）
}
