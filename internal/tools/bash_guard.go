package tools

import "regexp"

// 灾难命令四连（spec §7.4）：字面量匹配，无条件拒绝。
// 变量间接/拼接形态不在本层拦截——由 RoE 解析层与出口围栏兜底（fail-closed）。
var catastrophicPatterns = []*regexp.Regexp{
	// rm -rf 根目录或根下单段路径（允许相对路径与多段任务目录如 /tmp/mtx-x）。
	regexp.MustCompile(`\brm\s+[^|;&]*-[a-zA-Z]*r[a-zA-Z]*f?[^|;&]*\s+/(?:[a-zA-Z0-9_.-]+)?(?:\s|$|;|&)`),
	regexp.MustCompile(`\brm\s+[^|;&]*-[a-zA-Z]*f[a-zA-Z]*r?[^|;&]*\s+/(?:[a-zA-Z0-9_.-]+)?(?:\s|$|;|&)`),
	// fork 炸弹：匹配"自命名函数 + 自递归管道"定义本身（含 ;& 与截断变形）。
	regexp.MustCompile(`:\(\)\s*\{[^}]*:\s*\|[^}]*&?[^}]*\}`),
	// mkfs 直写块设备。
	regexp.MustCompile(`\bmkfs(?:\.\w+)?\s+(/dev/\S+)`),
	// dd 输出到块设备。
	regexp.MustCompile(`\bdd\b[^|;&]*\bof=/dev/(?:sd|nvme|vd|hd|mmcblk)`),
}

// CatastrophicBash 判断命令是否命中灾难正则（只挡字面量）。
func CatastrophicBash(cmd string) bool {
	for _, p := range catastrophicPatterns {
		if p.MatchString(cmd) {
			return true
		}
	}
	return false
}
