package tools

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ansiPattern 匹配 ANSI/VT 转义序列（CSI/OSC 等）。
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-_]`)

// SanitizeOutput 清洗管线（spec §6.1）：
// surrogate 重编码 → ANSI 剥离 → 重复行压缩（保留前 5+后 5，中间以省略计数代替）。
func SanitizeOutput(raw []byte) string {
	s := fixSurrogates(string(raw))
	s = ansiPattern.ReplaceAllString(s, "")
	return compressRepeats(s)
}

// fixSurrogates 把无效的 UTF-16 孤立代理重编码为 U+FFFD（防下游 JSON 编码报错）。
func fixSurrogates(s string) string {
	if utf8.ValidString(s) && !strings.ContainsRune(s, 0xD800) {
		return s
	}
	var sb strings.Builder
	for _, r := range s {
		if r >= 0xD800 && r <= 0xDFFF {
			sb.WriteRune(0xFFFD)
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// compressRepeats 压缩连续重复行：>10 行相同 → 前 5 + "…[省略 N 行]" + 后 5。
func compressRepeats(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	i := 0
	for i < len(lines) {
		j := i
		for j < len(lines) && lines[j] == lines[i] {
			j++
		}
		run := j - i
		if run > 10 {
			out = append(out, lines[i:i+5]...)
			out = append(out, fmt.Sprintf("…[省略 %d 行（重复）]", run-10))
			out = append(out, lines[j-5:j]...)
		} else {
			out = append(out, lines[i:j]...)
		}
		i = j
	}
	return strings.Join(out, "\n")
}

// Capture 截断层：超过 max 的输出返回 head2K+tail1K+卸载指针，完整正文交调用方落盘。
func Capture(s string, max int) (head string, offloaded string) {
	if len(s) <= max {
		return s, ""
	}
	offloaded = s
	ptr := fmt.Sprintf(".scratch/%d", len(s))
	marker := fmt.Sprintf("\n…[offloaded to %s（%d 字节，可用 read/grep 取回）]", ptr, len(s))
	return s[:2048] + marker + s[len(s)-1024:], offloaded
}
