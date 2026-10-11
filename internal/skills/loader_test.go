package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, dir, name, front, body string) {
	t.Helper()
	full := "---\n" + front + "\n---\n\n" + body
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(full), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCatalogFrontmatter 验证 frontmatter 解析（name/description/triggers/mitre）与目录排序。
func TestCatalogFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "sqli-login-bypass",
		"name: sqli-login-bypass\ndescription: 登录页 SQL 注入认证绕过矩阵\ntriggers: [\"登录\", \"SQL 注入\", \"login\"]\nmitre: [\"T1190\"]",
		"# 登录页认证绕过矩阵\n逐字段×逐形状扫完再下结论。")
	writeSkill(t, dir, "ssrf-cloudmeta",
		"name: ssrf-cloudmeta\ndescription: SSRF 与云元数据取证\ntriggers: [\"SSRF\", \"169.254\"]",
		"先测 169.254.169.254。")

	cat := Catalog(dir)
	if len(cat) != 2 {
		t.Fatalf("目录数 = %d, want 2", len(cat))
	}
	if cat[0].Name != "sqli-login-bypass" || !strings.Contains(cat[0].Description, "矩阵") {
		t.Fatalf("解析: %+v", cat[0])
	}
	if len(cat[0].Triggers) != 3 || cat[0].MITRE[0] != "T1190" {
		t.Fatalf("triggers/mitre: %+v", cat[0])
	}
}

// TestLoadProgressiveDisclosure 验证全文加载与缺失技能错误。
func TestLoadProgressiveDisclosure(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "jwt-attacks", "name: jwt-attacks\ndescription: JWT 攻击矩阵",
		"# 攻击矩阵\nalg=none / RS256→HS256 / kid 注入。")

	body, err := Load(dir, "jwt-attacks")
	if err != nil || !strings.Contains(body, "RS256→HS256") {
		t.Fatalf("Load: %q %v", body, err)
	}
	if _, err := Load(dir, "nope"); err == nil {
		t.Fatal("缺失技能应报错")
	}
}

// TestCatalogDirPrompt 验证目录段渲染（渐进披露：目录常驻、全文按需）。
func TestCatalogDirPrompt(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "rce-readonly", "name: rce-readonly\ndescription: RCE 只读证明\ntriggers: [\"RCE\"]", "id 命令即停。")
	prompt := CatalogDirPrompt(dir)
	if !strings.Contains(prompt, "rce-readonly") || !strings.Contains(prompt, "RCE 只读证明") {
		t.Fatalf("目录段: %q", prompt)
	}
	if strings.Contains(prompt, "id 命令即停") {
		t.Fatal("目录段不应含全文（渐进披露）")
	}
	if !strings.Contains(prompt, "load_skill") {
		t.Fatal("目录段应教 load_skill 用法")
	}
}
