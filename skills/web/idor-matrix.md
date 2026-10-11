---
name: idor-matrix
description: 越权（IDOR/BAC）矩阵测试
triggers: ["越权", "IDOR", "水平权限", "垂直权限"]
mitre: ["T1550"]
---

# 越权矩阵

## 水平（同角色跨用户）
1. 两个测试账号 A/B；枚举 A 可访问的对象 ID（订单/文件/消息）。
2. 用 B 的会话重放 A 的对象请求（保持方法与头一致，仅换会话）。
3. 判定 oracle：状态码 + 资源归属字段，不是"页面 200"（要看返回的是不是 A 的数据）。

## 垂直（普通用户 → 管理接口）
1. 收集管理端路由（前端 JS/接口清单），普通会话逐个重放。
2. 403 ≠ 安全：换方法（GET→POST）、换 Content-Type、路径大小写/尾点/编码绕过对照。

矩阵扫完一次性 record_fact；命中即 report_finding（confidence=runtime_observed，evidence 含两次请求对照）。
