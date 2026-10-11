package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
)

//go:embed schema/*.sql
var schemaFS embed.FS

// migrate 按文件名序执行尚未应用的迁移，进程间用 advisory 锁串行化。
// 每个 .sql 在单事务内执行并登记到 mtx_migrations，语句自身保持幂等（IF NOT EXISTS）。
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS mtx_migrations (
		name text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("建迁移登记表: %w", err)
	}

	entries, err := fs.Glob(schemaFS, "schema/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(entries)

	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	// 迁移互斥锁（固定 key），防止多实例并发迁移。
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(914726)`); err != nil {
		return fmt.Errorf("取迁移锁: %w", err)
	}
	defer conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(914726)`)

	for _, name := range entries {
		var exists bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM mtx_migrations WHERE name=$1)`, name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		body, err := schemaFS.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("迁移 %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO mtx_migrations (name) VALUES ($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}
