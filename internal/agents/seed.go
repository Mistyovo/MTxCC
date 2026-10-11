package agents

import (
	"bytes"
	"context"
	"text/template"

	"mtx/internal/store"
)

// SeedPrompts 幂等播种内置 prompt 到 agent_prompts（role+version 冲突时保留 DB 版本）。
func SeedPrompts(ctx context.Context, s *store.Store) error {
	seed := []struct {
		Role string
		Body string
	}{
		{"goals", GoalsPromptConst},
		{"planner", PlannerPromptConst},
		{"worker", WorkerPromptConst},
		{"mainagent", MainAgentPromptConst},
	}
	for _, p := range seed {
		// 幂等：role+version 已存在则跳过（DB 覆盖优先于常量）。
		tag, err := s.Exec(ctx, `INSERT INTO agent_prompts (role, version, body, enabled)
			VALUES ($1, 1, $2, true)
			ON CONFLICT (role, version) DO NOTHING`, p.Role, p.Body)
		if err != nil {
			return err
		}
		_ = tag
	}
	return nil
}

// renderWorkerPrompt 渲染模板；失败（缺变量/语法错）回退常量版本。
// 返回 (渲染结果, 是否使用了回退)。
func renderWorkerPrompt(tplBody, fallback string, vars map[string]any) (string, bool) {
	tpl, err := template.New("w").Option("missingkey=error").Parse(tplBody)
	if err != nil {
		return fallback, true
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, vars); err != nil {
		return fallback, true
	}
	return buf.String(), false
}
