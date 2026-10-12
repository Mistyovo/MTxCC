// Package server 提供 MTxCC 的 HTTP API 与内嵌前端（spec §10.1）。
// New() 是产品装配入口：内建 PG 连接/迁移、审批门、RoE 加载（C1 修复）。
package server

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

	"mtx/internal/engine"
	"mtx/internal/graph"
	"mtx/internal/guard"
	"mtx/internal/notify"
	"mtx/internal/roe"
	"mtx/internal/store"
	"mtx/internal/traffic"
)

// Options 配置服务端（产品装配参数，来自环境变量）。
type Options struct {
	DatabaseURL string // PostgreSQL DSN（New 内部建池+迁移）
	MasterKey   string // base64 32B（api_key AES-GCM 与 JWT 派生）
	DataDir     string
	BashLocal     bool   // true=worker 用本地执行（开发/测试；生产走沙箱）
	ProxyAddr     string // MITM 代理监听地址（默认 :8788；空则不启用留痕）
	SandboxImage  string // 任务沙箱镜像（非空即启用 per-task 容器，C2b 生产路径）
	Store         *store.Store // 测试注入替代 DatabaseURL
}

// Server 聚合全部 API。
type Server struct {
	opts    Options
	store   *store.Store
	graph   *graph.GraphStore
	guard   *guard.Guard
	mu      sync.Mutex
	engines map[int64]*engine.Engine
	cancels map[int64]context.CancelFunc
	gcm     cipher.AEAD

	traffic *traffic.Index
	proxy   *traffic.ProxyServer
	notify  *notify.Notifier
}

// New 建服务端：打开数据库（幂等迁移）并装配审批门。
func New(o Options) (*Server, error) {
	s := &Server{
		opts: o, engines: map[int64]*engine.Engine{}, cancels: map[int64]context.CancelFunc{},
	}
	if o.Store != nil {
		s.store = o.Store
	} else {
		st, err := store.Open(context.Background(), o.DatabaseURL)
		if err != nil {
			return nil, err
		}
		s.store = st
	}
	s.graph = graph.NewGraphStore(s.store)
	s.notify = notify.New()

	// 流量留痕层（I7）：索引常开；代理可选。
	if o.DataDir != "" {
		if idx, err := traffic.OpenIndex(o.DataDir + "/traffic.db"); err == nil {
			s.traffic = idx
		}
	}
	if o.ProxyAddr != "-" && o.DataDir != "" { // "-" 显式禁用
		addr := o.ProxyAddr
		if addr == "" {
			addr = ":8788"
		}
		if p, err := traffic.NewProxyServer(addr, o.DataDir+"/ca", s.traffic); err == nil {
			if err := p.Start(); err == nil {
				s.proxy = p
			}
		}
	}
	// RoE 加载：任务级 → 全局兜底（C2a）。
	s.guard = guard.NewGuard(guard.NewMemorySource(), s.loadRoE, nil)
	// 主密钥 → AES-GCM（api_key 落库加密）。
	if raw, err := base64.StdEncoding.DecodeString(o.MasterKey); err == nil && len(raw) == 32 {
		if blk, err := aes.NewCipher(raw); err == nil {
			if gcm, err := cipher.NewGCM(blk); err == nil {
				s.gcm = gcm
			}
		}
	}
	return s, nil
}

// Close 释放连接与代理。
func (s *Server) Close() {
	if s.proxy != nil {
		_ = s.proxy.Stop()
	}
	if s.traffic != nil {
		_ = s.traffic.Close()
	}
	s.store.Close()
}

// RecoverRunning 崩溃恢复（I4）：重启后把卡死的 running 意图重置 open、
// running 任务重新拉起引擎。main 启动时调用。
func (s *Server) RecoverRunning() {
	ctx := sqlCtx()
	_, _ = s.store.Exec(ctx, `UPDATE exploration_nodes SET state='open', owner=''
		WHERE kind='intent' AND state='running'`)
	rows, err := s.store.Query(ctx, `SELECT id FROM tasks WHERE status='running'`)
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		pool, profiles, err := s.buildPool()
		if err != nil {
			return
		}
		maxTokens := 0
		if len(profiles) > 0 {
			maxTokens = profiles[0].MaxTokens
		}
		go s.launchEngine(id, pool, maxTokens)
	}
}

// loadRoE 按 task 取 RoE：任务级 → 全局 → audit 兜底。
func (s *Server) loadRoE(taskID int64) roe.Rules {
	if taskID > 0 {
		var data []byte
		if err := s.store.QueryRow(sqlCtx(),
			`SELECT data FROM roe_rules WHERE scope='task' AND task_id=$1 ORDER BY id DESC LIMIT 1`, taskID).
			Scan(&data); err == nil {
			if r, err := roe.ParseRules(data); err == nil {
				return r
			}
		}
	}
	var data []byte
	if err := s.store.QueryRow(sqlCtx(),
		`SELECT data FROM roe_rules WHERE scope='global' ORDER BY id DESC LIMIT 1`).Scan(&data); err == nil {
		if r, err := roe.ParseRules(data); err == nil {
			return r
		}
	}
	return roe.Rules{Mode: "audit"}
}

