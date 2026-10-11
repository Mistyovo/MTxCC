package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

// funcProvider 把闭包适配为 Provider（仅测试用）。
type funcProvider struct {
	stream func(ctx context.Context, req Request) (<-chan Event, error)
	calls  int
}

func (f *funcProvider) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	f.calls++
	return f.stream(ctx, req)
}

// emitThen 把事件依次注入 channel（可选末尾错误）。
func emitThen(evs []Event, err error) <-chan Event {
	ch := make(chan Event, len(evs)+1)
	for _, e := range evs {
		ch <- e
	}
	if err != nil {
		ch <- &StreamError{err}
	}
	close(ch)
	return ch
}

func wantCalls(t *testing.T, p *funcProvider, want int) {
	t.Helper()
	if p.calls != want {
		t.Fatalf("调用次数 = %d, want %d", p.calls, want)
	}
}

// TestPoolSwitchesBeforeFirstEvent 验证"未交付任何事件前才允许换 member"：
// member1 首事件前断流 → 自动切 member2 并成功。
func TestPoolSwitchesBeforeFirstEvent(t *testing.T) {
	m1 := &funcProvider{stream: func(context.Context, Request) (<-chan Event, error) {
		return emitThen(nil, errors.New("conn reset")), nil
	}}
	m2 := &funcProvider{stream: func(context.Context, Request) (<-chan Event, error) {
		return emitThen([]Event{TextDelta{S: "ok"}}, nil), nil
	}}
	pool := NewPool(m1, m2)

	ch, err := pool.Stream(context.Background(), Request{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var got string
	for ev := range ch {
		if e, ok := ev.(TextDelta); ok {
			got += e.S
		}
	}
	if got != "ok" {
		t.Fatalf("文本 = %q, want ok", got)
	}
	wantCalls(t, m2, 1)
}

// TestPoolNoSwitchAfterDelivery 验证已交付事件后断流：不切换 member（防重复输出污染对话，spec §4.2）。
func TestPoolNoSwitchAfterDelivery(t *testing.T) {
	m1 := &funcProvider{stream: func(context.Context, Request) (<-chan Event, error) {
		return emitThen([]Event{TextDelta{S: "半"}}, errors.New("断流")), nil
	}}
	m2 := &funcProvider{stream: func(context.Context, Request) (<-chan Event, error) {
		return emitThen([]Event{TextDelta{S: "m2"}}, nil), nil
	}}
	pool := NewPool(m1, m2)

	ch, err := pool.Stream(context.Background(), Request{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range ch { // 排空：StreamError 也在流内
	}
	wantCalls(t, m2, 0)
}

// TestPoolBreakerCooldown 验证熔断：连续 2 次失败进入冷却，冷却期内直接跳过；
// 冷却结束后半开重试（注入时钟加速）。
func TestPoolBreakerCooldown(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	m1 := &funcProvider{stream: func(context.Context, Request) (<-chan Event, error) {
		return nil, errors.New("5xx")
	}}
	pool := NewPool(m1)
	pool.now = func() time.Time { return now }

	// 两次失败 → 熔断。
	for i := 0; i < 2; i++ {
		if _, err := pool.Stream(context.Background(), Request{}); err == nil {
			t.Fatalf("第 %d 次应报错", i+1)
		}
	}
	wantCalls(t, m1, 2)

	// 冷却期内：不再调 member1，直接 ErrAllCooling。
	if _, err := pool.Stream(context.Background(), Request{}); !errors.Is(err, ErrAllCooling) {
		t.Fatalf("冷却期内应 ErrAllCooling，实际 %v", err)
	}
	wantCalls(t, m1, 2)

	// 时间前进 61s：半开 → member1 再次被调（仍失败 → 再冷却）。
	now = now.Add(61 * time.Second)
	if _, err := pool.Stream(context.Background(), Request{}); err == nil {
		t.Fatal("半开重试失败应报错")
	}
	wantCalls(t, m1, 3)
}
