---
name: finding-report
description: report_finding 证据纪律（写报告前必读）
triggers: ["报告", "report_finding", "证据"]
---

# report_finding 纪律

- severity ≤ 证据等级：declared≤low、inferred≤medium、runtime_observed≤critical。
- evidence 每项 {kind: tool_output|traffic|file, ref, excerpt}：excerpt 是**原始响应摘录**不是转述。
- 只有真实触发过的才 report；版本匹配 CVE → record_fact（candidate 由 verifier 决定）。
- 标题格式：`[类] 资产 — 一句话影响`；修复建议一句可执行。