func sqlCtx() context.Context { return context.Background() }

// Router 构建全部路由：豁免路径直通，其余 /api/* 过鉴权门（支持 ?token= 供 SSE）。
func (s *Server) Router() http.Handler {
	inner := http.NewServeMux()
	inner.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	inner.HandleFunc("GET /api/setup/status", s.handleSetupStatus)
	inner.HandleFunc("POST /api/setup", s.handleSetup)
	inner.HandleFunc("POST /api/auth/login", s.handleLogin)

	inner.HandleFunc("GET /api/tasks", s.handleListTasks)
	inner.HandleFunc("GET /api/tasks/{id}/graph", s.handleTaskGraph)
	inner.HandleFunc("GET /api/exploration/{id}/activity", s.handleActivity)
	inner.HandleFunc("GET /api/exploration/{id}/activity/stream", s.handleActivityStream)
	inner.HandleFunc("GET /api/llm/profiles", s.handleListProfiles)
	inner.HandleFunc("POST /api/llm/profiles/{id}/test", s.handleTestProfile)
	inner.HandleFunc("GET /api/intercept/pending", s.handleListPending)
	inner.Handle("POST /api/intercept/pending/{id}/decide",
		s.requireRole("admin", "auditor")(http.HandlerFunc(s.handleDecide)))

	// 用户管理（M2 Task 1）。
	inner.Handle("POST /api/users",
		s.requireRole("admin")(http.HandlerFunc(s.handleCreateUser)))
	inner.Handle("GET /api/users",
		s.requireRole("admin", "auditor")(http.HandlerFunc(s.handleListUsers)))

	// 审计（M2 Task 2）。
	inner.Handle("GET /api/audit",
		s.requireRole("admin", "auditor")(http.HandlerFunc(s.handleListAudit)))

	// 任务写操作限 operator+（Review Focus 1）。
	inner.Handle("POST /api/tasks",
		s.requireRole("admin", "operator")(http.HandlerFunc(s.handleCreateTask)))
	inner.Handle("POST /api/tasks/{id}/control",
		s.requireRole("admin", "operator")(http.HandlerFunc(s.handleTaskControl)))
	inner.Handle("POST /api/tasks/{id}/steer",
		s.requireRole("admin", "operator")(http.HandlerFunc(s.handleTaskSteer)))

	// LLM 配置限 admin。
	inner.Handle("POST /api/llm/profiles",
		s.requireRole("admin")(http.HandlerFunc(s.handleCreateProfile)))

	// 报告导出与覆盖度。
	inner.HandleFunc("GET /api/tasks/{id}/report", s.handleTaskReport)
	inner.HandleFunc("GET /api/tasks/{id}/coverage", s.handleTaskCoverage)

	// 复测（operator+）。
	inner.Handle("POST /api/findings/{id}/retest",
		s.requireRole("admin", "operator")(http.HandlerFunc(s.handleFindingRetest)))

	// mainagent 对话（operator+）。
	inner.Handle("POST /api/tasks/{id}/chat",
		s.requireRole("admin", "operator")(http.HandlerFunc(s.handleTaskChat)))

	// 内嵌前端（/ 与非 /api 静态路径）。
	inner.Handle("/", webHandler())

	return s.authGate(inner)
}

// exempt 判断是否豁免鉴权（静态资源与页面路由豁免；API 数据受保护）。
func exempt(path string) bool {
	switch path {
	case "/healthz", "/api/setup/status", "/api/setup", "/api/auth/login":
		return true
	}
	return !strings.HasPrefix(path, "/api/")
}

func (s *Server) authGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !exempt(r.URL.Path) {
			tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if tok == "" {
				tok = r.URL.Query().Get("token") // EventSource 无法带 Header（I8）
			}
			if !s.verifyToken(tok) {
				errJSON(w, http.StatusUnauthorized, "未授权")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// --- 通用 ---

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func errJSON(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// encryptKey AES-GCM 加密 api_key（I2）；无主密钥时返回原文（开发模式）。
func (s *Server) encryptKey(plain string) []byte {
	if s.gcm == nil || plain == "" {
		return []byte(plain)
	}
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return []byte(plain)
	}
	return s.gcm.Seal(nonce, nonce, []byte(plain), nil)
}

// decryptKey 解密（与 encryptKey 对应）。
func (s *Server) decryptKey(b []byte) string {
	if s.gcm == nil || len(b) == 0 {
		return string(b)
	}
	ns := s.gcm.NonceSize()
	if len(b) <= ns {
		return string(b)
	}
	plain, err := s.gcm.Open(nil, b[:ns], b[ns:], nil)
	if err != nil {
		return ""
	}
	return string(plain)
}

// SchemaVersion 暴露 schema 版本（mtx version 用）。
func (s *Server) SchemaVersion(ctx context.Context) (int, error) {
	return s.store.SchemaVersion(ctx)
}
