package harness

import (
	"context"
	"encoding/json"
	"time"

	"mtx/internal/llm"
	"mtx/internal/tools"
)

// TerminalReason 是一次 Query 的终态（spec §7 接口）。
type TerminalReason string

const (
	ReasonCompleted   TerminalReason = "Completed"
	ReasonMaxTurns    TerminalReason = "MaxTurns"
	ReasonTimeout     TerminalReason = "Timeout"
	ReasonModelError  TerminalReason = "ModelError"
	ReasonPromptTooLong TerminalReason = "PromptTooLong"
	ReasonAborted     TerminalReason = "Aborted"
)

// QueryResult 是 Query 的返回。
type QueryResult struct {
	Reason    TerminalReason
	FinalText string
	Turns     int
	Usage     llm.Usage
	Settled   bool // 是否经过收尾阶段（Task 9）
}

// PermissionChecker 是工具执行前后的审批门（Task 13 实现具体 guard；
// PreToolUse 返回替换后的 input 供 modify 审批采用）。
type PermissionChecker interface {
	PreToolUse(ctx context.Context, tool string, input json.RawMessage) (json.RawMessage, error)
	PostToolUse(ctx context.Context, tool string, input, result string)
}

// Options 配置一次会话循环。
type Options struct {
	Provider       llm.Provider
	Tools          *tools.Registry
	Transcript     TranscriptStore
	Compactor      *Compactor // nil 表示不压缩
	Perms          PermissionChecker
	Model          string        // 请求所用模型名（透传 Provider）
	MaxTokens      int           // 单回复输出上限（0 = 不限制发；providers 兜底 1，务必设置）
	MaxTurns       int           // 0 = 默认 40
	MaxDuration    time.Duration // 0 = 不限
	ToolConcurrency int          // 0 = 默认 10
	System         string        // Query 可覆盖

	// Settlement 收尾（spec §2 超越点 4）：预算命中时注入收尾指令、禁副作用工具、
	// 换独立轮数预算（10 轮）让 agent 把已识别内容写回后再结束；终态仍记 MaxTurns/Timeout。
	Settlement          bool
	SettlementKeepTools []string // 收尾期间保留的非只读工具（写回类：record_fact 等）

	// OnToolCall 工具级事件回调（引擎接 activity 落库）；可为 nil。
	OnToolCall func(tool, input, result string, isErr bool)

	// OnUsage 每轮用量回调（成本记账 → llm_records）；可为 nil。
	OnUsage func(u llm.Usage)
}

// settlementPrompt 是收尾阶段注入的固定追加段（代码常量，DB 不可编辑）。
const settlementPrompt = `
【收尾阶段】你的常规预算已耗尽。现在只做一件事：把本次已经确认的知识写回——
用可用的写回工具（如 record_fact / insert_assets / report_finding）把已识别的
事实、资产与发现落库，然后输出一段简短总结文本结束。禁止再执行任何探测或命令。
写回完成或无内容可写时，立即以纯文本结束。`

// Session 持有一个 agent 会话的历史与循环选项。
type Session struct {
	opts       Options
	id         string
	history    []llm.Message
	settleMode bool
}

// settleMaxTurns 是收尾阶段的独立轮数预算（spec §2 超越点 4）。
const settleMaxTurns = 10

// beginSettlement 进入收尾：置位 + 禁用副作用工具（保留只读与白名单写回类）。
func (s *Session) beginSettlement(_ string) {
	s.settleMode = true
	if s.opts.Tools == nil {
		return
	}
	keep := map[string]bool{}
	for _, n := range s.opts.SettlementKeepTools {
		keep[n] = true
	}
	var disable []string
	for _, t := range s.opts.Tools.List() {
		if !t.Flags().ReadOnly && !keep[t.Name()] {
			disable = append(disable, t.Name())
		}
	}
	s.opts.Tools.Disable(disable...)
}

// NewSession 建立会话。
func NewSession(opts Options, sessionID string) *Session {
	if opts.MaxTurns <= 0 {
		opts.MaxTurns = 40
	}
	if opts.ToolConcurrency <= 0 {
		opts.ToolConcurrency = 10
	}
	return &Session{opts: opts, id: sessionID}
}

// Resume 从历史断点继续（调用方应先 SanitizeTail）。
func (s *Session) Resume(history []llm.Message) {
	s.history = append(s.history, history...)
}

// History 返回当前历史副本。
func (s *Session) History() []llm.Message {
	out := make([]llm.Message, len(s.history))
	copy(out, s.history)
	return out
}

// ID 返回会话 ID。
func (s *Session) ID() string { return s.id }
