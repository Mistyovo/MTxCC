package harness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"mtx/internal/llm"
	"mtx/internal/tools"
)

// recordingTool 记录调用输入并返回固定输出（仅测试用）。
type recordingTool struct {
	name    string
	output  string
	fail    bool
	mu      chan struct{}
	callsMu chan struct{}
	inputs  []json.RawMessage
	sem     *sem
}

type sem struct{ ch chan struct{} }

func newRecordingTool(name, output string) *recordingTool {
	return &recordingTool{name: name, output: output, mu: make(chan struct{}, 1)}
}

func (t *recordingTool) Name() string        { return t.name }
func (t *recordingTool) Description() string { return t.name + "（测试）" }
func (t *recordingTool) Schema() map[string]any {
	return map[string]any{"type": "object"}
}
func (t *recordingTool) Flags() tools.Flags { return tools.Flags{Concurrent: true} }
func (t *recordingTool) CheckPerms(_ context.Context, _ json.RawMessage) error { return nil }

func (t *recordingTool) Call(_ context.Context, input json.RawMessage) (string, error) {
	t.mu <- struct{}{}
	t.inputs = append(t.inputs, input)
	<-t.mu
	if t.fail {
		return "", assertErr("boom")
	}
	return t.output, nil
}

func toolUseScript(id, name, args string, stop string) []llm.Event {
	return []llm.Event{
		llm.ToolUseStart{ID: id, Name: name},
		llm.ToolInputJSON{ID: id, Fragment: args},
		llm.MessageDelta{StopReason: stop},
		llm.Usage{In: 10, Out: 5},
	}
}

// TestSessionLoopGolden 三轮金样本：tool_use → 双并发 tool_use → 纯文本完成。
// 断言：工具输入正确、结果按 tool_use_id 回填、Turns/Reason/Usage、transcript 落盘。
// 令其失败的生产改动：tool_result 未配对回填、轮次计数错、事件聚合丢失。
func TestSessionLoopGolden(t *testing.T) {
	bash := newRecordingTool("bash", "hi")
	read := newRecordingTool("read", "FILE_CONTENT")
	list := newRecordingTool("list", "a.txt")

	prov := &scriptProvider{script: [][]llm.Event{
		append([]llm.Event{llm.ThinkingDelta{S: "思考"}}, toolUseScript("t1", "bash", `{"command":"echo hi"}`, "tool_use")...),
		append([]llm.Event{}, // 轮2：两个并发工具（toolUseScript 已含一条 Usage）
			append(toolUseScript("t2", "read", `{"path":"a.txt"}`, ""),
				llm.ToolUseStart{ID: "t3", Name: "list"},
				llm.ToolInputJSON{ID: "t3", Fragment: `{"path":"."}`},
				llm.MessageDelta{StopReason: "tool_use"})...),
		{llm.TextDelta{S: "完"}, llm.TextDelta{S: "成"}, llm.MessageDelta{StopReason: "end_turn"}, llm.Usage{In: 10, Out: 5}},
	}}

	reg := tools.NewRegistry()
	reg.Register(bash)
	reg.Register(read)
	reg.Register(list)

	tr := NewFileTranscript(t.TempDir())
	s := NewSession(Options{
		Provider: prov, Tools: reg, Transcript: tr,
		Model: "test-model", MaxTurns: 10, ToolConcurrency: 10,
	}, "sess-golden")

	res, err := s.Query(context.Background(), "系统提示", "列目录")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	if res.Reason != ReasonCompleted {
		t.Fatalf("Reason = %v, want Completed", res.Reason)
	}
	if res.Turns != 3 {
		t.Fatalf("Turns = %d, want 3", res.Turns)
	}
	if res.FinalText != "完成" {
		t.Fatalf("FinalText = %q", res.FinalText)
	}
	if res.Usage.In != 30 || res.Usage.Out != 15 {
		t.Fatalf("Usage = %+v（应跨轮累计）", res.Usage)
	}

	// 工具收到的输入。
	if len(bash.inputs) != 1 || !jsonEqual(t, bash.inputs[0], `{"command":"echo hi"}`) {
		t.Fatalf("bash 输入 = %v", bash.inputs)
	}
	if len(read.inputs) != 1 || len(list.inputs) != 1 {
		t.Fatalf("read/list 输入数 = %d/%d", len(read.inputs), len(list.inputs))
	}

	// tool_result 按 ID 回填到下一轮请求。
	reqs := prov.capturedReqs()
	if len(reqs) != 3 {
		t.Fatalf("请求数 = %d, want 3", len(reqs))
	}
	turn2 := findToolResults(reqs[1])
	if got := turn2["t1"]; got != "hi" {
		t.Fatalf("轮2请求中 t1 结果 = %q, want hi", got)
	}
	turn3 := findToolResults(reqs[2])
	if turn3["t2"] != "FILE_CONTENT" || turn3["t3"] != "a.txt" {
		t.Fatalf("轮3请求中 t2/t3 结果 = %q/%q", turn3["t2"], turn3["t3"])
	}

	// transcript 落盘（user+assistant×3+tool_result×2 ≥ 6 条）。
	msgs, err := tr.Load("sess-golden")
	if err != nil || len(msgs) < 6 {
		t.Fatalf("transcript = %d 条, err %v, want ≥6", len(msgs), err)
	}
}

