package store

import (
	"context"
	"fmt"
)

// SchemaVersion 返回当前已应用迁移数（= schema 版本号）。
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var n int
	if err := s.QueryRow(ctx, `SELECT count(*) FROM mtx_migrations`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// VersionInfo 版本信息（mtx version 输出）。
type VersionInfo struct {
	Schema int
}

// String 见 fmt.Stringer。
func (v VersionInfo) String() string {
	return fmt.Sprintf("schema: v%d", v.Schema)
}
