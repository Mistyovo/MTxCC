---
name: kerberoast-gate
description: Kerberoasting 纪律（可请求离线破解）
triggers: ["kerberoast", "SPN", "TGS"]
mitre: ["T1558.003"]
---

# Kerberoasting（受限动作）

- 前置：ad-initial 已列出 SPN 账户。
- 请求 TGS（`GetUserSPNs -request` 或 Rubeus /roast）属中噪声动作：默认仅请求 **1 个**高价值 SPN 证明可行性。
- 离线破解本地进行（hashcat 模式 13100，top-1000 字典）；破解出的明文走 vault 占位符。
- 结论 record_fact + report_finding（confidence=runtime_observed 需含可破解证据）。
