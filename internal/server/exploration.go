package server

import (
	"fmt"
	"net/http"
	"time"
)

// handleActivity 分页读执行流水。
func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	var taskID int64
	if _, err := fmt.Sscanf(r.PathValue("id"), "%d", &taskID); err != nil {
		errJSON(w, http.StatusBadRequest, "bad id")
		return
	}
	since := r.URL.Query().Get("since_id")
	limit := r.URL.Query().Get("limit")
	if limit == "" {
		limit = "50"
	}
	rows, err := s.store.Query(sqlCtx(), `SELECT id, node_id, agent_role, kind, payload::text, created_at
		FROM activity WHERE task_id=$1 AND ($2='' OR id > $2::bigint)
		ORDER BY id ASC LIMIT $3::int`, taskID, since, limit)
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, node int64
		var role, kind, payload, created string
		_ = rows.Scan(&id, &node, &role, &kind, &payload, &created)
		out = append(out, map[string]any{
			"id": id, "node_id": node, "role": role, "kind": kind,
			"payload": payload, "created_at": created,
		})
	}
	writeJSON(w, 200, out)
}

// handleActivityStream SSE：activity 表尾轮询推送 + 15s 心跳。
func (s *Server) handleActivityStream(w http.ResponseWriter, r *http.Request) {
	var taskID int64
	if _, err := fmt.Sscanf(r.PathValue("id"), "%d", &taskID); err != nil {
		errJSON(w, http.StatusBadRequest, "bad id")
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		errJSON(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	var lastID int64
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, ": ping\n\n")
			fl.Flush()
		case <-tick.C:
			rows, err := s.store.Query(r.Context(), `SELECT id, payload::text FROM activity
				WHERE task_id=$1 AND id > $2 ORDER BY id ASC LIMIT 50`, taskID, lastID)
			if err != nil {
				return
			}
			for rows.Next() {
				var id int64
				var payload string
				if err := rows.Scan(&id, &payload); err == nil {
					lastID = id
					fmt.Fprintf(w, "data: {\"id\":%d,\"payload\":%s}\n\n", id, payload)
				}
			}
			rows.Close()
			fl.Flush()
		}
	}
}
