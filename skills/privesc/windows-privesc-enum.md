---
name: windows-privesc-enum
description: Windows 提权枚举（受控清单）
triggers: ["Windows 提权", "service", "alwaysinstall"]
mitre: ["T1068", "T1548"]
---

# Windows 提权枚举

单批收集：whoami /all（特权组）；unquoted service paths；可写服务目录；AlwaysInstallElevated 注册表键；计划任务；本地保存凭据（cmdkey /list 与 DPAPI 目录**仅列名不读内容**）；最近补丁（默认不弹 MS）。

产出同 linux：整体 record_fact，命中点转独立意图。
