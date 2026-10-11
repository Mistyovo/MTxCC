//go:build docker

package engine

import (
	"context"
	"testing"
	"time"

	"mtx/internal/llm"
)

// TestEngineDeadlineSetsTaskTimeout 任务超时后引擎必须落终态 timeout
//（线上 bug：ctx 超时直接返回，tasks.status 永远 running，benchmark 计分卡死）。
func TestEngineDeadlineSetsTaskTimeout(t *testing.T) {
	prov := newScriptProv()
	// worker 一直空转（bash 轮不停），goal 永不 met → 任务走 deadline。
	prov.scripts["worker"] = [][]llm.Event{
		toolCall("w1", "bash", `{"command":"echo x"}`),
		toolCall("w2", "bash", `{"command":"echo x"}`),
		toolCall("w3", "bash", `{"command":"echo x"}`),
		toolCall("w4", "bash", `{"command":"echo x"}`),
	}
	prov.scripts["planner"] = [][]llm.Event{llamkText("等")}

	e, _, s, taskID, expID := setupEngine(t, prov)
	e.MaxTurns = 2
	// 任务 deadline 2 秒。
	_, _ = s.Exec(ctx(), `UPDATE tasks SET timeout_seconds=2 WHERE id=$1`, taskID)
	_ = expID

	done := make(chan error, 1)
	go func() { done <- e.Run(ctx(), taskID) }()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("引擎未在 deadline 后退出")
	}

	// Run 返回后任务必须为终态（timeout 或 done），绝不能还是 running。
	var status string
	if err := s.QueryRow(ctx(), `SELECT status FROM tasks WHERE id=$1`, taskID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status == "running" || status == "created" || status == "queued" {
		t.Fatalf("任务终态缺失（status=%s）——deadline 兜底缺失（线上 bug）", status)
	}
}

func ctx() context.Context { return context.Background() }
