package server

import (
	"fmt"
	"net/http"
)

// handleListPending 待审列表（M0 从内存源读）。
func (s *Server) handleListPending(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"pending": []any{}}) // 内存源无枚举接口：decide 由 SSE 通知的 ID 直达
}

// handleDecide 人工决策：allow/deny/modify。
func (s *Server) handleDecide(w http.ResponseWriter, r *http.Request) {
	var id int64
	if _, err := fmt.Sscanf(r.PathValue("id"), "%d", &id); err != nil {
		errJSON(w, http.StatusBadRequest, "bad id")
		return
	}
	var in struct {
		Action      string `json:"action"`
		Replacement []byte `json:"replacement"`
	}
	if err := readJSON(r, &in); err != nil {
		errJSON(w, http.StatusBadRequest, "bad request")
		return
	}
	if err := s.guard.Decide(r.Context(), id, in.Action, in.Replacement, "admin"); err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
