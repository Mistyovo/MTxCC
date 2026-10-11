//go:build docker

package graph

import (
	"strings"
	"testing"
)

// TestFindingTrafficBinding report_finding 带 traffic_refs → evidence 追加速照引用（M1 Task 9）。
func TestFindingTrafficBinding(t *testing.T) {
	ctx, g, taskID, expID := setup(t)

	fid, err := g.ReportFinding(ctx, taskID, expID, 0, FindingIn{
		Vulnclass: "rce", Severity: "critical", Confidence: "runtime_observed",
		Title: "[RCE] 绑定验证", Summary: "带流量引用",
	})
	if err != nil {
		t.Fatalf("ReportFinding: %v", err)
	}
	if err := g.BindTraffic(ctx, taskID, fid, []string{"ex-15", "ex-16"}); err != nil {
		t.Fatalf("BindTraffic: %v", err)
	}

	var evidence string
	if err := g.DB().QueryRow(ctx, `SELECT evidence::text FROM findings WHERE id=$1`, fid).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(evidence, "ex-15") || !strings.Contains(evidence, "traffic") {
		t.Fatalf("evidence 缺流量引用: %s", evidence)
	}
}
