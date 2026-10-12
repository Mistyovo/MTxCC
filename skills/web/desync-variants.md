---
name: desync-variants
description: HTTP Desync 高级变体（与 http-smuggling 互补）
triggers: ["desync", "TE.0", "H2"]
mitre: ["T1190"]
---

# Desync 变体

TE.0、CL.0、H2.CL、H2.TE、header 名混淆、双 Content-Length。时间基线对照 + 响应串扰证据。默认走私探测请求不含攻击载荷。
