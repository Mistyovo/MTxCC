// Package tmux 提供持久会话执行内核（spec §6.1）：
// PS1 时代替方案——完成标记法：命令尾接 echo __MTX_EXIT_<code>__，
// capture-pane 轮询检测标记；交互程序吞掉标记 → 超时 → 提示符尾部正则识别。
package tmux

import "regexp"

// interactivePromptPatterns 匹配常见交互提示尾部（密码/确认/shell 提示符）。
var interactivePromptPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)password\s*:?\s*$`),          // Password: / 密码：
	regexp.MustCompile(`(?i)passphrase\s*:?\s*$`),
	regexp.MustCompile(`(?i)\b(yes/no|y/n)\b\s*[?:]?\s*$`), // yes/no 确认
	regexp.MustCompile(`(?i)\busername\s*:?\s*$`),
	regexp.MustCompile(`(?i)press\s+.*\s+to\s+continue.*$`),
	regexp.MustCompile(`\$\s*$`),   // 待输入 shell
	regexp.MustCompile(`>\s*$`),    // REPL/继续提示
	regexp.MustCompile(`#\s*$`),    // root/调试提示符
	regexp.MustCompile(`密码[：:]\s*$`), // 中文密码提示
}

// LooksLikeInteractivePrompt 判断输出尾部是否像"程序在等人输入"。
func LooksLikeInteractivePrompt(tail string) bool {
	if len(tail) > 200 {
		tail = tail[len(tail)-200:]
	}
	for _, p := range interactivePromptPatterns {
		if p.MatchString(tail) {
			return true
		}
	}
	return false
}
