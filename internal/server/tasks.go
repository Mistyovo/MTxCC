package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mtx/internal/agents"
	"mtx/internal/engine"
	"mtx/internal/harness"
	"mtx/internal/llm"
	"mtx/internal/tools"
)

// buildPool 从已启用 profile 构建 provider 池（顺序即故障转移链）。
func (s *Server) buildPool() (llm.Provider, []llm.Profile, error) {
	rows, err := s.store.Query(sqlCtx(), `SELECT name, protocol, base_url, model
		FROM llm_profiles WHERE enabled ORDER BY id`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var profiles []llm.Profile
	for rows.Next() {
		var p llm.Profile
		if err := rows.Scan(&p.Name, &p.Protocol, &p.BaseURL, &p.Model); err != nil {
			return nil, nil, err
		}
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
		TargetText     string `json:"target_text"`
		TimeoutSeconds int    `json:"timeout_seconds"`
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
	th := strings.ToLower(targetHost(in.TargetText))
	for _, p := range profiles {
		pu, err := url.Parse(p.BaseURL)
		if err == nil && pu.Hostname() != "" && strings.EqualFold(pu.Hostname(), th) {
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
	// 种子意图：首轮直接派一条"全量侦察"意图，planner 随后接管。
	if _, err := s.graph.AddIntent(sqlCtx(), expID, nil, "对目标 "+targetHost(in.TargetText)+" 做初始侦察（端口/服务/Web 指纹）", 5, nil); err != nil {
		errJSON(w, 500, err.Error())
		return
	}

	// 起引擎（异步）。
	go s.launchEngine(taskID, pool)

	writeJSON(w, 200, map[string]any{"id": taskID, "status": "queued"})
}

// launchEngine 组装并运行该任务的引擎。
func (s *Server) launchEngine(taskID int64, pool llm.Provider) {
	dataDir := s.opts.DataDir + "/tasks"
	var bashExec tools.BashExec = tools.NewLocalExec(dataDir, nil) // BashLocal 模式；沙箱模式 Task 24 接线
	e := engine.NewEngine(engine.Deps{
		Store: s.store, Graph: s.graph, Pool: pool,
		Bash:       bashExec,
		Transcript: harness.NewFileTranscript(s.opts.DataDir + "/transcripts"),
		Model:      "auto",
		Workers:    2, Debounce: 500 * time.Millisecond,
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
