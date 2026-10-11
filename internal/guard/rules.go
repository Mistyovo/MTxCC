// Package guard 实现审批门四层（spec §7.4）：灾难正则 → intercept 规则（allow/deny/ask）
// → RoE 解析层 → 人工队列（ask 超时默认拒绝）。deny 消息带平台前缀防 agent 当目标 WAF 绕。
package guard

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Rule 是一条 intercept 规则。
type Rule struct {
	Priority    int
	ToolNamePat string // 空串匹配全部；支持前缀通配 "bash*"
	InputPat    string
	Match       string // string | regex
	Action      string // allow | deny | ask
	TTLSecs     int    // ask 的等待上限（默认 60）
	Disabled    bool   // 默认启用（零值兼容）
}

// Pending 是一条待决审批。
type Pending struct {
	ID          int64
	RuleID      int64
	ToolName    string
	Input       json.RawMessage
	Replacement json.RawMessage
	Status      string // pending | allowed | denied | modified | expired
	DecidedBy   string
	ExpiresAt   time.Time
	CreatedAt   time.Time
}

// RuleSource 是规则与待决单的存取抽象（内存实现供测试，DB 实现见 Task 22 接线）。
type RuleSource interface {
	Rules(ctx context.Context) ([]Rule, error)
	CreatePending(ctx context.Context, p Pending) (int64, error)
	PendingStatus(ctx context.Context, id int64) (Pending, error)
	DecidePending(ctx context.Context, id int64, action string, replacement json.RawMessage, by string) error
}

// MemorySource 内存实现（测试与单机无 DB 场景）。
type MemorySource struct {
	mu      sync.Mutex
	rules   []Rule
	nextID  int64
	pending map[int64]*Pending
}

// NewMemorySource 建内存源。
func NewMemorySource(rules ...Rule) *MemorySource {
	return &MemorySource{rules: rules, nextID: 1, pending: map[int64]*Pending{}}
}

// Rules 见 RuleSource。
func (m *MemorySource) Rules(_ context.Context) ([]Rule, error) { return m.rules, nil }

// CreatePending 见 RuleSource。
func (m *MemorySource) CreatePending(_ context.Context, p Pending) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p.ID = m.nextID
	m.nextID++
	p.Status = "pending"
	cp := p
	m.pending[p.ID] = &cp
	return p.ID, nil
}

// PendingStatus 见 RuleSource。
func (m *MemorySource) PendingStatus(_ context.Context, id int64) (Pending, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.pending[id]; ok {
		return *p, nil
	}
	return Pending{}, ErrNotFound
}

// DecidePending 见 RuleSource。
func (m *MemorySource) DecidePending(_ context.Context, id int64, action string, replacement json.RawMessage, by string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pending[id]
	if !ok {
		return ErrNotFound
	}
	if p.Status != "pending" {
		return ErrAlreadyDecided
	}
	p.Status = action
	p.Replacement = replacement
	p.DecidedBy = by
	return nil
}
