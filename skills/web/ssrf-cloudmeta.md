---
name: ssrf-cloudmeta
description: SSRF 探测与云元数据端点取证
triggers: ["SSRF", "URL 参数", "169.254", "回调"]
mitre: ["T1190"]
---

# SSRF 与云元数据

## 顺序
1. 识别接收 URL 的参数（webhook/导入/预览/avatar）。
2. 先测回环与内网：`http://127.0.0.1/`、`http://10.0.0.5/`（对照响应差异）。
3. 云元数据（需在 RoE scope 内）：`http://169.254.169.254/latest/meta-data/`（AWS）、`http://metadata.google.internal/computeMetadata/v1/`（需 Metadata-Flavor 头）、`http://100.100.100.200/latest/meta-data/`（阿里云）。
4. 拿到角色凭据 → record_fact（confidence=runtime_observed）+ 存入凭据观察，**凭据值写进 evidence 引用而非正文**。

## 绕过形态
十进制/十六进制 IP、`127.1`、DNS 重绑定域名、`@` 重定向、30x 跟随、`file://`/`gopher://` 协议（视中间件）。
