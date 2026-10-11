package skills

import "testing"

// TestCatalogRepoCount 仓库内置技能数 ≥ 20（M1 验收）。
func TestCatalogRepoCount(t *testing.T) {
	n := len(Catalog("../../skills"))
	if n < 20 {
		t.Fatalf("内置技能 = %d, want >= 20", n)
	}
}
