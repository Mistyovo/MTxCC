---
name: springshell-libs
description: 组件库漏洞（Spring/Log4j/Fastjson 等）
triggers: ["Log4j", "Spring", "Fastjson", "组件漏洞", "供应链"]
mitre: ["T1190"]
---

# 组件漏洞

指纹（Server 头/报错特征/静态资源版本）→ 对照已知 CVE 清单（Log4Shell/Spring4Shell/Fastjson/Jackson）→ **默认只报指纹+受影响判断（candidate）**，验证利用需 RoE（避免破坏性 payload）。
