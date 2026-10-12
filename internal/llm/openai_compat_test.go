package llm

import (
	"context"
	"strings"
	"testing"
)

// TestOpenAIWireBodyNoStreamOptions 网关兼容（线上实测 mimo 对 stream_options 挂起）：
// 默认请求体不得含 stream_options——usage 统计改为从普通分片收集。
func TestOpenAIWireBodyNoStreamOptions(t *testing.T) {
	rec := &wireRecorder{}
	srv := startRecorder(rec, t)
	defer srv.Close()

	p := NewOpenAI(Profile{BaseURL: srv.URL, APIKey: "k", Model: "m"})
	ch, _ := p.Stream(context.Background(), Request{
		Model: "m", MaxTokens: 16,
		Messages: []Message{{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: "hi"}}}},
	})
	for range ch {
	}
	if strings.Contains(rec.body, "stream_options") {
		t.Fatalf("wire body 含 stream_options（mimo 不支持会挂起）: %s", rec.body)
	}
}
