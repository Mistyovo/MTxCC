package guard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"mtx/internal/roe"
	"mtx/internal/tools"
)

// 错误集。
var (
	ErrNotFound       = errors.New("guard: 待决单不存在")
	ErrAlreadyDecided = errors.New("guard: 待决单已决")
)

// 平台前缀：防 agent 把平台拦截误当目标 WAF 去绕过（spec §7.4）。
const denyPrefix = "【MTX 平台管控·非目标防御】"

// Judge 是 LLM 兜底判官（M0 可为 nil 跳过）。
type Judge interface {
	Review(ctx context.Context, tool string, input json.RawMessage) (bool, string, error)
}

// Guard 实现 harness.PermissionChecker（PreToolUse 返回替换后 input 供 modify 采用）。
type Guard struct {
	src       RuleSource
	roeLoad   func(taskID int64) roe.Rules
	judge     Judge
	taskID    int64

	Now        func() time.Time      // 可注入时钟
	OnPending  func(pendingID int64) // pending 创建回调（UI 推送/测试注入决策）

	stat *blockedStat // 共享计数（ForTask 拷贝安全）
}

type blockedStat struct {
	mu sync.Mutex
	n  int
}

// NewGuard 建审批门；roeLoad 按 task 取 RoE（nil 表示无 RoE）。
func NewGuard(src RuleSource, roeLoad func(taskID int64) roe.Rules, judge Judge) *Guard {
	if roeLoad == nil {
		roeLoad = func(int64) roe.Rules { return roe.Rules{Mode: "audit"} }
	}
	return &Guard{src: src, roeLoad: roeLoad, judge: judge, Now: time.Now, stat: &blockedStat{}}
}

// ForTask 绑定任务上下文（RoE 加载键）。
func (g *Guard) ForTask(taskID int64) *Guard {
	cp := *g
	cp.taskID = taskID
	return &cp
}

// PreToolUse 四层评估。返回替换后 input（modify 采用）或 error（拒绝）。
func (g *Guard) PreToolUse(ctx context.Context, toolName string, input json.RawMessage) (json.RawMessage, error) {
	// 第一层：灾难正则（无条件）。
	if toolName == "bash" {
		var p struct{ Command string `json:"command"` }
		if err := json.Unmarshal(input, &p); err == nil && tools.CatastrophicBash(p.Command) {
			return nil, fmt.Errorf("%s灾难命令被无条件拒绝", denyPrefix)
		}
	}

	// 第二层：intercept 规则（优先级序，首条命中生效）。
	rules, err := g.src.Rules(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s规则加载失败，fail-closed：%v", denyPrefix, err)
	}
	if r := matchRule(rules, toolName, string(input)); r != nil {
		switch r.Action {
		case "allow":
			return g.roeGate(ctx, toolName, input)
		case "deny":
			return nil, fmt.Errorf("%s命中拦截规则（tool=%s pattern=%s）", denyPrefix, toolName, r.InputPat)
		case "ask":
			return g.askAndWait(ctx, *r, toolName, input)
		}
	}

	// 第三层：RoE 解析层（enforce 模式拒绝范围外目标）。
	return g.roeGate(ctx, toolName, input)
}

// roeGate 对 bash 命令执行 RoE 解析层评估。
func (g *Guard) roeGate(ctx context.Context, toolName string, input json.RawMessage) (json.RawMessage, error) {
	if toolName != "bash" {
		return input, nil // 非 bash 工具 M0 无目标语义
	}
	var p struct{ Command string `json:"command"` }
	if err := json.Unmarshal(input, &p); err != nil {
		return nil, fmt.Errorf("%sbash 参数解析失败：%v", denyPrefix, err)
	}
	r := g.roeLoad(g.taskID)
	if r.Mode != "enforce" {
		return input, nil // audit/warn 模式仅记录（M0 不拦截）
	}
	d := roe.EvaluateCommand(p.Command, r)
	if !d.Allow {
		return nil, fmt.Errorf("%sRoE 拒绝：%s（若为误报请操作员修改 RoE 配置，不要重发同一命令）", denyPrefix, d.Reason)
	}
	return input, nil
}

