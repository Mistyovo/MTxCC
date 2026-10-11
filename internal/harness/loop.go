package harness

import (
	"context"
	"errors"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"mtx/internal/llm"
)

// Query 执行主循环：模型流式响应 → 聚合 assistant 消息 → 并发执行 tool_use →
// 结果按 tool_use_id 回填 → 下一轮；无 tool_use 即完成（spec §6/计划 Task 7）。
func (s *Session) Query(ctx context.Context, system, user string) (QueryResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.opts.MaxDuration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.opts.MaxDuration)
		defer cancel()
	}

	res := QueryResult{}
	s.append(llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: user}}})

	for {
		// 预算检查（轮边界）。收尾阶段换用独立预算。
		budget := s.opts.MaxTurns
		if s.settleMode {
			budget = s.opts.MaxTurns + settleMaxTurns
		}
		if res.Turns >= budget {
			if s.opts.Settlement && !s.settleMode {
				s.beginSettlement(system)
				continue
			}
			res.Reason = ReasonMaxTurns
			res.Settled = s.settleMode
			return res, nil
		}
		if !s.settleMode {
			if err := ctx.Err(); err != nil {
				res.Reason = ReasonTimeout
				return res, nil
			}
		}

		// 压缩（可选）。
		view := s.history
		if s.opts.Compactor != nil {
			var err error
			view, err = s.opts.Compactor.Pre(s.history)
			if err != nil {
				return res, fmt.Errorf("压缩历史: %w", err)
			}
		}

		req := llm.Request{
			Model:     s.opts.Model,
			MaxTokens: s.opts.MaxTokens,
			System:    system,
			Messages:  view,
			Tools:     s.toolSpecs(),
		}
		if s.settleMode {
			req.System = system + settlementPrompt // 收尾指令追加（代码常量）
		}
		ch, err := s.opts.Provider.Stream(ctx, req)
		if err != nil {
			if isPromptTooLong(err) && s.opts.Compactor != nil {
				// Reactive：413/prompt too long → 标记压缩后重试一次（审查 I3）。
				s.opts.Compactor.MarkPromptTooLong()
				view2, cerr := s.opts.Compactor.Pre(s.history)
				if cerr == nil {
					req.Messages = view2
					ch, err = s.opts.Provider.Stream(ctx, req)
				}
			}
			if err != nil {
				res.Reason = ReasonModelError
				return res, fmt.Errorf("模型调用: %w", err)
			}
		}

		assistant, stopReason, usage, streamErr := aggregateStream(ch)
		res.Usage.In += usage.In
		res.Usage.Out += usage.Out
		res.Usage.CacheRead += usage.CacheRead
		if streamErr != nil {
			s.append(assistant) // 已聚合部分仍留痕
			if ctx.Err() != nil {
				res.Reason = ReasonTimeout // 墙钟到期导致的断流按超时归类
				return res, nil
			}
			if isPromptTooLong(streamErr) && s.opts.Compactor != nil {
				s.opts.Compactor.MarkPromptTooLong()
				view2, cerr := s.opts.Compactor.Pre(s.history)
				if cerr == nil {
					_ = view2 // 流中 413：下一轮循环压缩生效（Pre 在轮首调用）
				}
			}
			res.Reason = ReasonModelError
			return res, fmt.Errorf("流中断: %w", streamErr)
		}
		// 文本协议工具调用（弱模型 fallback）：文本里嵌 tool_use 块 → 注入标准
		// tool_use blocks（调研 T3MP3ST 文本协议设计）。在落盘前规范化，
		// 保证历史/轮次计数单一路径。
		if countToolUse(assistant) == 0 {
			if calls := ParseTextToolCalls(textOf(assistant)); len(calls) > 0 {
				for i, c := range calls {
					assistant.Blocks = append(assistant.Blocks, llm.Block{
						Type:      llm.BlockToolUse,
						ID:        textToolID(i),
						Name:      c.Name,
						InputJSON: c.InputJSON,
					})
				}
			}
		}
		s.append(assistant)
		res.Turns++

		// 回合后墙钟检查：超时优先于"完成"上报（终态诚实，spec §4 终态枚举）。
		if err := ctx.Err(); err != nil {
			res.Reason = ReasonTimeout
			res.FinalText = textOf(assistant)
			return res, nil
		}

		// 无工具调用：本轮即最终回复。
		if countToolUse(assistant) == 0 {
			res.FinalText = textOf(assistant)
			if s.settleMode {
				res.Reason = ReasonMaxTurns // 收尾后终态不伪造 Completed（spec §2）
				res.Settled = true
			} else {
				res.Reason = reasonFromStop(stopReason)
			}
			return res, nil
		}

		// 并发执行工具并回填。
		results := s.execTools(ctx, assistant)
		s.append(llm.Message{Role: llm.RoleUser, Blocks: results})
	}
}

