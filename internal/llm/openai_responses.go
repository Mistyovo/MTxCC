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

// openaiResponsesProvider 实现 OpenAI Responses API（POST /v1/responses，流式事件）。
type openaiResponsesProvider struct{ cfg Profile }

// NewOpenAIResponses 以给定配置创建 Responses Provider。
func NewOpenAIResponses(cfg Profile) Provider { return &openaiResponsesProvider{cfg: cfg} }

// Stream 见 Provider。
func (p *openaiResponsesProvider) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	model := req.Model
	if model == "" || model == "auto" {
		model = p.cfg.Model
	}
	payload := map[string]any{
		"model":       model,
		"max_output_tokens": max(req.MaxTokens, 1),
		"stream":      true,
	}
	if req.System != "" {
		payload["instructions"] = req.System
	}
	input := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		for _, b := range m.Blocks {
			switch b.Type {
			case BlockText:
				input = append(input, map[string]any{
					"role": string(m.Role), "type": "message",
					"content": []map[string]any{{"type": "output_text", "text": b.Text}},
				})
			case BlockToolUse:
				input = append(input, map[string]any{
					"type": "function_call", "call_id": b.ID,
					"name": b.Name, "arguments": b.InputJSON,
				})
			case BlockToolResult:
				input = append(input, map[string]any{
					"type": "function_call_output", "call_id": b.ToolUseID,
					"output": b.Text,
				})
			}
		}
	}
	payload["input"] = input
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = map[string]any{
				"type": "function", "name": t.Name,
				"description": t.Description, "parameters": t.InputSchema,
			}
		}
		payload["tools"] = tools
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(p.cfg.BaseURL, "/")+"/v1/responses", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("responses HTTP %d: %s", resp.StatusCode, string(b))
	}
	ch := make(chan Event, 64)
	go p.pump(resp, ch)
	return ch, nil
}

func (p *openaiResponsesProvider) pump(resp *http.Response, ch chan<- Event) {
	defer resp.Body.Close()
	defer close(ch)

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var data strings.Builder
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimPrefix(line, "data:"))
		case line == "" && data.Len() > 0:
			raw := []byte(strings.TrimSpace(data.String()))
			data.Reset()
			if ev, stop := translateResponses(raw); ev != nil {
				ch <- ev
			} else if stop {
				return
			}
		}
	}
	if err := sc.Err(); err != nil {
		ch <- &StreamError{err}
	}
}

func translateResponses(raw []byte) (ev Event, stop bool) {
	var env struct {
		Type     string `json:"type"`
		Item     *struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"item"`
		ItemID  string `json:"item_id"`
		Delta   string `json:"delta"`
		Response *struct {
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		} `json:"response"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, false
	}
	switch {
	case env.Error != nil:
		return &StreamError{fmt.Errorf("responses: %s", env.Error.Message)}, true
	case env.Type == "response.output_item.added" && env.Item != nil && env.Item.Type == "function_call":
		return ToolUseStart{ID: env.Item.ID, Name: env.Item.Name}, false
	case env.Type == "response.function_call_arguments.delta":
		return ToolInputJSON{ID: env.ItemID, Fragment: env.Delta}, false
	case env.Type == "response.output_text.delta":
		return TextDelta{S: env.Delta}, false
	case env.Type == "response.completed" && env.Response != nil:
		return Usage{In: env.Response.Usage.InputTokens, Out: env.Response.Usage.OutputTokens}, false
	}
	return nil, false
}