// askAndWait 创建待决单并阻塞等待 decide 或 TTL 超时（默认拒绝）。
func (g *Guard) askAndWait(ctx context.Context, r Rule, toolName string, input json.RawMessage) (json.RawMessage, error) {
	ttl := r.TTLSecs
	if ttl <= 0 {
		ttl = 60
	}
	id, err := g.src.CreatePending(ctx, Pending{
		RuleID:    int64(r.Priority), // M0 无规则表主键，暂存优先级
		ToolName:  toolName,
		Input:     input,
		ExpiresAt: g.Now().Add(time.Duration(ttl) * time.Second),
		CreatedAt: g.Now(),
	})
	if err != nil {
		return nil, fmt.Errorf("%s创建待决单失败：%v", denyPrefix, err)
	}
	if g.OnPending != nil {
		g.OnPending(id)
	}

	deadline := g.Now().Add(time.Duration(ttl) * time.Second)
	for {
		if ctx.Err() != nil {
			_ = g.src.DecidePending(ctx, id, "expired", nil, "ctx")
			return nil, fmt.Errorf("%s上下文取消，默认拒绝", denyPrefix)
		}
		if !g.Now().Before(deadline) {
			_ = g.src.DecidePending(ctx, id, "expired", nil, "ttl")
			return nil, fmt.Errorf("%s审批超时（%ds）未决，默认拒绝", denyPrefix, ttl)
		}
		p, err := g.src.PendingStatus(ctx, id)
		if err == nil && p.Status != "pending" {
			switch p.Status {
			case "allowed":
				return g.roeGate(ctx, toolName, input) // 人工放行后仍过 RoE（操作员批准不能越过 RoE）
			case "modified":
				if len(p.Replacement) > 0 {
					return g.roeGate(ctx, toolName, p.Replacement)
				}
				return g.roeGate(ctx, toolName, input)
			case "denied":
				return nil, fmt.Errorf("%s人工审批拒绝", denyPrefix)
			default: // expired
				return nil, fmt.Errorf("%s审批超时未决，默认拒绝", denyPrefix)
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Decide 处理一次人工决策（API 层调用）。动作归一为终态名。
func (g *Guard) Decide(ctx context.Context, id int64, action string, replacement json.RawMessage, by string) error {
	status := map[string]string{"allow": "allowed", "deny": "denied", "modify": "modified"}[action]
	if status == "" {
		return fmt.Errorf("非法决策动作 %q", action)
	}
	return g.src.DecidePending(ctx, id, status, replacement, by)
}

// PostToolUse 统计 blocked 类结果（403/waf/blocked/ratelimit），供 planner 换策略。
func (g *Guard) PostToolUse(_ context.Context, _ string, _, result string) {
	if result == "" || len(result) > 100_000 {
		return
	}
	low := strings.ToLower(result)
	for _, kw := range []string{"403", "waf", "blocked", "ratelimit", "rate limit"} {
		if strings.Contains(low, kw) {
			g.stat.mu.Lock()
			g.stat.n++
			g.stat.mu.Unlock()
			return
		}
	}
}

// BlockedCount 返回 blocked 计数。
func (g *Guard) BlockedCount() int {
	g.stat.mu.Lock()
	defer g.stat.mu.Unlock()
	return g.stat.n
}

// matchRule 按优先级序找首条命中的规则。
func matchRule(rules []Rule, toolName, input string) *Rule {
	sorted := make([]Rule, len(rules))
	copy(sorted, rules)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Priority < sorted[j].Priority })
	for i := range sorted {
		r := &sorted[i]
		if r.Disabled {
			continue
		}
		if r.ToolNamePat != "" && !toolMatch(r.ToolNamePat, toolName) {
			continue
		}
		if r.InputPat == "" {
			return r
		}
		if r.Match == "regex" {
			if re, err := regexp.Compile(r.InputPat); err == nil && re.MatchString(input) {
				return r
			}
			continue
		}
		if strings.Contains(input, r.InputPat) {
			return r
		}
	}
	return nil
}

func toolMatch(pat, name string) bool {
	if strings.HasSuffix(pat, "*") {
		return strings.HasPrefix(name, strings.TrimSuffix(pat, "*"))
	}
	return pat == name
}
