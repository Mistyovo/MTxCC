package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// anthropicGolden 是一段真实形态的 Anthropic Messages SSE 流：
// 文本 delta ×2 → thinking delta → 两个 tool_use 交错 input 分片 → message_delta(end_turn) → usage。
const anthropicGolden = `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":25,"cache_read_input_tokens":10,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"你好"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"世界"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"让我想想"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: content_block_start
data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_01","name":"bash"}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"command\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"ls\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":2}

event: content_block_start
data: {"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_02","name":"read"}}

event: content_block_delta
data: {"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"/etc/hosts\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":3}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":42}}

event: message_stop
data: {"type":"message_stop"}

`

// TestAnthropicStreamEvents 验证 SSE 解析为统一事件流：文本/思考增量、双 tool_use 分片重组、终止原因、累计 usage。
// 令其失败的生产改动：事件类型映射错误、分片未按 ID 关联、usage 未累计。
func TestAnthropicStreamEvents(t *testing.T) {
	var gotReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotReq)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, anthropicGolden)
	}))
	defer srv.Close()

	p := NewAnthropic(Profile{BaseURL: srv.URL, APIKey: "sk-test", Model: "claude-x"})
	ch, err := p.Stream(context.Background(), Request{
		Model:     "claude-x",
		System:    "你是渗透执行者",
		MaxTokens: 1024,
		Messages: []Message{
			{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: "列目录"}}},
			{Role: RoleAssistant, Blocks: []Block{{Type: BlockToolUse, ID: "toolu_00", Name: "bash", InputJSON: `{"command":"id"}`}}},
			{Role: RoleUser, Blocks: []Block{{Type: BlockToolResult, ToolUseID: "toolu_00", Text: "uid=0"}}},
		},
		Tools: []ToolSpec{{Name: "bash", Description: "执行命令", InputSchema: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var (
		texts, thinkings []string
		toolStarts       []ToolUseStart
		inputFrags       = map[string]string{}
		usageFinal       Usage
		sawUsage         bool
		stopReason       string
	)
	for ev := range ch {
		switch e := ev.(type) {
		case TextDelta:
			texts = append(texts, e.S)
		case ThinkingDelta:
			thinkings = append(thinkings, e.S)
		case ToolUseStart:
			toolStarts = append(toolStarts, e)
		case ToolInputJSON:
			inputFrags[e.ID] += e.Fragment
		case MessageDelta:
			stopReason = e.StopReason
		case Usage:
			usageFinal, sawUsage = e, true
		}
	}

	if strings.Join(texts, "") != "你好世界" {
		t.Fatalf("文本增量 = %q, want 你好世界", strings.Join(texts, ""))
	}
	if len(thinkings) != 1 || thinkings[0] != "让我想想" {
		t.Fatalf("思考增量 = %v", thinkings)
	}
	if len(toolStarts) != 2 || toolStarts[0].ID != "toolu_01" || toolStarts[0].Name != "bash" || toolStarts[1].ID != "toolu_02" || toolStarts[1].Name != "read" {
		t.Fatalf("tool_use 开始事件 = %+v", toolStarts)
	}
	if got := inputFrags["toolu_01"]; got != `{"command":"ls"}` {
		t.Fatalf("toolu_01 重组 = %q", got)
	}
	if got := inputFrags["toolu_02"]; got != `{"path":"/etc/hosts"}` {
		t.Fatalf("toolu_02 重组 = %q", got)
	}
	if stopReason != "end_turn" {
		t.Fatalf("stop_reason = %q, want end_turn", stopReason)
	}
	if !sawUsage || usageFinal.In != 25 || usageFinal.Out != 42 || usageFinal.CacheRead != 10 {
		t.Fatalf("最终 usage = %+v, want {25 42 10}", usageFinal)
	}

	// 请求侧：模型、max_tokens、system、消息角色序列、工具定义形态。
	if gotReq["model"] != "claude-x" {
		t.Fatalf("请求 model = %v", gotReq["model"])
	}
	if gotReq["max_tokens"].(float64) != 1024 {
		t.Fatalf("max_tokens = %v", gotReq["max_tokens"])
	}
	msgs := gotReq["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages 数 = %d, want 3", len(msgs))
	}
	if msgs[0].(map[string]any)["role"] != "user" {
		t.Fatalf("首条角色 = %v", msgs[0].(map[string]any)["role"])
	}
	tools := gotReq["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "bash" {
		t.Fatalf("tools = %v", tools)
	}
}
