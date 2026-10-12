//go:build docker

package server

import (
	"context"
	"fmt"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcp "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"mtx/internal/store"
)

// setupRBAC 建 PG + server + admin setup，返回 (base, adminTok)。
func setupRBAC(t *testing.T) (string, string, *store.Store) {
	t.Helper()
	ctx := context.Background()
	pg, err := tcp.Run(ctx, "postgres:16-alpine",
		tcp.WithDatabase("mtx"), tcp.WithUsername("mtx"), tcp.WithPassword("mtx"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	dsn, _ := pg.ConnectionString(ctx, "sslmode=disable")
	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	srv, err := New(Options{Store: s, MasterKey: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", DataDir: t.TempDir(), ProxyAddr: "-"})
	if err != nil {
		t.Fatal(err)
	}
	ts := &http.Server{Addr: ":0", Handler: srv.Router()}
	ln := startOnFree(t, ts)
	base := "http://" + ln.Addr().String()

	_, _ = apiCall(t, "POST", base+"/api/setup", "", map[string]string{"password": "Passw0rd!123"})
	_, body := apiCall(t, "POST", base+"/api/auth/login", "", map[string]string{"username": "admin", "password": "Passw0rd!123"})
	var out struct{ Token string `json:"token"` }
	_ = json.Unmarshal(body, &out)
	return base, out.Token, s
}

// TestRBACRoleMatrix 角色矩阵（Review Focus 1+5）：
// viewer 403（非 404）；auditor 只读可 GET 不可 POST 任务；operator 可建不可管用户；admin 全能。
func TestRBACRoleMatrix(t *testing.T) {
	base, adminTok, st := setupRBAC(t)

	// admin 建三个用户。
	for _, u := range []map[string]string{
		{"username": "viewer1", "password": "Viewer123!", "role": "viewer"},
		{"username": "auditor1", "password": "Audit123!", "role": "auditor"},
		{"username": "oper1", "password": "Oper123!", "role": "operator"},
	} {
		r, b := apiCall(t, "POST", base+"/api/users", adminTok, u)
		t.Logf("建用户 %s → %d: %s", u["username"], r.StatusCode, string(b))
		if r.StatusCode != 200 {
			t.Fatalf("建用户 %s: %d %s", u["username"], r.StatusCode, b)
		}
	}
	// 诊断：DB 里有哪些用户。
	dctx := context.Background()
	rows, _ := st.Query(dctx, `SELECT username, role FROM users`)
	for rows.Next() {
		var un, rl string
		_ = rows.Scan(&un, &rl)
		t.Logf("DB user: %s (%s)", un, rl)
	}
	rows.Close()

	loginAs := func(u, p string) string {
		resp, b := apiCall(t, "POST", base+"/api/auth/login", "", map[string]string{"username": u, "password": p})
		var o struct{ Token string `json:"token"` }
		_ = json.Unmarshal(b, &o)
		if o.Token == "" {
			t.Logf("loginAs(%s) 失败: status=%d body=%s", u, resp.StatusCode, string(b))
		}
		return o.Token
	}
	viewer := loginAs("viewer1", "Viewer123!")
	auditor := loginAs("auditor1", "Audit123!")
	oper := loginAs("oper1", "Oper123!")

	cases := []struct {
		desc       string
		token      string
		method     string
		path       string
		body       any
		wantStatus int
	}{
		{"viewer 建任务 → 403", viewer, "POST", "/api/tasks", map[string]string{"target_text": "10.0.0.1"}, 403},
		{"viewer 列任务 → 200", viewer, "GET", "/api/tasks", nil, 200},
		{"viewer 建用户 → 403", viewer, "POST", "/api/users", map[string]string{"username": "x", "password": "x12345678", "role": "viewer"}, 403},
		{"viewer 审批 → 403", viewer, "POST", "/api/intercept/pending/1/decide", map[string]string{"action": "allow"}, 403},
		{"auditor 建任务 → 403", auditor, "POST", "/api/tasks", map[string]string{"target_text": "10.0.0.1"}, 403},
		{"auditor 审批 → 200（pending 不存在→404/400 皆非 403）", auditor, "POST", "/api/intercept/pending/999/decide", map[string]string{"action": "allow"}, 400},
		{"operator 建任务 → 400（无 LLM，非 403）", oper, "POST", "/api/tasks", map[string]string{"target_text": "10.0.0.1"}, 400},
		{"operator 建用户 → 403", oper, "POST", "/api/users", map[string]string{"username": "y", "password": "y12345678", "role": "viewer"}, 403},
		{"admin 建用户 → 200", adminTok, "POST", "/api/users", map[string]string{"username": "zz", "password": "z12345678", "role": "viewer"}, 200},
	}
	for _, c := range cases {
		r, b := apiCall(t, c.method, base+c.path, c.token, c.body)
		if r.StatusCode != c.wantStatus {
			t.Errorf("%s: got %d (%s), want %d", c.desc, r.StatusCode, clip(string(b)), c.wantStatus)
		}
		// 403 而非 404（信息不泄漏）。
		if strings.Contains(c.desc, "403") && r.StatusCode == 404 {
			t.Errorf("%s: 404 泄漏路径存在性", c.desc)
		}
	}
}

func clip(s string) string {
	if len(s) > 80 {
		return s[:80]
	}
	return s
}

// TestFindingRetest 复测闭环（Review Focus：retester 执行面）：
// POST /api/findings/{id}/retest → oracle 复跑 → finding_retests 记录 + 状态流转。
func TestFindingRetest(t *testing.T) {
	base, tok, st := setupRBAC(t)
	dctx := context.Background()

	// 预置 task + finding（evidence 含 replay）。
	var taskID, fid int64
	if err := st.QueryRow(dctx, `INSERT INTO tasks (title,target_text,status,created_at) VALUES ('t','x','created',now()) RETURNING id`).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	evidence := `[{"kind":"replay","url":"http://127.0.0.1:9/x","expect_body":"NOPE"}]`
	err := st.QueryRow(dctx, `INSERT INTO findings (task_id, vulnclass, severity, title, summary, evidence, confidence, status, verify_state, created_at)
		VALUES ($1,'test','medium','T','S',$2::jsonb,'runtime_observed','candidate','unconfirmed',now()) RETURNING id`, taskID, evidence).Scan(&fid)
	if err != nil {
		t.Fatal(err)
	}

	r, b := apiCall(t, "POST", base+"/api/findings/"+itoa(fid)+"/retest", tok, nil)
	if r.StatusCode != 200 {
		t.Fatalf("retest: %d %s", r.StatusCode, b)
	}
	// finding_retests 有记录。
	var n int
	_ = st.QueryRow(dctx, `SELECT count(*) FROM finding_retests WHERE finding_id=$1`, fid).Scan(&n)
	if n != 1 {
		t.Fatalf("retests 记录 = %d, want 1", n)
	}
	// 复测未命中 → inconclusive（不是武断 fixed）。
	var concl string
	_ = st.QueryRow(dctx, `SELECT conclusion FROM finding_retests WHERE finding_id=$1`, fid).Scan(&concl)
	if concl != "inconclusive" {
		t.Fatalf("conclusion = %q, want inconclusive（特征未复现但不武断）", concl)
	}
}

func itoa(v int64) string {
	return fmt.Sprintf("%d", v)
}
