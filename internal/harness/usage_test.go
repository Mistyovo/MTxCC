package harness

import (
	"sync"
	"testing"

	"mtx/internal/llm"
	"mtx/internal/tools"
)

// TestOnUsageCallback Usage 事件必须经 OnUsage 回调（成本记账，实测 tokens_used=0 的根因）。
func TestOnUsageCallback(t *testing.T) {
	echo := newRecordingTool("bash", "ok")
	prov := &scriptProvider{script: [][]llm.Event{
		toolUseScriptUsage("t1", "bash", "{}", 100, 50),
		{llm.TextDelta{S: "完"}, llm.MessageDelta{StopReason: "end_turn"}, llm.Usage{In: 20, Out: 10}},
	}}
	reg := tools.NewRegistry()
	reg.Register(echo)

	var mu sync.Mutex
	var got llm.Usage
	s := NewSession(Options{
		Provider: prov, Tools: reg, Transcript: NewFileTranscript(t.TempDir()),
		Model: "m", MaxTurns: 5,
		OnUsage: func(u llm.Usage) {
			mu.Lock()
			got.In += u.In
			got.Out += u.Out
			mu.Unlock()
		},
	}, "sess-usage")
	if _, err := s.Query(nil, "sys", "q"); err != nil {
		t.Fatalf("Query: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got.In == 0 || got.Out == 0 {
		t.Fatalf("OnUsage 未回调: %+v", got)
	}
}

// toolUseScriptUsage 带 usage 的工具轮。
func toolUseScriptUsage(id, name, args string, in, out int) []llm.Event {
	return []llm.Event{
		llm.ToolUseStart{ID: id, Name: name},
		llm.ToolInputJSON{ID: id, Fragment: args},
		llm.MessageDelta{StopReason: "tool_use"},
		llm.Usage{In: in, Out: out},
	}
}
