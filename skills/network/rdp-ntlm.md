---
name: rdp-ntlm
description: RDP NTLM 中继与加密缺陷
triggers: ["RDP", "3389", "NTLM 中继"]
mitre: ["T1557"]
---

# RDP

GATE：NLA 强制性对照（tls 0/1）→ NTLM 泄露面（受限：只测指纹不实中继）→ BlueKeep 类版本判断（candidate）。
