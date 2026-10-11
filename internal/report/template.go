package report

import (
	"bytes"
	"strings"
	"text/template"
	"time"
)

// TemplateData 是模板变量。
type TemplateData struct {
	CompanyName string
	Target      string
	Date        string
	Body        string // 渲染后的正文（摘要+发现）
}

// RenderWithTemplate 用自定义模板渲染；语法错回退默认（Review Focus 4）。
func RenderWithTemplate(tpl string, d TemplateData) (string, bool) {
	if d.Date == "" {
		d.Date = time.Now().Format("2006-01-02")
	}
	t, err := template.New("r").Parse(tpl)
	if err != nil {
		return defaultRender(d), true
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, d); err != nil {
		return defaultRender(d), true
	}
	return buf.String(), false
}

func defaultRender(d TemplateData) string {
	var sb strings.Builder
	sb.WriteString("# 渗透测试报告\n\n")
	if d.CompanyName != "" {
		sb.WriteString("公司：" + d.CompanyName + "\n")
	}
	sb.WriteString("目标：" + d.Target + "\n日期：" + d.Date + "\n\n")
	sb.WriteString(d.Body)
	return sb.String()
}
