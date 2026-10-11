---
name: linux-privesc-enum
description: Linux 提权枚举（受控清单）
triggers: ["Linux 提权", "sudo", "suid", "内核"]
mitre: ["T1068", "T1548"]
---

# Linux 提权枚举（一次性脚本扫）

单条组合命令收齐：`sudo -l`；suid（find / -perm -4000）； capabilities；定时任务；可写服务路径；历史文件中的凭据样字符串；内核版本对照已知提权（记录版本即可，默认不弹内核）。

- 枚举结果整体 record_fact（含否定结论）；命中可写 sudo 规则/凭据泄露 → 独立利用意图由 planner 派。
- 内核漏洞利用（dirty pipe 等）= 高危动作，默认只报版本+受影响判断（candidate）。
