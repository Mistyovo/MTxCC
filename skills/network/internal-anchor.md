---
name: internal-anchor
description: 内网横向锚点识别
triggers: ["内网", "横向", "网段", "路由"]
mitre: ["T1018"]
---

# 内网横向锚点

立足点建立后，先回答"我在哪、能到哪"：
1. 网卡/路由表/hosts（多宿主=网段跳板）。
2. 本网段存活快扫（arp ping 级，禁高并发 SYN 全扫）。
3. 高价值锚点优先级：域控 > 文件共享 > 跳板机 > 数据库 > 其他。
4. 产出 anchors：新网段 insert_assets + record_fact 可达性矩阵（能到/被防火墙拒）。
