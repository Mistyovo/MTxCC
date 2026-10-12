---
name: graphql-api
description: GraphQL 端点安全测试
triggers: ["GraphQL", "introspection", "gql"]
mitre: ["T1190"]
---

# GraphQL

1. 发现：/graphql /gql /v1/graphql；introspection 开启→全 schema 收割（insert_assets 每个 mutation）。
2. **GATE A**：introspection 可用性 → 未授权 mutation 探测（无害 mutation 如 updateProfile 无参数）→ 越权字段读取对照（用户 A token 读 B 资源）。
3. 批量查询 DoS 属性（nested query 深度）记 candidate 不实测打满。
4. IDOR 按 idor-matrix 复用。
