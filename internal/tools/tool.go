// Package tools 定义 agent 工具契约与内置工具实现。
package tools

import (
	"context"
	"encoding/json"
	"sync"
)

// Flags 是工具行为标志。
type Flags struct {
	ReadOnly   bool // 只读（可安全并发）
	Concurrent bool // 允许同轮并发执行
}

// Tool 是工具契约（spec §6）：schema 供 prompt，CheckPerms 是执行前置门，Call 返回字符串化结果。
type Tool interface {
	Name() string
	Description() string
	Schema() map[string]any
	Flags() Flags
	CheckPerms(ctx context.Context, input json.RawMessage) error
	Call(ctx context.Context, input json.RawMessage) (string, error)
}

// Registry 是工具注册表。
type Registry struct {
	mu     sync.RWMutex
	tools  map[string]Tool
	order  []string
}

// NewRegistry 建空注册表。
func NewRegistry() *Registry { return &Registry{tools: map[string]Tool{}} }

// Register 注册工具（同名覆盖）。
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name()]; !exists {
		r.order = append(r.order, t.Name())
	}
	r.tools[t.Name()] = t
}

// Get 按名取工具。
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List 列出全部启用工具（注册序）。
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.order))
	for _, n := range r.order {
		if t, ok := r.tools[n]; ok {
			out = append(out, t)
		}
	}
	return out
}

// Disable 移除工具（Settlement 收尾时禁用副作用工具也走这里）。
func (r *Registry) Disable(names ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range names {
		delete(r.tools, n)
	}
}
