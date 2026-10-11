# 用户手册

## 任务生命周期

```
创建 → queued → running（planner 派意图 → worker 执行）→ done/failed/timeout
```

- **planner**：唯一意图生成者；图变更唤醒；串行利用链分步派发
- **worker**（默认 3 并发）：CAS 领取意图；写回 fact/asset/finding 后停止
- **expert**：深度 1 专家委托（HTTP 单点 / PoC 编写）
- **verifier**：高危发现反证复测

## RoE（交战规则）

任务创建时填写（JSON 或表单模式）：

| 字段 | 说明 |
|------|------|
| in_scope | 授权范围（CIDR/通配域名） |
| out_of_scope | 排除项（优先于 in_scope） |
| forbidden_tlds | 禁用 TLD（默认 gov/mil） |
| forbidden_command_patterns | 禁令命令正则 |
| max_concurrent_connections | 并发连接上限 |
| min_inter_request_delay_ms | 请求间隔 |

同一规则在两层生效：解析层快速失败 + nftables/DNS 出口围栏。

## 技能库

23 个内置技能覆盖 Web/网络/AD/提权/报告，frontmatter 触发词命中时 SKILL-FIRST 规则要求先 load_skill 加载。

## 审批流

- ask 规则命中 → 待审队列 → admin/auditor 决策（allow/deny/modify）
- 超时 60s 默认拒绝
- deny 消息带【MTX 平台管控】前缀防绕过

## 审计链

所有写操作（建任务/决策/用户变更/报告导出）→ audit_log 只增记录。

## 凭据保护

- 所有凭据 AES-GCM 加密存储
- LLM 上下文只见 `${CRED:<label>}` 占位符
- 发送瞬间还原，输出回灌前重新掩码
