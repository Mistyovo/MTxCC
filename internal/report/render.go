// Package report 渲染结构化 Markdown 渗透报告（spec §8.3）。
package report

import (
	"fmt"
	"strings"
	"time"
)

// Evidence 是发现的一条证据引用。
type Evidence struct {
	Kind    string `json:"kind"` // tool_output | traffic | file
	Ref     string `json:"ref"`
	Excerpt string `json:"excerpt"`
}

// Finding 是报告视角的发现。
type Finding struct {
	ID          int64      `json:"id"`
	Vulnclass   string     `json:"vulnclass"`
	Severity    string     `json:"severity"`
	Title       string     `json:"title"`
	Status      string     `json:"status"`
	VerifyState string     `json:"verify_state"`
	Summary     string     `json:"summary"`
	Evidence    []Evidence `json:"evidence"`
}

// Data 是报告数据。
type Data struct {
	TaskID     int64     `json:"task_id"`
	TargetText string    `json:"target_text"`
	Status     string    `json:"status"`
	Findings   []Finding `json:"findings"`
}

var sevOrder = map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3, "info": 4}

// RenderMarkdown 渲染完整报告。
func RenderMarkdown(d Data) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# 渗透测试报告\n\n")
	fmt.Fprintf(&sb, "- 任务 #%d\n- 目标：`%s`\n- 状态：%s\n\n", d.TaskID, d.TargetText, d.Status)

	// 摘要。
	counts := map[string]int{}
	for _, f := range d.Findings {
		counts[f.Severity]++
	}
	sb.WriteString("## 执行摘要\n\n")
	if len(d.Findings) == 0 {
		sb.WriteString("本次测试未发现可确认漏洞（否定结论同样如实记录于任务探索图）。\n\n")
	} else {
		var parts []string
		for _, sev := range []string{"critical", "high", "medium", "low", "info"} {
			if counts[sev] > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", counts[sev], sev))
			}
		}
		fmt.Fprintf(&sb, "共 %d 项发现：%s。已验证项具备机器重放证据；候选项待复测确认。\n\n",
			len(d.Findings), strings.Join(parts, " · "))
	}

	// 发现表。
	if len(d.Findings) > 0 {
		sb.WriteString("## 发现总表\n\n| # | 级别 | 类别 | 标题 | 验证状态 |\n|---|---|---|---|---|\n")
		sorted := sortFindings(d.Findings)
		for i, f := range sorted {
			vs := "候选（未过验证）"
			if f.VerifyState == "verified" {
				vs = "已验证"
			}
			fmt.Fprintf(&sb, "| %d | %s | %s | %s | %s |\n", i+1, f.Severity, f.Vulnclass, f.Title, vs)
		}
		sb.WriteString("\n")
	}

	// 每发现详情。
	for _, f := range sortFindings(d.Findings) {
		fmt.Fprintf(&sb, "## %s\n\n", f.Title)
		fmt.Fprintf(&sb, "- 级别：%s（类别 %s）· 验证状态：%s\n\n", f.Severity, f.Vulnclass, f.VerifyState)
		fmt.Fprintf(&sb, "%s\n\n", f.Summary)
		if len(f.Evidence) > 0 {
			sb.WriteString("### 证据链\n\n")
			for _, e := range f.Evidence {
				fmt.Fprintf(&sb, "- **%s** `%s`\n  > %s\n\n", e.Kind, e.Ref, clip(e.Excerpt, 300))
			}
		}
	}

	sb.WriteString("## 复现步骤\n\n")
	sb.WriteString("每项已验证发现的证据引用（activity/traffic 编号）可在平台任务详情页回放原始交换。\n\n")
	sb.WriteString("## 修复建议\n\n")
	sb.WriteString("按发现总表自上而下处理；每项建议见对应发现的处置说明。\n")

	return sb.String()
}

