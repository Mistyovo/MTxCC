package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHealthz 验证健康检查端点返回 200 且 body 含 "ok"。
// 令其失败的生产改动：healthz 路由被移除、状态码改动或响应体不再包含 ok。
func TestHealthz(t *testing.T) {
	ts := httptest.NewServer(NewRouter())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "ok") {
		t.Fatalf("body %q 不包含 ok", string(body))
	}
}
