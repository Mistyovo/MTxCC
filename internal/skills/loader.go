// Package skills 实现技能库渐进披露（spec §9）：frontmatter 目录常驻 system，
// 全文经 load_skill 按需加载。
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SkillMeta 是技能 frontmatter 元数据。
type SkillMeta struct {
	Name        string
	Description string
	Triggers    []string
	MITRE       []string
	RequiredTools []string
}

// Catalog 递归扫描目录树（.md）解析 frontmatter，按名称排序。
func Catalog(dir string) []SkillMeta {
	var out []SkillMeta
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		m := parseFrontmatter(string(b))
		if m.Name == "" {
			m.Name = strings.TrimSuffix(d.Name(), ".md")
		}
		out = append(out, m)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Load 读技能全文（递归定位，含正文，去 frontmatter 块）。
func Load(dir, name string) (string, error) {
	var found string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == name+".md" {
			found = p
		}
		return nil
	})
	if found == "" {
		return "", fmt.Errorf("技能 %q 不存在（用目录里的名称）", name)
	}
	b, err := os.ReadFile(found)
	if err != nil {
		return "", err
	}
	s := string(b)
	if strings.HasPrefix(s, "---\n") {
		if end := strings.Index(s[4:], "\n---\n"); end >= 0 {
			s = s[4+end+5:]
		}
	}
	return strings.TrimSpace(s), nil
}

// CatalogDirPrompt 渲染目录段（渐进披露：仅名称+描述+触发词+load_skill 指引）。
func CatalogDirPrompt(dir string) string {
	cat := Catalog(dir)
	if len(cat) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n\n【技能库】命中触发词的任务必须先 load_skill 加载再动手（SKILL-FIRST 规则）。\n")
	for _, m := range cat {
		trigs := strings.Join(m.Triggers, "、")
		line := fmt.Sprintf("- %s：%s", m.Name, m.Description)
		if trigs != "" {
			line += "（触发：" + trigs + "）"
		}
		sb.WriteString(line + "\n")
	}
	sb.WriteString("用法：load_skill({name: \"<名称>\"}) 加载全文。\n")
	return sb.String()
}

// parseFrontmatter 解析 --- 包围的 YAML 子集（key: value / key: [a, b]）。
func parseFrontmatter(s string) SkillMeta {
	var m SkillMeta
	if !strings.HasPrefix(s, "---\n") {
		return m
	}
	end := strings.Index(s[4:], "\n---\n")
	if end < 0 {
		return m
	}
	block := s[4 : 4+end]
	for _, line := range strings.Split(block, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		v = strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
		switch k {
		case "name":
			m.Name = strings.Trim(v, `"'`)
		case "description":
			m.Description = strings.Trim(v, `"'`)
		case "triggers":
			m.Triggers = splitList(v)
		case "mitre":
			m.MITRE = splitList(v)
		case "required_tools":
			m.RequiredTools = splitList(v)
		}
	}
	return m
}

func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.Trim(p, `"'`))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
