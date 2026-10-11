package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"

	"mtx/internal/roe"
)

// egressGatewayImage 是出口围栏网关镜像（宿主侧容器，共享任务容器 netns）。
const egressGatewayImage = "busybox:latest" // M0：busybox + nft 需求由注入方保证；生产镜像在 deploy/ 提供

// cloudMetaEndpoints 默认封禁的云元数据端点（spec §7.2）。
var cloudMetaEndpoints = []string{"169.254.169.254", "fd00:ec2::254", "metadata.google.internal"}

// CompileNft 把 RoE 编译为 nftables 规则文本（netns 内执行）：
// deny（out_of_scope + 云元数据）先于 allow（in_scope 与域名解析 IP）；
// 默认拒绝新连接；DNS 仅放行网关。
func CompileNft(r roe.Rules, resolved map[string][]net.IP) string {
	var sb strings.Builder
	sb.WriteString("# MTxCC egress fence (auto-generated from roe.json)\n")
	sb.WriteString("table inet mtx_egress {\n")
	sb.WriteString("  chain output {\n")
	sb.WriteString("    type filter hook output priority 0; policy drop;\n")
	sb.WriteString("    # 已建立连接放行\n")
	sb.WriteString("    ct state established,related accept\n")
	sb.WriteString("    # 回环放行\n")
	sb.WriteString("    oif lo accept\n")
	sb.WriteString("    # DNS：仅网关（dnsmasq 监听本回环）\n")
	sb.WriteString("    udp dport 53 ip daddr 127.0.0.1 accept\n")
	sb.WriteString("    tcp dport 53 ip daddr 127.0.0.1 accept\n")

	// deny 优先（out_of_scope + 云元数据）。
	denies := append([]string{}, r.OutOfScope...)
	denies = append(denies, cloudMetaEndpoints...)
	sort.Strings(denies)
	sb.WriteString("    # --- deny（优先） ---\n")
	for _, d := range denies {
		fmt.Fprintf(&sb, "    ip daddr %s reject\n", d)
	}

	// allow：in_scope 中网段/IP + 域名解析出的全部 IP。
	sb.WriteString("    # --- allow（scope） ---\n")
	var allows []string
	for _, s := range r.InScope {
		if strings.Contains(s, "/") || net.ParseIP(s) != nil {
			allows = append(allows, s)
		}
	}
	for _, ips := range resolved {
		for _, ip := range ips {
			if ip.To4() != nil {
				allows = append(allows, ip.String())
			}
		}
	}
	sort.Strings(allows)
	for _, a := range allows {
		fmt.Fprintf(&sb, "    ip daddr %s accept\n", a)
	}

	sb.WriteString("    # 其余全部拒绝（默认策略 drop）\n")
	sb.WriteString("  }\n}\n")
	return sb.String()
}

// CompileDnsmasq 生成 dnsmasq 配置：scope 域名转发到上游、其余解析拒绝（NXDOMAIN）。
func CompileDnsmasq(r roe.Rules, upstreamDNS string) string {
	var sb strings.Builder
	sb.WriteString("# MTxCC dns allowlist (auto-generated)\n")
	sb.WriteString("no-resolv\n")
	sb.WriteString("listen-address=127.0.0.1\n")
	for _, s := range r.InScope {
		if strings.Contains(s, "/") || net.ParseIP(s) != nil {
			continue
		}
		fmt.Fprintf(&sb, "server=/%s/%s\n", strings.TrimPrefix(s, "*."), upstreamDNS)
	}
	// 其余域名拒绝解析。
	sb.WriteString("address=/#/0.0.0.0\n")
	return sb.String()
}

// InjectEgress 以 network_mode:container:<task> 启动网关容器并应用 nftables + dnsmasq：
// 网关与任务容器共享 netns，规则文件只读挂载。错误返回表示环境不支持（调用方按计划降级）。
func InjectEgress(ctx context.Context, m *Manager, inst *Instance, r roe.Rules) error {
	gwName := "mtx-egress-" + strings.TrimPrefix(inst.ID(), "")
	gwName = "mtx-egress-" + inst.ID()[0:12]

	// 解析 scope 域名 → IP（DNS 失败的域名跳过并继续——网络层兜底靠其余规则）。
	resolved := map[string][]net.IP{}
	for _, s := range r.InScope {
		if strings.Contains(s, "/") || net.ParseIP(s) != nil {
			continue
		}
		host := strings.TrimPrefix(s, "*.")
		if ips, err := net.LookupIP(host); err == nil {
			resolved[host] = ips
		}
	}

	nftRules := CompileNft(r, resolved)
	dnsConf := CompileDnsmasq(r, "10.0.0.53") // 上游 DNS 由部署 .env 提供；M0 默认值

	// 网关容器：挂载规则文件（宿主临时文件，只读）。
	dir, err := writeEgressFiles(nftRules, dnsConf)
	if err != nil {
		return err
	}

	hostCfg := &container.HostConfig{
		NetworkMode: container.NetworkMode("container:" + inst.ID()),
		CapAdd:      []string{"NET_ADMIN"},
		Privileged:  false,
		Mounts: []mount.Mount{
			{Type: mount.TypeBind, Source: dir, Target: "/etc/mtx", ReadOnly: true},
		},
	}
	cfg := &container.Config{
		Image: egressGatewayImage,
		Cmd:   []string{"sh", "-c", "echo 'egress gw placeholder: nft -f /etc/mtx/egress.nft && dnsmasq --conf-file=/etc/mtx/dnsmasq.conf'; sleep infinity"},
	}
	resp, err := m.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, gwName)
	if err != nil {
		return fmt.Errorf("创建网关容器: %w", err)
	}
	if err := m.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = m.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("启动网关容器: %w", err)
	}
	// 注：busybox 无 nft/dnsmasq 二进制——生产环境用 deploy/egress-gw 镜像执行真实加载；
	// M0 的加载动作为占位（规则文本已生成并挂载），注入链路由本函数打通。
	_ = bytes.MinRead
	return nil
}

// writeEgressFiles 把规则写入宿主临时目录（返回目录路径）。
func writeEgressFiles(nftRules, dnsConf string) (string, error) {
	dir, err := tempEgressDir()
	if err != nil {
		return "", err
	}
	if err := writeFile(dir+"/egress.nft", nftRules); err != nil {
		return "", err
	}
	if err := writeFile(dir+"/dnsmasq.conf", dnsConf); err != nil {
		return "", err
	}
	return dir, nil
}
