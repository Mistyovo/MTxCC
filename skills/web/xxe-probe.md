---
name: xxe-probe
description: XXE 外部实体注入探测（含盲 XXE）
triggers: ["XXE", "XML", "实体注入", "SOAP"]
mitre: ["T1190"]
---

# XXE 探测

1. 入口识别：XML 请求体（Content-Type: application/xml）、SOAP 端点、DOCX/XLSX 解析、SVG 上传。
2. **GATE A（必走完再报）**：回显探针（`<!ENTITY x "XXE_MARKER">` 引用看回显）→ 文件读探针（/etc/hostname 对照）→ OOB 盲注（HTTP/DNS 回调）。三步各自独立结论，缺步不得报"确认"。
3. 变体：参数实体、UTF-16 BOM、XInclude、SVG 嵌入。
4. 文件读成功即 report_finding（confidence=runtime_observed，evidence=读出的文件摘录）。
