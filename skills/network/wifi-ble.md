---
name: wifi-ble
description: 无线与 BLE 安全评估（受限）
triggers: ["WiFi", "WPA", "蓝牙", "BLE"]
mitre: ["T1557"]
---

# 无线（严格受限）

1. 枚举：信道/BSSID/加密类型（被动嗅探）。
2. **GATE**：WPA 握手捕获需 RoE 显式允许（避免影响合法用户）；PMKID/字典攻击默认禁止。
3. BLE：广播包嗅探、已知弱配对（Just Works）。
4. 结果 record_fact；实际破解仅授权场景。
