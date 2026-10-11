//go:build docker

package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/sync/errgroup"
)

// startPG 启动一次性 PG16 容器并返回 DSN。
func startPG(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	pg, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("mtx"),
		postgres.WithUsername("mtx"),
		postgres.WithPassword("mtx"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("5432/tcp").
				WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("启动 PG 容器: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("获取 DSN: %v", err)
	}
	return dsn
}

// TestOpenIdempotentMigrations 验证迁移幂等：同一 DSN 二次 Open 不报错。
// 令其失败的生产改动：迁移 SQL 去掉 IF NOT EXISTS、或迁移记录表逻辑回退。
func TestOpenIdempotentMigrations(t *testing.T) {
	dsn := startPG(t)
	ctx := context.Background()

	s1, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("第一次 Open: %v", err)
	}
	defer s1.Close()

	s2, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("第二次 Open（幂等迁移）: %v", err)
	}
	defer s2.Close()
}

// TestClaimIntentExactlyOnce 验证 CAS：两 worker 并发领取同一 open 意图，恰好一个成功。
// 令其失败的生产改动：ClaimIntent 改成先查后改的两步、或 WHERE 条件丢失 state='open'。
func TestClaimIntentExactlyOnce(t *testing.T) {
	dsn := startPG(t)
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// 准备：任务 → 探索 → 一条 open 意图。
	taskID := mustExecReturningID(t, s, `INSERT INTO tasks (title, target_text, status, timeout_seconds, created_at)
		VALUES ('t', 'demo', 'created', 3600, now()) RETURNING id`)
	expID := mustExecReturningID(t, s, `INSERT INTO explorations (task_id, created_at)
		VALUES ($1, now()) RETURNING id`, taskID)
	intentID := mustExecReturningID(t, s, `INSERT INTO exploration_nodes (exploration_id, kind, state, priority, title, created_at, updated_at)
		VALUES ($1, 'intent', 'open', 5, '探测 Web 入口', now(), now()) RETURNING id`, expID)

	var (
		mu      sync.Mutex
		results []bool
	)
	start := make(chan struct{})
	var g errgroup.Group
	for _, worker := range []string{"w1", "w2"} {
		worker := worker
		g.Go(func() error {
			<-start // barrier：两 goroutine 同时发起
			ok, err := s.ClaimIntent(ctx, expID, intentID, worker)
			if err != nil {
				return err
			}
			mu.Lock()
			results = append(results, ok)
			mu.Unlock()
			return nil
		})
	}
	close(start)
	if err := g.Wait(); err != nil {
		t.Fatalf("并发领取: %v", err)
	}

	gotTrue := 0
	for _, r := range results {
		if r {
			gotTrue++
		}
	}
	if gotTrue != 1 {
		t.Fatalf("恰好一个 worker 应领取成功，实际 %d/%d", gotTrue, len(results))
	}
}

// TestNodeStateMachineCHECK 验证 kind 状态机：intent 写非法 state 被 CHECK 拒绝。
// 令其失败的生产改动：exploration_nodes 的状态 CHECK 约束被删除。
func TestNodeStateMachineCHECK(t *testing.T) {
	dsn := startPG(t)
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	taskID := mustExecReturningID(t, s, `INSERT INTO tasks (title, target_text, status, timeout_seconds, created_at)
		VALUES ('t', 'demo', 'created', 3600, now()) RETURNING id`)
	expID := mustExecReturningID(t, s, `INSERT INTO explorations (task_id, created_at)
		VALUES ($1, now()) RETURNING id`, taskID)

	_, err = s.Exec(ctx, `INSERT INTO exploration_nodes (exploration_id, kind, state, title, created_at, updated_at)
		VALUES ($1, 'intent', 'bogus_state', 'x', now(), now())`, expID)
	if err == nil {
		t.Fatal("intent 写非法 state='bogus_state' 应被 CHECK 拒绝，实际成功")
	}
}

func mustExecReturningID(t *testing.T, s *Store, q string, args ...any) int64 {
	t.Helper()
	var id int64
	err := s.QueryRow(context.Background(), q, args...).Scan(&id)
	if err != nil {
		t.Fatalf("预置数据 %q: %v", q, err)
	}
	return id
}
