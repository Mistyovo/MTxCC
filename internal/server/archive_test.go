//go:build docker

package server

import (
	"context"
	"strings"
	"testing"
)

// TestTaskArchive 任务归档（合规留存）：导出全证据包 JSON
//（任务+发现+覆盖度+审计——报告附件可复现）。
func TestTaskArchive(t *testing.T) {
	base, tok, st := setupRBAC(t)
	dctx := context.Background()
	_, _ = st.Exec(dctx, `INSERT INTO llm_profiles (name,protocol,base_url,model,max_tokens,enabled) VALUES ('fake-arc','openai','http://127.0.0.1:9','fake',4096,true)`)

	// 建任务（模板路径）+ 预置发现。
	r, b := apiCall(t, "POST", base+"/api/tasks/from-template", tok, map[string]any{})
	_ = r
	_ = b
	r, b = apiCall(t, "POST", base+"/api/templates", tok, map[string]any{
		"name": "归档测试模板", "target_pattern": "http://{{target}}",
		"roe": map[string]any{"mode": "enforce", "in_scope": []string{"10.0.0.0/8"}},
	})
	if r.StatusCode != 200 {
		t.Fatalf("建模板: %d %s", r.StatusCode, b)
	}
	r, b = apiCall(t, "POST", base+"/api/tasks/from-template", tok, map[string]any{
		"template_name": "归档测试模板", "target": "10.0.0.9"})
	if r.StatusCode != 200 {
		t.Fatalf("模板建任务: %d %s", r.StatusCode, b)
	}

	// 归档导出。
	var taskID int64
	err := st.QueryRow(dctx, `SELECT id FROM tasks ORDER BY id DESC LIMIT 1`).Scan(&taskID)
	t.Logf("taskID=%d err=%v", taskID, err)
	r, b = apiCall(t, "GET", base+"/api/tasks/"+itoa(taskID)+"/archive", tok, nil)
	if r.StatusCode != 200 {
		t.Fatalf("archive: %d %s", r.StatusCode, b)
	}
	// 归包含任务/发现/覆盖度三段。
	for _, want := range []string{"task", "findings", "coverage"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("归档缺 %s", want)
		}
	}
}
