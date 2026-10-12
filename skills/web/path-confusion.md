---
name: path-confusion
description: 路径混淆与规范化差异
triggers: ["路径穿越", "规范化", "path confusion"]
mitre: ["T1190"]
---

# 路径混淆

GATE：`/admin` vs `/admin/` vs `/./admin` vs `/admin.json` vs `/%61dmin` vs `/ADMIN` ——每形态探测鉴权差异；反代与应用解析不一致（`/admin;.css`）单独记录。
