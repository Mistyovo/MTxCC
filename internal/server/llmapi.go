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

// handleCreateProfile 创建 profile（I1；api_key AES-GCM 落库 I2）。
func (s *Server) handleCreateProfile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      string `json:"name"`
		Protocol  string `json:"protocol"`
		BaseURL   string `json:"base_url"`
		Model     string `json:"model"`
		APIKey    string `json:"api_key"`
		MaxTokens int    `json:"max_tokens"`
	}
	if err := readJSON(r, &in); err != nil {
		errJSON(w, http.StatusBadRequest, "bad request: "+err.Error())
		return
	}
	switch in.Protocol {
	case "anthropic", "openai", "openai_responses":
	default:
		errJSON(w, http.StatusBadRequest, "protocol ∈ anthropic|openai|openai_responses")
		return
	}
	if in.Name == "" || in.Model == "" {
		errJSON(w, http.StatusBadRequest, "name 与 model 必填")
		return
	}
	if in.MaxTokens <= 0 {
		in.MaxTokens = 8192
	}
	var id int64
	err := s.store.QueryRow(sqlCtx(), `INSERT INTO llm_profiles
		(name, protocol, base_url, api_key_enc, model, max_tokens, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,true)
		ON CONFLICT (name) DO UPDATE SET protocol=EXCLUDED.protocol, base_url=EXCLUDED.base_url,
			api_key_enc=EXCLUDED.api_key_enc, model=EXCLUDED.model, max_tokens=EXCLUDED.max_tokens, enabled=true
		RETURNING id`,
		in.Name, in.Protocol, in.BaseURL, s.encryptKey(in.APIKey), in.Model, in.MaxTokens).Scan(&id)
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"id": id})
}
