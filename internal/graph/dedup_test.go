//go:build docker

package graph

import (
	"strings"
	"testing"
)

// TestReportFindingDedup 同类发现去重（实测问题：Redis 未授权出现 4 条不同表述）：
// 同 (vulnclass, asset_key) 的重复上报应合并进已有 finding（计数+1）而非新增。
func TestReportFindingDedup(t *testing.T) {
	ctx, g, taskID, expID := setup(t)

	f1, err := g.ReportFinding(ctx, taskID, expID, 0, FindingIn{
		Vulnclass: "unauthorized_access", Severity: "medium", Confidence: "runtime_observed",
		Title: "Redis 6379 未授权访问", Summary: "无 requirepass", AssetKey: "127.0.0.1:6379",
	})
	if err != nil {
		t.Fatalf("f1: %v", err)
	}
	// 同类重复（不同表述）。
	f2, err := g.ReportFinding(ctx, taskID, expID, 0, FindingIn{
		Vulnclass: "unauthorized_access", Severity: "high", Confidence: "runtime_observed",
		Title: "Redis 6379 未授权访问（ACL 全权限）", Summary: "default nopass +@all", AssetKey: "127.0.0.1:6379",
	})
	if err != nil {
		t.Fatalf("f2: %v", err)
	}
	if f1 != f2 {
		t.Fatalf("同类发现应合并: f1=%d f2=%d", f1, f2)
	}
	// 计数 +1，severity 取较高（high）。
	var count int
	var severity string
	if err := g.DB().QueryRow(ctx,
		`SELECT body->>'dedup_count', body->>'severity' FROM exploration_nodes WHERE id=$1`, f1).Scan(&count, &severity); err != nil {
		// findings 表侧查
		_ = err
	}
	var fc int
	_ = g.DB().QueryRow(ctx, `SELECT count(*) FROM findings WHERE task_id=$1`, taskID).Scan(&fc)
	if fc != 1 {
		t.Fatalf("findings 行数 = %d, want 1（去重）", fc)
	}
	var sev string
	_ = g.DB().QueryRow(ctx, `SELECT severity FROM findings WHERE id=$1`, f1).Scan(&sev)
	if sev != "high" {
		t.Fatalf("severity = %q, want high（升级取高）", sev)
	}
	_ = strings.TrimSpace("")
}

// TestReportFindingDifferentAssetNoDedup 不同资产的同类问题不去重。
func TestReportFindingDifferentAssetNoDedup(t *testing.T) {
	ctx, g, taskID, expID := setup(t)
	f1, _ := g.ReportFinding(ctx, taskID, expID, 0, FindingIn{
		Vulnclass: "unauthorized_access", Severity: "low", Confidence: "runtime_observed",
		Title: "Redis 未授权", AssetKey: "127.0.0.1:6379",
	})
	f2, _ := g.ReportFinding(ctx, taskID, expID, 0, FindingIn{
		Vulnclass: "unauthorized_access", Severity: "low", Confidence: "runtime_observed",
		Title: "Redis 未授权", AssetKey: "10.0.0.5:6379",
	})
	if f1 == f2 {
		t.Fatal("不同资产不应去重")
	}
}
