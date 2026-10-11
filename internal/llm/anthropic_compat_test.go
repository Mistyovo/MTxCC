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

// wireRecorder 抓取请求 wire body 供断言（GLM 兼容回归测试）。
type wireRecorder struct {
	body string
}

func (w *wireRecorder) handler(t *testing.T) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.body = string(b)
		rw.Header().Set("Content-Type", "text/event-stream")
		sse := "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
		_, _ = rw.Write([]byte(sse))
	}
}

// TestAnthropicModelFallback 引擎传 "auto" 时必须用 profile 模型名（线上 bug：发 model:"auto" 被拒）。
func TestAnthropicModelFallback(t *testing.T) {
	rec := &wireRecorder{}
	srv := httptest.NewServer(rec.handler(t))
	defer srv.Close()

	p := NewAnthropic(Profile{BaseURL: srv.URL, APIKey: "k", Model: "glm-5.3"})
	for _, model := range []string{"auto", ""} {
		ch, err := p.Stream(context.Background(), Request{
			Model: model, MaxTokens: 16,
			Messages: []Message{{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: "hi"}}}},
		})
		if err != nil {
			t.Fatalf("Stream(%q): %v", model, err)
		}
		for range ch {
		}
		var wire struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal([]byte(rec.body), &wire)
		if wire.Model != "glm-5.3" {
			t.Fatalf("req.Model=%q 时 wire model = %q, want glm-5.3", model, wire.Model)
		}
	}
	// 显式模型名仍透传。
	ch, _ := p.Stream(context.Background(), Request{
		Model: "claude-opus-4", MaxTokens: 16,
		Messages: []Message{{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: "hi"}}}},
	})
	for range ch {
	}
	var wire struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal([]byte(rec.body), &wire)
	if wire.Model != "claude-opus-4" {
		t.Fatalf("显式模型未透传: %q", wire.Model)
	}
}

// TestAnthropicStripsThinkingAndEmptyMessages GLM 兼容（线上 1214 回归）：
// ① thinking 块不得出现在请求 wire body；② 仅含 thinking 的 assistant 消息整条跳过
//（剥离后 content:[] 为空数组会被 GLM 判为 messages 参数非法）。
func TestAnthropicStripsThinkingAndEmptyMessages(t *testing.T) {
	rec := &wireRecorder{}
	srv := httptest.NewServer(rec.handler(t))
	defer srv.Close()

	p := NewAnthropic(Profile{BaseURL: srv.URL, APIKey: "k", Model: "glm-5.3"})
	ch, err := p.Stream(context.Background(), Request{
		Model: "glm-5.3", MaxTokens: 16,
		Messages: []Message{
			{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: "hello"}}},
			// 仅 thinking 的 assistant（模型纯思考轮）→ 整条跳过。
			{Role: RoleAssistant, Blocks: []Block{{Type: BlockThinking, Text: "让我想想…"}}},
			// thinking + tool_use → 只发 tool_use。
			{Role: RoleAssistant, Blocks: []Block{
				{Type: BlockThinking, Text: "思考"},
				{Type: BlockToolUse, ID: "t1", Name: "bash", InputJSON: `{"command":"id"}`},
			}},
			{Role: RoleUser, Blocks: []Block{{Type: BlockToolResult, ToolUseID: "t1", Text: "ok"}}},
		},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range ch {
	}

	if strings.Contains(rec.body, `"type":"thinking"`) || strings.Contains(rec.body, "让我想想") {
		t.Fatalf("thinking 泄漏到 wire body: %s", rec.body)
	}
	// 空 content 数组检查：每个 content 都必须非空。
	var wire struct {
		Messages []struct {
			Content []json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(rec.body), &wire); err != nil {
		t.Fatalf("wire body 解析: %v\n%s", err, rec.body)
	}
	for i, m := range wire.Messages {
		if len(m.Content) == 0 {
			t.Fatalf("第 %d 条消息 content 为空数组（GLM 1214 触发点）", i)
		}
	}
	if len(wire.Messages) != 3 {
		t.Fatalf("消息数 = %d, want 3（纯思考轮应跳过）", len(wire.Messages))
	}
}