// TestSessionResumeContinues 验证 Resume 后 Query 从恢复点继续（轮次计数重置为新增轮数）。
func TestSessionResumeContinues(t *testing.T) {
	prov := &scriptProvider{script: [][]llm.Event{
		{llm.TextDelta{S: "续跑完成"}, llm.MessageDelta{StopReason: "end_turn"}},
	}}
	reg := tools.NewRegistry()
	tr := NewFileTranscript(t.TempDir())

	s := NewSession(Options{Provider: prov, Tools: reg, Transcript: tr, Model: "m", MaxTurns: 5}, "sess-resume")
	s.Resume([]llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "旧问题"}}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "旧回答"}}},
	})

	res, err := s.Query(context.Background(), "sys", "新指令")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Turns != 1 {
		t.Fatalf("恢复后 Turns = %d, want 1", res.Turns)
	}
	// 新请求应携带恢复的历史。
	reqs := prov.capturedReqs()
	if len(reqs) != 1 || len(reqs[0].Messages) != 3 {
		t.Fatalf("请求消息数 = %d, want 3（历史2+新1）", len(reqs[0].Messages))
	}
}

// TestSessionToolErrorAsResult 验证工具执行错误变成 is_error tool_result 回填，不中断循环。
func TestSessionToolErrorAsResult(t *testing.T) {
	bad := newRecordingTool("boom", "")
	bad.fail = true
	prov := &scriptProvider{script: [][]llm.Event{
		toolUseScript("e1", "boom", "{}", "tool_use"),
		{llm.TextDelta{S: "吸收错误继续"}, llm.MessageDelta{StopReason: "end_turn"}},
	}}
	reg := tools.NewRegistry()
	reg.Register(bad)
	tr := NewFileTranscript(t.TempDir())

	s := NewSession(Options{Provider: prov, Tools: reg, Transcript: tr, Model: "m", MaxTurns: 5}, "sess-err")
	res, err := s.Query(context.Background(), "sys", "q")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Reason != ReasonCompleted {
		t.Fatalf("Reason = %v, want Completed", res.Reason)
	}
	// 轮2 请求中的 tool_result 块应带 IsError。
	reqs := prov.capturedReqs()
	foundErr := false
	for _, m := range reqs[1].Messages {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult && b.ToolUseID == "e1" && b.IsError {
				foundErr = true
			}
		}
	}
	if !foundErr {
		t.Fatal("tool 错误应以 is_error 结果回填")
	}
}

