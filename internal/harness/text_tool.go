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
		// 双括号变体归一：全局 {{ → {（合法 JSON 不出现连续两开括号，安全）；
		// 再按括号平衡削尾/补尾（弱模型常多闭或缺闭）。
		raw = strings.ReplaceAll(raw, "{{", "{")
		raw = balanceJSONBraces(raw)
		// 弱模型输出常缺右括号（如 3 开 2 闭）——容错补全（最多 3 个）。
		obj, ok := parseToolObj(raw)
		if !ok {
			for i := 0; i < 3 && !ok; i++ {
				obj, ok = parseToolObj(raw + strings.Repeat("}", i+1))
			}
		}
		if !ok || obj.Name == "" {
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

// balanceJSONBraces 按花括号平衡修正尾部（弱模型 JSON 容错）。
func balanceJSONBraces(raw string) string {
	opens := strings.Count(raw, "{")
	closes := strings.Count(raw, "}")
	if closes > opens {
		// 从尾部删除多余的 }。
		for closes > opens && strings.HasSuffix(raw, "}") {
			raw = raw[:len(raw)-1]
			closes--
		}
	} else if opens > closes {
		// 尾部补 }。
		raw += strings.Repeat("}", opens-closes)
	}
	return raw
}

// textToolObj 是解析出的工具调用 JSON 形态。
type textToolObj struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// parseToolObj 解析工具调用 JSON（容错入口）。
func parseToolObj(raw string) (textToolObj, bool) {
	var obj textToolObj
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return obj, false
	}
	return obj, true
}
