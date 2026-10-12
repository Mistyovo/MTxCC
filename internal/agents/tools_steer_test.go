package agents

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
)

// TestSteerTools planner 纠偏工具面（I6 遗留落地）：
// steer_work/kill_work 经回调注入引擎（planner 可纠偏在跑 worker）。
func TestSteerTools(t *testing.T) {
	var steered, killed atomic.Int64
	deps := CommonDeps{
		SteerFn: func(intentID int64, msg string) error {
			steered.Add(1)
			return nil
		},
		KillFn: func(intentID int64) error {
			killed.Add(1)
			return nil
		},
	}
	tools := SteerTools(deps, ExecContext{ExpID: 1})
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Name()] = true
	}
	if !names["steer_work"] || !names["kill_work"] {
		t.Fatalf("缺纠偏工具: %v", names)
	}
	var steerTool, killTool interface {
		Call(context.Context, json.RawMessage) (string, error)
	}
	for _, tl := range tools {
		if tl.Name() == "steer_work" {
			steerTool = tl.(interface {
				Call(context.Context, json.RawMessage) (string, error)
			})
		}
		if tl.Name() == "kill_work" {
			killTool = tl.(interface {
				Call(context.Context, json.RawMessage) (string, error)
			})
		}
	}
	if _, err := steerTool.Call(context.Background(), mustRaw(map[string]any{"intent_id": 5, "message": "改测 SSH"})); err != nil {
		t.Fatalf("steer_work: %v", err)
	}
	if _, err := killTool.Call(context.Background(), mustRaw(map[string]any{"intent_id": 5})); err != nil {
		t.Fatalf("kill_work: %v", err)
	}
	if steered.Load() != 1 || killed.Load() != 1 {
		t.Fatalf("回调计数 steer=%d kill=%d", steered.Load(), killed.Load())
	}
	// 缺回调时调用报错（防静默）。
	tools2 := SteerTools(CommonDeps{}, ExecContext{})
	for _, tl := range tools2 {
		if tl.Name() == "steer_work" {
			if _, err := tl.Call(context.Background(), mustRaw(map[string]any{"intent_id": 1, "message": "x"})); err == nil || !strings.Contains(err.Error(), "未配置") {
				t.Fatalf("缺回调应报错: %v", err)
			}
		}
	}
}
