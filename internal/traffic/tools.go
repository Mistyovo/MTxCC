package traffic

import (
	"context"
	"encoding/json"
	"fmt"

	"mtx/internal/tools"
)

// NewTrafficTools 生成 traffic 三件套（search/get/blob），实现 tools.Tool 契约。
func NewTrafficTools(idx *Index) []tools.Tool {
	return []tools.Tool{
		&trafficSearchTool{idx: idx},
		&trafficGetTool{idx: idx},
		&trafficBlobTool{idx: idx},
	}
}

// --- traffic_search ---

type trafficSearchTool struct{ idx *Index }

func (t *trafficSearchTool) Name() string { return "traffic_search" }
func (t *trafficSearchTool) Description() string {
	return `检索已录制的 HTTP 流量。参数：host（必填，如 app.example.com）、
body_contains（可选，正文子串，支持中文）、limit（默认 20）。
发现疑似注入点后应先查流量而不是重复发同样的请求。`
}
func (t *trafficSearchTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"host":          map[string]any{"type": "string"},
		"body_contains": map[string]any{"type": "string"},
		"limit":         map[string]any{"type": "integer"},
	}, "required": []string{"host"}}
}
func (t *trafficSearchTool) Flags() tools.Flags { return tools.Flags{ReadOnly: true, Concurrent: true} }
func (t *trafficSearchTool) CheckPerms(context.Context, json.RawMessage) error { return nil }

func (t *trafficSearchTool) Call(ctx context.Context, input json.RawMessage) (string, error) {
	var in struct {
		Host         string `json:"host"`
		BodyContains string `json:"body_contains"`
		Limit        int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	if in.Host == "" {
		return "", fmt.Errorf("host 参数必填（防止全库扫描）")
	}
	refs, err := t.idx.Search(ctx, in.Host, in.BodyContains, in.Limit)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(refs)
	return string(out), nil
}

// --- traffic_get ---

type trafficGetTool struct{ idx *Index }

func (t *trafficGetTool) Name() string { return "traffic_get" }
func (t *trafficGetTool) Description() string {
	return "按 id 取回一条完整交换（含请求/响应头与正文预览）。"
}
func (t *trafficGetTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"id": map[string]any{"type": "integer"},
	}, "required": []string{"id"}}
}
func (t *trafficGetTool) Flags() tools.Flags { return tools.Flags{ReadOnly: true, Concurrent: true} }
func (t *trafficGetTool) CheckPerms(context.Context, json.RawMessage) error { return nil }

func (t *trafficGetTool) Call(ctx context.Context, input json.RawMessage) (string, error) {
	var in struct{ ID int64 `json:"id"` }
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	return t.idx.Get(in.ID)
}

// --- traffic_blob ---

type trafficBlobTool struct{ idx *Index }

func (t *trafficBlobTool) Name() string        { return "traffic_blob" }
func (t *trafficBlobTool) Description() string { return "按 blob 引用取回溢出大正文的指定区间（offset/limit 字节）。" }
func (t *trafficBlobTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"ref":    map[string]any{"type": "string"},
		"offset": map[string]any{"type": "integer"},
		"limit":  map[string]any{"type": "integer"},
	}, "required": []string{"ref"}}
}
func (t *trafficBlobTool) Flags() tools.Flags { return tools.Flags{ReadOnly: true, Concurrent: true} }
func (t *trafficBlobTool) CheckPerms(context.Context, json.RawMessage) error { return nil }

func (t *trafficBlobTool) Call(_ context.Context, input json.RawMessage) (string, error) {
	var in struct {
		Ref    string `json:"ref"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	if in.Ref == "" {
		return "", fmt.Errorf("ref 必填")
	}
	b, err := t.idx.ReadBlob(in.Ref)
	if err != nil {
		return "", err
	}
	if in.Offset < 0 || in.Offset > len(b) {
		in.Offset = 0
	}
	if in.Limit <= 0 || in.Offset+in.Limit > len(b) {
		in.Limit = len(b) - in.Offset
	}
	return string(b[in.Offset : in.Offset+in.Limit]), nil
}
