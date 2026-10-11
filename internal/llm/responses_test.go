package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// responsesGolden 是 OpenAI Responses API 流式金样本（子集）：
// function_call item 出现 → 参数分片 → output_text 增量 → completed（usage）。
const responsesGolden = `event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_01","name":"bash"}}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","item_id":"fc_01","delta":"{\"command\":\"id\"}"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"完成"}

event: response.completed
data: {"type":"response.completed","response":{"usage":{"input_tokens":7,"output_tokens":9}}}

`

// TestOpenAIResponsesStreamEvents 验证 Responses API SSE → 统一事件流。
// 令其失败的生产改动：function_call 事件形态未映射、usage 未提取。
func TestOpenAIResponsesStreamEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/responses") {
			t.Errorf("路径 = %s, want /v1/responses", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, responsesGolden)
	}))
	defer srv.Close()

	p := NewOpenAIResponses(Profile{BaseURL: srv.URL, APIKey: "sk-x", Model: "gpt-x"})
	ch, err := p.Stream(context.Background(), Request{
		Model: "gpt-x", MaxTokens: 256,
		Messages: []Message{{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: "跑"}}}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var (
		texts  []string
		starts []ToolUseStart
		frags  = map[string]string{}
		usage  Usage
		sawU   bool
	)
	for ev := range ch {
		switch e := ev.(type) {
		case TextDelta:
			texts = append(texts, e.S)
		case ToolUseStart:
			starts = append(starts, e)
		case ToolInputJSON:
			frags[e.ID] += e.Fragment
		case Usage:
			usage, sawU = e, true
		}
	}
	if strings.Join(texts, "") != "完成" {
		t.Fatalf("文本 = %q", strings.Join(texts, ""))
	}
	if len(starts) != 1 || starts[0].ID != "fc_01" || starts[0].Name != "bash" {
		t.Fatalf("tool 开始 = %+v", starts)
	}
	if frags["fc_01"] != `{"command":"id"}` {
		t.Fatalf("参数 = %q", frags["fc_01"])
	}
	if !sawU || usage.In != 7 || usage.Out != 9 {
		t.Fatalf("usage = %+v", usage)
	}
}
