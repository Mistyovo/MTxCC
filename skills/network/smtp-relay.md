---
name: smtp-relay
description: SMTP 开放中继与枚举
triggers: ["SMTP", "25", "邮件"]
mitre: ["T1190"]
---

# SMTP

GATE：VRFY/EXPN 枚举 → 开放中继测试（外发探针邮件到自控地址）→ 用户枚举对照。中继开放 = high。
