---
name: windows-tokens
description: Windows 令牌/凭据窃取防御性枚举
triggers: ["Windows", "token", "mimikatz", "SAM"]
mitre: ["T1003"]
---

# Windows 凭据（受限）

1. 枚举：cmdkey /list、DPAPI 目录、注册表保存凭据（只列不取）。
2. **GATE A**：kerberoasting（pass-spray-gate 纪律）/ AS-REP roasting 枚举出可攻击账户（只发 1 个请求证明可行）。
3. LSASS/SAM 转储 = 高危动作，默认禁止（RoE 显式允许）。
4. 结果进 vault + record_fact。
