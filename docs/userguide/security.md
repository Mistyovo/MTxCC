# 安全白皮书

## 授权边界（RoE 双层执行）

| 层 | 机制 | 性质 |
|----|------|------|
| 解析层 | 命令目标提取 + CIDR/域名/TLD/时间窗判定 | 快速失败（可被绕过） |
| 出口层 | nftables 连接白名单 + DNS 白名单 | 权威封堵 |

## 审批四层

1. 灾难正则（rm -rf / fork 炸弹/mkfs/dd 写盘）
2. intercept_rules（allow/deny/ask）
3. RoE 解析层
4. 人工 ask 队列（超时默认拒）

## 沙箱隔离

- per-task 容器：cap_drop ALL + 白名单能力、pids_limit 2048、只读 rootfs
- 禁挂 docker.sock 到沙箱
- 独立网络命名空间（nftables 围栏注入点）

## 提示注入防御

- 工具输出 untrusted 围栏（随机 nonce 包裹）
- deny 消息带平台前缀（防 agent 当 WAF 绕过）
- 三层 prompt 叠加（全局基线>角色>微档案）

## 凭据保护

- AES-GCM 加密存储
- LLM 上下文只见 ${CRED:<label>} 占位符
- 发送还原 / 回灌掩码 双向网关

## 审计

- 全写操作 audit_log 只增
- 每步工具调用 activity 表留痕（token 四列）
- 流量全量 MITM 记录 + FTS 检索
