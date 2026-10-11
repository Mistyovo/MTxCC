---
name: port-scan-discipline
description: 端口扫描纪律（默认受控）
triggers: ["端口扫描", "nmap", "masscan"]
mitre: ["T1046"]
---

# 端口扫描纪律

- 默认 top-1000 TCP + 服务版本（-sV --open），限速 --max-rate 100。
- 全端口/UDP 仅 RoE 允许；UDP 先只测 53/123/161/500。
- 结果先 search_traces 防重复扫；产出全部 insert_assets（service 类型）。
