---
name: ad-initial
description: AD 域初始侦察（立足点上的标准动作）
triggers: ["域", "AD", "域控", "LDAP"]
mitre: ["T1018", "T1087"]
---

# AD 初始侦察

1. 域信息：`echo %USERDOMAIN%` / `nltest /dsgetdc:`；当前用户与组（whoami /all）。
2. 血亲：`net group "domain admins" /domain`、`net user /domain`（低噪声优先于 BloodHound 全量采集——全量需 RoE 允许）。
3. SPN 与 kerberoastable 账户清单（record_fact，先不请求票据）。
4. 域控 IP 与 SMB/ LDAP 可达性 insert_assets。

产出锚定 planner：kerberoast / asreproast / GPO 滥用各成独立后续意图。
