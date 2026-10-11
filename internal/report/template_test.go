package report

import (
	"strings"
	"testing"
)

// TestCustomTemplate 自定义模板变量注入 + 语法错回退默认（Review Focus 4）。
func TestCustomTemplate(t *testing.T) {
	custom := "# {{.CompanyName}} 安全评估\n\n目标：{{.Target}}\n日期：{{.Date}}\n\n{{.Body}}"
	data := TemplateData{
		CompanyName: "Acme 安全实验室",
		Target:      "http://10.0.0.5",
		Body:        "## 摘要\n一切正常",
	}
	out, usedFallback := RenderWithTemplate(custom, data)
	if usedFallback {
		t.Fatal("有效模板不应回退")
	}
	if !strings.Contains(out, "Acme 安全实验室") || !strings.Contains(out, "10.0.0.5") {
		t.Fatalf("变量未注入: %q", out)
	}

	// 语法错 → 回退默认。
	bad := "{{.nope"
	out2, fb := RenderWithTemplate(bad, data)
	if !fb {
		t.Fatal("坏模板应回退")
	}
	if !strings.Contains(out2, "渗透测试报告") {
		t.Fatal("回退内容应为默认报告")
	}
}
