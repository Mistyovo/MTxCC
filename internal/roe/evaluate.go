package roe

import (
	"net"
	"regexp"
	"strings"
	"time"
)

// Decision 是一次评估结论。
type Decision struct {
	Allow  bool
	Reason string
}

// Resolver 是 DNS 解析器抽象（测试注入）。
type Resolver func(host string) ([]net.IP, error)

// EvaluateCommand 解析层评估一条命令：
// 时间窗 → 禁令正则 → 目标提取 → 逐目标 EvaluateTarget；
// 命令含目标位但提取不到目标（变量间接）→ fail-closed。
func EvaluateCommand(cmd string, r Rules) Decision {
	if d := checkWindow(r); !d.Allow {
		return d
	}
	for _, pat := range r.ForbiddenCmdPatterns {
		if re, err := regexp.Compile(pat); err == nil && re.MatchString(cmd) {
			return Decision{false, "命中禁令命令正则: " + pat}
		}
	}
	targets := ExtractTargets(cmd)
	if len(targets) == 0 {
		// 无目标无害命令（echo/ls 等）：放行；含目标位语法但全为变量 → 同样零目标，
		// 这里无法区分——按 fail-closed 原则：命令中出现 $VAR/@host 类"疑似目标未解析"即拒。
		if looksLikeTargeting(cmd) {
			return Decision{false, "unresolvable target（命令疑似携带目标但无法提取，fail-closed）"}
		}
		return Decision{true, "无目标命令"}
	}
	for _, t := range targets {
		if d := EvaluateTarget(t, r); !d.Allow {
			return d
		}
	}
	return Decision{true, "全部目标在授权范围内"}
}

// EvaluateTarget 评估单个目标：显式 out_of_scope → 禁 TLD → in_scope 白名单
//（CIDR 数学、通配域、尾点归一）→ 默认拒。
func EvaluateTarget(host string, r Rules) Decision {
	if d := checkWindow(r); !d.Allow {
		return d
	}
	h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".") // 尾点 FQDN 归一

	if matchAny(h, r.OutOfScope) || ipInList(h, r.OutOfScope) {
		return Decision{false, "目标在 out_of_scope 清单"}
	}
	if isIP(h) {
		if !ipInList(h, r.InScope) {
			return Decision{false, "IP 不在任何授权网段"}
		}
		return Decision{true, "IP 在授权网段"}
	}
	// 域名：禁 TLD（任意级后缀）。
	for _, tld := range r.ForbiddenTLDs {
		if strings.HasSuffix(h, "."+tld) {
			return Decision{false, "命中禁用 TLD ." + tld}
		}
	}
	for _, pat := range r.InScope {
		if matchHostPattern(h, pat) {
			return Decision{true, "域名命中授权范围 " + pat}
		}
	}
	return Decision{false, "域名不在授权范围（默认拒）"}
}

// ResolveNet 经注入解析器做 DNS 解析（供命令级目标判定的域名分支）。
func ResolveNet(resolve Resolver, host string) ([]net.IP, error) {
	h := strings.TrimSuffix(strings.TrimSpace(host), ".")
	if ip := net.ParseIP(h); ip != nil {
		return []net.IP{ip}, nil
	}
	return resolve(h)
}

// ---- 内部 ----

func checkWindow(r Rules) Decision {
	if len(r.Windows) == 0 {
		return Decision{true, ""}
	}
	now := Now()
	for _, w := range r.Windows {
		for _, seg := range w.Authorized {
			if inSegment(now, seg) {
				return Decision{true, ""}
			}
		}
	}
	return Decision{false, "当前时间不在授权时间窗内"}
}

func inSegment(now time.Time, seg string) bool {
	parts := strings.SplitN(seg, "/", 2)
	if len(parts) != 2 {
		return false
	}
	start, err1 := time.ParseInLocation("2006-01-02T15:04", parts[0], now.Location())
	end, err2 := time.ParseInLocation("2006-01-02T15:04", parts[1], now.Location())
	if err1 != nil || err2 != nil {
		return false
	}
	return !now.Before(start) && now.Before(end)
}

func matchAny(host string, pats []string) bool {
	for _, p := range pats {
		if matchHostPattern(host, p) {
			return true
		}
	}
	return false
}

// matchHostPattern 判定目标是否命中一条 scope 模式（端口级语义）：
// - scope 条目带端口（host:port）：目标必须同 host 且同端口（精确）
// - scope 条目不带端口（CIDR/IP/域名/通配）：仅比 host，目标带端口也放行
// - 目标不带端口而 scope 带端口：拒绝（端口级 scope 要求明确端口）
func matchHostPattern(target, pat string) bool {
	tHost, tPort, tHasPort := splitHostPort(target)
	pHost, pPort, pHasPort := splitHostPort(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(pat)), "."))

	if pHasPort {
		if !tHasPort || tPort != pPort {
			return false
		}
	}
	host := tHost
	if !hostMatch(host, pHost) {
		return false
	}
	return true
}

// splitHostPort 分解 host[:port]。
func splitHostPort(s string) (host, port string, hasPort bool) {
	if h, p, err := net.SplitHostPort(s); err == nil {
		return h, p, true
	}
	return s, "", false
}

// hostMatch host 模式匹配（CIDR/精确/通配）。
func hostMatch(host, p string) bool {
	if ip := net.ParseIP(host); ip != nil {
		if strings.Contains(p, "/") {
			if _, ipnet, err := net.ParseCIDR(p); err == nil && ipnet.Contains(ip) {
				return true
			}
		}
		return host == p
	}
	if strings.HasPrefix(p, "*.") {
		suffix := strings.TrimPrefix(p, "*")
		return strings.HasSuffix(host, suffix)
	}
	return host == p
}

func ipInList(host string, pats []string) bool {
	for _, p := range pats {
		if matchHostPattern(host, p) {
			return true
		}
	}
	return false
}

func isIP(s string) bool { return net.ParseIP(s) != nil }
