//go:build docker

package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcp "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"mtx/internal/graph"
	"mtx/internal/store"
)

func mustJ(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func newDomainTools(t *testing.T) (context.Context, *graph.GraphStore, map[string]any, ExecContext) {
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
	g := graph.NewGraphStore(s)

	var taskID, expID int64
	_ = s.QueryRow(ctx, `INSERT INTO tasks (title,target_text,status,created_at) VALUES ('t','demo','created',now()) RETURNING id`).Scan(&taskID)
	_ = s.QueryRow(ctx, `INSERT INTO explorations (task_id,created_at) VALUES ($1,now()) RETURNING id`, taskID).Scan(&expID)

	ec := ExecContext{TaskID: taskID, ExpID: expID, IntentID: 0, Role: "worker"}
	tools := map[string]any{}
	for _, tl := range DomainTools(g, ec) {
		tools[tl.Name()] = tl
	}
	return ctx, g, tools, ec
}

// TestAddIntentBatchLimit 验证批量意图上限 4。
func TestAddIntentBatchLimit(t *testing.T) {
	ctx, _, tools, _ := newDomainTools(t)
	ai := tools["add_intent"].(interface {
		Call(context.Context, json.RawMessage) (string, error)
	})

	five := mustJ(map[string]any{"intents": []map[string]any{
		{"title": "a"}, {"title": "b"}, {"title": "c"}, {"title": "d"}, {"title": "e"},
	}})
	if _, err := ai.Call(ctx, five); err == nil || !strings.Contains(err.Error(), "4") {
		t.Fatalf("5 条应拒绝且提示上限: %v", err)
	}

	four := mustJ(map[string]any{"intents": []map[string]any{
		{"title": "a"}, {"title": "b"}, {"title": "c"}, {"title": "d"},
	}})
	if _, err := ai.Call(ctx, four); err != nil {
		t.Fatalf("4 条应允许: %v", err)
	}
}

// TestRecordFactDefaultConfidence 验证 confidence 缺省 inferred。
func TestRecordFactDefaultConfidence(t *testing.T) {
	ctx, g, tools, _ := newDomainTools(t)
	rf := tools["record_fact"].(interface {
		Call(context.Context, json.RawMessage) (string, error)
	})
	out, err := rf.Call(ctx, mustJ(map[string]any{"title": "发现登录页", "body": "http://app/…"}))
	if err != nil {
		t.Fatalf("record_fact: %v", err)
	}
	var res struct{ NodeID int64 `json:"node_id"` }
	_ = json.Unmarshal([]byte(out), &res)
	var conf string
	if err := g.DB().QueryRow(ctx,
		`SELECT body->>'confidence' FROM exploration_nodes WHERE id=$1`, res.NodeID).Scan(&conf); err != nil {
		t.Fatal(err)
	}
	if conf != "inferred" {
		t.Fatalf("缺省 confidence = %q, want inferred", conf)
	}
}

// TestReportFindingValidation 验证必填缺失返回可读错误。
func TestReportFindingValidation(t *testing.T) {
	ctx, _, tools, _ := newDomainTools(t)
	rp := tools["report_finding"].(interface {
		Call(context.Context, json.RawMessage) (string, error)
	})
	_, err := rp.Call(ctx, mustJ(map[string]any{"severity": "high"}))
	if err == nil || !strings.Contains(err.Error(), "title") {
		t.Fatalf("缺 title 应有可读错误: %v", err)
	}
}

// TestGraphOverviewTool 验证 graph_overview 返回计数与死账。
func TestGraphOverviewTool(t *testing.T) {
	ctx, g, tools, ec := newDomainTools(t)
	if _, err := g.AddIntent(ctx, ec.ExpID, nil, "探 Web", 5, nil); err != nil {
		t.Fatalf("AddIntent: %v", err)
	}
	ov := tools["graph_overview"].(interface {
		Call(context.Context, json.RawMessage) (string, error)
	})
	out, err := ov.Call(ctx, []byte(`{}`))
	if err != nil {
		t.Fatalf("graph_overview: %v", err)
	}
	if !strings.Contains(out, "intents") {
		t.Fatalf("输出缺 intents: %s", out)
	}
}
