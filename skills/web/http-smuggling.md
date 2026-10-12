---
name: http-smuggling
description: HTTP 请求走私（CL.TE / TE.CL）探测
triggers: ["走私", "smuggling", "代理链"]
mitre: ["T1190"]
---

# HTTP 请求走私

1. 前置：确认有代理链（前端代理+后端）。
2. **GATE A**：时间基线（正常请求 RTT）→ CL.TE 探针（400/超时差异）→ TE.CL 对照 → obfused TE（`Transfer-Encoding: chunked` 变体）。必须有时间差证据才 candidate。
3. 危害演示只走私一个 GET 探测（不带攻击 payload）。
4. 证据：两个请求的时间差 + 响应串扰摘录。
