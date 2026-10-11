package report

import (
	"strings"
	"testing"
)

func sampleData() Data {
	ev1 := Evidence{Kind: "tool_output", Ref: "activity#42", Excerpt: "uid=0(root) gid=0(root)"}
	ev2 := Evidence{Kind: "traffic", Ref: "exchange#15", Excerpt: "GET /api/exec?id=id HTTP/1.1"}
	ev3 := Evidence{Kind: "tool_output", Ref: "activity#61", Excerpt: "elapsed=3.21s vs baseline=0.08s"}
	f1 := Finding{
		ID: 1, Vulnclass: "rce", Severity: "critical",
		Title: "[RCE] /api/exec 命令注入", Status: "verified", VerifyState: "verified",
		Summary: "id 参数直接拼入 shell，已取得只读执行证明",
		Evidence: []Evidence{ev1, ev2},
	}
	f2 := Finding{
		ID: 2, Vulnclass: "sqli", Severity: "medium",
		Title: "[SQLi] /search 时间盲注（候选）", Status: "candidate", VerifyState: "unconfirmed",
		Summary: "sleep(3) 产生 3.2s 时延差",
		Evidence: []Evidence{ev3},
	}
	return Data{TaskID: 7, TargetText: "http://10.0.0.5", Status: "done", Findings: []Finding{f1, f2}}
}

// TestRenderMarkdown 渲染包含：执行摘要、分级发现表、每发现证据链、复现与修复。
func TestRenderMarkdown(t *testing.T) {
	md := RenderMarkdown(sampleData())

	for _, want := range []string{
		"# 渗透测试报告", "任务 #7", "http://10.0.0.5",
		"## 执行摘要", "1 critical", "1 medium",
		"[RCE] /api/exec", "uid=0(root)",
		"已验证", "候选（未过验证）",
		"activity#42", "exchange#15",
		"## 复现步骤", "## 修复建议",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("报告缺 %q", want)
		}
	}
}

// TestRenderEmpty 无发现时的空报告不崩且如实陈述。
func TestRenderEmpty(t *testing.T) {
	md := RenderMarkdown(Data{TaskID: 1, TargetText: "x", Status: "done"})
	if !strings.Contains(md, "未发现可确认漏洞") {
		t.Fatalf("空报告: %.100s", md)
	}
}
