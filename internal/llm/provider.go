package llm

import "context"

// Event 是流式增量事件。具体形态：
//
//	TextDelta{S}            文本增量
//	ThinkingDelta{S}        思考增量
//	ToolUseStart{ID,Name}   工具调用开始
//	ToolInputJSON{ID,Fragment} 工具参数 JSON 分片
//	MessageDelta{StopReason}    终止原因（end_turn/tool_use/…）
//	Usage{In,Out,CacheRead}     累计用量（多次发射时为累计值）
type Event interface{}

// TextDelta 见 Event。
type TextDelta struct{ S string }

// ThinkingDelta 见 Event。
type ThinkingDelta struct{ S string }

// ToolUseStart 见 Event。
type ToolUseStart struct {
	ID   string
	Name string
}

// ToolInputJSON 见 Event。
type ToolInputJSON struct {
	ID       string
	Fragment string
}

// MessageDelta 见 Event。
type MessageDelta struct{ StopReason string }

// Usage 见 Event。
type Usage struct {
	In        int
	Out       int
	CacheRead int
}

// Provider 是流式 LLM 端点。Stream 返回的 channel 在流结束/出错后关闭；
// 出错时先发射 *StreamError 再关闭（池据其判断"未交付任何事件"）。
type Provider interface {
	Stream(ctx context.Context, req Request) (<-chan Event, error)
}

// StreamError 在流中断时经事件通道传递。
type StreamError struct{ Err error }

// Error 实现 error。
func (e *StreamError) Error() string { return e.Err.Error() }

// Unwrap 暴露底层错误。
func (e *StreamError) Unwrap() error { return e.Err }
