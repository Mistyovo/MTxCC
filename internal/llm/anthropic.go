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

// anthropicProvider 实现 Anthropic Messages API（裸 HTTP，流式 SSE）。
type anthropicProvider struct {
	cfg Profile
}

// NewAnthropic 以给定配置创建 Anthropic Provider。
func NewAnthropic(cfg Profile) Provider {
	return &anthropicProvider{cfg: cfg}
}

// Stream 见 Provider。
func (p *anthropicProvider) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	body, err := p.buildBody(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(p.cfg.BaseURL, "/")+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.cfg.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("anthropic HTTP %d: %s", resp.StatusCode, string(b))
	}

	ch := make(chan Event, 64)
	go p.pump(resp, ch)
	return ch, nil
}

func (p *anthropicProvider) buildBody(req Request) ([]byte, error) {
	payload := map[string]any{
		"model":      req.Model,
		"max_tokens": max(req.MaxTokens, 1),
		"stream":     true,
	}
	if req.System != "" {
		payload["system"] = req.System
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": t.InputSchema,
			}
		}
		payload["tools"] = tools
	}
	if req.Thinking != nil && req.Thinking.Enabled {
		payload["thinking"] = map[string]any{
			"type":          "enabled",
			"budget_tokens": req.Thinking.Budget,
		}
	}
	msgs := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		blocks := make([]map[string]any, 0, len(m.Blocks))
		for _, b := range m.Blocks {
			switch b.Type {
			case BlockText:
				blocks = append(blocks, map[string]any{"type": "text", "text": b.Text})
			case BlockThinking:
				tb := map[string]any{"type": "thinking", "thinking": b.Text}
				if b.ThinkingSig != "" {
					tb["signature"] = b.ThinkingSig
				}
				blocks = append(blocks, tb)
			case BlockToolUse:
				var in any
				if json.Valid([]byte(b.InputJSON)) {
					_ = json.Unmarshal([]byte(b.InputJSON), &in)
				} else {
					in = map[string]any{}
				}
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": b.ID, "name": b.Name, "input": in})
			case BlockToolResult:
				tb := map[string]any{"type": "tool_result", "tool_use_id": b.ToolUseID, "content": b.Text}
				if b.IsError {
					tb["is_error"] = true
				}
				blocks = append(blocks, tb)
			}
		}
		msgs = append(msgs, map[string]any{"role": string(m.Role), "content": blocks})
	}
	payload["messages"] = msgs
	return json.Marshal(payload)
}

// pump 消费 SSE 流直至结束，翻译为统一事件后关闭 channel。
// tool_use 的 input 分片经 index→toolID 表关联为 ToolInputJSON{ID,…}。
func (p *anthropicProvider) pump(resp *http.Response, ch chan<- Event) {
	defer resp.Body.Close()
	defer close(ch)

	usage := Usage{}
	toolIDs := map[int]string{} // content_block index → tool_use id
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var data strings.Builder
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimPrefix(line, "data:"))
			continue
		}
		if line != "" || data.Len() == 0 {
			continue
		}
		raw := []byte(strings.TrimSpace(data.String()))
		data.Reset()

		evs, stop := translateAnthropic(raw, toolIDs, &usage)
		for _, ev := range evs {
			ch <- ev
		}
		if stop {
			return
		}
	}
	if err := sc.Err(); err != nil {
		ch <- &StreamError{err}
	}
}

// translateAnthropic 把一个 SSE data JSON 翻译为统一事件（可能多条，如 message_delta 同载 usage 与 stop_reason）。
// 返回 stop=true 表示流应终止（error 事件）。
func translateAnthropic(raw []byte, toolIDs map[int]string, usage *Usage) (evs []Event, stop bool) {
	var env struct {
		Type         string `json:"type"`
		Index        int    `json:"index"`
		Message      *struct {
			Usage struct {
				InputTokens          int `json:"input_tokens"`
				CacheReadInputTokens int `json:"cache_read_input_tokens"`
				OutputTokens         int `json:"output_tokens"`
			} `json:"usage"`
		} `json:"message"`
		ContentBlock *struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content_block"`
		Delta *struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			PartialJSON string `json:"partial_json"`
			StopReason  string `json:"stop_reason"`
		} `json:"delta"`
		Usage *struct {
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, false // 无法解析的行当噪声跳过
	}
	switch env.Type {
	case "message_start":
		if env.Message != nil {
			usage.In = env.Message.Usage.InputTokens
			usage.CacheRead = env.Message.Usage.CacheReadInputTokens
			usage.Out = env.Message.Usage.OutputTokens
			return []Event{Usage{In: usage.In, Out: usage.Out, CacheRead: usage.CacheRead}}, false
		}
	case "content_block_start":
		if env.ContentBlock != nil && env.ContentBlock.Type == "tool_use" {
			toolIDs[env.Index] = env.ContentBlock.ID
			return []Event{ToolUseStart{ID: env.ContentBlock.ID, Name: env.ContentBlock.Name}}, false
		}
	case "content_block_delta":
		if env.Delta == nil {
			return nil, false
		}
		switch env.Delta.Type {
		case "text_delta":
			return []Event{TextDelta{S: env.Delta.Text}}, false
		case "thinking_delta":
			return []Event{ThinkingDelta{S: env.Delta.Thinking}}, false
		case "input_json_delta":
			return []Event{ToolInputJSON{ID: toolIDs[env.Index], Fragment: env.Delta.PartialJSON}}, false
		}
	case "message_delta":
		// 真实协议：usage 与 stop_reason 常同载一个事件，两者都发。
		var evs []Event
		if env.Usage != nil {
			usage.Out = env.Usage.OutputTokens
			evs = append(evs, Usage{In: usage.In, Out: usage.Out, CacheRead: usage.CacheRead})
		}
		if env.Delta != nil && env.Delta.StopReason != "" {
			evs = append(evs, MessageDelta{StopReason: env.Delta.StopReason})
		}
		return evs, false
	case "error":
		msg := ""
		if env.Error != nil {
			msg = env.Error.Message
		}
		return []Event{&StreamError{fmt.Errorf("anthropic: %s", msg)}}, true
	}
	return nil, false
}
