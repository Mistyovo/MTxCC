---
name: web-entry-recon
description: Web 入口侦察（首轮意图标准动作）
triggers: ["Web 入口", "指纹", "首页", "侦察"]
---

# Web 入口侦察（意图模板）

首轮意图对每个 Web 资产依次：
1. 指纹：响应头、favicon hash、登录/注册/找回入口、技术栈（`/actuator /admin /api` 探测用受控字典）。
2. JS 收集：html 里的 script src 全量记 asset（endpoint 类型）；JS 内 API 路径与密钥样字符串 record_fact。
3. 目录受控字典（common.txt 级别，限速率）——**禁全字典轰炸**。
4. 产出：insert_assets（全部入口）+ record_fact（指纹与高价值面清单）。
