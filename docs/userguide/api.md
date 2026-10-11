# API 参考

Base URL：`http://<host>:8080`；鉴权：`Authorization: Bearer <token>` 或 `?token=<token>`（SSE）

## 认证

| 方法 | 路径 | 角色 | 说明 |
|------|------|------|------|
| GET | /api/setup/status | - | 初始化状态 |
| POST | /api/setup | - | 首次设置 admin 密码 |
| POST | /api/auth/login | - | 登录获取 JWT |

## 任务

| 方法 | 路径 | 角色 | 说明 |
|------|------|------|------|
| GET | /api/tasks | viewer+ | 任务列表 |
| POST | /api/tasks | operator+ | 创建任务（target_text/timeout/roe） |
| POST | /api/tasks/{id}/control | operator+ | pause/resume/cancel |
| POST | /api/tasks/{id}/steer | operator+ | 在跑意图注入纠偏 |
| GET | /api/tasks/{id}/graph | viewer+ | 探索图 JSON |
| GET | /api/tasks/{id}/report | viewer+ | Markdown 报告 |

## 探索

| 方法 | 路径 | 角色 |
|------|------|------|
| GET | /api/exploration/{id}/activity | viewer+ |
| GET(SSE) | /api/exploration/{id}/activity/stream | viewer+ |

## LLM

| 方法 | 路径 | 角色 |
|------|------|------|
| GET | /api/llm/profiles | viewer+ |
| POST | /api/llm/profiles | admin |
| POST | /api/llm/profiles/{id}/test | viewer+ |

## 用户（M2）

| 方法 | 路径 | 角色 |
|------|------|------|
| GET | /api/users | admin/auditor |
| POST | /api/users | admin |

## 审计（M2）

| 方法 | 路径 | 角色 |
|------|------|------|
| GET | /api/audit | admin/auditor |