// TestSessionMalformedToolInputFallsBack 验证畸形 tool 参数 JSON 兜底 {}。
func TestSessionMalformedToolInputFallsBack(t *testing.T) {
	echo := newRecordingTool("echo", "ok")
	prov := &scriptProvider{script: [][]llm.Event{
		{
			llm.ToolUseStart{ID: "m1", Name: "echo"},
			llm.ToolInputJSON{ID: "m1", Fragment: `{bad json`},
			llm.MessageDelta{StopReason: "tool_use"},
		},
		{llm.TextDelta{S: "done"}, llm.MessageDelta{StopReason: "end_turn"}},
	}}
	reg := tools.NewRegistry()
	reg.Register(echo)

	s := NewSession(Options{Provider: prov, Tools: reg, Transcript: NewFileTranscript(t.TempDir()), Model: "m", MaxTurns: 5}, "sess-malformed")
	if _, err := s.Query(context.Background(), "sys", "q"); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(echo.inputs) != 1 || !jsonEqual(t, echo.inputs[0], `{}`) {
		t.Fatalf("兜底输入 = %v, want {}", echo.inputs)
	}
}

// TestSessionMaxTurns 验证 MaxTurns 到顶后终态。
func TestSessionMaxTurns(t *testing.T) {
	loop := toolUseScript("x", "none", "{}", "tool_use")
	prov := &scriptProvider{}
	for i := 0; i < 10; i++ {
		prov.script = append(prov.script, loop)
	}
	reg := tools.NewRegistry() // 无工具：全部 unknown → is_error 回填，继续耗轮
	s := NewSession(Options{Provider: prov, Tools: reg, Transcript: NewFileTranscript(t.TempDir()), Model: "m", MaxTurns: 3}, "sess-max")
	res, err := s.Query(context.Background(), "sys", "q")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Reason != ReasonMaxTurns {
		t.Fatalf("Reason = %v, want MaxTurns", res.Reason)
	}
	if res.Turns != 3 {
		t.Fatalf("Turns = %d, want 3", res.Turns)
	}
}

// TestSessionTimeout 验证 MaxDuration 超时终态：慢 provider 超过墙钟后按 Timeout 收场。
func TestSessionTimeout(t *testing.T) {
	slow := &slowProvider{delay: 50 * time.Millisecond}
	reg := tools.NewRegistry()
	s := NewSession(Options{
		Provider: slow, Tools: reg, Transcript: NewFileTranscript(t.TempDir()),
		Model: "m", MaxTurns: 100, MaxDuration: 5 * time.Millisecond,
	}, "sess-timeout")
	res, err := s.Query(context.Background(), "sys", "q")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Reason != ReasonTimeout {
		t.Fatalf("Reason = %v, want Timeout", res.Reason)
	}
	_ = strings.TrimSpace("")
}

// slowProvider 每轮流前阻塞 delay（仅测试用）。
type slowProvider struct{ delay time.Duration }

func (p *slowProvider) Stream(ctx context.Context, _ llm.Request) (<-chan llm.Event, error) {
	select {
	case <-time.After(p.delay):
	case <-ctx.Done():
		ch := make(chan llm.Event, 1)
		ch <- &llm.StreamError{Err: ctx.Err()}
		close(ch)
		return ch, nil
	}
	ch := make(chan llm.Event, 2)
	ch <- llm.TextDelta{S: "慢"}
	ch <- llm.MessageDelta{StopReason: "end_turn"}
	close(ch)
	return ch, nil
}

// --- 断言辅助 ---

func jsonEqual(t *testing.T, got json.RawMessage, want string) bool {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		return false
	}
	return jsonEqualVal(a, b)
}

func jsonEqualVal(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

// findToolResults 提取请求里全部 tool_result（ID→文本）。
func findToolResults(req llm.Request) map[string]string {
	out := map[string]string{}
	for _, m := range req.Messages {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult {
				out[b.ToolUseID] = b.Text
			}
		}
	}
	return out
}
