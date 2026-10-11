package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"mtx/internal/llm"
)

func (s *Server) handleListProfiles(w http.ResponseWriter, _ *http.Request) {
	rows, err := s.store.Query(sqlCtx(), `SELECT id, name, protocol, base_url, model, enabled
		FROM llm_profiles ORDER BY id`)
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, proto, base, model string
		var enabled bool
		_ = rows.Scan(&id, &name, &proto, &base, &model, &enabled)
		out = append(out, map[string]any{
			"id": id, "name": name, "protocol": proto, "base_url": base,
			"model": model, "enabled": enabled,
			// api_key 永不回传明文（spec §10.1）。
			"has_api_key": true,
		})
	}
	writeJSON(w, 200, out)
}

// handleTestProfile 连通性测试：发一次 max_tokens=16 的最小流式请求。
func (s *Server) handleTestProfile(w http.ResponseWriter, r *http.Request) {
	var id int64
	if _, err := fmt.Sscanf(r.PathValue("id"), "%d", &id); err != nil {
		errJSON(w, http.StatusBadRequest, "bad id")
		return
	}
	var proto, base, model string
	var keyEnc []byte
	if err := s.store.QueryRow(sqlCtx(),
		`SELECT protocol, base_url, model, api_key_enc FROM llm_profiles WHERE id=$1`, id).
		Scan(&proto, &base, &model, &keyEnc); err != nil {
		errJSON(w, 404, "profile 不存在")
		return
	}
	p := llm.Profile{Protocol: proto, BaseURL: base, Model: model, APIKey: s.decryptKey(keyEnc)}
	var prov llm.Provider
	switch proto {
	case "anthropic":
		prov = llm.NewAnthropic(p)
	case "openai_responses":
		prov = llm.NewOpenAIResponses(p)
	default:
		prov = llm.NewOpenAI(p)
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	ch, err := prov.Stream(ctx, llm.Request{Model: model, MaxTokens: 16,
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "ping"}}}}})
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	gotText := false
	var streamErr string
	for ev := range ch {
		switch e := ev.(type) {
		case llm.TextDelta:
			gotText = true
		case *llm.StreamError:
			streamErr = e.Err.Error()
		}
	}
	writeJSON(w, 200, map[string]any{
		"ok": gotText && streamErr == "", "latency_ms": time.Since(start).Milliseconds(),
		"model": model, "error": streamErr,
	})
}

// decryptKey M0：api_key_enc 暂以明文存储兼容（AES-GCM 加密在 M2 凭据模块统一接入）。
func (s *Server) decryptKey(b []byte) string { return string(b) }
