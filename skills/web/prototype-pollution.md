---
name: prototype-pollution
description: 原型链污染（Node/前端）
triggers: ["原型链", "prototype", "__proto__"]
mitre: ["T1190"]
---

# 原型链污染

入参 JSON 含 `__proto__`/`constructor.prototype` → 观察全局行为变化（DOM 属性、配置注入）。GATE：源污染探针 → 到达面（Gadget）识别 → 危害验证（只读证明）。
