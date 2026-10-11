---
name: auth-session
description: 认证与会话机制测试
triggers: ["会话", "cookie", "session", "oauth"]
mitre: ["T1539"]
---

# 认证与会话

1. 会话固定：登录前后 cookie 值对照。
2. 登出后重放旧会话（服务端失效验证）。
3. cookie 属性：HttpOnly/Secure/SameSite 缺失对照。
4. OAuth/SSO：state 校验、redirect_uri 开放重定向、code 重放。
5. 记住我 token 结构与过期。
每项独立 record_fact；命中即 report_finding。
