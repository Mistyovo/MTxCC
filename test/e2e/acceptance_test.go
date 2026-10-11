//go:build docker

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcp "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"mtx/internal/server"
	"mtx/internal/store"
)

// TestM0AcceptanceAssembly M0 端到端验收（修复 C1/C2a/C3/C4 的驱动测试）：
// 模拟 install.sh 产物（env/密钥）→ server.New 真实装配 → setup →
// UI 可达 → profile API 创建（含 api_key 加密）→ 建 RoE enforce 任务 →
// 引擎全链路（worker 命令受 RoE 拦截、合法工具调用成功、意图终态）→ SSE token 可读。
func TestM0AcceptanceAssembly(t *testing.T) {
	ctx := context.Background()

	// 1) PG。
	pg, err := tcp.Run(ctx, "postgres:16-alpine",
		tcp.WithDatabase("mtx"), tcp.WithUsername("mtx"), tcp.WithPassword("mtx"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	dsn, _ := pg.ConnectionString(ctx, "sslmode=disable")

	// 2) 假 LLM 网关：worker 出网命令 + record_fact + 收尾；planner 文本。
	llm := newAcceptanceLLM(t)

	// 3) 真实装配（C1 修复目标：这就是 cmd/mtx 应走的路径）。
	masterKey := "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	srv, err := server.New(server.Options{
		DatabaseURL: dsn,
		MasterKey:   masterKey,
		DataDir:     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("C1: server.New 装配失败: %v", err)
	}
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)
	base := ts.URL

	// 4) UI 可达（C1）。
	resp, body := httpGet(t, base+"/")
	if resp.StatusCode != 200 || !strings.Contains(body, "MTxCC") {
		t.Fatalf("C1: 首页不可达: %d %.60s", resp.StatusCode, body)
	}
	// healthz 兼容。
	if resp, _ := httpGet(t, base+"/healthz"); resp.StatusCode != 200 {
		t.Fatal("healthz 不可达")
	}

	// 5) setup + 登录。
	httpPost(t, base, "/api/setup", "", map[string]string{"password": "Passw0rd!123"})
	tok := loginAcceptance(t, base)

	// 6) profile 创建 API（I1）+ api_key 加密落库（I2）。
	profResp, profBody := httpPost(t, base, "/api/llm/profiles", tok, map[string]any{
		"name": "fake", "protocol": "openai", "base_url": llm.url(), "model": "fake-m",
		"api_key": "sk-secret-123", "max_tokens": 4096,
	})
	if profResp.StatusCode != 200 {
		t.Fatalf("I1: profile 创建失败 url=%s/api/llm/profiles: %d %s", base, profResp.StatusCode, profBody)
	}
	var storedKey []byte
	s, _ := store.Open(ctx, dsn)
	t.Cleanup(s.Close)
	_ = s.QueryRow(ctx, `SELECT api_key_enc FROM llm_profiles WHERE name='fake'`).Scan(&storedKey)
	if bytes.Contains(storedKey, []byte("sk-secret-123")) {
		t.Fatal("I2: api_key 明文落库")
	}
	// 连通性测试仍可用。
	r, b := httpPost(t, base, "/api/llm/profiles/1/test", tok, nil)
	if r.StatusCode != 200 || !strings.Contains(b, `"ok":true`) {
		t.Fatalf("profile test: %d %s", r.StatusCode, b)
	}

	// 7) 建 RoE enforce 任务（C2a：任务级 RoE 生效）。
	roe := map[string]any{
		"mode": "enforce",
		"in_scope": []string{"10.0.0.0/8"},
		"forbidden_tlds": []string{"gov"},
	}
	taskResp, taskBody := httpPost(t, base, "/api/tasks", tok, map[string]any{
		"target_text": "10.0.0.5", "timeout_seconds": 120, "roe": roe,
	})
	if taskResp.StatusCode != 200 {
		t.Fatalf("建任务: %d %s", taskResp.StatusCode, taskBody)
	}
	var task struct{ ID int64 `json:"id"` }
	_ = json.Unmarshal([]byte(taskBody), &task)

	// 8) 全链路：等意图终态（假 LLM 脚本：越权 curl 被拒 → 合法 record_fact → 收尾）。
	deadline := time.Now().Add(40 * time.Second)
	done := false
	for time.Now().Before(deadline) {
		var state string
		_ = s.QueryRow(ctx, `SELECT state FROM exploration_nodes WHERE kind='intent' ORDER BY id LIMIT 1`).Scan(&state)
		if state == "done" || state == "exhausted" || state == "blocked" {
			done = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !done {
		t.Fatal("C1/C3: 任务未运转到终态（装配断裂或 max_tokens 截断）")
	}

	// 9) RoE 拦截证据：活动流中出现平台管控拒绝（对 172.16.x 的 curl）。
	if !actsOf(t, base, task.ID, tok) {
		t.Fatal("C2a: 未观察到 RoE 拦截（授权边界未装配）")
	}

	// 10) SSE 可带 token 查询参数（I8）。
	sseReq, _ := http.NewRequest("GET",
		base+fmt.Sprintf("/api/exploration/%d/activity/stream?token=%s", task.ID, tok), nil)
	sseResp, err := http.DefaultClient.Do(sseReq)
	if err != nil || sseResp.StatusCode != 200 {
		st := 0
		if sseResp != nil {
			sseResp.Body.Close()
			st = sseResp.StatusCode
		}
		t.Fatalf("I8: SSE token 认证失败: err=%v status=%d", err, st)
	}
	sseResp.Body.Close()

	// 11) MaxTokens 透传（C3）：假网关记录请求体应含 max_tokens=4096。
	if !llm.sawMaxTokens(4096) {
		t.Fatal("C3: 请求 max_tokens 未透传（真实模型将被截为 1 token）")
	}
}

func actsOf(t *testing.T, base string, taskID int64, tok string) bool {
	t.Helper()
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/exploration/%d/activity?limit=100", base, taskID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return strings.Contains(string(b), "RoE") || strings.Contains(string(b), "平台管控")
}

// --- 假 LLM：openai 兼容 SSE，脚本 = [越权 curl 工具轮, 合法 record_fact 轮, 收尾轮] ---

type accLLM struct {
	srv   *httptest.Server
	mu    chan struct{}
	bodies []string
	idx   int
}

func (l *accLLM) url() string { return l.srv.URL }

func (l *accLLM) sawMaxTokens(want int) bool {
	<-l.mu
	defer func() { l.mu <- struct{}{} }()
	for _, b := range l.bodies {
		if strings.Contains(b, fmt.Sprintf(`"max_tokens":%d`, want)) {
			return true
		}
	}
	return false
}

func newAcceptanceLLM(t *testing.T) *accLLM {
	l := &accLLM{mu: make(chan struct{}, 1)}
	l.mu <- struct{}{}

	chunk := func(s string) string { return "data: " + s + "\n\n" }
	toolUse := func(id, name, args string) string {
		return chunk(fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":%q,"type":"function","function":{"name":%q,"arguments":""}}]}}]}`, id, name)) +
			chunk(fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":%q}}]}}]}`, args)) +
			chunk(`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`) +
			"data: [DONE]\n\n"
	}
	text := func(s string) string {
		return chunk(fmt.Sprintf(`{"choices":[{"delta":{"content":%q}}]}`, s)) +
			chunk(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`) +
			"data: [DONE]\n\n"
	}
	turns := []string{
		// 轮1：越权目标 curl（RoE 应拒）+ 合法 record_fact 同轮。
		toolUse("c1", "bash", `{"command":"curl http://172.16.0.9/"}`),
		toolUse("c2", "record_fact", `{"title":"发现登录页","body":"http://10.0.0.5/login"}`),
		// 轮2：收尾。
		text("意图完成"),
	}
	l.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		<-l.mu
		l.bodies = append(l.bodies, string(b))
		isPing := strings.Contains(string(b), "ping")
		i := l.idx
		if !isPing {
			l.idx++
		}
		l.mu <- struct{}{}
		w.Header().Set("Content-Type", "text/event-stream")
		if isPing {
			_, _ = w.Write([]byte(chunk(`{"choices":[{"delta":{"content":"pong"}}]}`) + "data: [DONE]\n\n"))
			return
		}
		if i < len(turns) {
			_, _ = w.Write([]byte(turns[i]))
		}
	}))
	t.Cleanup(l.srv.Close)
	return l
}

func httpGet(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func httpPost(t *testing.T, base, path, token string, body any) (*http.Response, string) {
	t.Helper()
	bd, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", base+path, bytes.NewReader(bd))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func loginAcceptance(t *testing.T, base string) string {
	t.Helper()
	r, b := httpPost(t, base, "/api/auth/login", "", map[string]string{"username": "admin", "password": "Passw0rd!123"})
	if r.StatusCode != 200 {
		t.Fatalf("登录: %d %s", r.StatusCode, b)
	}
	var out struct{ Token string `json:"token"` }
	_ = json.Unmarshal([]byte(b), &out)
	return out.Token
}

var _ = os.Getenv
