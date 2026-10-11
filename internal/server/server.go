// Package server 提供 MTxCC 的 HTTP API（spec §10.1）。
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"mtx/internal/engine"
	"mtx/internal/graph"
	"mtx/internal/guard"
	"mtx/internal/store"
)

// Options 配置服务端。
type Options struct {
	Store     *store.Store
	MasterKey string // base64 32B
	DataDir   string
	BashLocal bool   // true=worker 用本地执行（开发/测试；生产走沙箱）
	ProxyAddr string // MITM 代理地址（可空）
}

// Server 聚合全部 API。
type Server struct {
	opts    Options
	store   *store.Store
	graph   *graph.GraphStore
	guard   *guard.Guard
	mu      sync.Mutex
	engines map[int64]*engine.Engine
}

// New 建服务端。
func New(o Options) *Server {
	return &Server{
		opts: o, store: o.Store, graph: graph.NewGraphStore(o.Store),
		guard:   guard.NewGuard(guard.NewMemorySource(), nil, nil),
		engines: map[int64]*engine.Engine{},
	}
}

func sqlCtx() context.Context { return context.Background() }

// Router 构建全部路由：豁免路径直通，其余 /api/* 过鉴权门。
func (s *Server) Router() http.Handler {
	inner := http.NewServeMux()
	inner.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	inner.HandleFunc("GET /api/setup/status", s.handleSetupStatus)
	inner.HandleFunc("POST /api/setup", s.handleSetup)
	inner.HandleFunc("POST /api/auth/login", s.handleLogin)

	inner.HandleFunc("GET /api/tasks", s.handleListTasks)
	inner.HandleFunc("POST /api/tasks", s.handleCreateTask)
	inner.HandleFunc("POST /api/tasks/{id}/control", s.handleTaskControl)
	inner.HandleFunc("GET /api/tasks/{id}/graph", s.handleTaskGraph)
	inner.HandleFunc("GET /api/exploration/{id}/activity", s.handleActivity)
	inner.HandleFunc("GET /api/exploration/{id}/activity/stream", s.handleActivityStream)
	inner.HandleFunc("GET /api/llm/profiles", s.handleListProfiles)
	inner.HandleFunc("POST /api/llm/profiles/{id}/test", s.handleTestProfile)
	inner.HandleFunc("GET /api/intercept/pending", s.handleListPending)
	inner.HandleFunc("POST /api/intercept/pending/{id}/decide", s.handleDecide)

	return s.authGate(inner)
}

// exempt 判断是否豁免鉴权。
func exempt(path string) bool {
	switch path {
	case "/healthz", "/api/setup/status", "/api/setup", "/api/auth/login":
		return true
	}
	return false
}

func (s *Server) authGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !exempt(r.URL.Path) {
			tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
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
	return json.NewDecoder(r.Body).Decode(v)
}
