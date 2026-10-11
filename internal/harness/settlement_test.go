package harness

import (
	"strings"
	"testing"

	"mtx/internal/llm"
	"mtx/internal/tools"
)

// TestSettlementWrapUp 验证收尾机制（spec §2 超越点 4 / 计划 Task 9）：
// MaxTurns 耗尽 → 注入收尾提示 + 禁副作用工具（保留白名单写回类）+ 独立轮数；
// 终态仍记 MaxTurns（不伪造 Completed），Settled=true，轮数计入。
func TestSettlementWrapUp(t *testing.T) {
	bash := newRecordingTool("bash", "out")
	rec := newRecordingTool("record_fact", "written")

	prov := &scriptProvider{script: [][]llm.Event{
		toolUseScript("t1", "bash", "{}", "tool_use"),
		toolUseScript("t2", "bash", "{}", "tool_use"),
		// 收尾轮：record_fact 可用；bash 已被禁。
		{
			llm.ToolUseStart{ID: "t3", Name: "record_fact"},
			llm.ToolInputJSON{ID: "t3", Fragment: "{}"},
			llm.ToolUseStart{ID: "t4", Name: "bash"},
			llm.ToolInputJSON{ID: "t4", Fragment: "{}"},
			llm.MessageDelta{StopReason: "tool_use"},
		},
		{llm.TextDelta{S: "收尾完成"}, llm.MessageDelta{StopReason: "end_turn"}},
	}}

	reg := tools.NewRegistry()
	reg.Register(bash)
	reg.Register(rec)

	s := NewSession(Options{
		Provider: prov, Tools: reg, Transcript: NewFileTranscript(t.TempDir()),
		Model: "m", MaxTurns: 2, Settlement: true,
		SettlementKeepTools: []string{"record_fact"},
	}, "sess-settle")

	res, err := s.Query(nil, "sys", "q")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Reason != ReasonMaxTurns {
		t.Fatalf("Reason = %v, want MaxTurns（收尾后终态不伪造 Completed）", res.Reason)
	}
	if !res.Settled {
		t.Fatal("Settled 应为 true")
	}
	if res.Turns != 4 { // 2 常规 + 2 收尾
		t.Fatalf("Turns = %d, want 4", res.Turns)
	}
	if len(rec.inputs) != 1 {
		t.Fatalf("record_fact 调用 = %d, want 1", len(rec.inputs))
	}

	// 收尾轮 bash 被禁：其 tool_result 为 is_error 且提示不可用。
	reqs := prov.capturedReqs()
	var bashDenied bool
	for _, m := range reqs[3].Messages {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult && b.ToolUseID == "t4" && b.IsError &&
				(strings.Contains(b.Text, "不存在") || strings.Contains(b.Text, "不可用") || strings.Contains(b.Text, "禁用")) {
				bashDenied = true
			}
		}
	}
	if !bashDenied {
		t.Fatal("收尾期间 bash 应被禁用并回错误结果")
	}

	// 收尾轮请求的 system 应含收尾指令。
	if !strings.Contains(reqs[2].System, "收尾") {
		t.Fatalf("收尾轮 system 缺收尾指令: %q", reqs[2].System)
	}
}

// TestSettlementAlsoCapsOwnTurns 验证收尾自身轮数上限（防收尾无限续跑）。
func TestSettlementAlsoCapsOwnTurns(t *testing.T) {
	prov := &scriptProvider{}
	for i := 0; i < 12; i++ {
		prov.script = append(prov.script, toolUseScript("x", "nope", "{}", "tool_use"))
	}
	reg := tools.NewRegistry()
	s := NewSession(Options{
		Provider: prov, Tools: reg, Transcript: NewFileTranscript(t.TempDir()),
		Model: "m", MaxTurns: 1, Settlement: true,
	}, "sess-settle-cap")
	res, err := s.Query(nil, "sys", "q")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Reason != ReasonMaxTurns || !res.Settled {
		t.Fatalf("Reason=%v Settled=%v", res.Reason, res.Settled)
	}
	if res.Turns != 11 { // 1 常规 + 10 收尾上限
		t.Fatalf("Turns = %d, want 11", res.Turns)
	}
	_ = llm.Usage{}
}
