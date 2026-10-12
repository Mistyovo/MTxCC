//go:build docker

package graph

import (
	"strings"
	"testing"
)

// TestCoverageReport 覆盖度管理（ARTEX list_untested_assets 对标）：
// scope 分母（任务资产）vs 已测分子（intent 覆盖资产）→ 未测清单。
func TestCoverageReport(t *testing.T) {
	ctx, g, taskID, expID := setup(t)

	// 3 个 scope 资产。
	ids, err := g.InsertAssets(ctx, 0, []RawAsset{
		{Type: "service", Key: "10.0.0.5:80:http"},
		{Type: "service", Key: "10.0.0.5:22:ssh"},
		{Type: "service", Key: "10.0.0.5:3306:mysql"},
	}, taskID)
	if err != nil {
		t.Fatal(err)
	}
	// 意图只覆盖 80 端口。
	iid, err := g.AddIntent(ctx, expID, nil, "测 Web 80", 5, []int64{ids[0]})
	if err != nil {
		t.Fatal(err)
	}
	// 意图完成后（模拟 done）。
	_, _ = g.DB().Exec(ctx, `UPDATE exploration_nodes SET state='done' WHERE id=$1`, iid[0])

	cov, err := g.Coverage(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if cov.Tested != 1 || cov.Scope != 3 {
		t.Fatalf("tested=%d scope=%d, want 1/3", cov.Tested, cov.Scope)
	}
	if len(cov.Untested) != 2 {
		t.Fatalf("untested = %d, want 2: %v", len(cov.Untested), cov.Untested)
	}
	for _, u := range cov.Untested {
		if strings.Contains(u, ":80:") {
			t.Fatal("已测资产不应在未测清单")
		}
	}
}