func sortFindings(fs []Finding) []Finding {
	out := append([]Finding{}, fs...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && sevOrder[out[j].Severity] < sevOrder[out[j-1].Severity]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func clip(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// RenderHTML 渲染打印友好的 HTML 报告（浏览器打印即 PDF——合规交付零依赖）。
func RenderHTML(d Data) string {
	counts := map[string]int{}
	for _, f := range d.Findings {
		counts[f.Severity]++
	}
	var rows, detail strings.Builder
	for _, f := range sortFindings(d.Findings) {
		vs := "候选（未过验证）"
		if f.VerifyState == "verified" {
			vs = "已验证"
		}
		fmt.Fprintf(&rows, "<tr><td>%d</td><td class=\"sev-%s\">%s</td><td>%s</td><td>%s</td></tr>\n",
			f.ID, f.Severity, f.Severity, f.Title, vs)
		fmt.Fprintf(&detail, "<section><h3>%s</h3><p><b>级别</b>：<span class=\"sev-%s\">%s</span> · <b>验证</b>：%s</p><p>%s</p>",
			f.Title, f.Severity, f.Severity, f.VerifyState, f.Summary)
		if len(f.Evidence) > 0 {
			detail.WriteString("<h4>证据链</h4><ul>")
			for _, e := range f.Evidence {
				fmt.Fprintf(&detail, "<li><code>%s</code> %s — %s</li>", e.Kind, e.Ref, clip(e.Excerpt, 300))
			}
			detail.WriteString("</ul>")
		}
		detail.WriteString("</section>")
	}
	var sevLine []string
	for _, s := range []string{"critical", "high", "medium", "low", "info"} {
		if counts[s] > 0 {
			sevLine = append(sevLine, fmt.Sprintf("%d %s", counts[s], s))
		}
	}
	summary := "本次测试未发现可确认漏洞。"
	if len(d.Findings) > 0 {
		summary = fmt.Sprintf("共 %d 项发现：%s。", len(d.Findings), strings.Join(sevLine, " · "))
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>渗透测试报告 · 任务 #%d</title>
<style>
body{font-family:system-ui,sans-serif;max-width:900px;margin:2rem auto;line-height:1.7;color:#1a1a1a}
h1{border-bottom:3px solid #1a1a1a;padding-bottom:.5rem}
h3{margin-top:2rem;border-left:4px solid #1a1a1a;padding-left:.75rem}
.sev-critical{color:#b91c1c;font-weight:700}.sev-high{color:#c2410c;font-weight:600}
.sev-medium{color:#a16207}.sev-low{color:#15803d}.sev-info{color:#4b5563}
table{width:100%%;border-collapse:collapse;margin:1rem 0}
th,td{border:1px solid #d4d4d4;padding:.5rem;text-align:left}
code{background:#f5f5f5;padding:.15rem .4rem;border-radius:3px;font-size:.9em}
@media print{body{margin:0}.noprint{display:none}}
</style></head><body>
<div class="noprint"><button onclick="window.print()">🖨 打印 / 导出 PDF</button></div>
<h1>渗透测试报告</h1>
<p><b>任务</b>：#%d &nbsp; <b>目标</b>：<code>%s</code> &nbsp; <b>状态</b>：%s &nbsp; <b>日期</b>：%s</p>
<h2>执行摘要</h2><p>%s</p>
<h2>发现总表</h2>
<table><tr><th>#</th><th>级别</th><th>标题</th><th>验证状态</th></tr>%s</table>
<h2>发现详情</h2>%s
<h2>修复建议</h2><p>按发现总表自上而下处理；每项建议见对应发现的处置说明。</p>
<h2>免责声明</h2><p>本报告仅用于授权安全测试范围内的风险评估。</p>
</body></html>`, d.TaskID, d.TaskID, d.TargetText, d.Status, timeNow().Format("2006-01-02"), summary, rows.String(), detail.String())
}

var timeNow = func() time.Time { return time.Now() }
