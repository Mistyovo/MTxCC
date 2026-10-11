package harness

import (
	"encoding/json"
	"regexp"
	"strings"
)

// TextToolCall 是从模型文本里解析出的工具调用。
type TextToolCall struct {
	Name      string
	InputJSON string
}

// textToolRE 匹配 <tool_call>  形态（含双括号转义变体）。
// 调研 T3MP3ST 设计：弱模型（本地 qwen 等）不产标准 tool_use 事件，把调用写进文本。
var textToolRE = regexp.MustCompile("(?s)<tool_call>(.*?)</tool_call>")

// ParseTextToolCalls 从文本中提取工具调用（容忍 }}/{{ 双括号模板残留）。
func ParseTextToolCalls(text string) []TextToolCall {
	var out []TextToolCall
	for _, m := range textToolRE.FindAllStringSubmatch(text, -1) {
		raw := strings.TrimSpace(m[1])
		// 双括号变体归一：{{ → {，}} → }（模板转义残留）。
		if strings.Contains(raw, "{{") {
			raw = strings.ReplaceAll(raw, "{{", "{")
			raw = strings.ReplaceAll(raw, "}}", "}")
		}
		var obj struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(raw), &obj); err != nil || obj.Name == "" {
			continue
		}
		args := string(obj.Arguments)
		if args == "" {
			args = "{}"
		}
		if !json.Valid([]byte(args)) {
			continue
		}
		out = append(out, TextToolCall{Name: obj.Name, InputJSON: args})
	}
	return out
}

// stripTextToolCalls 去掉文本中的工具调用块（回填前的 user 内容清洁）。
func stripTextToolCalls(text string) string {
	return strings.TrimSpace(textToolRE.ReplaceAllString(text, ""))
}
