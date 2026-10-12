---
name: race-condition
description: 竞态条件（支付/限额）
triggers: ["竞态", "race", "并发重放"]
mitre: ["T1190"]
---

# 竞态条件

GATE：基线单请求 → 并发 2/5/10 同请求对照（余额/优惠券/投票）→ TOCTOU 文件上传对照。成功标准：同一操作产生 >1 次副作用（需前后状态对照证据）。
