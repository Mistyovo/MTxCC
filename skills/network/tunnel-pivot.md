---
name: tunnel-pivot
description: 隧道与内网跳板
triggers: ["隧道", "pivot", "代理", "内网穿透"]
mitre: ["T1572"]
---

# 隧道/跳板

1. 已有通道枚举：出网端口对照（payload-egress-confirm 复用）、已控服务的代理能力。
2. 隧道工具：ssh -L/-D、socat、chisel/frp（需 RoE 显式允许建立隧道）。
3. **默认只侦察不建立**：报告"可用通道"candidate；建立动作留给人工批准。
4. 跳板发现（多宿主机）走 internal-anchor。