// append 追加消息到历史与 transcript。
func (s *Session) append(m llm.Message) {
	s.history = append(s.history, m)
	if s.opts.Transcript != nil {
		_ = s.opts.Transcript.Append(s.id, m)
	}
}

// toolSpecs 从注册表生成请求工具定义。
func (s *Session) toolSpecs() []llm.ToolSpec {
	if s.opts.Tools == nil {
		return nil
	}
	list := s.opts.Tools.List()
	specs := make([]llm.ToolSpec, 0, len(list))
	for _, t := range list {
		specs = append(specs, llm.ToolSpec{Name: t.Name(), Description: t.Description(), InputSchema: t.Schema()})
	}
	return specs
}

// execTools 并发执行 assistant 消息中的 tool_use 块，返回按到达序排列的 tool_result 块。
// 错误一律转为 is_error 的 tool_result（不中断循环）；畸形参数 JSON 兜底 {}。
func (s *Session) execTools(ctx context.Context, assistant llm.Message) []llm.Block {
	var (
		uses []llm.Block
	)
	for _, b := range assistant.Blocks {
		if b.Type == llm.BlockToolUse {
			uses = append(uses, b)
		}
	}
	results := make([]llm.Block, len(uses))
	sem := make(chan struct{}, s.opts.ToolConcurrency)
	exclusive := make(chan struct{}, 1) // 非并发工具（如 bash）独占（审查 I11）
	var wg sync.WaitGroup
	for i, u := range uses {
		wg.Add(1)
		go func(i int, u llm.Block) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if t, ok := s.opts.Tools.Get(u.Name); ok && !t.Flags().Concurrent {
				exclusive <- struct{}{}
				defer func() { <-exclusive }()
			}
			results[i] = s.execOneTool(ctx, u)
		}(i, u)
	}
	wg.Wait()
	return results
}

func (s *Session) execOneTool(ctx context.Context, u llm.Block) llm.Block {
	mk := func(text string, isErr bool) llm.Block {
		return llm.Block{Type: llm.BlockToolResult, ToolUseID: u.ID, Text: text, IsError: isErr}
	}

	input := json.RawMessage(u.InputJSON)
	if len(input) == 0 || !json.Valid(input) {
		input = json.RawMessage("{}") // 畸形兜底
	}

	var toolName string
	// 工具不存在 → 直接错误结果。
	if s.opts.Tools == nil {
		return mk(fmt.Sprintf("工具 %q 不存在（当前无可用工具）", u.Name), true)
	}
	t, ok := s.opts.Tools.Get(u.Name)
	if !ok {
		avail := make([]string, 0)
		for _, x := range s.opts.Tools.List() {
			avail = append(avail, x.Name())
		}
		return mk(fmt.Sprintf("工具 %q 不存在。可用工具：%s", u.Name, strings.Join(avail, ", ")), true)
	}
	toolName = t.Name()

	// 审批门（可选）。拒绝也走 OnToolCall（activity 需要留痕被拦截的动作）。
	if s.opts.Perms != nil {
		replaced, err := s.opts.Perms.PreToolUse(ctx, toolName, input)
		if err != nil {
			denyMsg := fmt.Sprintf("【MTX 平台管控·非目标防御】拒绝执行：%v", err)
			if s.opts.OnToolCall != nil {
				s.opts.OnToolCall(toolName, string(input), denyMsg, true)
			}
			return mk(denyMsg, true)
		}
		if len(replaced) > 0 {
			input = replaced
		}
	}

	out, err := t.Call(ctx, input)

	if s.opts.Perms != nil {
		s.opts.Perms.PostToolUse(ctx, toolName, string(input), out)
	}
	if s.opts.OnToolCall != nil {
		s.opts.OnToolCall(toolName, string(input), out, err != nil)
	}
	if err != nil {
		return mk(err.Error(), true)
	}
	return mk(out, false)
}

