---
name: saml-signature
description: SAML 签名绕过与断言滥用
triggers: ["SAML", "断言", "XSW"]
mitre: ["T1550"]
---

# SAML 签名

GATE：签名剥离 → XSW（XML Signature Wrapping 8 形态）→ 注释绕过 → 断言重放 → 非法 Audience。命中即认证绕过 high。
