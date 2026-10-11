---
name: rce-readonly-proof
description: RCE 只读证明纪律（默认不破坏）
triggers: ["RCE", "命令执行", "代码执行"]
mitre: ["T1059"]
---

# RCE 只读证明

**默认只读证明**（RCE_AGGRESSIVE_PAYLOADS=false）：id、uname -a、hostname、whoami 足以定级 critical（confidence=runtime_observed）。

## 硬纪律
- 反弹 shell、写文件、改状态 = 需任务 RoE 显式允许；默认禁止。
- 无回显 RCE：时间盲（sleep 3 对比基线）或 OOB（DNS/HTTP 回调）证明，二选一，标注证据类型。
- 每次证明命令前查 search_traces——同命令已跑过就复用结论。

## 报告
report_finding 时 evidence 必须含：原命令、原始响应/时间差、判定依据。禁止只有"疑似"。
