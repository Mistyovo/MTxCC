//go:build docker

package server

import (
	"context"
	"strings"
	"testing"
)

// TestTaskTemplates 任务模板（ARTEX task_templates 对标）：
// 创建模板（预置 RoE+技能提示）→ 从模板建任务 → RoE 生效。
func TestTaskTemplates(t *testing.T) {
	base, tok, st := setupRBAC(t)
	dctx := context.Background()
	_, _ = st.Exec(dctx, `INSERT INTO llm_profiles (name,protocol,base_url,model,max_tokens,enabled) VALUES ('fake-tpl','openai','http://127.0.0.1:9','fake',4096,true)`)

	// 建模板。
	r, b := apiCall(t, "POST", base+"/api/templates", tok, map[string]any{
		"name":          "内网 Web 快速评估",
		"target_pattern": "http://{{target}}",
		"roe":           map[string]any{"mode": "enforce", "in_scope": []string{"10.0.0.0/8"}},
		"skill_hints":   []string{"web-entry-recon", "sqli-login-bypass"},
		"timeout_seconds": 3600,
	})
	if r.StatusCode != 200 {
		t.Fatalf("建模板: %d %s", r.StatusCode, b)
	}

	// 从模板建任务。
	r, b = apiCall(t, "POST", base+"/api/tasks/from-template", tok, map[string]any{
		"template_name": "内网 Web 快速评估",
		"target":        "10.0.0.5",
	})
	if r.StatusCode != 200 {
		t.Fatalf("模板建任务: %d %s", r.StatusCode, b)
	}
	if !strings.Contains(string(b), `"id"`) {
		t.Fatalf("响应缺任务 id: %s", b)
	}

	// RoE 落库（task 级）。
	var n int
	_ = st.QueryRow(dctx, `SELECT count(*) FROM roe_rules WHERE scope='task'`).Scan(&n)
	if n == 0 {
		t.Fatal("模板 RoE 未落库")
	}
}
