package roe

import (
	"net"
	"regexp"
	"strings"
)

// URL/IP/域名/变量目标 的粗提取模式（bash 词法级，无完整 shell 解析——
// 解析层承认可被绕过，egress 层兜底）。
var (
	urlRE    = regexp.MustCompile(`(?:https?|ftp|ssh)://(?:[^\s/@]+@)?(\[[0-9a-fA-F:.]+\]|[^\s/:]+)(?::\d+)?`)
	ipRE     = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	domainRE = regexp.MustCompile(`\b(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}\b`)
	atRE     = regexp.MustCompile(`\b[\w.-]+@([a-zA-Z0-9.-]+)`) // user@host
	varRE    = regexp.MustCompile(`\$[A-Za-z_][A-Za-z0-9_]*|\$\{[^}]+\}`)
)

// targetCmdHints 是"目标位命令"提示：这些命令的裸词参数更可能是目标。
var targetCmdHints = []string{"nmap", "dig", "nc", "ncat", "netcat", "ssh", "scp", "smbclient",
	"redis-cli", "mysql", "hydra", "medusa", "sqlmap", "nikto", "gobuster", "ffuf", "masscan", "amass"}

// ExtractTargets 从命令中提取候选目标（host/IP），去重。
func ExtractTargets(cmd string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
		if h == "" || seen[h] {
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
	return out
}

// looksLikeTargeting 判断命令是否"疑似携带目标但提取失败"（变量间接）→ fail-closed。
func looksLikeTargeting(cmd string) bool {
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
	return urlRE.MatchString(cmd) || strings.Contains(cmd, "@")
}

func looksDomain(s string) bool { return domainRE.MatchString(s) || strings.Contains(s, ".") }
