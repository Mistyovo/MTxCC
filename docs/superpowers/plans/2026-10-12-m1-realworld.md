# MTxCC M1 实战期 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** M0 内核之上补齐实战能力：专家委托、技能库 v1（20 个）、机器 oracle 验证 + Verifier 反证、报告生成、MCP 接入、凭据 vault、流量绑定发现。

**Architecture:** 沿用 M0 分层（spec §2.2）。新组件：verify 包（oracle 执行器）、skills 加载器、expert 委托工具、vault 网关；全部以接线到 server.launchEngine 为完成标准（吸取 M0 审查教训：每个任务必须有装配级验收）。

**Tech Stack:** 同 M0（Go 1.26 / pgx / testcontainers）；无新外部依赖。

**Spec:** `docs/superpowers/specs/2026-10-12-mtxcc-design.md` §5.1（expert/verifier/reporter 角色）、§6.3（MCP）、§7.5（vault）、§8（验证引擎）、§9（技能库）。

## Global Constraints

- 同 M0（依赖单向 / 800 行红线 / 迁移幂等 / GPL 禁抄 / 提交含 Co-Authored-By）。
- 每个任务以「装配级测试绿」为完成标准（assembly-test-first，M0 审查教训）。

## Review Focus

1. expert 委托深度必须 ≤1（防递归爆炸）——测试钉 Task 1。
2. oracle verdict 只有 verified/candidate 两态，永不 refuted——测试钉 Task 4。
3. 凭据占位符永不出现在 LLM 上下文与 transcript——测试钉 Task 8。
4. skill 加载后 worker 行为受技能内容影响（注入可见）——测试钉 Task 2。
5. verifier confirmed 无 hasRetest 必须拒——测试钉 Task 5。

---

### Task 1: expert 委托（深度 1 小循环）
Create: `internal/agents/tools_expert.go`；Test: `internal/agents/tools_expert_test.go`（docker）。
Produces: `ExpertTools(parent agents.CommonDeps, ec ExecContext) []tools.Tool`——expert_write_poc（写 PoC 脚本到工作区并自检语法）、expert_http（构造/发送单条 HTTP 请求并返回响应摘要）；内部各自建小 harness 会话（MaxTurns≤12，工具面= bash+read+write），经 `ec.Role="expert"` 标记；**深度门**：ExecContext 加 `Depth int`，ExpertTools 在 Depth>0 时返回空切片。装配：AssembleWorker 注册（Depth=1 传子）。
- [ ] 失败测试（expert 工具存在 + 深度门 + 假 LLM 小循环真跑通）→ 实现 → 绿 → commit `feat(agents): expert delegation with depth gate`

### Task 2: 技能库框架 + load_skill
Create: `internal/skills/loader.go`；`skills/` 目录；Test: 同包。
Produces: `Catalog(dir) []SkillMeta`（frontmatter：name/description/triggers/mitre/required_tools）、`Load(dir, name) (string, error)`；worker 工具 `load_skill`（内容注入轮内提示）；目录常驻 system（名称+描述+触发词）。SKILL-FIRST 规则进 WorkerPromptConst。
- [ ] 失败测试（frontmatter 解析/渐进披露/装配注入）→ 实现 → 绿 → commit `feat(skills): progressive-disclosure skill loader`

### Task 3: 技能内容 v1（20 个）
Create: `skills/web/*.md` 等 20 个文档（SQLi 登录页认证绕过矩阵、SSRF 云元数据、RCE 只读证明、JWT、IDOR、弱口令受控字典、SMB/SSH 枚举、AD 初始侦察、Linux/Win 提权枚举、内网锚点、payload 出网确认、报告撰写、复测规范等——内容取自 analysis 报告中沉淀的方法论）。
验收：Catalog 数=20 + 文件行数合理 → commit `feat(skills): 20 skill documents`

### Task 4: 机器 oracle（verification_recipe）
Create: `internal/verify/oracle.go` + schema `002_verify.sql`；Test: docker。
Produces: `Recipe{Kind: replay|differential, Steps []Req}`；`Oracle.Run(ctx, recipe) (Verdict, error)`——重放比对签名特征（状态码/正文包含/时延阈值）；`Verdict ∈ {verified, candidate}`（**永不 refuted**）；finding 提交时生成 recipe（record of request via traffic index 或工具参数）。装配：ReportFinding 成功后异步跑 oracle，回写 findings.verify_state。
- [ ] 失败测试（重放 verified/特征不匹配 candidate/无 refuted）→ 实现 → 绿 → commit `feat(verify): machine oracle with replay verdicts`

### Task 5: Verifier agent（反证）
Create: `internal/agents/verifier.go`；Test: docker（假 LLM 脚本 submit_verdict）。
Produces: `AssembleVerifier`（只读工具 + bash 复测 + `submit_verdict{confirmed, hasRetest, notes}` 单出口）；**hasRetest=false 时 confirmed 拒绝**；高危 finding（high/critical）触发独立验证会话，verdict 写回 findings.verify_state（confirmed/two_of_two）。
- [ ] 失败测试 → 实现 → 绿 → commit `feat(agents): verifier agent with retest gate`

### Task 6: reporter 报告生成
Create: `internal/report/render.go` + server 端点 `GET /api/tasks/{id}/report`；Test: docker。
Produces: Markdown 报告（executive summary/范围/发现分级表/每发现证据链（activity+traffic 引用）/复现步骤/修复建议）；reporter agent 由 finding 触发（M1：任务终态时同步渲染一次，agent 化 M2）。
- [ ] 失败测试（含 verified/candidate 分级与证据引用）→ 实现 → 绿 → commit `feat(report): markdown report with evidence chains`

### Task 7: MCP 客户端
Create: `internal/mcp/client.go`（stdio JSON-RPC）+ 注册进 AssembleWorker（deferred：仅列名，`mcp__<srv>__<tool>` 执行时校验 schema）；Test: 假 stdio server（echo 工具）。
- [ ] 失败测试 → 实现 → 绿 → commit `feat(mcp): stdio client with deferred tools`

### Task 8: 凭据 vault（占位符网关）
Create: `internal/vault/vault.go`（AES-GCM，主密钥）+ bash 工具进出双向替换；Test: docker。
Produces: `Put/Get/List`；`${CRED:label}` 占位符——发送前本地还原、输出回灌前重新掩码；transcript/llm_records 落盘前过滤。工具 `cred_put/cred_list`（读值永不回 LLM）。
- [ ] 失败测试（占位符不出现在 transcript/输出再掩码）→ 实现 → 绿 → commit `feat(vault): credential placeholder gateway`

### Task 9: 流量绑定 finding
Create: server/graph 加 `finding_traffic`（迁移 003）+ report_finding 参数 traffic_refs（exchange id 列表）→ 快照正文入 evidence。
- [ ] 失败测试 → 实现 → 绿 → commit `feat(graph): finding traffic bindings`

### Task 10: M1 验收 + 发布
- [ ] 全套件绿（unit+docker+tmux）+ 装配级 e2e（假 LLM：expert 委托 + 技能加载 + oracle verified + 报告导出 + vault 掩码，一个综合测试）
- [ ] README 更新 M1 能力清单 → commit `feat: M1 acceptance` → tag `v0.2.0-m1`

## 执行说明
串行为主；Task 3（纯文档）可与 4-9 并行。真实靶场跑批（红日/DVWA + 真 LLM key）在用户配置密钥后执行，不阻塞 tag。
