---
name: service-enum
description: 服务枚举（SMB/FTP/SSH/RDP 标准动作）
triggers: ["SMB", "FTP", "SSH", "445", "枚举"]
mitre: ["T1046"]
---

# 服务枚举

- SMB：`enum4linux -a` / `smbclient -L`（空会话对照）；共享列表 insert_assets。
- FTP：匿名登录对照；可写目录 record_fact。
- SSH：banner 与版本；弱密钥/默认口令走 weak-cred-controlled 纪律。
- RDP：`nmap --script rdp-ntlm-info`。
- 全部结论（含否定）record_fact；新端口服务 insert_assets。
