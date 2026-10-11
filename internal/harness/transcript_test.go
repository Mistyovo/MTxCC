package harness

import (
	"os"
	"path/filepath"
	"testing"

	"mtx/internal/llm"
)

// TestTranscriptRoundtrip 验证 Append→Load 往返一致。
// 令其失败的生产改动：序列化丢字段、文件名拼接错误。
func TestTranscriptRoundtrip(t *testing.T) {
	dir := t.TempDir()
	ts := NewFileTranscript(dir)

	msgs := []llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "列目录"}}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{
			{Type: llm.BlockToolUse, ID: "t1", Name: "bash", InputJSON: `{"command":"ls"}`},
		}},
		{Role: llm.RoleUser, Blocks: []llm.Block{
			{Type: llm.BlockToolResult, ToolUseID: "t1", Text: "a.txt"},
		}},
	}
	for _, m := range msgs {
		if err := ts.Append("sess-1", m); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	got, err := ts.Load("sess-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != len(msgs) {
		t.Fatalf("消息数 = %d, want %d", len(got), len(msgs))
	}
	for i := range msgs {
		if got[i].Role != msgs[i].Role || len(got[i].Blocks) != len(msgs[i].Blocks) {
			t.Fatalf("第 %d 条不一致: %+v vs %+v", i, got[i], msgs[i])
		}
		for j := range msgs[i].Blocks {
			a, b := got[i].Blocks[j], msgs[i].Blocks[j]
			if a.Type != b.Type || a.ID != b.ID || a.ToolUseID != b.ToolUseID || a.Text != b.Text || a.InputJSON != b.InputJSON {
				t.Fatalf("第 %d 条第 %d 块不一致: %+v vs %+v", i, j, a, b)
			}
		}
	}

	// 文件确实落盘为 JSONL。
	path := filepath.Join(dir, "sess-1.jsonl")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("文件未落盘: %v", err)
	}
}

// TestSanitizeTailRemovesDanglingToolUse 验证恢复前清理：尾部无 tool_result 配对的 tool_use 被删除，
// 有效前缀（含已有配对）完整保留。
// 令其失败的生产改动：清理逻辑误删全部、或保留悬空块导致 API 400。
func TestSanitizeTailRemovesDanglingToolUse(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "q"}}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{
			{Type: llm.BlockToolUse, ID: "ok1", Name: "bash", InputJSON: "{}"}, // 有配对
		}},
		{Role: llm.RoleUser, Blocks: []llm.Block{
			{Type: llm.BlockToolResult, ToolUseID: "ok1", Text: "done"},
		}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{ // 悬空：崩溃时未回填
			{Type: llm.BlockToolUse, ID: "dang1", Name: "read", InputJSON: "{}"},
		}},
	}
	got := SanitizeTail(msgs)

	// 收集剩余 tool_use / tool_result 配对。
	seenUse := map[string]bool{}
	seenResult := map[string]bool{}
	for _, m := range got {
		for _, b := range m.Blocks {
			switch b.Type {
			case llm.BlockToolUse:
				seenUse[b.ID] = true
			case llm.BlockToolResult:
				seenResult[b.ToolUseID] = true
			}
		}
	}
	if seenUse["dang1"] {
		t.Fatal("悬空 tool_use dang1 应被删除")
	}
	if !seenUse["ok1"] || !seenResult["ok1"] {
		t.Fatal("有效配对 ok1 不应被删除")
	}
	if len(got) != 3 {
		t.Fatalf("清理后消息数 = %d, want 3（悬空块整条消息移除）", len(got))
	}
}
