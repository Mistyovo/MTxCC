---
name: credential-harvest
description: 凭据收割与复用（vault 纪律）
triggers: ["凭据", "hash", "密码", "secrets"]
mitre: ["T1552"]
---

# 凭据收割（vault 占位符纪律）

1. 来源：配置文件/历史命令/内存转储（需 RoE）/浏览器存储/SSH 密钥。
2. **硬纪律**：真值只进 vault（${CRED:label}），绝不进 transcript/正文；报告引用 label。
3. 复用测试：同凭据跨服务横向（一个凭据 3 服务对照）。
4. 每次收割 record_fact（label 清单）+ report_finding（severity 按可复用面）。
