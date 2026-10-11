package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// openaiProvider 实现 OpenAI Chat Completions 流式（兼容网关/vLLM/Ollama 等同形态端点）。
type openaiProvider struct{ cfg Profile }

// NewOpenAI 以给定配置创建 Chat Completions Provider。
func NewOpenAI(cfg Profile) Provider { return &openaiProvider{cfg: cfg} }

// Stream 见 Provider。
func (p *openaiProvider) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	body, err := p.buildBody(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(p.cfg.BaseURL, "/")+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	p.setHeaders(httpReq.Header)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("openai HTTP %d: %s", resp.StatusCode, string(b))
	}
	ch := make(chan Event, 64)
	go pumpSSE(resp, ch, translateOpenAI)
	return ch, nil
}

func (p *openaiProvider) setHeaders(h http.Header) {
	h.Set("Content-Type", "application/json")
	h.Set("Authorization", "Bearer "+p.cfg.APIKey)
}

func (p *openaiProvider) buildBody(req Request) ([]byte, error) {
	payload := map[string]any{
		"model":           req.Model,
		"max_tokens":      max(req.MaxTokens, 1),
		"stream":          true,
		"stream_options":  map[string]any{"include_usage": true},
	}
	if req.System != "" {
		// system 作为首条消息（兼容网关最广）。
		payload["messages"] = append(
			[]map[string]any{{"role": "system", "content": req.System}},
			openaiMessages(req.Messages)...,
		)
	} else {
		payload["messages"] = openaiMessages(req.Messages)
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":         t.Name,
					"description":  t.Description,
					"parameters":   t.InputSchema,
				},
			}
		}
		payload["tools"] = tools
	}
	if req.Thinking != nil && req.Thinking.Enabled && req.Thinking.Effort != "" {
		payload["reasoning_effort"] = req.Thinking.Effort
	}
	return json.Marshal(payload)
}

// openaiMessages 把统一消息转为 wire 形态：
// tool_use / tool_result 折叠进 assistant.tool_calls 与 user(tool) 消息（OpenAI 约定）。
func openaiMessages(msgs []Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		var (
			content      strings.Builder
			toolCalls    []map[string]any
			toolResults  []map[string]any
		)
		for _, b := range m.Blocks {
			switch b.Type {
			case BlockText, BlockThinking:
				if b.Type == BlockText {
					content.WriteString(b.Text)
				}
			case BlockToolUse:
				args := b.InputJSON
				if !json.Valid([]byte(args)) {
					args = "{}"
				}
				toolCalls = append(toolCalls, map[string]any{
					"id":   b.ID,
					"type": "function",
					"function": map[string]any{
						"name":      b.Name,
						"arguments": args,
					},
				})
			case BlockToolResult:
				toolResults = append(toolResults, map[string]any{
					"role":         "tool",
					"tool_call_id": b.ToolUseID,
					"content":      b.Text,
				})
			}
		}
		role := string(m.Role)
		if m.Role == RoleAssistant && len(toolCalls) > 0 {
			msg := map[string]any{"role": role, "tool_calls": toolCalls}
			if content.Len() > 0 {
				msg["content"] = content.String()
			}
			out = append(out, msg)
		} else if content.Len() > 0 || len(toolResults) == 0 {
			out = append(out, map[string]any{"role": role, "content": content.String()})
		}
		out = append(out, toolResults...)
	}
	return out
}

// translateOpenAI 把一个 Chat Completions chunk 翻译为统一事件。
func translateOpenAI(raw []byte, toolIDs map[int]string, usage *Usage) (evs []Event, stop bool) {
	var chunk struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Delta        *struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function *struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			PromptTokensDetails *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return nil, false
	}
	if chunk.Error != nil {
		return []Event{&StreamError{fmt.Errorf("openai: %s", chunk.Error.Message)}}, true
	}
	for _, c := range chunk.Choices {
		if c.Delta == nil {
			continue
		}
		if c.Delta.Content != "" {
			evs = append(evs, TextDelta{S: c.Delta.Content})
		}
		if c.Delta.ReasoningContent != "" {
			evs = append(evs, ThinkingDelta{S: c.Delta.ReasoningContent})
		}
		for _, tc := range c.Delta.ToolCalls {
			if tc.ID != "" && tc.Function != nil && tc.Function.Name != "" {
				toolIDs[tc.Index] = tc.ID
				evs = append(evs, ToolUseStart{ID: tc.ID, Name: tc.Function.Name})
			}
			if tc.Function != nil && tc.Function.Arguments != "" {
				if id, ok := toolIDs[tc.Index]; ok {
					evs = append(evs, ToolInputJSON{ID: id, Fragment: tc.Function.Arguments})
				}
			}
		}
		if c.FinishReason != "" {
			evs = append(evs, MessageDelta{StopReason: c.FinishReason})
		}
	}
	if chunk.Usage != nil {
		usage.In = chunk.Usage.PromptTokens
		usage.Out = chunk.Usage.CompletionTokens
		if chunk.Usage.PromptTokensDetails != nil {
			usage.CacheRead = chunk.Usage.PromptTokensDetails.CachedTokens
		}
		evs = append(evs, Usage{In: usage.In, Out: usage.Out, CacheRead: usage.CacheRead})
	}
	return evs, false
}

// pumpSSE 是 SSE 消费骨架（data: 行 + 空行分隔），translate 注入协议差异。
// OpenAI 以 `data: [DONE]` 结束；anthropic 以 message_stop 事件结束（translate 返回 stop）。
func pumpSSE(resp *http.Response, ch chan<- Event, translate func([]byte, map[int]string, *Usage) ([]Event, bool)) {
	defer resp.Body.Close()
	defer close(ch)

	usage := Usage{}
	toolIDs := map[int]string{}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var data strings.Builder
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			seg := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if seg == "[DONE]" {
				return
			}
			data.WriteString(seg)
		case line == "" && data.Len() > 0:
			raw := []byte(data.String())
			data.Reset()
			evs, stop := translate(raw, toolIDs, &usage)
			for _, ev := range evs {
				ch <- ev
			}
			if stop {
				return
			}
		}
	}
	if err := sc.Err(); err != nil {
		ch <- &StreamError{err}
	}
}
