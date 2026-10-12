---
name: oauth-saml
description: OAuth/OIDC/SAML 认证缺陷
triggers: ["OAuth", "OIDC", "SAML", "单点登录"]
mitre: ["T1550"]
---

# OAuth/OIDC/SAML

**GATE A（认证流程必测四件）**：
1. state 参数缺失/可预测（CSRF）对照。
2. redirect_uri 绕过（前缀匹配、双参数、# 绕过）。
3. code 重放/跨客户端复用。
4. SAML：签名剥离、XXE、注释绕过、断言重放。
每项独立 record_fact；命中即 report_finding（认证绕过类 high）。
