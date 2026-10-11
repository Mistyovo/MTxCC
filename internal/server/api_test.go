//go:build docker

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcp "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"mtx/internal/store"
)

// fakeLLM 假 OpenAI 兼容 SSE 服务：按调用序回放脚本。
type fakeLLM struct {
	srv  *httptest.Server
	idx  int
	turn []string // 每元素 = 一次响应的完整 SSE 文本
	sys  []string // 捕获的 system
}

func newFakeLLM(turn []string) *fakeLLM {
	f := &fakeLLM{turn: turn}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ System string `json:"system"` }
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.sys = append(f.sys, req.System)
		i := f.idx
		f.idx++
		w.Header().Set("Content-Type", "text/event-stream")
		if i < len(f.turn) {
			_, _ = w.Write([]byte(f.turn[i]))
		}
	}))
	return f
}

func (f *fakeLLM) url() string { return f.srv.URL }

// workerTurn：record_fact 工具调用轮 + 结束轮（openai chunks）。
func workerSSE() []string {
	return []string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_w1","type":"function","function":{"name":"record_fact","arguments":""}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"title\":\"发现登录页\",\"body\":\"http://10.0.0.5/login\"}"}}]}}]}

data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}

data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":5}}

data: [DONE]

`,
		`data: {"choices":[{"delta":{"content":"意图完成"}}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`,
	}
}

func plannerSSE() []string {
	return []string{
		`data: {"choices":[{"delta":{"content":"规划完成，等待 worker"}}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`,
	}
}

func startAPI(t *testing.T, llmURL string) (*httptest.Server, *store.Store, string) {
	t.Helper()
	ctx := context.Background()
	pg, err := tcp.Run(ctx, "postgres:16-alpine",
		tcp.WithDatabase("mtx"), tcp.WithUsername("mtx"), tcp.WithPassword("mtx"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	dsn, _ := pg.ConnectionString(ctx, "sslmode=disable")
	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	// 插入启用的 LLM profile（指向假网关）。
	_, _ = s.Exec(ctx, `INSERT INTO llm_profiles (name, protocol, base_url, api_key_enc, model, enabled)
		VALUES ('fake', 'openai', $1, NULL, 'fake-model', true)`, llmURL)

	srv, err := New(Options{Store: s, MasterKey: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", DataDir: t.TempDir(), BashLocal: true})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)
	return ts, s, ""
}

func apiCall(t *testing.T, method, url, token string, body any) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, rd)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, b
}

func doSetupLogin(t *testing.T, base string) string {
	resp, body := apiCall(t, "GET", base+"/api/setup/status", "", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("setup status: %d %s", resp.StatusCode, body)
	}
	resp, body = apiCall(t, "POST", base+"/api/setup", "", map[string]string{"password": "Passw0rd!123"})
	if resp.StatusCode != 200 {
		t.Fatalf("setup: %d %s", resp.StatusCode, body)
	}
	resp, body = apiCall(t, "POST", base+"/api/auth/login", "", map[string]string{"username": "admin", "password": "Passw0rd!123"})
	if resp.StatusCode != 200 {
		t.Fatalf("login: %d %s", resp.StatusCode, body)
	}
	var out struct{ Token string `json:"token"` }
	_ = json.Unmarshal(body, &out)
	return out.Token
}

// TestSetupFlowAndAuthGate 验证：setup 一次性；未登录 401；登录后可访问。
func TestSetupFlowAndAuthGate(t *testing.T) {
	f := newFakeLLM(nil)
	ts, _, _ := startAPI(t, f.url())
	base := ts.URL

	// 未 setup 未登录：受保护端点 401。
	if resp, _ := apiCall(t, "GET", base+"/api/tasks", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录应 401, got %d", resp.StatusCode)
	}
	tok := doSetupLogin(t, base)
	if tok == "" {
		t.Fatal("无 token")
	}
	// setup 仅一次。
	if resp, _ := apiCall(t, "POST", base+"/api/setup", "", map[string]string{"password": "x"}); resp.StatusCode != http.StatusConflict {
		t.Fatalf("二次 setup 应 409, got %d", resp.StatusCode)
	}
	if resp, _ := apiCall(t, "GET", base+"/api/tasks", tok, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("登录后应 200")
	}
}

// TestLLMProfileTestEndpoint 验证连通性测试端点。
func TestLLMProfileTestEndpoint(t *testing.T) {
	f := newFakeLLM([]string{`data: {"choices":[{"delta":{"content":"pong"}}]}

data: [DONE]

`})
	ts, _, _ := startAPI(t, f.url())
	base := ts.URL
	tok := doSetupLogin(t, base)

	resp, body := apiCall(t, "POST", base+"/api/llm/profiles/1/test", tok, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "ok") {
		t.Fatalf("test 端点: %d %s", resp.StatusCode, body)
	}
}

// TestTaskLifecycleFullLoop 全链路：建任务 → 引擎跑 → activity 出现 worker tool_use → 意图 done。
func TestTaskLifecycleFullLoop(t *testing.T) {
	f := newFakeLLM(append(workerSSE(), plannerSSE()...))
	ts, s, _ := startAPI(t, f.url())
	base := ts.URL
	tok := doSetupLogin(t, base)

	resp, body := apiCall(t, "POST", base+"/api/tasks", tok, map[string]any{
		"target_text": "http://10.0.0.5", "timeout_seconds": 300,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("建任务: %d %s", resp.StatusCode, body)
	}
	var task struct{ ID int64 `json:"id"` }
	_ = json.Unmarshal(body, &task)

	// 轮询 activity 出现 tool_use（record_fact）。
	deadline := time.Now().Add(30 * time.Second)
	sawToolUse := false
	for time.Now().Before(deadline) {
		_, b := apiCall(t, "GET", fmt.Sprintf("%s/api/exploration/%d/activity?limit=50", base, task.ID), tok, nil)
		if strings.Contains(string(b), "record_fact") {
			sawToolUse = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !sawToolUse {
		t.Fatal("activity 未出现 record_fact")
	}

	// 意图应到终态。
	ok := false
	for time.Now().Before(deadline) {
		var state string
		_ = s.QueryRow(context.Background(),
			`SELECT state FROM exploration_nodes WHERE kind='intent' ORDER BY id LIMIT 1`).Scan(&state)
		if state == "done" || state == "exhausted" || state == "blocked" {
			ok = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !ok {
		t.Fatal("意图未到终态")
	}
}

// TestTaskWithoutProfileRejected 禁用全部 profile 后建任务应 400 引导配置。
func TestTaskWithoutProfileRejected(t *testing.T) {
	f := newFakeLLM(nil)
	ts, s, _ := startAPI(t, f.url())
	base := ts.URL
	tok := doSetupLogin(t, base)

	_, _ = s.Exec(context.Background(), `UPDATE llm_profiles SET enabled=false`)
	resp, body := apiCall(t, "POST", base+"/api/tasks", tok, map[string]any{"target_text": "http://10.0.0.5"})
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "LLM") {
		t.Fatalf("无 profile 应 400 引导: %d %s", resp.StatusCode, body)
	}
}

// TestEmbeddedUIServed 验证内嵌前端：/ 返回含 MTxCC 标识的 HTML；未匹配路由回退 index。
func TestEmbeddedUIServed(t *testing.T) {
	f := newFakeLLM(nil)
	ts, _, _ := startAPI(t, f.url())

	resp, body := apiCall(t, "GET", ts.URL+"/", "", nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "MTxCC") {
		t.Fatalf("首页: %d %.80s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type = %q", ct)
	}
	// SPA 回退：任意路径返回 index 而非 404。
	resp, body = apiCall(t, "GET", ts.URL+"/tasks", "", nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "MTxCC") {
		t.Fatalf("SPA 回退失败: %d", resp.StatusCode)
	}
}
