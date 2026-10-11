// Package server 提供 MTxCC 的 HTTP API 与嵌入式前端。
package server

import "net/http"

// NewRouter 构建根路由。随任务推进按资源分文件挂载子路由。
func NewRouter() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	return mux
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
