---
name: api-fuzz
description: API 参数模糊测试（受控）
triggers: ["API", "参数", "fuzz"]
---

# API fuzz

- 先 traffic_search 已有流量还原接口清单（方法/参数/类型），不盲猜。
- 每参数单变量对照：类型混淆、空值/超长/特殊字符各一发；错误差异 record_fact。
- 未文档化路由（JS 里发现的）逐个验证存在性，再进矩阵。
- 限速 ≤ 5 req/s；全量 fuzz 需 RoE 允许。
