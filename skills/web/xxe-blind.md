---
name: xxe-blind
description: 盲 XXE 与 OOB（与 xxe-probe 互补）
triggers: ["盲 XXE", "OOB XXE"]
mitre: ["T1190"]
---

# 盲 XXE OOB

参数实体外带（DTD 外链）、DNS/HTTP OOB 回调（Burp collaborator 形态）、报错型 XXE（异常信息回显）。三形态全走；回调成功即 runtime_observed。
