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

// Compactor 是历史压缩器（Task 8 实现；nil 表示不压缩）。
type Compactor interface {
	Pre(msgs []llm.Message) ([]llm.Message, error)
	MarkPromptTooLong()
}

// Options 配置一次会话循环。
type Options struct {
	Provider       llm.Provider
	Tools          *tools.Registry
	Transcript     TranscriptStore
	Compactor      Compactor
	Perms          PermissionChecker
	Model          string        // 请求所用模型名（透传 Provider）
	MaxTurns       int           // 0 = 默认 40
	MaxDuration    time.Duration // 0 = 不限
	ToolConcurrency int          // 0 = 默认 10
	System         string        // Query 可覆盖
}

// Session 持有一个 agent 会话的历史与循环选项。
type Session struct {
	opts    Options
	id      string
	history []llm.Message
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
