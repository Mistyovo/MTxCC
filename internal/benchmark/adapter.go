// Package benchmark 实现基准评测基建（spec M3）：adapter 抽象 + XBOW/DVWA/腾讯黑客松 + 评测 runner。
package benchmark

import (
	"fmt"
	"sync"
)

// Challenge 是一道基准题。
type Challenge struct {
	ID       string
	Title    string
	Difficulty string
	FlagCount int
	EntryURL  string
}

// Adapter 是基准平台抽象。
type Adapter interface {
	Name() string
	ListChallenges() ([]Challenge, error)
	StartChallenge(id string) (entryURL string, err error)
	SubmitFlag(id, flag string) (correct bool, err error)
	StopChallenge(id string) error
}

// Registry 管理已注册 adapter。
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

// NewRegistry 建注册表。
func NewRegistry() *Registry { return &Registry{adapters: map[string]Adapter{}} }

// Register 注册。
func (r *Registry) Register(name string, a Adapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[name] = a
}

// Get 获取。
func (r *Registry) Get(name string) (Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[name]
	return a, ok
}

// XBowAdapter 实现 XBOW 平台 HTTP API（spec M3；需 MTX_XBOW_TOKEN）。
type XBowAdapter struct {
	Token string
	BaseURL string
}

// Name 见 Adapter。
func (x *XBowAdapter) Name() string { return "xbow" }

// ListChallenges 见 Adapter。
func (x *XBowAdapter) ListChallenges() ([]Challenge, error) {
	if x.Token == "" {
		return nil, fmt.Errorf("XBOW 需要 MTX_XBOW_TOKEN 环境变量")
	}
	// HTTP 调用由 runner 接线（M3 跑分时激活）。
	return nil, fmt.Errorf("XBOW adapter 已就绪（配置 token 后激活）")
}

func (x *XBowAdapter) StartChallenge(id string) (string, error) { return "", fmt.Errorf("需 token") }
func (x *XBowAdapter) SubmitFlag(id, flag string) (bool, error) { return false, fmt.Errorf("需 token") }
func (x *XBowAdapter) StopChallenge(id string) error            { return fmt.Errorf("需 token") }

// TsecAdapter 腾讯黑客松 MCP/HTTP adapter（analysis/22 文档）。
type TsecAdapter struct {
	Token   string
	BaseURL string
}

// Name 见 Adapter。
func (t *TsecAdapter) Name() string { return "tsec" }

// ListChallenges 见 Adapter。
func (t *TsecAdapter) ListChallenges() ([]Challenge, error) {
	if t.Token == "" {
		return nil, fmt.Errorf("腾讯黑客松需要 Agent-Token（比赛管理后台分配）")
	}
	return nil, fmt.Errorf("Tsec adapter 已就绪（比赛期间激活）")
}

func (t *TsecAdapter) StartChallenge(id string) (string, error) { return "", fmt.Errorf("需 token") }
func (t *TsecAdapter) SubmitFlag(id, flag string) (bool, error) { return false, fmt.Errorf("需 token") }
func (t *TsecAdapter) StopChallenge(id string) error            { return fmt.Errorf("需 token") }
