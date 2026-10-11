package tools

import "fmt"

// ProxyEnv 是 MITM 留痕代理与 CA 注入配置（spec §6.5）：
// 六生态信任变量让任意 CLI 工具零配置走记录代理且 HTTPS 正常校验。
type ProxyEnv struct {
	Addr       string // 代理地址（如 http://gateway:8788）
	CACertPath string // MITM CA 证书容器内路径
	Enabled    bool
}

// Environ 返回注入子进程的环境变量切片（Enabled=false 时为空切片）。
func (p *ProxyEnv) Environ() []string {
	if p == nil || !p.Enabled {
		return []string{}
	}
	env := []string{
		"HTTP_PROXY=" + p.Addr,
		"HTTPS_PROXY=" + p.Addr,
		"ALL_PROXY=" + p.Addr,
		"http_proxy=" + p.Addr,
		"https_proxy=" + p.Addr,
	}
	if p.CACertPath != "" {
		env = append(env,
			"SSL_CERT_FILE="+p.CACertPath,
			"REQUESTS_CA_BUNDLE="+p.CACertPath,
			"CURL_CA_BUNDLE="+p.CACertPath,
			"GIT_SSL_CAINFO="+p.CACertPath,
			fmt.Sprintf("NODE_EXTRA_CA_CERTS=%s", p.CACertPath),
			"NODE_USE_ENV_PROXY=1",
		)
	}
	return env
}
