package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"mtx/internal/harness"
	"mtx/internal/llm"
	"mtx/internal/tools"
)

// fakeProvider 最小脚本 provider（expert 会话用，无 DB 依赖）。
type fakeProvider struct {
	mu     sync.Mutex
	script [][]llm.Event
	idx    int
	lastSys string
}

func (p *fakeProvider) Stream(_ context.Context, req llm.Request) (<-chan llm.Event, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastSys = req.System
	i := p.idx
	p.idx++
	ch := make(chan llm.Event, 8)
	if i < len(p.script) {
		for _, e := range p.script[i] {
			ch <- e
		}
	}
	close(ch)
	return ch, nil
}

func expertTurn(id, name, args string) []llm.Event {
	return []llm.Event{
		llm.ToolUseStart{ID: id, Name: name},
		llm.ToolInputJSON{ID: id, Fragment: args},
		llm.MessageDelta{StopReason: "tool_use"},
	}
}

func expertText(s string) []llm.Event {
	return []llm.Event{llm.TextDelta{S: s}, llamEnd()}
}

func llamEnd() llm.MessageDelta { return llm.MessageDelta{StopReason: "end_turn"} }

func mustRaw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// TestExpertDepthGate 深度门：Depth>0 时无 expert 工具（防递归委托，Review Focus 1）。
func TestExpertDepthGate(t *testing.T) {
	deps := CommonDeps{Model: "m"}
	if got := ExpertTools(deps, ExecContext{Depth: 1, Role: "worker"}); len(got) != 0 {
		t.Fatalf("Depth=1 应返回空，得到 %d 个", len(got))
	}
	got := ExpertTools(deps, ExecContext{Depth: 0, Role: "worker"})
	if len(got) != 2 {
		t.Fatalf("Depth=0 应返回 2 个工具，得到 %d", len(got))
	}
	if got[0].Name() == got[1].Name() {
		t.Fatal("工具重名")
	}
}

// TestExpertHTTPRealLoop expert_http 真跑小循环：假 LLM 发 curl 工具轮 + 收尾轮，
// 结果文本返回给调用方。
func TestExpertHTTPRealLoop(t *testing.T) {
	// 目标服务器。
	target := startTestTarget(t)

	prov := &fakeProvider{script: [][]llm.Event{
		expertTurn("e1", "bash", `{"command":"curl -s `+target+`/api/data"}`),
		expertText("EXPERT_DONE: 响应包含 SECRET_MARKER"),
	}}

	deps := CommonDeps{
		Model: "m", Provider: prov,
		Bash:       tools.NewLocalExec(t.TempDir(), nil),
		Transcript: harness.NewFileTranscript(t.TempDir()),
	}
	et := ExpertTools(deps, ExecContext{Depth: 0, Role: "worker"})
	var httpTool tools.Tool
	for _, tl := range et {
		if tl.Name() == "expert_http" {
			httpTool = tl
		}
	}
	if httpTool == nil {
		t.Fatal("缺 expert_http")
	}
	out, err := httpTool.Call(context.Background(), mustRaw(map[string]string{
		"objective": "验证 /api/data 可达并概述响应",
	}))
	if err != nil {
		t.Fatalf("expert_http: %v", err)
	}
	if !strings.Contains(out, "EXPERT_DONE") {
		t.Fatalf("expert 结果未透传: %q", out)
	}
	// expert 会话 system 应含 expert 标记。
	if !strings.Contains(prov.lastSys, "专家") {
		t.Fatalf("expert prompt 未注入: %.60s", prov.lastSys)
	}
}

func startTestTarget(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/data", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"secret":"SECRET_MARKER"}`))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts.URL
}
