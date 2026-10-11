// Package roe 实现 RoE（交战规则）数据模型与解析层评估器（spec §7.1/§7.2）。
// 解析层是快速失败层（可被绕过），权威封堵在 egress 编译层（Task 15）。
package roe

import (
	"encoding/json"
	"fmt"
	"time"
)

// Window 是授权时间窗（ISO8601 起止段 "YYYY-MM-DDTHH:MM/YYYY-MM-DDTHH:MM"）。
type Window struct {
	Authorized []string `json:"authorized"`
}

// Rules 是 RoE JSON（字段与 spec §7.1 逐字段一致）。
type Rules struct {
	Mode                  string   `json:"mode"` // audit | warn | enforce
	InScope               []string `json:"in_scope"`
	OutOfScope            []string `json:"out_of_scope"`
	ForbiddenTLDs         []string `json:"forbidden_tlds"`
	ForbiddenCmdPatterns  []string `json:"forbidden_command_patterns"`
	MaxConcurrent         int      `json:"max_concurrent_connections"`
	MinDelayMS            int      `json:"min_inter_request_delay_ms"`
	Windows               []Window `json:"windows"`
}

// Now 可注入时钟（测试时间窗）。
var Now = time.Now

// ParseRules 解析 RoE JSON。
func ParseRules(b []byte) (Rules, error) {
	var r Rules
	if err := json.Unmarshal(b, &r); err != nil {
		return Rules{}, fmt.Errorf("RoE JSON: %w", err)
	}
	if r.Mode == "" {
		r.Mode = "enforce"
	}
	return r, nil
}
