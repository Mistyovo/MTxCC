package llm

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrAllCooling 表示全部 member 均处于熔断冷却期。
var ErrAllCooling = errors.New("llm: 所有 profile 均在熔断冷却中")

// Pool 是按序故障转移池（spec §4.2）：
// 只有未交付任何事件前才允许换 member（防重复输出污染对话）；
// 连续失败 ≥2 触发熔断（指数冷却 1s→…→60s 上限），冷却后半开试探。
type Pool struct {
	members []Provider
	health  []*memberHealth
	mu      sync.Mutex
	now     func() time.Time // 可注入（测试）
}

type memberHealth struct {
	fails         int
	cooldownUntil time.Time
}

// NewPool 建立按序尝试的 provider 池。
func NewPool(members ...Provider) *Pool {
	h := make([]*memberHealth, len(members))
	for i := range h {
		h[i] = &memberHealth{}
	}
	return &Pool{members: members, health: h, now: time.Now}
}

// Stream 启动首个可用 member；流中断且未交付事件时由 supervise 依序换下一 member。
func (p *Pool) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	p.mu.Lock()
	now := p.now()
	idx := -1
	anyCandidate := false
	for i, m := range p.members {
		if p.health[i].cooldownUntil.After(now) {
			continue // 冷却中，跳过
		}
		anyCandidate = true
		ch, err := m.Stream(ctx, req)
		if err != nil {
			p.recordFailure(i)
			continue
		}
		idx = i
		_ = m
		out := make(chan Event, 64)
		p.mu.Unlock()
		go p.supervise(ctx, req, idx, ch, out)
		return out, nil
	}
	p.mu.Unlock()
	if !anyCandidate {
		return nil, ErrAllCooling
	}
	return nil, errors.New("llm: 全部可用 member 启动失败")
}

// supervise 消费当前 member 的流并转发；首事件前断流则依序换下一 member，
// 已交付事件后断流则直接透传错误终止（防重复输出污染对话）。
func (p *Pool) supervise(ctx context.Context, req Request, idx int, ch <-chan Event, out chan<- Event) {
	defer close(out)
	for {
		delivered := false
		switchNext := false
		for ev := range ch {
			se, isErr := ev.(*StreamError)
			if !isErr {
				delivered = true
				out <- ev
				continue
			}
			if delivered {
				out <- se // 已交付：不切换，透传错误
				return
			}
			// 未交付：记失败并尝试下一可用 member。
			p.mu.Lock()
			p.recordFailure(idx)
			next := -1
			now := p.now()
			for j := idx + 1; j < len(p.members); j++ {
				if !p.health[j].cooldownUntil.After(now) {
					next = j
					break
				}
			}
			p.mu.Unlock()
			if next < 0 {
				out <- se
				return
			}
			nch, err := p.members[next].Stream(ctx, req)
			if err != nil {
				p.mu.Lock()
				p.recordFailure(next)
				p.mu.Unlock()
				out <- se
				return
			}
			idx, ch = next, nch
			switchNext = true
			break
		}
		if switchNext {
			continue
		}
		if !delivered {
			// 流正常关闭但零事件：按失败记账，保守不重试。
			p.mu.Lock()
			p.recordFailure(idx)
			p.mu.Unlock()
		}
		return
	}
}

// recordFailure 熔断记账：连续失败 ≥2 → 指数冷却（1s 起、×2、60s 上限）。
// 调用方须持锁。
func (p *Pool) recordFailure(idx int) {
	h := p.health[idx]
	h.fails++
	if h.fails >= 2 {
		backoff := time.Second << (h.fails - 2)
		if backoff > 60*time.Second {
			backoff = 60 * time.Second
		}
		h.cooldownUntil = p.now().Add(backoff)
	}
}
