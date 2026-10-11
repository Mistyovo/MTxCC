// Package store 封装 PostgreSQL 访问：连接池、嵌入迁移与各域 store。
package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 是数据库访问入口。Open 时自动执行幂等迁移。
type Store struct {
	pool *pgxpool.Pool
}

// Open 建立连接池、探活并执行迁移。
func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("创建连接池: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("数据库探活: %w", err)
	}
	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭连接池。
func (s *Store) Close() { s.pool.Close() }

// Pool 暴露底层连接池（供 store 内各域文件使用）。
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Exec / Query / QueryRow 是薄封装，统一走连接池。
func (s *Store) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return s.pool.Exec(ctx, sql, args...)
}

// Query 见 Exec。
func (s *Store) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return s.pool.Query(ctx, sql, args...)
}

// QueryRow 见 Exec。
func (s *Store) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return s.pool.QueryRow(ctx, sql, args...)
}
