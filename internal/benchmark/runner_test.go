package benchmark

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubAPI 模拟 MTxCC API：一次任务即 done（含 report_finding 活动）。
func stubAPI(t *testing.T) *httptest.Server {
	t.Helper()
	taskID := int64(1)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/tasks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": taskID, "status": "queued"})
	})
	mux.HandleFunc("GET /api/tasks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": taskID, "status": "done", "target_text": "x"},
		})
	})
	mux.HandleFunc("GET /api/exploration/", func(w http.ResponseWriter, _ *http.Request) {
		// 活动流含一次 report_finding 成功。
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": 1, "kind": "tool_use", "payload": `{"tool":"report_finding","result":"{\"finding_id\":42}"}`},
		})
	})
	return httptest.NewServer(mux)
}

// TestRunSuiteEndToEnd runner 全链：建任务→轮询→计 findings→评分。
func TestRunSuiteEndToEnd(t *testing.T) {
	srv := stubAPI(t)
	defer srv.Close()

	r := &Runner{
		Adapter: &DVWAAdapter{Host: "127.0.0.1:4280"},
		APIBase: srv.URL,
		Token:   "tok",
		Model:   "test-model",
	}
	rep, err := r.RunSuite()
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	if rep.Total != 10 {
		t.Fatalf("total = %d, want 10（DVWA 十题）", rep.Total)
	}
	if rep.Solved != 10 {
		t.Fatalf("solved = %d, want 10（stub 全 done+finding）", rep.Solved)
	}
	if rep.AvgLatencySec < 0 {
		t.Fatalf("latency 异常: %d", rep.AvgLatencySec)
	}
	// 明细可核对。
	if len(r.Results) != 10 {
		t.Fatalf("明细 = %d 条", len(r.Results))
	}
	for _, res := range r.Results {
		if !res.Solved {
			t.Errorf("题 %s 未 solved", res.ID)
		}
	}
	// 对比表含参考系。
	comp := rep.CompareWithReference()
	if comp["decepticon"] == nil {
		t.Fatal("缺参考系")
	}
}

// TestCreateTaskAuth 失败路径：API 401 时错误可读。
func TestCreateTaskAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"未授权"}`))
	}))
	defer srv.Close()
	r := &Runner{Adapter: &DVWAAdapter{}, APIBase: srv.URL, Token: "bad"}
	if _, err := r.RunSuite(); err == nil {
		// 逐题 create 失败被吞进 Results，RunSuite 本身不报错——检查明细。
	}
	for _, res := range r.Results {
		if !strings.Contains(res.Err, "401") {
			t.Errorf("错误应含 401: %q", res.Err)
		}
	}
}
