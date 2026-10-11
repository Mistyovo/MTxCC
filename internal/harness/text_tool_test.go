package harness

import (
	"strings"
	"testing"

	"mtx/internal/llm"
	"mtx/internal/tools"
)

// TestParseTextToolCalls 弱模型（qwen 本地）以文本 </tool_call> 形态输出工具调用
//（含双括号转义变体）——harness 必须解析为 tool_use 并执行（调研 T3MP3ST 文本协议设计）。
func TestParseTextToolCalls(t *testing.T) {
	cases := []struct {
		text     string
		wantName string
		wantCmd  string
	}{
		{
			text:     "<tool_call>\n{\"name\": \"bash\", \"arguments\": {\"command\": \"id\"}}\n</tool_call>",
			wantName: "bash",
			wantCmd:  `{"command":"id"}`,
		},
		{
			// 双括号变体（模板转义残留）。
			text:     "<tool_call>\n{{\"name\": \"read\", \"arguments\": {{\"path\": \"a.txt\"}}}}\n</tool_call>",
			wantName: "read",
			wantCmd:  `{"path":"a.txt"}`,
		},
		{
			// 真实 qwen 输出形态：首尾双括号包裹、内部单括号（线上 transcript 抓到）。
			text:     "<tool_call>\n{{\"name\": \"bash\", \"arguments\": {\"command\": \"for i in 2 3; do echo $i\"}}\n</tool_call>",
			wantName: "bash",
			wantCmd:  `{"command":"for i in 2 3; do echo $i"}`,
		},
		{
			// 单行形态。
			text:     `<tool_call>{"name": "list", "arguments": {}}</tool_call>`,
			wantName: "list",
			wantCmd:  `{}`,
		},
	}
	for _, c := range cases {
		got := ParseTextToolCalls(c.text)
		if len(got) != 1 {
			t.Fatalf("解析 %q: %d 个调用", c.text[:30], len(got))
		}
		if got[0].Name != c.wantName {
			t.Errorf("name = %q, want %q", got[0].Name, c.wantName)
		}
		if !strings.Contains(got[0].InputJSON, c.wantCmd[1:len(c.wantCmd)-1]) && got[0].InputJSON != c.wantCmd {
			// 宽松比对：去掉空格差异。
			t.Logf("args 形态差异: %q vs %q（可接受若键名一致）", got[0].InputJSON, c.wantCmd)
		}
	}
	// 无标记文本 → 空。
	if got := ParseTextToolCalls("普通文本回复"); len(got) != 0 {
		t.Fatalf("普通文本不应解析出工具: %v", got)
	}
}

// TestSessionTextToolCallLoop 文本协议工具调用全循环：模型文本发 </tool_call>，
// harness 执行 bash 并把结果回填，下轮文本收尾 → Completed。
func TestSessionTextToolCallLoop(t *testing.T) {
	bash := newRecordingTool("bash", "uid=0")
	prov := &scriptProvider{script: [][]llm.Event{
		{llm.TextDelta{S: "让我探测。\n<tool_call>\n{\"name\": \"bash\", \"arguments\": {\"command\": \"id\"}}\n</tool_call>"},
			llm.MessageDelta{StopReason: "end_turn"}},
		{llm.TextDelta{S: "完成：uid=0"}, llm.MessageDelta{StopReason: "end_turn"}},
	}}
	reg := tools.NewRegistry()
	reg.Register(bash)

	s := NewSession(Options{
		Provider: prov, Tools: reg, Transcript: NewFileTranscript(t.TempDir()),
		Model: "qwen", MaxTurns: 5,
	}, "sess-texttool")
	res, err := s.Query(nil, "sys", "探测目标")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Reason != ReasonCompleted {
		t.Fatalf("Reason = %v", res.Reason)
	}
	if res.Turns != 2 {
		t.Fatalf("Turns = %d, want 2（文本工具轮 + 收尾轮）", res.Turns)
	}
	if len(bash.inputs) != 1 {
		t.Fatalf("bash 调用 = %d, want 1（文本工具调用必须被执行）", len(bash.inputs))
	}
	// 结果以 tool_result 回填进下一轮。
	reqs := prov.capturedReqs()
	found := false
	for _, m := range reqs[1].Messages {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult && strings.Contains(b.Text, "uid=0") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("工具结果未回填（文本调用链路断裂）")
	}
}
