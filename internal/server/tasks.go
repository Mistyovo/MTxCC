package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/docker/docker/client"

	"mtx/internal/roe"
	"mtx/internal/sandbox"

	"mtx/internal/agents"
	"mtx/internal/engine"
	"mtx/internal/harness"
	"mtx/internal/llm"
	"mtx/internal/tools"
)

// buildPool 从已启用 profile 构建 provider 池（顺序即故障转移链）。
func (s *Server) buildPool() (llm.Provider, []llm.Profile, error) {
	rows, err := s.store.Query(sqlCtx(), `SELECT name, protocol, base_url, model, max_tokens, api_key_enc
		FROM llm_profiles WHERE enabled ORDER BY id`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var profiles []llm.Profile
	for rows.Next() {
		var p llm.Profile
		var keyEnc []byte
		if err := rows.Scan(&p.Name, &p.Protocol, &p.BaseURL, &p.Model, &p.MaxTokens, &keyEnc); err != nil {
			return nil, nil, err
		}
		p.APIKey = s.decryptKey(keyEnc)
		p.Enabled = true
		profiles = append(profiles, p)
	}
	if len(profiles) == 0 {
		return nil, nil, fmt.Errorf("无启用的 LLM profile")
	}
	providers := make([]llm.Provider, len(profiles))
	for i, p := range profiles {
		switch p.Protocol {
		case "anthropic":
			providers[i] = llm.NewAnthropic(p)
		case "openai_responses":
			providers[i] = llm.NewOpenAIResponses(p)
		default:
			providers[i] = llm.NewOpenAI(p)
		}
	}
	return llm.NewPool(providers...), profiles, nil
}

// targetHost 从目标文本提取主机（粗提取：URL 或裸 host）。
func targetHost(t string) string {
	t = strings.TrimSpace(t)
	if u, err := url.Parse(t); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	fields := strings.Fields(t)
	if len(fields) > 0 {
		return fields[0]
	}
	return t
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TargetText     string          `json:"target_text"`
		TimeoutSeconds int             `json:"timeout_seconds"`
		RoE            json.RawMessage `json:"roe"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.TargetText) == "" {
		errJSON(w, http.StatusBadRequest, "target_text 必填")
		return
	}
	if in.TimeoutSeconds <= 0 {
		in.TimeoutSeconds = 7200
	}
	pool, profiles, err := s.buildPool()
	if err != nil {
		errJSON(w, http.StatusBadRequest, "请先配置 LLM（系统设置 → LLM）："+err.Error())
		return
	}
	// LLM 网关防呆（spec §7.3）：目标不得是模型网关自身。
	for _, p := range profiles {
		if isGatewayTarget(in.TargetText, p.BaseURL) {
			errJSON(w, http.StatusBadRequest, "目标疑似 LLM 网关本身，拒绝创建（防误伤）")
			return
		}
	}

	// 落库：任务 + 探索 + 根 goal（M0：单 goal 兜底，goals 分解器 M1 接入）。
	var taskID int64
	err = s.store.QueryRow(sqlCtx(), `INSERT INTO tasks (title, target_text, status, timeout_seconds, created_at)
		VALUES ('', $1, 'queued', $2, now()) RETURNING id`, in.TargetText, in.TimeoutSeconds).Scan(&taskID)
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	var expID int64
	if err := s.store.QueryRow(sqlCtx(), `INSERT INTO explorations (task_id, created_at)
		VALUES ($1, now()) RETURNING id`, taskID).Scan(&expID); err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	if _, err := s.store.Exec(sqlCtx(), `INSERT INTO exploration_nodes
		(exploration_id, kind, state, title, created_at, updated_at)
		VALUES ($1, 'goal', 'open', $2, now(), now())`, expID, "完成对目标的授权渗透测试"); err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	// 任务级 RoE 落库（C2a：guard.loadRoE 读取）。
	if len(in.RoE) > 0 {
		if _, err := roe.ParseRules(in.RoE); err != nil {
			errJSON(w, http.StatusBadRequest, "RoE JSON 无效："+err.Error())
			return
		}
		if _, err := s.store.Exec(sqlCtx(),
			`INSERT INTO roe_rules (scope, task_id, data) VALUES ('task', $1, $2::jsonb)`, taskID, string(in.RoE)); err != nil {
			errJSON(w, 500, err.Error())
			return
		}
	}
	// 种子意图：首轮直接派一条"全量侦察"意图，planner 随后接管。
	if _, err := s.graph.AddIntent(sqlCtx(), expID, nil, "对目标 "+targetHost(in.TargetText)+" 做初始侦察（端口/服务/Web 指纹）", 5, nil); err != nil {
		errJSON(w, 500, err.Error())
		return
	}

	// 起引擎（异步）。
	maxTokens := 0
	if len(profiles) > 0 {
		maxTokens = profiles[0].MaxTokens
	}
	go s.launchEngine(taskID, pool, maxTokens)

	writeJSON(w, 200, map[string]any{"id": taskID, "status": "queued"})
}

// launchEngine 组装并运行该任务的引擎。
func (s *Server) launchEngine(taskID int64, pool llm.Provider, maxTokens int) {
	dataDir := s.opts.DataDir + "/tasks"
	_ = os.MkdirAll(dataDir, 0o750)
	var bashExec tools.BashExec = tools.NewLocalExec(dataDir, nil) // BashLocal 开发模式；生产沙箱见 deploy（MTX_SANDBOX）
	e := engine.NewEngine(engine.Deps{
		Store: s.store, Graph: s.graph, Pool: pool,
		Bash:       bashExec,
		Transcript: harness.NewFileTranscript(s.opts.DataDir + "/transcripts"),
		Model:      "auto",
		MaxTokens:  maxTokens,
		Workers:    2, Debounce: 500 * time.Millisecond,
		CtxWindowK: 120, // 120K token 窗口（GLM 兼容；profile 细化 M3 接）
		Perms: s.guard.ForTask(taskID),
		ActivityFn: func(intentID int64, role, kind, payload string) {
			_, _ = s.store.Exec(sqlCtx(),
				`INSERT INTO activity (task_id, node_id, agent_role, kind, payload) VALUES ($1,$2,$3,$4,$5::jsonb)`,
				taskID, intentID, role, kind, payload)
		},
	})
	_ = agents.SeedPrompts(sqlCtx(), s.store)
	s.mu.Lock()
	s.engines[taskID] = e
	s.mu.Unlock()
	_, _ = s.store.Exec(sqlCtx(), `UPDATE tasks SET status='running', started_at=now() WHERE id=$1`, taskID)
	_ = e.Run(sqlCtx(), taskID)
}

func (s *Server) handleListTasks(w http.ResponseWriter, _ *http.Request) {
	rows, err := s.store.Query(sqlCtx(), `SELECT id, title, target_text, status, created_at
		FROM tasks ORDER BY id DESC LIMIT 100`)
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var title, target, status string
		var created string
		_ = rows.Scan(&id, &title, &target, &status, &created)
		out = append(out, map[string]any{"id": id, "title": title, "target_text": target, "status": status, "created_at": created})
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleTaskControl(w http.ResponseWriter, r *http.Request) {
	var id int64
	if _, err := fmt.Sscanf(pathParam(r), "%d", &id); err != nil {
		errJSON(w, http.StatusBadRequest, "bad id")
		return
	}
	var in struct{ Action string `json:"action"` }
	_ = readJSON(r, &in)
	switch in.Action {
	case "pause":
		_, _ = s.store.Exec(sqlCtx(), `UPDATE tasks SET status='paused' WHERE id=$1 AND status='running'`, id)
	case "resume":
		_, _ = s.store.Exec(sqlCtx(), `UPDATE tasks SET status='running' WHERE id=$1 AND status='paused'`, id)
	case "cancel":
		s.mu.Lock()
		if e, ok := s.engines[id]; ok {
			_ = e.Kill(sqlCtx(), 0)
			delete(s.engines, id)
		}
		s.mu.Unlock()
		_, _ = s.store.Exec(sqlCtx(), `UPDATE tasks SET status='failed', finished_at=now() WHERE id=$1`, id)
	default:
		errJSON(w, http.StatusBadRequest, "action ∈ pause|resume|cancel")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// handleTaskSteer 对在跑意图注入纠偏（I6 API 面；工具面 M1）。
func (s *Server) handleTaskSteer(w http.ResponseWriter, r *http.Request) {
	var taskID int64
	if _, err := fmt.Sscanf(r.PathValue("id"), "%d", &taskID); err != nil {
		errJSON(w, http.StatusBadRequest, "bad id")
		return
	}
	var in struct {
		IntentID int64  `json:"intent_id"`
		Message  string `json:"message"`
	}
	if err := readJSON(r, &in); err != nil || in.Message == "" {
		errJSON(w, http.StatusBadRequest, "message 必填")
		return
	}
	s.mu.Lock()
	e := s.engines[taskID]
	s.mu.Unlock()
	if e == nil {
		errJSON(w, 404, "任务引擎未在运行")
		return
	}
	if err := e.Steer(r.Context(), in.IntentID, in.Message); err != nil {
		errJSON(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleTaskGraph(w http.ResponseWriter, r *http.Request) {
	var taskID int64
	if _, err := fmt.Sscanf(pathParam(r), "%d", &taskID); err != nil {
		errJSON(w, http.StatusBadRequest, "bad id")
		return
	}
	var expID int64
	if err := s.store.QueryRow(sqlCtx(),
		`SELECT id FROM explorations WHERE task_id=$1`, taskID).Scan(&expID); err != nil {
		errJSON(w, 404, "任务不存在")
		return
	}
	nodes, _ := s.store.Query(sqlCtx(), `SELECT id, kind, state, title FROM exploration_nodes WHERE exploration_id=$1 ORDER BY id`, expID)
	defer nodes.Close()
	nodeList := []map[string]any{}
	for nodes.Next() {
		var id int64
		var kind, state, title string
		_ = nodes.Scan(&id, &kind, &state, &title)
		nodeList = append(nodeList, map[string]any{"id": id, "kind": kind, "state": state, "title": title})
	}
	edges, _ := s.store.Query(sqlCtx(), `SELECT src, dst, kind FROM exploration_edges WHERE exploration_id=$1`, expID)
	defer edges.Close()
	edgeList := []map[string]any{}
	for edges.Next() {
		var src, dst int64
		var kind string
		_ = edges.Scan(&src, &dst, &kind)
		edgeList = append(edgeList, map[string]any{"source": src, "target": dst, "kind": kind})
	}
	writeJSON(w, 200, map[string]any{"nodes": nodeList, "edges": edgeList})
}

func pathParam(r *http.Request) string {
	return r.PathValue("id")
}

// ensureSandbox 建任务专属加固容器并尽力注入出口围栏（C2b 生产路径）。
func (s *Server) ensureSandbox(taskID int64) (*sandbox.Instance, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	m := sandbox.NewManager(cli)
	dir := s.opts.DataDir + "/tasks"
	inst, err := m.Ensure(context.Background(), sandbox.Spec{
		Image: s.opts.SandboxImage,
		Name:  fmt.Sprintf("mtx-task-%d", taskID),
		WorkdirVol: dir,
	})
	if err != nil {
		return nil, err
	}
	// RoE 出口围栏（尽力而为；失败不阻断任务，解析层仍在岗）。
	if r := s.loadRoE(taskID); r.Mode == "enforce" {
		_ = sandbox.InjectEgress(context.Background(), m, inst, r)
	}
	return inst, nil
}

// proxyEnv 返回留痕代理注入配置（未启用时 Enabled=false）。
func (s *Server) proxyEnv() *tools.ProxyEnv {
	if s.proxy == nil {
		return &tools.ProxyEnv{Enabled: false}
	}
	addr := s.proxy.ListenerAddr()
	if addr == "" {
		return &tools.ProxyEnv{Enabled: false}
	}
	return &tools.ProxyEnv{
		Addr: "http://" + strings.Split(addr, ":")[0] + ":8788", Enabled: true,
		CACertPath: s.opts.DataDir + "/ca/mitm.pem",
	}
}

// isGatewayTarget 判定目标是否就是 LLM 网关自身（防误伤，按 host:port 完整比较）。
func isGatewayTarget(target, gateway string) bool {
	tHost, tPort := splitAuthority(target)
	gHost, gPort := splitAuthority(gateway)
	return tHost == gHost && tPort == gPort && tHost != ""
}

// splitAuthority 提取 host 与端口（协议默认端口补全）。
func splitAuthority(raw string) (host, port string) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", ""
	}
	host = u.Hostname()
	port = u.Port()
	if port == "" {
		switch u.Scheme {
		case "https":
			port = "443"
		default:
			port = "80"
		}
	}
	return strings.ToLower(host), port
}
