---
name: capabilities-suid
description: Linux capabilities 与 SUID 深度利用
triggers: ["capabilities", "SUID", "cap_", "提权"]
mitre: ["T1548"]
---

# capabilities / SUID

1. 枚举：`getcap -r /`、`find / -perm -4000`（GTFOBins 对照）。
2. **GATE A**：清单全集 → 每个候选查 GTFOBins 利用路径 → 高危 capability（cap_dac_override/cap_sys_admin 等）单独定级。
3. 利用需 RoE（默认只报枚举结果+利用路径 candidate）。
4. SUID 二进制版本对照已知 CVE（record_fact）。
