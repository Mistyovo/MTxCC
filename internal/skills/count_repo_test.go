package skills_test

import (
	"os"
	"testing"

	"mtx/internal/skills"
)

// TestCatalogRepoCount 仓库内置技能数 ≥ 20（M1 验收）。
func TestCatalogRepoCount(t *testing.T) {
	dir := "../../skills"
	if _, err := os.Stat(dir); err != nil {
		t.Skip("非仓库根运行")
	}
	if n := len(skills.Catalog(dir)); n < 20 {
		t.Fatalf("内置技能 = %d, want >= 20", n)
	}
}
