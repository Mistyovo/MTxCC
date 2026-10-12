package harness

import (
	"strings"
	"testing"

	"mtx/internal/llm"
	"mtx/internal/tools"
)

// TestEmptyTurnRecovery 空轮恢复（调研 cai _recover_after_empty_completion）：
// 模型只回 thinking（或空）不能当"完成"——重试并注入提醒，拿到内容才继续。
func TestEmptyTurnRecovery(t *testing.T) {
	echo := newRecordingTool("bash", "ok")
	prov := &scriptProvider{script: [][]llm.Event{
		// 轮1：纯 thinking（mimo/GLM 实测形态：思考用光输出）
		{llm.ThinkingDelta{S: "让我想想这题…"}, llm.MessageDelta{StopReason: "end_turn"}},
		// 轮2：恢复提示后给正常收尾。
		{llm.TextDelta{S: "我需要先跑探测"}, llm.MessageDelta{StopReason: "end_turn"}},
	}}
	reg := tools.NewRegistry()
	reg.Register(echo)

	s := NewSession(Options{
		Provider: prov, Tools: reg, Transcript: NewFileTranscript(t.TempDir()),
		Model: "mimo", MaxTurns: 6,
	}, "sess-empty")
	res, err := s.Query(nil, "sys", "探测目标")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Reason != ReasonCompleted {
		t.Fatalf("Reason = %v", res.Reason)
	}
	// 第二轮请求里必须有恢复提醒（模型被纠正）。
	reqs := prov.capturedReqs()
	if len(reqs) < 2 {
		t.Fatalf("请求数 = %d, want ≥2（空轮须重试）", len(reqs))
	}
	found := false
	for _, m := range reqs[1].Messages {
		for _, b := range m.Blocks {
			if strings.Contains(b.Text, "只有思考") || strings.Contains(b.Text, "输出工具调用或文字结论") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("重试请求缺恢复提醒")
	}
	if res.FinalText == "" {
		t.Fatal("最终文本为空——恢复失败")
	}
}

// TestEmptyTurnExhausted 连续空轮超限 → ModelError 终态（不空转）。
func TestEmptyTurnExhausted(t *testing.T) {
	prov := &scriptProvider{script: [][]llm.Event{
		{llm.ThinkingDelta{S: "空1"}, llm.MessageDelta{StopReason: "end_turn"}},
		{llm.ThinkingDelta{S: "空2"}, llm.MessageDelta{StopReason: "end_turn"}},
		{llm.ThinkingDelta{S: "空3"}, llm.MessageDelta{StopReason: "end_turn"}},
		{llm.ThinkingDelta{S: "空4"}, llm.MessageDelta{StopReason: "end_turn"}},
	}}
	reg := tools.NewRegistry()
	s := NewSession(Options{
		Provider: prov, Tools: reg, Transcript: NewFileTranscript(t.TempDir()),
		Model: "mimo", MaxTurns: 10,
	}, "sess-empty4")
	res, err := s.Query(nil, "sys", "q")
	if err == nil && res.Reason == ReasonCompleted {
		t.Fatal("连续空轮不得判 Completed")
	}
}
