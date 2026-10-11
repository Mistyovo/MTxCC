package harness

import (
	"context"
	"sync"

	"mtx/internal/llm"
)

// scriptProvider 按脚本逐轮回放事件流（仅测试用）。
type scriptProvider struct {
	mu     sync.Mutex
	script [][]llm.Event // 每元素 = 一轮的完整事件流
	call   int
	reqs   []llm.Request // 捕获每次请求（供断言 tool_result 回填）
}

func (p *scriptProvider) Stream(_ context.Context, req llm.Request) (<-chan llm.Event, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reqs = append(p.reqs, req)
	if p.call >= len(p.script) {
		ch := make(chan llm.Event, 1)
		ch <- &llm.StreamError{Err: assertErr("脚本耗尽")}
		close(ch)
		return ch, nil
	}
	evs := p.script[p.call]
	p.call++
	ch := make(chan llm.Event, len(evs)+1)
	for _, e := range evs {
		ch <- e
	}
	close(ch)
	return ch, nil
}

type assertErr string

func (e assertErr) Error() string { return string(e) }

// capturedReqs 返回捕获的请求副本。
func (p *scriptProvider) capturedReqs() []llm.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]llm.Request, len(p.reqs))
	copy(out, p.reqs)
	return out
}

// turns 返回已消耗轮数。
func (p *scriptProvider) turns() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.call
}
