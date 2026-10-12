---
name: ssrf-variants
description: SSRF 绕过矩阵（与 ssrf-cloudmeta 互补）
triggers: ["SSRF", "内网访问", "重定向"]
mitre: ["T1190"]
---

# SSRF 绕过矩阵（GATE：全形状走完再定级）

1. 协议族：http/https/file/gopher/dict/ftp。
2. 地址族：十进制 IP、十六进制 IP、八进制、IPv6 [::1]、0.0.0.0、localtest.me 类域名。
3. 解析绕过：DNS 重绑定、@ 截断、# 片段、%00、URL 编码双重、30x 跟随 vs 不跟随对照。
4. 每形状记 record_fact（含否定）；任一形状通 → report_finding。
