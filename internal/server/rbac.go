package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"mtx/internal/agents"
	"mtx/internal/notify"
	"mtx/internal/harness"
	"mtx/internal/report"
	"mtx/internal/verify"

	"golang.org/x/crypto/bcrypt"
)

type testingT interface {
	Helper()
	Fatal(...any)
}

// startOnFree 在随机端口启动并返回 listener（测试用）。
func startOnFree(t testingT, srv *http.Server) net.Listener {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	return ln
}

func jsonUnmarshal(b string, v any) error { return json.Unmarshal([]byte(b), v) }

// requireRole 角色守卫（Review Focus 1）：viewer 只读；auditor 读+审批；operator 读+任务操作；admin 全能。
func (s *Server) requireRole(roles ...string) func(http.Handler) http.Handler {
	allow := map[string]bool{}
	for _, r := range roles {
		allow[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role := s.roleOfRequest(r)
			if !allow[role] {
				errJSON(w, http.StatusForbidden, "权限不足（需要角色："+fmt.Sprint(roles)+"）")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// roleOfRequest 从 token 提取用户角色（JWT 第一段是用户名）。
func (s *Server) roleOfRequest(r *http.Request) string {
	tok := bearer(r)
	user := s.userOfToken(tok)
	if user == "" {
		return ""
	}
	var role string
	if err := s.store.QueryRow(sqlCtx(),
		`SELECT role FROM users WHERE username=$1 AND NOT disabled`, user).Scan(&role); err != nil {
		return ""
	}
	return role
}

func bearer(r *http.Request) string {
	tok := r.Header.Get("Authorization")
	if len(tok) > 7 && tok[:7] == "Bearer " {
		return tok[7:]
	}
	return r.URL.Query().Get("token")
}

// userOfToken 解 token 的用户段（HMAC 校验在 verifyToken；这里仅取 payload）。
func (s *Server) userOfToken(tok string) string {
	if !s.verifyToken(tok) {
		return ""
	}
	for i := 0; i < len(tok); i++ {
		if tok[i] == '.' {
			return tok[:i]
		}
	}
	return ""
}

// handleCreateUser admin 创建用户。
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := readJSON(r, &in); err != nil {
		errJSON(w, http.StatusBadRequest, "bad request")
		return
	}
	switch in.Role {
	case "admin", "auditor", "operator", "viewer":
	default:
		errJSON(w, http.StatusBadRequest, "role ∈ admin|auditor|operator|viewer")
		return
	}
	if len(in.Username) < 2 || len(in.Password) < 8 {
		errJSON(w, http.StatusBadRequest, "用户名 ≥2 位且密码 ≥8 位")
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if _, err := s.store.Exec(sqlCtx(),
		`INSERT INTO users (username, pass_hash, role) VALUES ($1,$2,$3)`, in.Username, string(hash), in.Role); err != nil {
		errJSON(w, http.StatusConflict, "用户已存在")
		return
	}
	s.audit(r, "user.create", "user", in.Username, map[string]string{"role": in.Role})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// handleListUsers 用户列表（admin/auditor 可见）。
func (s *Server) handleListUsers(w http.ResponseWriter, _ *http.Request) {
	rows, err := s.store.Query(sqlCtx(), `SELECT username, role, disabled, created_at FROM users ORDER BY id`)
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var u, role, created string
		var dis bool
		_ = rows.Scan(&u, &role, &dis, &created)
		out = append(out, map[string]any{"username": u, "role": role, "disabled": dis, "created_at": created})
	}
	writeJSON(w, 200, out)
}

// audit 写审计（Review Focus 2：只增）。
func (s *Server) audit(r *http.Request, action, targetType, targetID string, detail map[string]string) {
	user := s.userOfToken(bearer(r))
	if detail == nil {
		detail = map[string]string{}
	}
	_, _ = s.store.Exec(sqlCtx(),
		`INSERT INTO audit_log (user_id, action, target_type, target_id, detail)
		 SELECT id, $1, $2, $3, $4::jsonb FROM users WHERE username=$5`,
		action, targetType, targetID, jsonStr(detail), user)
}

func jsonStr(m map[string]string) string {
	b, _ := json.Marshal(m)
	return string(b)
}



// handleListAudit 审计查询（admin/auditor）。
func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	limit := r.URL.Query().Get("limit")
	if limit == "" {
		limit = "100"
	}
	rows, err := s.store.Query(sqlCtx(), `SELECT a.id, coalesce(u.username,''), a.action, a.target_type,
		a.target_id, a.detail::text, a.created_at
		FROM audit_log a LEFT JOIN users u ON u.id=a.user_id
		ORDER BY a.id DESC LIMIT $1::int`, limit)
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var user, action, tt, tid, detail, created string
		_ = rows.Scan(&id, &user, &action, &tt, &tid, &detail, &created)
		out = append(out, map[string]any{"id": id, "user": user, "action": action,
			"target_type": tt, "target_id": tid, "detail": detail, "created_at": created})
	}
	writeJSON(w, 200, out)
}

// handleTaskReport 报告导出。
func (s *Server) handleTaskReport(w http.ResponseWriter, r *http.Request) {
	var taskID int64
	if _, err := fmt.Sscanf(r.PathValue("id"), "%d", &taskID); err != nil {
		errJSON(w, http.StatusBadRequest, "bad id")
		return
	}
	var targetText string
	if err := s.store.QueryRow(sqlCtx(), `SELECT target_text FROM tasks WHERE id=$1`, taskID).Scan(&targetText); err != nil {
		errJSON(w, 404, "任务不存在")
		return
	}
	rows, err := s.store.Query(sqlCtx(), `SELECT id, vulnclass, severity, title, status, verify_state,
		summary, evidence::text FROM findings WHERE task_id=$1 ORDER BY id`, taskID)
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	defer rows.Close()
	data := report.Data{TaskID: taskID, TargetText: targetText, Status: "done"}
	for rows.Next() {
		var f report.Finding
		var ev string
		_ = rows.Scan(&f.ID, &f.Vulnclass, &f.Severity, &f.Title, &f.Status, &f.VerifyState, &f.Summary, &ev)
		_ = json.Unmarshal([]byte(ev), &f.Evidence)
		data.Findings = append(data.Findings, f)
	}
	md := report.RenderMarkdown(data)
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = w.Write([]byte(md))
	s.audit(r, "report.export", "task", fmt.Sprint(taskID), nil)
}

// handleFindingRetest 复测闭环：oracle 复跑 → finding_retests 记录 → 状态流转
//（reproduced→verified / inconclusive→保持 / fixed→status=fixed）。
func (s *Server) handleFindingRetest(w http.ResponseWriter, r *http.Request) {
	var fid int64
	if _, err := fmt.Sscanf(r.PathValue("id"), "%d", &fid); err != nil {
		errJSON(w, http.StatusBadRequest, "bad id")
		return
	}
	var evidence string
	var status, verifyState string
	if err := s.store.QueryRow(sqlCtx(),
		`SELECT evidence::text, status, verify_state FROM findings WHERE id=$1`, fid).
		Scan(&evidence, &status, &verifyState); err != nil {
		errJSON(w, 404, "finding 不存在")
		return
	}

	// 机器复测（oracle）。
	v := verify.AutoVerify(verify.RealSleeper(), evidence)
	conclusion := "inconclusive"
	switch {
	case v == verify.Verified:
		conclusion = "reproduced"
	case status == "fixed":
		conclusion = "fixed"
	}

	_, err := s.store.Exec(sqlCtx(),
		`INSERT INTO finding_retests (finding_id, conclusion, detail, created_at)
		 VALUES ($1,$2,$3,now())`, fid, conclusion, "自动复测：oracle "+v.String())
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	// 状态流转。
	switch conclusion {
	case "reproduced":
		_, _ = s.store.Exec(sqlCtx(),
			`UPDATE findings SET verify_state='verified', status='verified' WHERE id=$1`, fid)
	case "fixed":
		_, _ = s.store.Exec(sqlCtx(),
			`UPDATE findings SET status='fixed' WHERE id=$1`, fid)
	}
	s.audit(r, "finding.retest", "finding", fmt.Sprint(fid), map[string]string{"conclusion": conclusion})
	writeJSON(w, 200, map[string]any{"conclusion": conclusion, "verify_state": v.String()})
}

// handleTaskChat mainagent 人在环路对话（操舵：add_hint/add_intent/steer_work）。
func (s *Server) handleTaskChat(w http.ResponseWriter, r *http.Request) {
	var taskID int64
	if _, err := fmt.Sscanf(r.PathValue("id"), "%d", &taskID); err != nil {
		errJSON(w, http.StatusBadRequest, "bad id")
		return
	}
	var in struct{ Message string `json:"message"` }
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Message) == "" {
		errJSON(w, http.StatusBadRequest, "message 必填")
		return
	}
	var expID int64
	if err := s.store.QueryRow(sqlCtx(), `SELECT id FROM explorations WHERE task_id=$1`, taskID).Scan(&expID); err != nil {
		errJSON(w, 404, "任务不存在")
		return
	}

	pool, profiles, err := s.buildPool()
	if err != nil {
		errJSON(w, 400, "请先配置 LLM")
		return
	}
	maxTokens := 8192
	if len(profiles) > 0 {
		maxTokens = profiles[0].MaxTokens
	}
	ec := agents.ExecContext{TaskID: taskID, ExpID: expID, Role: "mainagent"}
	deps := agents.CommonDeps{
		Graph: s.graph, MaxTokens: maxTokens,
		SteerFn: func(intentID int64, msg string) error {
			s.mu.Lock()
			e := s.engines[taskID]
			s.mu.Unlock()
			if e == nil {
				return fmt.Errorf("引擎未运行")
			}
			return e.Steer(r.Context(), intentID, msg)
		},
		KillFn: func(intentID int64) error {
			s.mu.Lock()
			e := s.engines[taskID]
			s.mu.Unlock()
			if e == nil {
				return fmt.Errorf("引擎未运行")
			}
			return e.Kill(r.Context(), intentID)
		},
	}
	opts := agents.AssembleMainAgent(deps, ec)
	opts.Provider = pool
	opts.Model = "auto"
	sess := harness.NewSession(opts, fmt.Sprintf("t%d-mainagent", taskID))
	res, err := sess.Query(r.Context(), opts.System, in.Message)
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	s.audit(r, "task.chat", "task", fmt.Sprint(taskID), map[string]string{"message": in.Message})
	writeJSON(w, 200, map[string]any{"reply": res.FinalText, "turns": res.Turns})
}

// handleAddChannel 注册通知通道（admin）。
func (s *Server) handleAddChannel(w http.ResponseWriter, r *http.Request) {
	var c notify.Channel
	if err := readJSON(r, &c); err != nil {
		errJSON(w, http.StatusBadRequest, "bad request")
		return
	}
	if err := s.notify.Register(c); err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "notify.channel.add", "channel", c.Name, map[string]string{"type": c.Type})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// handleTestNotify 发送测试通知。
func (s *Server) handleTestNotify(w http.ResponseWriter, r *http.Request) {
	if err := s.notify.Deliver(notify.Event{
		Kind: "system.test", Title: "MTxCC 通知测试",
		Detail: "通道连通性验证成功",
	}); err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
