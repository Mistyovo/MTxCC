# MTxCC — AI 自主渗透测试系统

> **仅限授权渗透测试场景使用。** 使用前必须获得目标系统书面授权；未经授权的扫描与测试违法。

多 agent 编排引擎在受控沙箱内自主完成「侦察 → 枚举 → 利用 → 验证 → 报告」闭环；Web 界面管理任务/发现/资产；RoE 交战规则双层强制（解析层 + 网络出口围栏）；全流量留痕可审计。

## 快速开始

```bash
curl -fsSL https://raw.githubusercontent.com/mistyovo/mtx/main/deploy/install.sh | bash
# 或：git clone 后 ./deploy/install.sh
```

安装脚本自动完成：环境检测（docker / nftables）→ 生成主密钥 → 拉起 compose → 打印访问地址。
浏览器打开后两步完成初始化：

1. `/setup` 设置 admin 密码
2. 「LLM 配置」页添加模型（Anthropic / OpenAI / 兼容网关 / Ollama 本地），点「测试连接」

然后「任务」页输入目标（如 `http://10.0.0.5`）即可开始。想先试？`docker compose --profile demo up -d` 附带 DVWA 靶机（127.0.0.1:4280，默认账号 admin/password）。

## 架构

```
Next.js 前端（内嵌）
        │ REST + SSE
Go 单二进制 ── PostgreSQL（双图：探索图 × 资产图）
  ├─ harness   agent 循环（流式/并发工具/三层压缩/Settlement 收尾/transcript 断点恢复）
  ├─ engine    planner/worker 双循环（CAS 领意图/TriggerEvent 唤醒/steer·kill/失败三模态/死亡账本）
  ├─ roe+guard 审批四层（灾难正则→拦截规则→RoE 解析层→人工 ask 超时默认拒）
  ├─ sandbox   per-task 容器（cap-drop ALL/只读 rootfs/pids 限制）+ nftables/DNS 出口围栏
  ├─ traffic   MITM 记录代理 + SQLite FTS 全文检索
  └─ llm       三协议裸 HTTP + 按序故障转移池（熔断冷却）
```

## RoE（交战规则）

任务创建时填写授权范围（表单或 JSON）：

```json
{
  "mode": "enforce",
  "in_scope": ["10.0.0.0/8", "*.example.com"],
  "out_of_scope": ["10.66.0.0/16"],
  "forbidden_tlds": ["gov", "mil"],
  "forbidden_command_patterns": ["hydra.*rockyou"],
  "max_concurrent_connections": 8,
  "min_inter_request_delay_ms": 200
}
```

同一份规则在两个执行点生效：命令解析层快速失败（可读拒绝理由），nftables/DNS 出口围栏权威封堵（解析层被绕也出不去）。

## 开发

```bash
make build     # 构建单二进制（前端产物需先 make web）
make test      # 全部单元测试
make lint      # vet + 800 行单文件红线
# 集成测试（需要 docker + tmux）：
go test ./internal/store/ -tags=docker
go test ./internal/tools/tmux/ -tags=tmux
```

设计文档 `docs/superpowers/specs/`，实施计划 `docs/superpowers/plans/`。

## 路线

- [x] M0 内核：引擎/沙箱/RoE/审批/留痕/开箱即用（v0.1.0）
- [x] M1 实战：专家委托、技能库（23 个）、机器 oracle 验证、报告生成、MCP、凭据 vault（v0.2.0）
- [x] M2 产品：多用户 RBAC 四角色、审计链、模板、完整文档（v0.3.0）
- [x] M3 成绩：基准 adapter + mtx bench 一键跑分（v0.4.0）
- [x] v0.5 实证：DVWA 双漏洞打穿 15 findings（v0.5.0-validated）
- [x] v0.6 强化（实测差距修复）：发现去重+oracle 卡门禁 / 成本记账 /
  沙箱默认+端口级 RoE / Jaccard 反僵化 / 复测闭环 / steer 工具面 /
  mainagent 对话操舵 / 技能库 40 个（GATE 硬门）/ 8 个线上 bug 修复

## 基准跑分

```bash
# 本地靶场（demo profile 含 DVWA）
docker compose --profile demo up -d
mtx bench --adapter dvwa --api http://127.0.0.1:8080 --token <jwt> --model <模型名>

# XBOW 平台
export MTX_XBOW_TOKEN=<token>
mtx bench --adapter xbow --token <jwt>
```

支持任意 OpenAI 兼容端点（含本地 llama.cpp/Ollama）；弱模型自动启用文本工具协议 fallback。

## 免责声明

本工具仅供授权渗透测试与安全研究。使用者须确保已获得目标系统书面授权，并对使用行为承担全部法律责任。
