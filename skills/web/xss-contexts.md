---
name: xss-contexts
description: XSS 结构族探测（按上下文选 payload 家族）
triggers: ["XSS", "跨站", "反射", "DOM"]
mitre: ["T1059.007"]
---

# XSS 按上下文探测

每个注入点先探**结构族**（各一发）：原始 HTML 标签、属性逃逸（`"><`）、script 表达式（`';`）、注释（`-->`）、URL（`%22`）。

- 判定读**字面位置**（原始响应体中 payload 落在哪），不读应用自述。
- 存储型：先在低权入口写入探针，再到展示位验证（两步都留 traffic 证据）。
- DOM 型：读 JS 源确认 sink（innerHTML/eval/document.write）再构造。
- 命中一个上下文即 report_finding；其余上下文 record_fact 记录否定结论。
