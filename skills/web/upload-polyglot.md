---
name: upload-polyglot
description: 多态文件上传绕过（与 file-upload 互补）
triggers: ["上传绕过", "polyglot", "webshell"]
mitre: ["T1105"]
---

# 多态上传

**GATE A**：扩展名矩阵（大小写/双写/空格/点尾/::$DATA）→ Content-Type 错配 → 魔数/内容校验绕过（图片马）→ 渲染/解析面（ImageMagick、SVG、办公文档宏）。
webshell 只读证明纪律（rce-readonly-proof 复用）。
