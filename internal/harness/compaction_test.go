package harness

import (
	"context"
	"strings"
	"testing"

	"mtx/internal/llm"
)

func msgWithResults(ids ...string) llm.Message {
	m := llm.Message{Role: llm.RoleUser}
	for _, id := range ids {
		m.Blocks = append(m.Blocks, llm.Block{Type: llm.BlockToolResult, ToolUseID: id, Text: "R_" + id + "_" + strings.Repeat("x", 20)})
	}
	return m
}

// TestMicroCompactKeepsLastFiveResults 验证 Micro：清旧工具结果体，只保留最近 5 条。
// 令其失败的生产改动：保留条数错误、清错块类型。
func TestMicroCompactKeepsLastFiveResults(t *testing.T) {
	c := NewCompactor(nil, 1_000_000)
	msgs := []llm.Message{
		msgWithResults("a", "b"), // 旧
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "文本不动作"}}},
		msgWithResults("c"),
		msgWithResults("d", "e"),
		msgWithResults("f"), // 第 6 条 → 应被清
	}
	view, err := c.Pre(msgs)
	if err != nil {
		t.Fatalf("Pre: %v", err)
	}
	bodies := map[string]string{}
	for _, m := range view {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult {
				bodies[b.ToolUseID] = b.Text
			}
		}
	}
	// 最近 5 条：b,c,d,e,f 保留原文；a 被清。
	if !strings.Contains(bodies["f"], "R_f_") {
		t.Fatalf("最近结果 f 被误清: %q", bodies["f"])
	}
	if strings.Contains(bodies["a"], "R_a_") {
		t.Fatalf("最旧结果 a 应被清体: %q", bodies["a"])
	}
	// 非破坏：原历史未变。
	if got := msgs[0].Blocks[0].Text; !strings.Contains(got, "R_a_") {
		t.Fatalf("原历史被改动: %q", got)
	}
}

// TestAutoCompactSummarizesOld 验证 Auto：超阈值 → 旧消息折叠为摘要块，最近 2 轮原样。
// 令其失败的生产改动：阈值公式错、未保留最近轮、破坏原历史。
func TestAutoCompactSummarizesOld(t *testing.T) {
	var summarized []llm.Message
	c := NewCompactor(func(_ context.Context, old []llm.Message) (string, error) {
		summarized = old
		return "SUMMARY_OF_OLD", nil
	}, 60_000) // 阈值 = 60000 − 8192 − 30000 ≈ 21808 token ≈ 87K 字符

	msgs := []llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "早 " + strings.Repeat("a", 40000)}}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: strings.Repeat("b", 40000)}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: strings.Repeat("c", 40000)}}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "近轮1"}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: strings.Repeat("d", 40000)}}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "近轮2"}}},
	}
	view, err := c.Pre(msgs)
	if err != nil {
		t.Fatalf("Pre: %v", err)
	}
	if len(summarized) == 0 {
		t.Fatal("summarizer 未被调用")
	}
	var hasSummary bool
	for _, m := range view {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockText && strings.Contains(b.Text, "SUMMARY_OF_OLD") {
				hasSummary = true
			}
		}
	}
	if !hasSummary {
		t.Fatal("视图缺摘要块")
	}
	// 最近 2 轮原样保留（近轮1 所在 assistant 起的全部消息）。
	found := false
	for _, m := range view {
		for _, b := range m.Blocks {
			if b.Text == "近轮2" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("最近轮被折叠")
	}
	// 原历史非破坏。
	if msgs[0].Blocks[0].Text == "SUMMARY_OF_OLD" {
		t.Fatal("原历史被破坏")
	}
}

// TestReactiveCompactRetriesOnce 验证 Reactive：413 标记 → 强制压缩一次；
// 再标记 → Pre 返回 ErrPromptTooLong（只重试一次，不无限循环）。
func TestReactiveCompactRetriesOnce(t *testing.T) {
	c := NewCompactor(func(_ context.Context, _ []llm.Message) (string, error) {
		return "S", nil
	}, 1_000_000)
	msgs := []llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "q1"}}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "a1"}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "q2"}}},
	}

	c.MarkPromptTooLong()
	view, err := c.Pre(msgs)
	if err != nil {
		t.Fatalf("第一次强制压缩: %v", err)
	}
	if len(view) >= len(msgs) {
		t.Fatalf("强制压缩后视图未变小: %d", len(view))
	}

	c.MarkPromptTooLong()
	if _, err := c.Pre(msgs); err == nil {
		t.Fatal("第二次应返回 ErrPromptTooLong")
	}
}
