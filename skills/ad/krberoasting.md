---
name: kerberoast
description: Kerberoasting 完整流程（与 kerberoast-gate 互补）
triggers: ["kerberoast", "SPN"]
mitre: ["T1558.003"]
---

# Kerberoasting 流程

GATE：SPN 枚举 → 单票据请求（proof）→ 离线破解（hashcat 13100）→ 明文进 vault。全程 record_fact。
