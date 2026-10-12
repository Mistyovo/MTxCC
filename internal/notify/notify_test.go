package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWebhookDeliver 通知投递（ARTEX IM 推送对标）：
// 事件 → 模板渲染 → webhook POST → 记录投递结果。
func TestWebhookDeliver(t *testing.T) {
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		got = append(got, m)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	ch := Channel{Type: "webhook", URL: srv.URL, Name: "测试通道"}
	n := New()
	if err := n.Register(ch); err != nil {
		t.Fatal(err)
	}
	ev := Event{Kind: "finding.new", TaskID: 1, Title: "发现 RCE", Detail: "DVWA 命令注入"}
	if err := n.Deliver(ev); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("投递次数 = %d", len(got))
	}
	body, _ := json.Marshal(got[0])
	if !strings.Contains(string(body), "RCE") {
		t.Fatalf("模板未渲染标题: %s", body)
	}
}

// TestDingTalkFormat 钉钉消息格式（msgtype=markdown）。
func TestDingTalkFormat(t *testing.T) {
	msg := DingTalkMessage(Event{Kind: "finding.new", Title: "SQLi", Detail: "登录绕过"})
	if msg["msgtype"] != "markdown" {
		t.Fatalf("msgtype = %v", msg["msgtype"])
	}
	md := msg["markdown"].(map[string]any)
	if !strings.Contains(md["text"].(string), "SQLi") {
		t.Fatal("markdown 正文缺标题")
	}
}

// TestFeishuFormat 飞书消息格式（msg_type=text）。
func TestFeishuFormat(t *testing.T) {
	msg := FeishuMessage(Event{Kind: "task.done", Title: "任务完成"})
	if msg["msg_type"] != "text" {
		t.Fatalf("msg_type = %v", msg["msg_type"])
	}
}

// TestEventRouter 事件路由：注册两类事件通道，只投递匹配的。
func TestEventRouter(t *testing.T) {
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	n := New()
	_ = n.Register(Channel{Type: "webhook", URL: srv.URL + "/findings", Name: "发现通道", Events: []string{"finding.*"}})
	_ = n.Register(Channel{Type: "webhook", URL: srv.URL + "/tasks", Name: "任务通道", Events: []string{"task.*"}})

	_ = n.Deliver(Event{Kind: "finding.new", Title: "x"})
	_ = n.Deliver(Event{Kind: "task.done", Title: "y"})
	if len(hits) != 2 || hits[0] != "/findings" || hits[1] != "/tasks" {
		t.Fatalf("事件路由错: %v", hits)
	}
}
