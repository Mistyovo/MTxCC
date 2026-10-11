package harness

import (
	"context"
	"errors"
	"sort"
	"sync"

	"mtx/internal/llm"
)

// ErrPromptTooLong 表示压缩后仍超窗（Reactive 只重试一次，spec Review Focus 4）。
var ErrPromptTooLong = errors.New("harness: 压缩后仍超上下文窗口")

// Compactor 三层压缩（spec §4.4）：
//   - Micro：清旧工具结果体（保留最近 5 条 tool_result 原文）
//   - Auto：估算超 window − min(maxOut,20K) − 30K 时折叠旧消息为摘要块（非破坏投影）
//   - Reactive：MarkPromptTooLong 后强制全量压缩一次；再标记即 ErrPromptTooLong
type Compactor struct {
	summarizer func(ctx context.Context, old []llm.Message) (string, error)
	windowTok  int
	maxOutTok  int

	mu            sync.Mutex
	reactiveArmed bool // 已强制压缩过一次
	reactiveMark  bool // 收到 413 标记待处理
}

// NewCompactor 建压缩器；summarizer 为 nil 时 Auto/Reactive 用占位摘要。
func NewCompactor(summarizer func(ctx context.Context, old []llm.Message) (string, error), windowTokens int) *Compactor {
	return &Compactor{
		summarizer: summarizer,
		windowTok:  windowTokens,
		maxOutTok:  8192,
	}
}

// MarkPromptTooLong 记录一次 413 信号；若已重试过（armed），下一次 Pre 报 ErrPromptTooLong。
func (c *Compactor) MarkPromptTooLong() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reactiveMark = true
}

// Pre 返回发送给模型的投影视图（不修改传入历史）。
func (c *Compactor) Pre(msgs []llm.Message) ([]llm.Message, error) {
	c.mu.Lock()
	marked, armed := c.reactiveMark, c.reactiveArmed
	c.mu.Unlock()

	// Reactive：已重试过一次仍收到标记 → 放弃。
	if marked && armed {
		return nil, ErrPromptTooLong
	}
	if marked {
		// 全量压缩：折叠全部历史，仅保留最后一条消息（通常是最新 user 输入）。
		cut := len(msgs) - 1
		if cut < 0 {
			cut = 0
		}
		view, err := c.compress(msgs, cut)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.reactiveMark = false
		c.reactiveArmed = true
		c.mu.Unlock()
		return view, nil
	}

	view := microCompact(msgs)

	// Auto：估算超阈值 → 折叠（保留最近 2 轮）。
	threshold := c.windowTok - min(c.maxOutTok, 20_000) - 30_000
	if estimateTokens(view) > threshold {
		cut := keepLastRounds(view, 2)
		if cut > 0 {
			return c.compress(view, cut)
		}
	}
	return view, nil
}

// compress 把 msgs[:cut] 折叠为单条摘要消息 + 保留后缀。
func (c *Compactor) compress(msgs []llm.Message, cut int) ([]llm.Message, error) {
	summary := "[compact] 历史已压缩（无 summarizer）"
	if c.summarizer != nil {
		s, err := c.summarizer(context.Background(), msgs[:cut])
		if err != nil {
			return nil, err
		}
		summary = s
	}
	out := make([]llm.Message, 0, cut+1)
	out = append(out, llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{
		{Type: llm.BlockText, Text: "[compact] " + summary},
	}})
	return append(out, msgs[cut:]...), nil
}

// microCompact 返回清了旧工具结果体的视图（最近 5 条保留，其余置占位）。
func microCompact(msgs []llm.Message) []llm.Message {
	// 收集全部 tool_result 块的定位与顺序。
	type loc struct{ mi, bi int }
	var order []loc
	for i, m := range msgs {
		for j, b := range m.Blocks {
			if b.Type == llm.BlockToolResult {
				order = append(order, loc{i, j})
			}
		}
	}
	if len(order) <= 5 {
		out := make([]llm.Message, len(msgs))
		copy(out, msgs)
		return out
	}
	cleared := map[loc]bool{}
	for _, l := range order[:len(order)-5] {
		cleared[l] = true
	}
	out := make([]llm.Message, len(msgs))
	copy(out, msgs)
	for l := range cleared {
		blocks := make([]llm.Block, len(out[l.mi].Blocks))
		copy(blocks, out[l.mi].Blocks)
		blocks[l.bi].Text = "[cleared]"
		out[l.mi].Blocks = blocks
	}
	return out
}

// keepLastRounds 返回保留最近 n 轮（轮 = assistant 及其后消息）的切割下标。
func keepLastRounds(msgs []llm.Message, n int) int {
	var assistantIdx []int
	for i, m := range msgs {
		if m.Role == llm.RoleAssistant {
			assistantIdx = append(assistantIdx, i)
		}
	}
	if len(assistantIdx) <= n {
		return 0
	}
	sort.Ints(assistantIdx)
	return assistantIdx[len(assistantIdx)-n]
}

// keepLastAssistant 返回最后一条 assistant 的下标（无则 0）。
func keepLastAssistant(msgs []llm.Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleAssistant {
			return i
		}
	}
	return 0
}

// estimateTokens 粗估 token 数（字符/4）。
func estimateTokens(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		for _, b := range m.Blocks {
			n += len(b.Text) + len(b.InputJSON)
		}
	}
	return n / 4
}
