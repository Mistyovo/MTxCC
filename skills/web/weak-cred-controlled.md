---
name: weak-cred-controlled
description: 弱口令测试（受控字典纪律）
triggers: ["弱口令", "爆破", "登录尝试"]
mitre: ["T1110"]
---

# 弱口令（受控）

- 默认字典：admin/root/user 等用户名 × top-50 常见口令（**禁 rockyou 全量**，除非 RoE 允许）。
- 速率：≤2 req/s + 失败退避；触发锁定/验证码 → 立即停止并 record_fact（锁定机制存在本身是结论）。
- 命中即 record_fact（凭据写 vault 占位符不写正文）+ report_finding（confidence=runtime_observed，evidence=成功会话证明）。
- 撞库前先 search_traces：同用户已试过的不重试。
