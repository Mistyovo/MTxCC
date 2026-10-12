//go:build docker

package graph

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcp "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"mtx/internal/store"
)

// startPG 启动一次性 PG16 容器。
func startPG(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	pg, err := tcp.Run(ctx, "postgres:16-alpine",
		tcp.WithDatabase("mtx"), tcp.WithUsername("mtx"), tcp.WithPassword("mtx"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("PG 容器: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	dsn, _ := pg.ConnectionString(ctx, "sslmode=disable")
	return dsn
}

func setup(t *testing.T) (context.Context, *GraphStore, int64, int64) {
	t.Helper()
	dsn := startPG(t)
	ctx := context.Background()
	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(s.Close)
	g := NewGraphStore(s)

	var taskID, expID int64
	if err := s.QueryRow(ctx, `INSERT INTO tasks (title,target_text,status,timeout_seconds,created_at)
		VALUES ('t','demo','created',3600,now()) RETURNING id`).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if err := s.QueryRow(ctx, `INSERT INTO explorations (task_id,created_at)
		VALUES ($1,now()) RETURNING id`, taskID).Scan(&expID); err != nil {
		t.Fatal(err)
	}
	return ctx, g, taskID, expID
}

func addIntent(t *testing.T, ctx context.Context, g *GraphStore, expID int64, title string, prio int) int64 {
	t.Helper()
	ids, err := g.AddIntent(ctx, expID, nil, title, prio, nil)
	if err != nil || len(ids) != 1 {
		t.Fatalf("AddIntent: %v ids=%v", err, ids)
	}
	return ids[0]
}

// TestFrontierOrder 验证 frontier 按 priority DESC, id ASC。
func TestFrontierOrder(t *testing.T) {
	ctx, g, _, expID := setup(t)
	low := addIntent(t, ctx, g, expID, "低优先", 2)
	mid := addIntent(t, ctx, g, expID, "中优先", 5)
	high := addIntent(t, ctx, g, expID, "高优先", 9)
	_ = mid

	ns, err := g.Frontier(ctx, expID, 10)
	if err != nil {
		t.Fatalf("Frontier: %v", err)
	}
	if len(ns) != 3 || ns[0].ID != high || ns[2].ID != low {
		t.Fatalf("顺序错误: %+v", ns)
	}
}

// TestAddIntentIllegalParent 验证 parent 必须 fact|finding（intent 父拒绝）。
func TestAddIntentIllegalParent(t *testing.T) {
	ctx, g, _, expID := setup(t)
	intentID := addIntent(t, ctx, g, expID, "父意图", 5)
	if _, err := g.AddIntent(ctx, expID, []int64{intentID}, "子", 5, nil); err == nil {
		t.Fatal("intent 作为 parent 应被拒绝")
	}
	// fact 作为 parent 合法。
	var factID int64
	if err := g.DB().QueryRow(ctx, `INSERT INTO exploration_nodes (exploration_id,kind,state,title,created_at,updated_at)
		VALUES ($1,'fact','active','线索',now(),now()) RETURNING id`, expID).Scan(&factID); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddIntent(ctx, expID, []int64{factID}, "派生意图", 6, nil); err != nil {
		t.Fatalf("fact 作为 parent 应允许: %v", err)
	}
}

// TestInsertAssetsUpsert 验证类型化去重键 upsert：重复不增行、attrs 追加合并、父子关系。
func TestInsertAssetsUpsert(t *testing.T) {
	ctx, g, taskID, _ := setup(t)
	raw := []RawAsset{
		{Type: "domain", Key: "app.example.com", Attrs: map[string]any{"tech": []string{"nginx"}}},
		{Type: "service", Key: "app.example.com:443:https", Attrs: map[string]any{"title": "Portal"}},
	}
	ids1, err := g.InsertAssets(ctx, 0, raw, taskID)
	if err != nil || len(ids1) != 2 {
		t.Fatalf("首次插入: %v %v", ids1, err)
	}
	// 重复插入：不增行；attrs 合并（tech 追加 php）。
	raw[0].Attrs = map[string]any{"tech": []string{"php"}}
	ids2, err := g.InsertAssets(ctx, 0, raw, taskID)
	if err != nil {
		t.Fatalf("二次插入: %v", err)
	}
	if ids2[0] != ids1[0] || ids2[1] != ids1[1] {
		t.Fatalf("upsert 应复用 ID: %v vs %v", ids1, ids2)
	}
	var count int
	if err := g.DB().QueryRow(ctx, `SELECT count(*) FROM assets`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("assets 行数 = %d, want 2 (%v)", count, err)
	}
	var tech string
	if err := g.DB().QueryRow(ctx, `SELECT attrs->'tech' FROM assets WHERE id=$1`, ids1[0]).Scan(&tech); err != nil {
		t.Fatal(err)
	}
	if tech == "" || tech == "null" {
		t.Fatalf("attrs 丢失: %q", tech)
	}
	// task_ids 累积包含 taskID。
	var taskIDs []int64
	if err := g.DB().QueryRow(ctx, `SELECT task_ids FROM assets WHERE id=$1`, ids1[0]).Scan(&taskIDs); err != nil {
		t.Fatal(err)
	}
	if len(taskIDs) == 0 || taskIDs[0] != taskID {
		t.Fatalf("task_ids = %v", taskIDs)
	}
}

// TestReportFindingEvidenceGate 验证 severity≤confidence 天花板：critical+declared 拒。
func TestReportFindingEvidenceGate(t *testing.T) {
	ctx, g, taskID, expID := setup(t)
	if _, err := g.ReportFinding(ctx, taskID, expID, 0, FindingIn{
		Vulnclass: "rce", Severity: "critical", Confidence: "declared", Title: "假 RCE",
	}); err == nil {
		t.Fatal("critical+declared 应被证据门拒绝")
	}
	if _, err := g.ReportFinding(ctx, taskID, expID, 0, FindingIn{
		Vulnclass: "rce", Severity: "critical", Confidence: "runtime_observed", Title: "真 RCE",
	}); err != nil {
		t.Fatalf("critical+runtime_observed 应允许: %v", err)
	}
}

// TestTriedLedger 验证死亡账本：同键两次失败进入 DeadVectors。
func TestTriedLedger(t *testing.T) {
	ctx, g, _, expID := setup(t)
	if err := g.MarkTried(ctx, expID, "sqli", "app:8080/login", "wandering"); err != nil {
		t.Fatal(err)
	}
	dead := g.DeadVectors(ctx, expID)
	if len(dead) != 0 {
		t.Fatalf("一次失败不应入死账: %v", dead)
	}
	if err := g.MarkTried(ctx, expID, "sqli", "app:8080/login", "wandering"); err != nil {
		t.Fatal(err)
	}
	dead = g.DeadVectors(ctx, expID)
	if dead[VectorKey{VectorClass: "sqli", TargetKey: "app:8080/login"}] != 2 {
		t.Fatalf("死账 = %v", dead)
	}
}

// TestAddIntentJaccardReject 换皮拒绝（反僵化机制化，调研 RedAmon Jaccard 设计）：
// 新意图标题与已死向量（≥2 次失败）标题相似度 >0.6 → 拒绝重派（强制换打法）。
func TestAddIntentJaccardReject(t *testing.T) {
	ctx, g, _, expID := setup(t)

	// 同标题试两次失败 → 死向量。
	_ = g.MarkTried(ctx, expID, "sqli", "Redis 6379 未授权打穿：键枚举写入 CONFIG 评估 RCE", "wandering")
	_ = g.MarkTried(ctx, expID, "sqli", "Redis 6379 未授权打穿：键枚举写入 CONFIG 评估 RCE", "wandering")

	// 同义换皮标题 → 拒。
	if _, err := g.AddIntent(ctx, expID, nil, "Redis 6379 未授权打穿：键枚举与写入 CONFIG 读取评估 RCE", 5, nil); err == nil {
		t.Fatal("换皮意图应被拒绝（Jaccard>0.6）")
	}
	// 全新方向 → 放行。
	if _, err := g.AddIntent(ctx, expID, nil, "DVWA 4280 登录后命令注入测试", 5, nil); err != nil {
		t.Fatalf("新方向应放行: %v", err)
	}
}

// TestJaccardHelper 相似度边界。
func TestJaccardHelper(t *testing.T) {
	if jaccard("Redis 6379 未授权打穿", "Redis 6379 未授权打穿") < 0.99 {
		t.Fatal("相同应接近 1")
	}
	if jaccard("Redis 6379 未授权打穿", "DVWA 登录命令注入") > 0.3 {
		t.Fatal("无关应低")
	}
}
