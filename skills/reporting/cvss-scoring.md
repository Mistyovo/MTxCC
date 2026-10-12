---
name: cvss-scoring
description: CVSS v4 评分规范（报告定级）
triggers: ["CVSS", "评分", "定级"]
---

# CVSS 评分规范

report_finding 的 severity 必须与 CVSS 向量一致：
- 9.0+ critical（RCE/认证绕过+数据）；7.0-8.9 high；4.0-6.9 medium；0.1-3.9 low
- 证据等级天花板：declared≤low、inferred≤medium、runtime_observed≤critical（数据模型强制）
- 向量字符串写进 evidence（AV/AC/PR/UI/S/C/I/A）
