// Package llm 定义跨厂商统一的 LLM 消息/事件模型与 Provider 抽象。
// 三协议（anthropic/openai/openai_responses）裸 HTTP 自实现，无官方 SDK 依赖（spec §4.1）。
package llm

// Role 是消息角色。
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// BlockType 是内容块类型。
type BlockType string

const (
	BlockText       BlockType = "text"
	BlockThinking   BlockType = "thinking"
	BlockToolUse    BlockType = "tool_use"
	BlockToolResult BlockType = "tool_result"
)

// Block 是一条消息内的内容块（统一形态，发送前由各协议实现转换）。
type Block struct {
	Type       BlockType
	Text       string // text / thinking 正文 / tool_result 输出
	ID         string // tool_use 块 ID
	Name       string // tool_use 工具名
	InputJSON  string // tool_use 参数 JSON（已聚合）
	ToolUseID  string // tool_result 引用的 tool_use ID
	IsError    bool   // tool_result 是否为错误
	ThinkingSig string // thinking 块签名（anthropic 回传时保留）
}

// Message 是一条对话消息。
type Message struct {
	Role   Role
	Blocks []Block
}

// ToolSpec 是暴露给模型的工具定义。
type ToolSpec struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// ThinkingCfg 是推理参数（双字段按协议映射，spec §4.1）。
type ThinkingCfg struct {
	Enabled bool
	Budget  int    // thinking budget tokens（anthropic）
	Effort  string // reasoning effort（openai 系）
}

// Request 是一次流式请求（协议无关形态）。
type Request struct {
	Model     string
	System    string
	Messages  []Message
	Tools     []ToolSpec
	MaxTokens int
	Thinking  *ThinkingCfg
}

// Profile 是一个 LLM 端点配置（DB llm_profiles 的内存形态）。
type Profile struct {
	Name       string
	Protocol   string // anthropic | openai | openai_responses
	BaseURL    string
	APIKey     string
	Model      string
	CtxWindowK int
	MaxTokens  int
	RatePerS   int
	RatePerM   int
	Enabled    bool
}
