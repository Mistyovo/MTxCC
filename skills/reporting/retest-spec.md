---
name: retest-spec
description: 复测规范（verifier 专用）
triggers: ["复测", "retest", "verifier"]
---

# 复测规范

1. 读原 finding 的 evidence 引用，复放原始请求（traffic_get 取原始交换）。
2. 判定：reproduced（特征重现）/ fixed（特征消失）/ inconclusive（环境变化，说明原因）。
3. hasRetest=true 是 confirmed 的硬前提——没有真实复测动作不得确认。
4. 结论写回 finding_retests；"fixed" 自动流转处置状态。
