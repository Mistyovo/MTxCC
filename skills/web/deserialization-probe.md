---
name: deserialization-probe
description: 反序列化探测（默认只探测不执行 gadget）
triggers: ["反序列化", "serialize", "pickle", "Java", "PHP 序列化"]
mitre: ["T1203"]
---

# 反序列化探测

- 指纹：入参出现 `O:N:`（PHP）、`rO0AB`（Java base64）、`ACED`（Java hex）、Python pickle 前缀。
- **默认探测不执行**：发结构合法的良性对象（如改个字符串字段），看应用是否解析回显——这是"可反序列化"证据。
- 执行 gadget（ysoserial/phpggc/pickle RCE）= 高危动作，仅 RoE 允许时；默认以可解析 + 框架指纹定 candidate，交 verifier 复核。
