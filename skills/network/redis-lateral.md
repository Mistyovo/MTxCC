---
name: redis-lateral
description: Redis 利用链（写文件/未授权/主从）
triggers: ["Redis", "6379", "未授权"]
mitre: ["T1190"]
---

# Redis 利用链

1. 未授权确认：INFO / CONFIG GET requirepass（对照）。
2. **GATE A**：未授权 → 写能力探测（SET 测试键再 DEL）→ 危险动作门：写 ssh key/webshell 需 RoE 允许（默认只报未授权+写能力，不落 shell）。
3. 主从复制 RCE、Lua 沙箱逃逸（CVE-2022-0543）记知识 candidate。
4. 凭据（AUTH 弱口令）走 weak-cred-controlled 纪律。