// aggregateStream 把流聚合成一条 assistant 消息：thinking → text → tool_use（到达序）。
// 返回错误表示流中断（StreamError）。
func aggregateStream(ch <-chan llm.Event) (msg llm.Message, stopReason string, usage llm.Usage, streamErr error) {
	var (
		text      strings.Builder
		thinking  strings.Builder
		toolOrder []string
		toolJSON  = map[string]*strings.Builder{}
		toolMeta  = map[string]string{}
	)
	msg.Role = llm.RoleAssistant

	for ev := range ch {
		switch e := ev.(type) {
		case llm.ThinkingDelta:
			thinking.WriteString(e.S)
		case llm.TextDelta:
			text.WriteString(e.S)
		case llm.ToolUseStart:
			toolOrder = append(toolOrder, e.ID)
			toolJSON[e.ID] = &strings.Builder{}
			toolMeta[e.ID] = e.Name
		case llm.ToolInputJSON:
			if b, ok := toolJSON[e.ID]; ok {
				b.WriteString(e.Fragment)
			}
		case llm.MessageDelta:
			stopReason = e.StopReason
		case llm.Usage:
			usage.In += e.In
			usage.Out += e.Out
			usage.CacheRead += e.CacheRead
		case *llm.StreamError:
			streamErr = e.Err
		}
	}

	if thinking.Len() > 0 {
		msg.Blocks = append(msg.Blocks, llm.Block{Type: llm.BlockThinking, Text: thinking.String()})
	}
	if text.Len() > 0 {
		msg.Blocks = append(msg.Blocks, llm.Block{Type: llm.BlockText, Text: text.String()})
	}
	for _, id := range toolOrder {
		msg.Blocks = append(msg.Blocks, llm.Block{
			Type: llm.BlockToolUse, ID: id, Name: toolMeta[id], InputJSON: toolJSON[id].String(),
		})
	}
	return msg, stopReason, usage, streamErr
}

// isPromptTooLong 识别 413/prompt too long 形态（跨协议）。
func isPromptTooLong(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrPromptTooLong) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "prompt is too long") ||
		strings.Contains(msg, "prompt too long") ||
		strings.Contains(msg, "context length exceeded") ||
		strings.Contains(msg, "413") ||
		strings.Contains(msg, "maximum context length")
}

func countToolUse(m llm.Message) int {
	n := 0
	for _, b := range m.Blocks {
		if b.Type == llm.BlockToolUse {
			n++
		}
	}
	return n
}

func textOf(m llm.Message) string {
	var sb strings.Builder
	for _, b := range m.Blocks {
		if b.Type == llm.BlockText {
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

func reasonFromStop(stop string) TerminalReason {
	switch stop {
	case "end_turn", "stop", "max_tokens", "length":
		return ReasonCompleted
	case "":
		return ReasonCompleted
	default:
		return ReasonCompleted // tool_use 之外的罕见 stop 原因按完成处理
	}
}

// textToolID 为文本协议工具调用生成 ID。
func textToolID(i int) string {
	return "ttool-" + string(rune('a'+i)) + "-auto"
}
