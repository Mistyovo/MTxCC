package roe

import (
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// URL/IP/域名/变量目标 的粗提取模式（bash 词法级，无完整 shell 解析——
// 解析层承认可被绕过，egress 层兜底）。
var (
	urlRE    = regexp.MustCompile(`(?:https?|ftp|ssh)://(?:[^\s/@]+@)?(\[[0-9a-fA-F:.]+\]|[^\s/:]+(?::\d+)?)`)
	ipRE     = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	domainRE = regexp.MustCompile(`\b(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}\b`)
	atRE     = regexp.MustCompile(`\b[\w.-]+@([a-zA-Z0-9.:-]+)`) // user@host[:port]
	varRE    = regexp.MustCompile(`\$[A-Za-z_][A-Za-z0-9_]*|\$\{[^}]+\}`)
)

// targetCmdHints 是"目标位命令"提示：这些命令的裸词参数更可能是目标。
var targetCmdHints = []string{"nmap", "dig", "nc", "ncat", "netcat", "ssh", "scp", "smbclient",
	"redis-cli", "mysql", "hydra", "medusa", "sqlmap", "nikto", "gobuster", "ffuf", "masscan", "amass",
	"ping", "ping6", "traceroute", "curl", "wget", "telnet"}

// suspiciousTargetForms 是 fail-closed 的"无法判定目标"形态（审查 C2d）：
// 输入文件（-iL/-i/-f）、命令替换 $()、反引号、十六进制/八进制 IP。
var (
	cmdSubRE     = regexp.MustCompile(`\$\(|\` + "`" + `|\$\{[A-Za-z_]`)
	inputFileRE  = regexp.MustCompile(`(?:^|\s)-(?:iL|il|I|i|f)\s+\S+`)
	hexOctIPRE   = regexp.MustCompile(`\b0x[0-9a-fA-F]{6,}\b|\b0[0-7]{9,}\b`)
)

// ExtractTargets 从命令中提取候选目标（host/IP），去重。
func ExtractTargets(cmd string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
		if h == "" || seen[h] {
			return
		}
		// 路径/脚本形态排除：含 / 的 token 是路径不是目标；脚本扩展名结尾的文件名
		// 不是域名（实证 bug：ok.py 被误提为域名）。
		if strings.Contains(h, "/") {
			return
		}
		if isScriptFilename(h) {
			return
		}
		// host:port 形态：拆分后按 host 判定合法性（端口级 scope 保留端口）。
		if host, _, err := net.SplitHostPort(h); err == nil {
			if net.ParseIP(host) != nil || looksDomain(host) {
				seen[h] = true
				out = append(out, h)
			}
			return
		}
		if net.ParseIP(h) != nil || looksDomain(h) {
			seen[h] = true
			out = append(out, h)
		}
	}

	for _, m := range urlRE.FindAllStringSubmatch(cmd, -1) {
		add(strings.Trim(m[1], "[]"))
	}
	for _, m := range atRE.FindAllStringSubmatch(cmd, -1) {
		add(m[1])
	}
	for _, m := range ipRE.FindAllString(cmd, -1) {
		add(m)
	}
	for _, m := range domainRE.FindAllString(cmd, -1) {
		add(m)
	}
	// 同 host 去重：host:port 形态优先（端口级 scope 需要具体端口，裸 host 会被误拒）。
	return dedupByHost(out)
}

// dedupByHost 保留 host 形态最具体者（带端口 > 裸 host）。
func dedupByHost(targets []string) []string {
	best := map[string]string{} // host → target
	order := []string{}
	for _, t := range targets {
		h := t
		if host, _, err := net.SplitHostPort(t); err == nil {
			h = host
		}
		if prev, ok := best[h]; ok {
			// 已有带端口形态则保留它；否则新形态若是带端口则替换。
			_, _, prevPort := net.SplitHostPort(prev)
			_, _, newPort := net.SplitHostPort(t)
			if prevPort == nil && newPort == nil {
				continue // 都带端口或都不带：保持首个
			}
			if prevPort == nil && newPort != nil {
				// 替换为带端口版本。
				for i, x := range order {
					if x == h {
						order[i] = t
					}
				}
				best[h] = t
			}
			continue
		}
		best[h] = t
		order = append(order, t)
	}
	out := order
	return out
}

// looksLikeTargeting 判断命令是否"疑似携带目标但无法判定"→ fail-closed（审查 C2d 加固）。
// 命中任一：目标位命令出现 / 命令替换 / 输入文件 / 十六进制 IP / URL / user@host。
func looksLikeTargeting(cmd string) bool {
	if cmdSubRE.MatchString(cmd) || inputFileRE.MatchString(cmd) || hexOctIPRE.MatchString(cmd) {
		return true
	}
	if urlRE.MatchString(cmd) || strings.Contains(cmd, "@") {
		return true
	}
	if !varRE.MatchString(cmd) {
		return false
	}
	fields := strings.Fields(cmd)
	for _, f := range fields {
		for _, hint := range targetCmdHints {
			if f == hint || strings.HasPrefix(f, hint) {
				return true
			}
		}
	}
	return false
}

func looksDomain(s string) bool { return domainRE.MatchString(s) || strings.Contains(s, ".") }

// scriptExts 会被扫描内容的脚本扩展名（间接目标检测）。
var scriptExts = []string{".py", ".sh", ".bash", ".rb", ".pl", ".ps1"}

// scanScriptTargets 检测命令中引用的脚本文件并扫描其内容提取目标
//（实证 gap：python 脚本内目标绕过命令行解析层）。
func scanScriptTargets(cmd string) []string {
	var out []string
	for _, f := range strings.Fields(cmd) {
		// 路径形态 token（含 .py/.sh）。
		if !strings.Contains(f, ".") {
			continue
		}
		isScript := false
		for _, ext := range scriptExts {
			if strings.HasSuffix(f, ext) {
				isScript = true
				break
			}
		}
		if !isScript {
			continue
		}
		path := f
		if !filepath.IsAbs(path) {
			path = filepath.Join(".", path)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		out = append(out, ExtractTargets(string(b))...)
		// 脚本内容含疑似目标但提取不到 → fail-closed 信号（返回哨兵）。
		if looksLikeTargeting(string(b)) && len(ExtractTargets(string(b))) == 0 {
			out = append(out, "__UNRESOLVED_SCRIPT__")
		}
	}
	return out
}

// isScriptFilename 判定 token 是否为脚本文件名（非域名）。
func isScriptFilename(h string) bool {
	for _, ext := range scriptExts {
		if strings.HasSuffix(h, ext) {
			return true
		}
	}
	return false
}
