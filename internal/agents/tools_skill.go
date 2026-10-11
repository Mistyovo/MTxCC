package agents

import (
	"context"
	"encoding/json"
	"fmt"

	"mtx/internal/skills"
	"mtx/internal/tools"
)

// NewLoadSkillTool 技能全文加载工具（渐进披露的按需半边）。
func NewLoadSkillTool(dir string) tools.Tool {
	return &loadSkillTool{dir: dir}
}

type loadSkillTool struct{ dir string }

func (t *loadSkillTool) Name() string        { return "load_skill" }
func (t *loadSkillTool) Description() string { return "按名称加载技能全文（SKILL-FIRST：命中触发词必须先加载）。" }
func (t *loadSkillTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"name": map[string]any{"type": "string"},
	}, "required": []string{"name"}}
}
func (t *loadSkillTool) Flags() tools.Flags                            { return tools.Flags{ReadOnly: true} }
func (t *loadSkillTool) CheckPerms(context.Context, json.RawMessage) error { return nil }

func (t *loadSkillTool) Call(_ context.Context, input json.RawMessage) (string, error) {
	var in struct{ Name string `json:"name"` }
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	if in.Name == "" {
		return "", fmt.Errorf("name 必填")
	}
	body, err := skills.Load(t.dir, in.Name)
	if err != nil {
		return "", err
	}
	return body, nil
}
