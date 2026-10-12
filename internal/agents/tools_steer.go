package agents

import (
	"context"
	"encoding/json"
	"fmt"

	"mtx/internal/tools"
)

// SteerTools planner 过程控制原语（I6 遗留落地）：
// steer_work 纠偏在跑 worker（下一次工具调用前注入）、kill_work 止损。
func SteerTools(deps CommonDeps, ec ExecContext) []tools.Tool {
	return []tools.Tool{
		&steerTool{deps: deps, kind: "steer"},
		&steerTool{deps: deps, kind: "kill"},
	}
}

type steerTool struct {
	deps CommonDeps
	kind string
}

func (t *steerTool) Name() string {
	if t.kind == "steer" {
		return "steer_work"
	}
	return "kill_work"
}

func (t *steerTool) Description() string {
	if t.kind == "steer" {
		return "给在跑的 worker 注入纠偏指令（下次工具调用前生效，不杀进程）：{intent_id, message}。"
	}
	return "止损单个跑偏的意图：{intent_id}。"
}

func (t *steerTool) Schema() map[string]any {
	props := map[string]any{"intent_id": map[string]any{"type": "integer"}}
	req := []string{"intent_id"}
	if t.kind == "steer" {
		props["message"] = map[string]any{"type": "string"}
		req = append(req, "message")
	}
	return map[string]any{"type": "object", "properties": props, "required": req}
}

func (t *steerTool) Flags() tools.Flags                                { return tools.Flags{} }
func (t *steerTool) CheckPerms(context.Context, json.RawMessage) error { return nil }

func (t *steerTool) Call(ctx context.Context, input json.RawMessage) (string, error) {
	var in struct {
		IntentID int64  `json:"intent_id"`
		Message  string `json:"message"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	if t.kind == "steer" {
		if t.deps.SteerFn == nil {
			return "", fmt.Errorf("steer_work 未配置（引擎未注入回调）")
		}
		if err := t.deps.SteerFn(in.IntentID, in.Message); err != nil {
			return "", err
		}
		return fmt.Sprintf("已注入纠偏到意图 %d", in.IntentID), nil
	}
	if t.deps.KillFn == nil {
		return "", fmt.Errorf("kill_work 未配置（引擎未注入回调）")
	}
	if err := t.deps.KillFn(in.IntentID); err != nil {
		return "", err
	}
	return fmt.Sprintf("已止损意图 %d", in.IntentID), nil
}
