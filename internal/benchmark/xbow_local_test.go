package benchmark

import (
	"os"
	"path/filepath"
	"testing"
)

// TestXBowLocalList 模式改造（实证：XBOW validation bench 是开源仓库，无需 token）：
// 扫描题目目录（XBEN-*）→ 列出；FlagOf 读 .env 注入的 flag。
func TestXBowLocalList(t *testing.T) {
	dir := t.TempDir()
	// 造两个题目目录。
	for _, name := range []string{"XBEN-001-24", "XBEN-002-24"} {
		d := filepath.Join(dir, name)
		_ = os.MkdirAll(d, 0o755)
		_ = os.WriteFile(filepath.Join(d, ".env"), []byte(`FLAG="flag{test-`+name+`}"`), 0o644)
		_ = os.WriteFile(filepath.Join(d, "README.md"), []byte("# "+name+" IDOR 测试题"), 0o644)
	}
	a := NewXBowLocal(dir)
	chs, err := a.ListChallenges()
	if err != nil {
		t.Fatal(err)
	}
	if len(chs) != 2 {
		t.Fatalf("challenges = %d, want 2", len(chs))
	}
	if chs[0].ID != "XBEN-001-24" {
		t.Fatalf("ID = %s", chs[0].ID)
	}
	// flag 读取。
	f, err := a.FlagOf("XBEN-001-24")
	if err != nil || f != "flag{test-XBEN-001-24}" {
		t.Fatalf("FlagOf = %q %v", f, err)
	}
	// 计分：flag 匹配。
	if !a.CheckFlag("XBEN-001-24", "flag{test-XBEN-001-24}") {
		t.Fatal("正确 flag 应通过")
	}
	if a.CheckFlag("XBEN-001-24", "flag{wrong}") {
		t.Fatal("错误 flag 应拒绝")
	}
}
