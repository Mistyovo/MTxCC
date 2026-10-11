package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// resolveInRoot 把 path 限制在 root 内（拒绝逃逸）。
func resolveInRoot(root, p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("path 不能为空")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	var joined string
	if filepath.IsAbs(p) {
		joined = filepath.Clean(p)
	} else {
		joined = filepath.Join(absRoot, p)
	}
	if joined != absRoot && !strings.HasPrefix(joined, absRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("路径 %q 越出工作区边界", p)
	}
	return joined, nil
}

type readTool struct{ root string }

// NewRead 创建工作区内文件读取工具。
func NewRead(root string) Tool { return &readTool{root} }

func (t *readTool) Name() string        { return "read" }
func (t *readTool) Description() string { return "读取工作区内一个文本文件的内容。" }
func (t *readTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"path": map[string]any{"type": "string", "description": "工作区内相对路径"},
	}, "required": []string{"path"}}
}
func (t *readTool) Flags() Flags { return Flags{ReadOnly: true, Concurrent: true} }
func (t *readTool) CheckPerms(_ context.Context, _ json.RawMessage) error { return nil }

func (t *readTool) Call(_ context.Context, input json.RawMessage) (string, error) {
	var in struct{ Path string `json:"path"` }
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	full, err := resolveInRoot(t.root, in.Path)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

type writeTool struct{ root string }

// NewWrite 创建工作区内文件写入工具。
func NewWrite(root string) Tool { return &writeTool{root} }

func (t *writeTool) Name() string        { return "write" }
func (t *writeTool) Description() string { return "写入（覆盖）工作区内一个文本文件。" }
func (t *writeTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"path":    map[string]any{"type": "string"},
		"content": map[string]any{"type": "string"},
	}, "required": []string{"path", "content"}}
}
func (t *writeTool) Flags() Flags { return Flags{} }
func (t *writeTool) CheckPerms(_ context.Context, _ json.RawMessage) error { return nil }

func (t *writeTool) Call(_ context.Context, input json.RawMessage) (string, error) {
	var in struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	full, err := resolveInRoot(t.root, in.Path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return "", err
	}
	if err := os.WriteFile(full, []byte(in.Content), 0o640); err != nil {
		return "", err
	}
	return fmt.Sprintf("已写入 %s（%d 字节）", in.Path, len(in.Content)), nil
}

type listTool struct{ root string }

// NewList 创建工作区目录列表工具。
func NewList(root string) Tool { return &listTool{root} }

func (t *listTool) Name() string        { return "list" }
func (t *listTool) Description() string { return "列出工作区内一个目录的条目。" }
func (t *listTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"path": map[string]any{"type": "string", "description": "默认 ."},
	}}
}
func (t *listTool) Flags() Flags { return Flags{ReadOnly: true, Concurrent: true} }
func (t *listTool) CheckPerms(_ context.Context, _ json.RawMessage) error { return nil }

func (t *listTool) Call(_ context.Context, input json.RawMessage) (string, error) {
	var in struct{ Path string `json:"path"` }
	_ = json.Unmarshal(input, &in)
	if in.Path == "" {
		in.Path = "."
	}
	full, err := resolveInRoot(t.root, in.Path)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name()+"/")
		} else {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return strings.Join(names, "\n"), nil
}
