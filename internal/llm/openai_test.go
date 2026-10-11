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

// openaiGolden 是 Chat Completions 流式金样本：
// 文本增量 ×2 → reasoning_content（思考）→ tool_calls 分片（call_01）→ finish_reason=tool_calls → usage（cached_tokens）。
const openaiGolden = `data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","content":"你"}}]}

data: {"id":"c1","choices":[{"index":0,"delta":{"content":"好"}}]}

data: {"id":"c1","choices":[{"index":0,"delta":{"reasoning_content":"想一想"}}]}

data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_01","type":"function","function":{"name":"bash","arguments":""}}]}}]}

data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"command\":"}}]}}]}

data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]}}]}

data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: {"id":"c1","choices":[],"usage":{"prompt_tokens":25,"completion_tokens":42,"prompt_tokens_details":{"cached_tokens":10}}}

data: [DONE]

`

// TestOpenAIStreamEvents 验证 Chat Completions SSE → 统一事件流。
// 令其失败的生产改动：tool_calls 分片未按 index 关联、reasoning_content 未映射思考、usage 丢失。
func TestOpenAIStreamEvents(t *testing.T) {
	var gotReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotReq)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, openaiGolden)
	}))
	defer srv.Close()

	p := NewOpenAI(Profile{BaseURL: srv.URL, APIKey: "sk-x", Model: "gpt-x"})
	ch, err := p.Stream(context.Background(), Request{
		Model: "gpt-x", System: "系统提示", MaxTokens: 512,
		Messages: []Message{{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: "列目录"}}}},
		Tools:   []ToolSpec{{Name: "bash", Description: "执行", InputSchema: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var (
		texts []string
		thinks []string
		starts []ToolUseStart
		frags  = map[string]string{}
		stop   string
		usage  Usage
		sawU   bool
	)
	for ev := range ch {
		switch e := ev.(type) {
		case TextDelta:
			texts = append(texts, e.S)
		case ThinkingDelta:
			thinks = append(thinks, e.S)
		case ToolUseStart:
			starts = append(starts, e)
		case ToolInputJSON:
			frags[e.ID] += e.Fragment
		case MessageDelta:
			stop = e.StopReason
		case Usage:
			usage, sawU = e, true
		}
	}
	if strings.Join(texts, "") != "你好" {
		t.Fatalf("文本 = %q", strings.Join(texts, ""))
	}
	if len(thinks) != 1 || thinks[0] != "想一想" {
		t.Fatalf("思考 = %v", thinks)
	}
	if len(starts) != 1 || starts[0].ID != "call_01" || starts[0].Name != "bash" {
		t.Fatalf("tool 开始 = %+v", starts)
	}
	if frags["call_01"] != `{"command":"ls"}` {
		t.Fatalf("tool 参数重组 = %q", frags["call_01"])
	}
	if stop != "tool_calls" {
		t.Fatalf("finish_reason = %q", stop)
	}
	if !sawU || usage.In != 25 || usage.Out != 42 || usage.CacheRead != 10 {
		t.Fatalf("usage = %+v", usage)
	}
	if gotReq["model"] != "gpt-x" || gotReq["max_tokens"].(float64) != 512 {
		t.Fatalf("请求 model/max_tokens = %v/%v", gotReq["model"], gotReq["max_tokens"])
	}
	if msgs := gotReq["messages"].([]any); msgs[0].(map[string]any)["role"] != "system" {
		t.Fatalf("system 应为首条消息，实际 %v", msgs[0])
	}
}
