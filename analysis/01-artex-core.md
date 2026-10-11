# ARTEX（Autumn-27/ARTEX）深度源码分析报告

> 分析对象：`/home/mistyovo/桌面/MTxCC/refer/03-artex-mirrors/mhtsec-ARTEX/`（原 Autumn-27/ARTEX 最终版纯源码备份）
> 分析方式：纯代码走读，只读不执行。文中代码引用均为相对该仓库根目录的 `file:line` 格式。

---

## 1. 一句话定位 + 项目背景

**ARTEX 是一套 LLM 多 Agent 驱动的自主渗透测试系统：Go 单体后端（内嵌 Next.js 静态前端）+ PostgreSQL，以"探索图 + 资产图"双图为核心数据结构，由事件驱动的 planner/worker 编排引擎自主完成 侦察 → 枚举 → 利用 → 验证 → 报告 的完整渗透闭环。**

- 定位为"网络安全平台授权渗透测试系统"（README 与全部 system prompt 均反复强调"授权范围内操作"），AGPL-3.0 协议，作者同时声明仅限学习研究。
- agent 能力由自研 SDK `norma`（同作者独立仓库，此处内嵌为 Go module）提供：`agentcore`（会话）/ `harness`（主循环）/ `tool`（工具合约）/ `permission` / `compaction` / `transcript` / `hook` / `skill` / `mcp` / `subagent` 等，形态上是一个"自建 Claude Code SDK"。
- 工程化程度极高：自带 MITM 流量留痕代理、人工审批门（含 LLM 兜底审批官）、LLM 多配置故障转移池、任务归档冷存储、IM 漏洞推送（钉钉/飞书/企微/TG/邮件/webhook）、自更新二进制等生产级子系统。
- 上游参考了 oritera/Cairn（README 末尾注明）。

---

## 2. 技术栈与代码规模

| 项 | 数据 |
| --- | --- |
| Go 文件 / 总行数 | 533 个 / 130,508 行（其中非测试 83,647 行，测试 46,861 行，测试占比 36%） |
| TS/TSX 文件 / 行数（web/） | 187 个 / 56,088 行 |
| HTTP API 路由 | 243 条（`server/server.go:655-933`） |
| 数据库 | PostgreSQL（pgx），schema 随 `go:embed` 每次启动幂等重放（`db/db.go:20` → `db/schema.sql`，1387 行） |
| 前端 | Next.js 15 静态导出（`next build:static`）→ `go:embed` 内嵌单二进制；shadcn/ui + Zustand + SSE |
| 外部依赖 | go-mitmproxy（流量代理）、modernc.org/sqlite（流量/证据侧车索引）、zstd（任务归档），LLM 侧零 SDK 依赖（裸 HTTP 实现 Anthropic/OpenAI/Responses 三协议） |

Go 各包非测试行数：`norma`（SDK）20,443；`server` 25,089；`db` 19,566；`agent` 7,437；`notify` 2,561；`traffic` 2,493；`intercept` 1,178；`mcphttp` 578；`llmrec` 550；`sidequestion` 758；`evidence` 419；`enrich` 292；`report` 299；`llmpool` 483；`guard` 197。

---

## 3. 整体架构

### 3.1 模块划分（分层）

```
cmd/artex/main.go          进程入口：flag、selfupdate bootstrap、Manager/Server 装配、信号与退出码
├─ server/                 net/http 路由 + JWT 鉴权 + SSE 广播 + Manager/Engine 生命周期
│   ├─ Manager             任务表、store 工厂、全局设置、并发上限、代理与流量开关
│   ├─ Engine              每任务 1 plannerLoop + N workerLoop + deadline 协调器
│   └─ assembly.go         skills/MCP/DB 工具目录 → agent 运行时的接线（ToolAugment/ToolResolve/PromptOverride）
├─ agent/                  planner / worker / mainagent / goals 四角色 + ToolSet（双图领域工具）+ prompt/wrapup/compaction
├─ norma/（SDK）           harness 主循环、agentcore 会话、llm 三协议 Provider、tool（Bash/文件/搜索/后台任务/MCP）、
│                          permission、compaction（Micro/Auto/Reactive）、transcript、skill、hook、noa（实验性模型驱动压缩）
├─ db/                     双图 + 任务 + LLM profile + 拦截规则 + 通知等全部表的 store 层（schema.sql 幂等迁移）
├─ guard/ + intercept/     PreToolUse 审批门：DB 规则匹配 → LLM 兜底审批官 → 人工 ask 队列
├─ traffic/                go-mitmproxy 记录型代理（全文落盘 + SQLite FTS5 trigram 索引）
├─ llmpool/ + llmrec/      Provider 故障转移池（熔断/轮询）与 LLM 调用录制归因
├─ enrich/                 资产异步补全（DNS 解析 / HTTP 探测）
├─ report/ + evidence/     Markdown 报告渲染；独立于流量的证据 blob 保存（sha256 内容寻址）
├─ skills/                 文件系统技能（api-recon / playwright-cli / scopesentry）
├─ sidequestion/           "旁路提问"：会话执行中并行问 LLM 而不打断主对话
└─ web/                    Next.js 前端（见 §3.3）
```

### 3.2 核心数据流（一次探索的闭环）

1. 用户在 UI 建任务（`POST /api/tasks` → `server/server.go:1462` createTask）→ `Manager.CreateTaskWithOptions` 落库（tasks + 独立 exploration 行）。
2. `launchTask`（`server/goals.go:28`）：`seed()` 从目标文本解析 host/IP 建根资产（`server/server.go:1667`）→ 可选 `seedFirstIntent` 直投一条种子意图（`server/server.go:1715`）→ `admitTask` 并发准入（FIFO 排队）。
3. `startTaskEngine`（`server/goals.go:42`）：发"第 0 轮目标拆解"活动 → `createGoals` 用任务同链 LLM 跑目标分解器（`agent/goals.go:110`，同时抽操作约束 set_constraints + 登记范围 add_task_scope）→ `engine.Run`。
4. `Engine.Run`（`server/engine.go:748`）起 1 个 plannerLoop + N 个 workerLoop（默认 3）+ deadline 协调器。
5. plannerLoop 被图变更/心跳唤醒（debounce 800ms）→ `planner.Plan`（`agent/planner.go:355`）：预取 graph_overview 态势拼进 user 输入 → LLM 决策 → `add_intent` 批量派 0..4 条意图入 frontier / `prove_goal` 判目标。
6. workerLoop `claimNext`（CAS `db/exploration.go:1173` ClaimIntent：`UPDATE ... WHERE state='open'`，天然防双领）→ `worker.Execute`（`agent/worker.go:349`）→ agentcore/harness 主循环调工具（Bash 走记录代理、写回 insert_assets/record_fact/report_finding）。
7. worker 结束 → `t.NotifyDone` 唤醒 planner（携带刚完成的 intent id 作为 TriggerEvent）→ 回到 5，直到 `prove_goal` 标完最后一个目标 → Engine 置任务 done。

### 3.3 前端结构与 API 对接

前端为 Next.js App Router 静态导出，三个路由组：`(auth)`（login/setup）、`(external)`（落地页）、`(main)`（业务）。页面清单（`web/src/app/(main)/`）：

- `dashboard`（仪表盘：总览/Token 消耗/活动流）、`chat`（通用对话页，Auto/pentest/自定义 agent 的入口）
- `function/tasks` + `tasks/detail`（任务详情：会话过程/探索图/复测/拦截）、`function/assets`（资产 + 力导向覆盖图）、`function/findings`（+detail）、`function/traffic`（流量录制回放）、`function/commands`（命令统计）、`function/llm-records`（LLM 调用账单）、`function/workspace`（工作区文件管理）、`function/sync`（ScopeSentry 资产同步）
- `system/agents`（+detail：提示词/工具/LLM/预算/触发器编辑）、`system/llm`（多 LLM profile + 轮询池）、`system/mcp`、`system/skills`、`system/tools`、`system/intercept`（+approvals/assets：审批队列与资产黑名单）、`system/notify`、`system/logs`、`system/settings`

对接方式：统一 fetch 封装 `web/src/lib/api.ts`，实时流两条 SSE（活动流 `/api/exploration/activity/stream`、日志 `/api/logs/stream`），活动记录合并逻辑在 `web/src/lib/activity-merge.ts`（把 tool_use/tool_result 按 tool_use_id 配对成卡片）。支持 `NEXT_PUBLIC_MOCK=1` 纯前端演示模式。

API 路由（Go 1.22 `method pattern` 风格，共 243 条，`server/server.go:655-933`）按前缀归组：

| 前缀 | 职责（代表路由） |
| --- | --- |
| `/api/auth`、`/api/health`、`/api/logs*` | JWT 初始化/登录/改密、健康、内存+DB 日志流 |
| `/api/tasks*`、`/api/task-categories`、`/api/task-templates`、`/api/task-archives*` | 任务 CRUD/控制(启停/队列)/目标/约束/意图级控制(暂停/取消/重跑/人工消息)/覆盖图/归档还原 |
| `/api/exploration/*` | frontier/findings(分组/血缘/导出/复测)/intents/graph/nodes/activity(SSE+分页)/tokens |
| `/api/assets`、`/api/companies*` | 资产与公司 scope CRUD |
| `/api/llm*`、`/api/llm/profiles*`、`/api/llm/pool*`、`/api/llm/records*` | 全局 LLM 配置、profile 链、轮询池熔断状态、调用记录与 token 聚合 |
| `/api/agents*`、`/api/tools*`、`/api/mcp*`、`/api/skills*`、`/api/visibility/*` | 内置/自定义 agent 编辑、工具目录（DB 覆盖描述/schema/绑定）、MCP 服务器、技能文件管理、可见性矩阵 |
| `/api/intercept/*`、`/api/asset-intercept/*` | 命令级拦截规则/审批队列/LLM 审批官配置；资产级黑白名单 |
| `/api/traffic*`、`/api/report`、`/api/chat*`、`/api/conversations*` | 流量查询(blob/交换)、报告导出、人在环路对话（@提及资产/意图/任务）、独立会话 |
| `/api/notify/*`、`/api/sync/scopesentry*`、`/api/update*`、`/api/workspace/*` | IM 推送渠道、资产同步、自更新、工作区文件 |

---

## 4. Agent / 编排设计

### 4.1 多 Agent 拓扑：1 planner + N worker + 1 mainagent + 一次性 goals 分解器（+ 可选 Auto/pentest/reporter/retester 自定义 agent）

| 角色 | 文件 | 职责 | 权限模式 |
| --- | --- | --- | --- |
| goals 分解器 | `agent/goals.go` | 任务创建时把自由文本目标拆成 goal 节点，抽操作约束、登记资产范围（MaxTurns=8 一次性） | `permission.ModeBypass` |
| planner（规划者） | `agent/planner.go:23` | **唯一意图生成者**。事件驱动唤醒，读态势→判目标→只在有未覆盖新方向时派意图 | ModeBypass |
| worker（执行者）×N | `agent/worker.go:54` | 领**一条**意图，用真实工具执行，写回 fact/asset/finding 后即停。不产新方向 | ModeBypass（guard 钩子兜底） |
| mainagent（主 agent） | `agent/mainagent.go:20` | 人在环路接口：观察 + `add_hint`/`add_intent`/`set_goals`/`set_constraints`/`steer_work` 操舵，不自主探索 | ModeBypass |
| Auto / pentest / reporter / retester | `agent/promptcatalog.go` | 平台操作助手 / 单 agent 全流程渗透 / 漏洞报告撰写（由 report_finding 触发）/ 漏洞复测 | 经会话页 |

关键点：**planner 与 worker 通过 PostgreSQL 的探索图解耦**——planner 只写 intent 节点，worker 只 CAS 领取，互相不见面对象。

### 4.2 主循环在哪

两层的"主循环"：

1. **引擎层调度循环**（artex 自己的）：`server/engine.go:800` plannerLoop（select on `t.notify` / heartbeat timer，debounce 800ms 合并风暴）与 `server/engine.go:916` workerLoop（自旋 claim）。
2. **LLM Agent 循环**（norma SDK 的）：`norma/harness/query.go:366` `loop.run()` —— 标准 "while { 调模型 → 流式解析 → 并发执行 tool_use → 结果回填 → 下一轮 }"，终态由 `TerminalReason` 枚举（`norma/harness/` streaming.go/query.go）：`ReasonCompleted / ReasonMaxTurns / ReasonTimeout / ReasonModelError / ReasonPromptTooLong / ReasonStopHookPrevented / ReasonAbortedStreaming / ReasonAbortedTools`。

### 4.3 如何决策下一步

- planner：每轮把 **graph_overview 态势 JSON 预取后直接拼进 user 消息**（`agent/planner.go:382` situational，注释明言"省一次工具往返；system 保持静态利于缓存"），加上 **TriggerEvent 渲染**（本轮因何醒来：哪个意图完成/报了 finding/人加了目标，`agent/planner.go:173`）与 **跨唤醒共享 TodoList**（`agent/planner.go:129` renderPlannerTodos）。LLM 按提示词里的决策流程（`agent/planner.go:298` plannerDefaultTmpl）判断"已覆盖/等依赖/新方向"，用一次 `add_intent` 批量提交 ≤4 条。
- worker：领到的意图连同意图锚定资产的原始 JSON 一起**写进 system prompt**（`agent/worker.go:384-420`，注释解释了为何意图放 system 而态势放 user：意图是贯穿 run 的不变量、绝不能被 compaction 压掉），LLM 自主选择 Bash/WebFetch/流量工具等执行。

### 4.4 循环终止条件（多层预算）

| 层 | 机制 | 位置 |
| --- | --- | --- |
| 单次 run 步数 | `MaxTurns`（agent 表可配，0=不限）；轮边界检查 | `norma/harness/query.go:563` |
| 单次 run 墙钟 | `MaxDuration`（agents.run_seconds，默认 1200s）；工具超时中断后进收尾 | `norma/harness/query.go:384,532-544` |
| 预算命中后的**收尾（Settlement）** | 注入 wrap-up 提示词、隐藏 Bash 等 DisabledTools、单独 10 轮预算，让 agent 把已识别内容写回再结束（终态仍记 MaxTurns/Timeout 而非 Completed） | `norma/harness/query.go:594` beginSettlement；`agent/wrapup.go` |
| 任务级超时 | tasks.timeout_seconds → deadline 协调器 settleTask（收尾时序 settling：停派停领，硬兜底 cancel） | `server/engine.go` engine_timeout.go |
| 目标达成 | planner `prove_goal` 逐个标记，最后一个 met 自动判完成（`agent/tools.go:1186-1199`）→ Engine cancelExec 杀在跑 worker | `server/engine.go:885-894` |
| 空转回合 | steerHooks.Stop 检测"仅思考无正文无工具"的回合，注入续跑指令（每意图上限 2 次） | `server/engine.go:552-620` |
| model_error | worker 收场为 model_error 时额外重跑 2 次（退避 3s），quota 类错误除外 | `server/engine.go:71-84,1030-1046` |

意图终态机：`open → running → done/blocked/exhausted/stopped/paused/deleted`（`db/schema.sql:186`），其中 **exhausted（撞预算）≠ done** 是刻意区分——planner 提示词里明确要求对 exhausted 意图"看 trace 实际进度再决定续派/换打法"。

### 4.5 token / 轮数限制

- 每 profile：`context_window_k`（压缩阈值用，默认 200K，夹逼 [32K,1M]，`agent/provider.go:105-126`）、`max_tokens`（单回复输出上限，0=不发）、`max_tokens_field`（openai 格式选 `max_tokens` vs `max_completion_tokens`，兼容 o 系列推理模型）。
- 每任务：LLM profile 有序链（task_llm_profiles 表）+ quota 耗尽标记；并发 worker 数全局设置（默认 3）；planner 心跳间隔（默认 300s，下限 600s 见 `server/engine.go:781`）。
- 全局：llmpool 每秒/每分钟限流（`norma/llm/provider.go:88` RateLimit，全 agent 共享同一 provider 实例的限流器）。

### 4.6 三条编排控制原语（亮点）

- **steer_work**：planner/人给在跑 worker 的下一条工具调用前注入纠偏消息（阻塞该次调用，不 kill、不丢进展）——`server/engine.go:531-587` steerHooks.PreToolUse。
- **kill_work**：planner 止损单个跑偏 worker（per-work context cancel）——`server/engine.go:624`。
- **runDetachedIntent**：人工消息继续某条 paused 意图，在 worker 池外独立 goroutine 跑（不占池槽位）——`server/engine.go:1171`。

---

## 5. LLM 集成

### 5.1 支持的 Provider

三协议全为裸 HTTP 自实现（`norma/llm/`，无任何官方 SDK 依赖）：

| Format | 文件 | 说明 |
| --- | --- | --- |
| `anthropic` | `norma/llm/anthropic.go`（386 行） | Messages API，默认 model `claude-opus-4-8`（`agent/provider.go:185`） |
| `openai` | `norma/llm/openai.go`（578 行） | Chat Completions，含空响应重试、`max_completion_tokens` 切换 |
| `openai-responses` | `norma/llm/openai_responses.go`（372 行） | POST /v1/responses，默认 `gpt-5` |

统一事件流：`norma/llm/provider.go:18-27` 把各家流式增量归一为 `SETextDelta/SEThinkingDelta/SEToolUseStart/SEToolInputJSON/SEMessageDelta` 等 StreamEvent。思考参数双字段（thinking_type 开关 + reasoning_effort 强度）按格式映射（`norma/llm/provider.go:91-108`）。

**配置面**：DB 表 `llm_profiles`（schema.sql:362，含 base_url/proxy/限流/上下文窗口/流式开关/重试三组覆盖/session_header_key 粘性路由头），UI 管理，多 profile 组成**故障转移链**：

- `llmpool/pool.go:68` Stream 实现"只有未交付任何事件前才允许换 member"的硬规则（防重复输出污染对话），失败即熔断（`llm_profile_health` 表，指数冷却，半开试探）。
- 任务级可绑独立链（`task_llm_profiles`，侦察用便宜模型/利用用强模型的场景），链耗尽时 `SetAuthoritativeAgentResolver` 保证不静默回落全局配置（`server/engine.go:713`）。

### 5.2 调用封装位置

`agentcore.Options.Provider`（`norma/agentcore/agentcore.go:29`）→ `harness.QueryDeps.CallModel/CallModelSync`（`norma/harness/query.go:177`）→ Provider.Stream/Complete。artex 侧装配在 `server/task_llm.go`（按任务链解析 runtime）与 `agent/provider.go`（Config→llm.Config→NewProvider，再包 llmrec 录制器）。

### 5.3 System Prompt 原文摘录

**planner**（`agent/planner.go:298`，可 DB 覆盖编辑）：

> 你是一个网络安全平台授权渗透测试系统的"规划者"，被频繁唤醒（图一变就唤醒）。职责：读态势 → 判目标 → **只在确有未被覆盖的新方向时**补充探索意图。你是规划者、不是执行者：本轮所有产物只能是【生成/说清意图】或【判定目标】，绝不在 plan 里把活干了。
>
> **硬底线（最高优先）**：只要【目标未达成】且【当前没有任何 open 或 running 意图】……本轮就【必须】产出至少一个向目标推进的意图……产出 0 意图=任务停摆……
>
> **深度优先于覆盖度**：coverage 是下限/验收项、不是探索目标本身；发现高价值入口（可能通向 RCE/提权/数据外泄）后，优先派意图把那条路【往深打穿】……
>
> **串行利用链：分步派，别拆成并行。** 强依赖串行链（①→②→③……）不要一次性并行下发……用 TodoWrite 把整条链记成待办……本轮只派"前置已满足"的那步……

**worker**（`agent/worker.go:230`）：

> 你是一个网络安全平台授权渗透测试系统的"执行者"(work agent)。你领到【一条意图】(一句话探索方向)，唯一职责：**完成这一条意图、把发现写回知识图谱、然后停止返回。**
>
> **边界（红线）**：1. 只做你领到的这一条意图……探本意图时若瞥见本意图之外值得深挖的线索……在 fact 的 summary 里点一句交给规划者。2. 初次受阻（payload 被过滤 / 404 / 注入无回显）不代表已探透——把本意图的所有绕过手段走完再输出结论；3. 只在授权范围内操作……
>
> **边发现边写回**（写进图才算数……）：新资产/资源 → insert_assets；探索结论/事实 → record_fact（多个观察汇总成【一条】事实……只写增量……只写真实看到的：给 evidence、标 confidence observed/inferred）；确认漏洞 → report_finding（**只有你本次真实触发过、拿到可复现证据才用**，严禁把"版本/指纹匹配到 CVE""参数看起来可注入"当已确认）。

**mainagent**（`agent/mainagent.go:87`）：人不亲探、观察 + 操舵（add_hint/add_intent/steer_work/set_goals/set_constraints）。

**goals 分解器**（`agent/goals.go:19`）：先抽操作约束（deny/allow，指代词必须替换成具体值）再拆目标；"目标=最终可交付结果，信息收集/攻击步骤不是目标"。

**pentest 独立 agent**（`agent/promptcatalog.go:31`）：六条心法（先广后聚/走透再下结论/封锁不无理由重试/对抗式自检防自欺/具体结论/不轻言放弃）——单 agent 场景的提示词工程非常完整。

全部内置 prompt 开机幂等播种进 `agent_prompts` 表（`server/assembly.go:194` seedPrompts），DB 可编辑、代码常量兜底；渲染用 Go template + 变量白名单（`agent/prompt.go:38` renderSystem，missingkey=error，渲染失败逐级回退）。traffic 工具说明、中间产物输出规约（禁写 /tmp）等**关键段落代码固定追加**，DB 编辑不可去除（`agent/worker.go:250-269`）。

### 5.4 上下文管理 / 历史压缩（四套机制并存）

1. **SDK 内置 compaction**（`norma/compaction/compaction.go`）：对齐 Claude Code 三层——MicroCompact（清旧工具结果体，保留最近 5 条）、AutoCompact（token 阈值处摘要 + 边界标记，**非破坏性**：全历史保留在内存、只发边界后窗口 `llm.MessagesForAPI`）、Reactive（413 prompt-too-long 强制压缩一次重试）。阈值数学：window − min(maxOut,20K) 摘要预留 − 分层 buffer（13K/30K/50K）。
2. **探索图冷节点压缩（cold-digest）**（`agent/compaction.go` + `agent/coldgraph.go` + `db/digest.go`）：planner 每轮 maintain（盖 cold_since_round 戳），冷节点 ≥20 触发 minor（增量折叠成 digest 节点）、活跃 digest ≥8 触发 major（重分组）；后台 goroutine 跑、LLM 生成摘要体、commit 时复查活跃性。**这是把"对话历史压缩"思想搬到"知识图谱"上**——planner 上下文里的 recent_facts/recent_done 被折叠为 digest，需要时 `expand_digest` 还原。
3. **transcript 持久化 + Resume**（`norma/transcript/transcript.go`）：JSONL 追加式原始对话；worker 意图被暂停/重跑时 `s.Resume(sessionID)` 从断点继续（`agent/worker.go:507`），恢复时清掉尾部悬空 tool_use（`norma/agentcore/agentcore.go:383`）。
4. **noa 实验压缩**（`norma/noa/` + `agent/noa.go`）：模型驱动的上下文接管（Compactor 实现 ContextView 接口自建请求投影），系统设置开关，归档持久化到 `<workDir>/noa/`。

另外 harness 层还有：max_tokens 截断的 escalate(64K)→resume-recovery(×3) 流程（`norma/harness/query.go:632`）、TokenBudget 预算续跑（90% 阈值 + 边际递减检测）、Todo 系统提醒节流（10/10 轮，对齐 Claude Code）。

---

## 6. 工具层

### 6.1 工具合约与内置工具

CoreTool 接口自描述（`norma/tool/tool.go:1-60`）：Name/Description/Prompt/InputSchema + 行为标志（ReadOnly/Concurrent）+ 权限自检 CheckPermissions + Call；`Build(Spec)` 应用 fail-closed 默认。SDK 默认工具集 `DefaultTools()`：Read/Write/Edit/MultiEdit/LS/Glob/Grep/**Bash**/Sleep（文件实现 `norma/tool/files.go`、ripgrep `norma/tool/ripgrep.go`）。可选注入：WebFetch（代理+CA）、web_search（ddgs/brave/tavily/deepseek 四后端，`norma/tool/websearch.go`）、TodoWrite、AskUserQuestion、后台任务四件套（TaskOutput/TaskStop/TaskList/Monitor）、交互式 shell 五件套（shell_open/send/close，`norma/tool/shell_session.go`，按 agent 的 interactive_shell 开关注入）。

**worker 刻意裁剪**：不给 MultiEdit/Glob/Grep（`agent/worker.go:379`——收敛工具面，检索走 Bash grep/find）。

### 6.2 领域工具目录（ToolSet，`agent/tools.go` 2054 行 + `agent/tools_insert.go`）

按 agent 分组的领域工具（全部围绕双图）：

- **planner**（`agent/tools.go:2033` PlannerTools）：graph_overview / list_findings / list_facts / node_detail / expand_digest / get_worker_output / get_worker_trace / search_all_worker_traces / list_goals / **add_intent**（批量）/ **prove_goal** / goal_met（默认不绑任何 agent）/ kill_work / steer_work / report_finding / list_companies / list_assets / add_company_scope / add_task_scope / list_untested_assets
- **worker**（`agent/tools_insert.go:644` WorkerTools）：list_findings / **report_finding** / **record_fact** / **insert_assets** / list_assets / search_all_worker_traces / get_worker_trace / node_detail（刻意不给 list_facts/list_companies——"跨 work 复盘是规划职责"）
- **mainagent**（`agent/tools_insert.go:664`）：读工具全集 + add_hint / add_intent / steer_work / set_goals / set_constraints / 资产工具
- **Auto**（编排，`server/orchestration.go`）：list_tasks / **spawn_task**（起子任务可指定 LLM）/ get_task_graph / list_task_findings / get_task_worker_trace / pause_task / add_task_hint / create_skill / create_custom_tool / create_mcp 等平台操作工具

**工具目录化（toolcatalog.go）**：内置工具开机播种进 `tools` 表（key 死绑 handler、DB 只覆盖 description/schema/agent 绑定/enabled），运行时 `ToolResolve` 装饰器（`agent/toolcatalog.go:113` DecorateTool）注入 DB 覆盖的描述与**参数缺省值**；另有 kind='shell' 的"环境声明工具"（不可调用，仅把已安装命令行工具追加到 Bash 描述里，`server/assembly.go:294-307`）。

### 6.3 Shell 命令构造 / 执行 / 解析

- Bash 工具（`norma/tool/bash.go:33`）：`command/timeout_ms(默认120s,上限600s)/run_in_background`；经任务管理器执行，超时可自动转后台（sleep 禁止转后台）；`shellCmd()` 跨平台选 shell。
- **渗透工具即 Kali 命令行**：没有内置"nmap 工具/sqlmap 工具"这样的封装——nmap/curl/sqlmap 等直接由 LLM 写 Bash 命令调用（Docker 镜像预装 ripgrep/curl/nmap/playwright/node 等）。工具知识通过两条通道供给：① shell hints（环境声明工具）；② skills（如 api-recon 技能教 JS bundle 静态收割 + Puppeteer 运行时拦截的完整方法论）。
- **输出解析**：大输出经 `tool.Capture` 截断（30000 字符头）+ 溢出落盘 `cmd-output/` 返回 head+指针（`agent/worker.go:463-465`）；Bash 合并 stdout/stderr，exit code 附在错误结果里。
- **代理环境注入**（`agent/worker.go:204-224` proxyEnv）：Bash 子进程注入 HTTP_PROXY/HTTPS_PROXY/ALL_PROXY 指向记录代理 + 六种生态各自的 CA 信任变量（SSL_CERT_FILE/REQUESTS_CA_BUNDLE/CURL_CA_BUNDLE/GIT_SSL_CAINFO/NODE_EXTRA_CA_CERTS/NODE_USE_ENV_PROXY）——**工具零配置走 MITM 留痕且 HTTPS 正常校验**，这是渗透工具链适配的精华。

### 6.4 MCP 支持

完整：`norma/mcp/mcp.go`（stdio JSON-RPC 2.0 + 进程内 server，工具名 `mcp__<server>__<tool>`）+ `mcphttp/client.go`（Streamable HTTP 与 legacy SSE 双传输，2025-06-18 / 2024-11-05 协议版本）。DB 表 `mcp_servers`（transport∈stdio/http/sse）+ `mcp_tools_cache`（发现缓存）。**Deferred tools 机制**（`norma/agentcore/agentcore.go:319-326`）：MCP 工具 schema 不进 prompt，仅列名（`SearchExtraTools` 发现 / `ExecuteExtraTool` 执行），skill 门控的 MCP 在技能加载时解锁（`server/assembly.go:113-139` unlock set + 恢复会话时从历史重放解锁 C2）。

---

## 7. 渗透方法论建模

### 7.1 层级组织：任务 → 目标 → 意图 → 执行 → 写回

```
Task(tasks 表)
 └─ Exploration(explorations 表, 1:1)
     ├─ origin fact(begin 根)
     ├─ goal 节点 ×N（goals 分解器产出；state: open/met/abandoned）
     ├─ intent 节点 ×N（planner 派发；frontier 按 priority DESC FIFO 领取）
     │   └─ worker 领取执行 → activity 过程记录(tool_use/tool_result/text/thinking/result/usage)
     │        ├─ yields → fact 节点（探索结论，含否定结论，confidence=observed/inferred）
     │        ├─ yields → finding 节点（确认漏洞，双写独立 findings 表）
     │        └─ anchor → assets（资产图，全局共享）
     ├─ hint 节点（人/mainagent 的战略提示）
     └─ digest 节点（冷节点压缩摘要，covers 成员）
边：spawns / derived_from / yields / proves / covers（exploration_edges）
```

阶段驱动**不是预定义 pipeline**，而是"意图即开放方向"的涌现式推进：recon/enum/exploit 没有硬编码阶段机，而是由 planner 提示词的启发（"深度优先于覆盖度""保持 2-3 条机理不同路线""串行利用链分步派"）+ 图上血缘（recent_facts→derived_from→新 intent）自然形成侦察→利用的推进。配套结构化机制：

- **TriggerEvent**（`agent/planner.go:158`）：每次唤醒告诉 planner 具体发生了什么（意图 #N 结束、输出结论摘要、新 fact id 列表），避免重扫全图。
- **worker 过程级信息交换**：`search_all_worker_traces(q)` 跨意图全文检索其他 worker 的执行过程（自动排除自己），`get_worker_trace(intent_id, step_ids)` 取完整步骤——**信息以"执行过程"粒度在 worker 间流动**，无需等 fact 落图。
- **覆盖度**：task_scope（分母：授权范围内资产）vs 被 fact 锚定的资产（分子），`list_untested_assets` 供 planner 自主补测。
- **复测闭环**：finding_retests 表 + retester agent（结论 reproduced/fixed/inconclusive，"已修复"自动回写漏洞处置状态）。
- **报告**：reporter agent 由 report_finding 触发，自动取全证据 + worker trace + 流量绑定写结构化 Markdown 存回 findings.report（`agent/promptcatalog.go:76`）；另有模板化 `report/report.go` Markdown 渲染。

### 7.2 planner 共享 TodoList（串行攻击链稳定推进的关键）

planner 每次唤醒是**全新会话**（无对话记忆），串行利用链（注入→拿凭据→横向→提权）靠 `Planner.todos map[int64]*actool.TodoStore`（按 exploration id 常驻内存，`agent/planner.go:47-49`）跨轮保留：整条链一次记入，每轮只派"前置已满足"的下一步，fact 满足后标 completed。

---

## 8. 状态与记忆

### 8.1 数据库 Schema（PostgreSQL，`db/schema.sql`）

核心表（全部幂等建表/迁移）：

| 分区 | 表 | 要点 |
| --- | --- | --- |
| 资产层 | `companies` / `assets` / `company_scope` | assets 按 type 部分唯一索引去重（root_domain 按 domain、http service 按 url、endpoint 按 url+method…）；task_ids GIN 数组；公司归属 explicit/scope 双来源；scope 支持 domain/ip/cidr/icp/keyword |
| 探索层 | `explorations` / `exploration_nodes` / `exploration_edges` / `exploration_anchors` / `task_constraints` / `activity` / `main_sessions` | 节点 kind 状态机 CHECK 约束（schema.sql:184）；边关系 CHECK（spawns/derived_from/yields/proves/covers）；anchors 连两图；activity 带 token 四列 + main_seg 会话分段 |
| LLM | `settings` / `llm_profiles` / `llm_profile_health` / `task_llm_profiles` | profile 全参数化（含重试三组覆盖、session_header_key）；熔断状态持久化重启不丢 |
| 任务层 | `tasks` / `task_categories` / `task_templates` / `task_relations` / `task_asset_links` / `task_scope` / `task_archives` | 任务状态机 created/running/paused/done/failed/timeout + queued FIFO + timeout/deadline/first_run 时间戳；归档为版本化 tar.zst 冷包（PG 只留元数据）；task_relations 支持跨任务只读上下文继承 |
| Agent | `agents` / `agent_prompts`（版本化）/ `agent_prompt_vars` / `agent_visibility` / `agent_skill_visibility` / `agent_triggers` | agent 角色五类；触发器（interval/on_finding/on_goal_met/on_tool_call/on_task_create）驱动 reporter 等 |
| 工具 | `tools` / `mcp_servers` / `mcp_tools_cache` / `skill_usage` / `tool_usage` | 工具目录 DB 化；调用账本（skill_usage 记录"想用但不存在"的未命中） |
| 会话 | `conversations` / `conversation_activities` | 独立对话页（chat agent 用） |
| 拦截 | `intercept_rules` / `intercept_pending` / `asset_intercept_rules` / `task_intercept_rules` | 命令级（tool_name/tool_input × string/regex → allow/deny/ask + 超时）与资产级（exact/fuzzy/cidr 黑白名单，block/allow 语义：有 allow 规则时须命中才放行） |
| 漏洞 | `findings` / `finding_retests` / `traffic_evidence_snapshots` / `finding_traffic_bindings` | findings 独立留存（任务删除不级联）；流量证据快照 + 有序绑定（role/position） |
| 其它 | `server_logs` / `side_question_*` / `notification_channels` / `notification_events` / `notification_deliveries` | 通知三表分离：事件在写漏洞事务内盲插（爆炸半径隔离），投递事务外 fan-out 带租约重试 |

### 8.2 会话状态持久化与恢复

- 原始 LLM 对话：transcript JSONL（`data/transcripts/`），session id 规则 `exp<N>-planner` / `exp<N>-worker-i<意图id>` / `exp<N>-main[-s<段>]`（`agent/worker.go:94`）；重启后 Resume 续跑（活动意图 running 恢复）。
- 过程可见性：每步工具调用/结果/思考都写 activity 表（`agent/capture.go:37` captureRunSession 把流式 delta 合并成整条记录），SSE 实时推前端。
- 记忆分层：**短期**=会话 messages（compaction 管理）；**中期**=探索图 fact/asset/finding（任务内共享、跨 run 持久）；**长期**=资产图（跨任务全局真值库）+ 任务归档可还原。norma 另有 memory 包（长期记忆工具）但 artex 主链路未启用。

### 8.3 findings / assets 记录路径

- `report_finding`（`agent/tools.go:1219`）→ FindingRecorder 接口（`agent/finding_recorder.go:15`，宿主注入保证原子写）→ 双写：探索图 finding 节点（血缘）+ 独立 findings 表（task_id/node_id/vulnclass/severity/summary/evidence/asset_ids/status/report）；`traffic_refs` 参数把复现流量按序绑定成快照证据（防流量回收后失证，`db/finding_traffic.go`）。
- `insert_assets`（`agent/tools_insert.go:122`）：混合类型批量 upsert，程序算去重 key 与父子关系（域名→子域→服务→端点），agent 只交原始字段；auth/technologies/params 追加合并；同时触发 asset_intercept 黑名单校验 + enrich 异步补全 + 自动累积 task_scope + 锚定 ownerNode（血缘）。

---

## 9. 隔离与安全

### 9.1 命令在哪台机器执行

**所有 shell 命令在 ARTEX 后端进程所在主机（或容器）本地执行**——`norma/tool/bash.go` 直接 `exec.CommandContext`，工作目录为任务专属目录 `<data>/tasks/<taskID>/i<intentID>`。攻击流量经本机 MITM 代理（:8788）打到目标。**没有按任务/按意图的容器级沙箱**：隔离手段是"同一容器内多任务共享文件系统但工作目录分离 + 审批门"。这是该系统最大的安全短板之一（见 §11 弱点）。

### 9.2 Docker 隔离

部署层面提供 Docker Compose（artex + postgres 两容器），镜像预装 Kali 常用工具；Dockerfile 多阶段（前端静态导出 → Go 静态编译）。运行时按 agent 无 per-task 容器调度。

### 9.3 危险命令过滤（多层）

| 层 | 机制 | 位置 |
| --- | --- | --- |
| SDK 安全底座 | catastrophicBash 正则四连：`rm -rf /`、fork 炸弹、`mkfs /dev/*`、`dd of=/dev/sd*` 无条件拒绝 | `norma/tool/bash.go:20-25` |
| 命令级拦截规则 | DB intercept_rules：tool_name/tool_input × string/regex → allow/deny/**ask**（人工审批，超时默认 deny，60s） | `intercept/intercept.go:1-12`；默认纳管 Bash/WebFetch/web_search/shell_*/Write/Edit/MultiEdit（:143-147） |
| LLM 兜底审批官 | 无规则命中时可选 LLM 判官（Reviewer 接口注入，判定理由前缀 `[模型]`，token 用量单独统计 `/api/intercept/judge/usage`） | `guard/guard.go:95-99`；`intercept/review_context.go`（喂配对执行证据、不含 assistant prose/thinking） |
| 资产级拦截 | asset_intercept_rules（全局）+ task_intercept_rules（任务级 block/allow）：exact/fuzzy domain/ip/url + CIDR；insert_assets 与命令双路校验 | `db/asset_intercept*.go`；`agent/tools_insert.go:18` |
| 操作约束 | task_constraints（allow/deny 自然语言）注入 planner/worker system prompt（"最高优先级红线：每条命令执行前先自检"） | `agent/constraints.go`；`db/schema.sql:287` |
| 目标防呆 | seed 拒绝把 LLM 网关主机当渗透目标 | `server/server.go:1674` |
| 拦截话术设计 | block 消息加"【ARTEX 平台管控·非目标防御】"前缀——防止 agent 把平台拦截误当目标 WAF 去绕过 | `guard/guard.go:138` |

### 9.4 其它安全面

- 全 API JWT（首次 /setup 设管理员密码），api_key 列表接口掩码不回传明文。
- 自更新链路强制 GitHub 域 HTTPS + SHA256SUMS 校验 + 冒烟测试 + 失败自动回滚（`selfupdate/`）。
- notify 渲染有 secret 掩码与 SSRF 防护测试（`notify/ssrf_test.go`）。
- 流量代理明文全录（设计取舍，本地留痕系统）。

---

## 10. 关键代码走读（6 条核心执行链路）

### 链路 A：一次完整 agent 循环（worker 视角，从领意图到终态）

1. `server/engine.go:916` workerLoop 自旋 → `runWorkerStep` → `claimNext`（:1237）：`Frontier(20)` 取 open 意图（`db/exploration.go:1136`，`ORDER BY priority DESC, id ASC`）逐条 CAS：
```go
// db/exploration.go:1173
func (s *ExplorationStore) ClaimIntent(id int64, owner string) (bool, error) {
    res, err := s.db.Exec(`UPDATE exploration_nodes SET state='running', owner=$1
WHERE id=$2 AND exploration_id=$3 AND kind='intent' AND state='open'`, owner, id, s.expID)
    ...
    return n == 1, nil   // 影响行数=1 才算领到——天然防多 worker 双领
}
```
2. `runIntent`（`server/engine.go:982`）：注册 per-work cancel、包 steerHooks（纠偏注入点）、`intercept.WithTaskContext` 挂审批上下文 → `worker.Execute`。
3. `agent/worker.go:361` execute：建 ToolSet → 装配工具（领域 ∪ host(traffic) ∪ SDK 默认 − MultiEdit/Glob/Grep）→ `AugmentTools`（skills/MCP/DB 覆盖 + guardPanic 防工具 panic 杀进程，`agent/assembly.go:75`）→ 意图与锚定资产进 system、态势进 user → `agentcore.Options{... MaxTurns, MaxDuration, Settlement, Compaction, Todos}` → `agentcore.NewSession` → `s.Resume(sessionID)` 续历史 → `captureRunSession`。
4. `norma/agentcore/agentcore.go:436` Prompt → `harness.Query`（`norma/harness/query.go:201`）→ `loop.run()`：每轮 `Compactor.Pre` → `streamAndExecute`（:779，流中工具边到边执行 `finalizeTool → exec.add → exec.drain`）→ 无 tool_use 时走 Stop 钩子/后台通知/预算续跑后 `finish(ReasonCompleted)`；有则回填结果、轮边界查预算命中即 `beginSettlement`（:594）注入收尾词进 wrap-up 阶段。
5. 事件流被 `agent/capture.go:37` captureRunSession 消费成 activity 记录（kind: tool_use/tool_result/text/thinking/usage/result，带 token 四列）→ `engine.emitActivity`（`server/engine.go:640`）持久化 + SSE 广播。
6. 收场：`runIntent` 尾部按 TerminalReason 分流终态（:1135-1152）：
```go
state := "done"
switch {
case err != nil:            state = "blocked"
case reason == harness.ReasonMaxTurns: state = "exhausted"  // 撞步数≠完成
case reason == harness.ReasonTimeout:  state = "exhausted"
}
```
→ `t.NotifyDone(intent.ID)` 带 id 唤醒 planner → 闭环。

### 链路 B：一个工具调用从 LLM 输出到执行返回（Bash + 审批）

1. 流式解析到 `SEToolUseStart/SEToolInputJSON` → 累积成 `ContentBlock{BlockToolUse}`（`norma/harness/query.go:820-848`），非法 JSON 兜底 `{}`（jsonValid :911）。
2. streamExec 调度：信号量限并发（默认 10），工具在独立 goroutine 跑，先过权限链 `CheckPermissions` → catastrophicBash 正则拒绝或 `permission.AskUser`（ModeBypass 下放行，:61-72）。
3. **Guard PreToolUse**（`guard/guard.go:57`）：抽 Bash command 入审计 → `applyIntercept`（:83）：规则匹配（优先级序）→ 未命中问 LLM 判官 → `ask` 则 `HandleAsk` 建 intercept_pending 行并**阻塞等待**人工 `/api/intercept/pending/{id}/decide` 或超时 deny（pendingManager channel 机制，`intercept/intercept.go:86-115`）。
4. 若有 steer 消息排队（`server/engine.go:578` steerHooks.PreToolUse），本次调用被 block 并把纠偏文本作为 tool_result 返回给模型重规划。
5. `runBash`（`norma/tool/bash.go:74`）→ 任务管理器前台执行（WorkingDir=意图目录、Env=proxyEnv 代理+CA）→ 输出经 `Capture` 截断/落盘。
6. PostToolUse：Guard 失败归因（结果文本匹配 403/waf/blocked/ratelimit → blocked 计数，供 planner 换策略，`guard/guard.go:148`）；工具 panic 被 guardedTool recover 成普通错误（`agent/assembly.go:79`）。

### 链路 C：任务创建 → 目标分解 → 首轮规划 → 报告生成

1. `POST /api/tasks`（`server/server.go:1462`）→ 校验 LLM 链/超时/关联任务/拦截规则 → `CreateTaskWithOptions`。
2. `launchTask`（`server/goals.go:28`）→ `seed`（server.go:1667，解析目标建根资产 + LLM 网关防呆 + 锚定 begin）→ 可选种子意图 → `admitTask`（并发上限 FIFO 准入，goals.go:110）。
3. `startTaskEngine`（goals.go:42）：发"第 0 轮目标拆解"活动 → `createGoals`（goals.go:371）→ `agent.DecomposeGoalsWithProvider`（`agent/goals.go:110`，8 轮预算，set_constraints → add_task_scope → set_goals 三步）→ LLM 无产出则原始目标单条兜底。
4. `engine.Run`（engine.go:748）：起 goroutine 组；无活动意图才 kick 首轮（注释详述 frontier 竞态，:769-776）。
5. planner 首轮（`agent/planner.go:355` Plan）：`compactor.OnPlannerRound` → 工具装配 → situational（触发渲染 + graph_overview 预取）→ constraintBlock 注入 → captureRun → LLM `add_intent`（`agent/tools.go:1091`，批量 ≤4，parent_ids 只准填 fact/finding——"意图必须锚在已确认知识上"）。
6. 逐步推进至 prove_goal 最后一个目标 → `tsx.GoalMet` → engine 置 done。
7. 报告：report_finding 触发 agent_triggers 的 on_finding → reporter agent 会话（取 node_detail 全证据 + worker trace + 流量绑定 → update_finding_report 存 Markdown）；`GET /api/report`（report/report.go:49）模板化导出总报告。

### 链路 D：LLM 故障转移（llmpool）

`llmpool/pool.go:68` Stream：按序尝试 member，**"只有未 yield 任何事件前才允许换"**的硬注释；失败 `health.Trip`（连续失败→熔断冷却，指数退避，DB 持久化 `llm_profile_health`）；同 Rank 轮转起点分摊负载（`rr atomic.Uint64`）；链耗尽返回 ErrExhausted → 任务侧标记 `IntentBlockedLLMQuota`（engine.go:1146）。

### 链路 E：流量留痕与检索

- 注入：worker/planner `BashEnv: proxyEnv(...)` + `WebFetchProxy/CACert`（agent/worker.go:439-453）→ 子命令与内置 HTTP 工具全走 :8788 MITM（go-mitmproxy，`traffic/traffic.go:1-8`）→ 每次交换全文落盘 + SQLite 索引（exchanges / exchange_bodies / blob_refs，>256KB 正文溢出内容寻址 blob + 8KB 预览，FTS5 **trigram** 支持子串与中文全文检索，traffic.go:75-90）。
- 消费：worker 工具 `traffic_search(host 必须, body_contains)/traffic_get/traffic_blob`（`agent/worker.go:254` traffic block："先查流量、不要重复 curl 同一 URL"）；finding 的 traffic_refs 绑定快照。

### 链路 F：人在环路（mainagent Chat + 直投意图）

`POST /api/tasks/{id}/chat` → `MainAgent.Chat`（`agent/mainagent.go:112`）：MainAgentTools（含 steer_work/set_goals/set_constraints）→ 人说"立刻测 X" → `add_intent`（priority 8-10）→ ToolSet.resumeTask（server 注入）把终态任务拉回 running（goals.go:355 reviveTask 注释解释为何普通 notify 唤不醒终态任务）→ `runDetachedIntent`（engine.go:1171）池外执行；对在跑意图可直接 `steer_work` 纠偏。

---

## 11. 亮点与弱点

### 11.1 值得借鉴的设计（Top 8）

1. **双图架构（探索图 × 资产图 + anchors）**——"目标怎么推进"与"资产真值"分离，资产跨任务复用、探索每任务独立、锚点双向查询支撑覆盖度。这是整个系统最有产品价值的建模：LLM 的"记忆"不靠对话历史而靠可查询的结构化图谱，天然抗 compaction、抗会话重置。(`db/schema.sql:40-281`)
2. **planner/worker 经数据库解耦 + CAS 领取 + TriggerEvent 增量唤醒**——无内存任务队列，重启即恢复；触发事件把"因何醒来"写进 prompt（避免全图重扫）；"大多数唤醒派 0 个意图是正常的"防止意图爆炸。(`server/engine.go:800-914`, `agent/planner.go:158`)
3. **预算终态的 Settlement（收尾）机制**——步数/时间耗尽不是硬切断，而是注入收尾提示词 + 禁用 Bash + 独立 10 轮，强制把已识别内容写回图谱再结束；意图终态区分 done/exhausted/blocked，planner 对 exhausted 看 trace 决定续派。这是长任务不烂尾的关键。(`norma/harness/query.go:594`, `agent/wrapup.go`)
4. **steer_work / kill_work 过程控制原语**——对在跑 agent 的"下一次工具调用前注入纠偏"（不 kill 不丢进展）与止损 kill，加上空转回合检测续跑（仅思考无动作时注入指令）。编排系统对执行中 agent 的细粒度干预范本。(`server/engine.go:531-633`)
5. **工具面与提示词的"可运营化"**——内置工具/prompt 开机幂等播种进 DB，UI 可按 agent 绑定/改描述/改 schema/注入参数默认值，但 handler 与关键段落（流量规约、禁 /tmp）代码固定不可编辑；DB 模板渲染失败逐级回退代码默认。产品化 agent 平台的标准做法。(`agent/toolcatalog.go`, `agent/prompt.go:38`, `server/assembly.go`)
6. **全链路留痕与可观测**——MITM 代理 + 六生态 CA 注入让任意命令行工具零配置留痕；activity 表记录每步（含 token 四列）；worker 间可全文检索彼此执行过程（search_all_worker_traces）；拦截审批详情带配对执行证据（非 prose）。(`agent/worker.go:204`, `agent/capture.go`)
7. **三层上下文治理**——SDK compaction（对齐 Claude Code 三机制）+ 探索图冷节点 digest 折叠（把压缩思想搬到知识图谱）+ transcript 断点 Resume；"意图进 system（不可压缩）、态势进 user（可降级）"的取舍有明确注释论证。(`norma/compaction/`, `agent/compaction.go`, `agent/worker.go:384-390`)
8. **审批门的产品细节**——deny 消息加"平台管控·非目标防御"前缀防止 agent 把拦截当 WAF 绕过；LLM 兜底审批官（规则未命中时）+ 人工 ask 超时默认拒绝；资产级黑白名单与命令级规则互补。(`guard/guard.go:126-141`)

### 11.2 应当避免的问题

1. **无执行沙箱**：所有 Bash 在后端主机/单容器内执行，任务间共享文件系统与网络位置，恶意目标反制（反弹 shell 到内网、读 data/ 下密钥）无隔离手段。命令过滤只有四条灾难正则 + 规则审批，绕过容易。做同类系统必须上 per-task 容器/microVM（如 Firecracker、 Kata）或至少 nsjail/seccomp。
2. **单点 planner 的提示词复杂度失控**：planner 提示词 ~40 行密集中文规则（硬底线/复核规则/深度优先/多样性/串行链……），互相间已有张力（"0 意图正常" vs "硬底线必须派"），行为正确性依赖模型遵从长指令的能力；且 prompt 越长越难回归测试。多目标约束应下沉为程序化校验（如 frontier 空转检测已在代码层兜底，说明作者也在补）。
3. **架构文档与代码不一致的痕迹**：注释中残留"SQLite 双图"（`cmd/artex/main.go:1-3`）而实际已是 PostgreSQL；`goal_met` 工具"默认不绑任何 agent"却仍在 PlannerTools 里构造。大型快速迭代项目需要文档同步纪律。
4. **server 包过大**：`server/server.go` 3908 行、243 条路由单文件注册，manager/engine/orchestration/conversations 等耦合在同一包，测试虽多（task_admission_test 822 行等）但结构上应按资源拆分。
5. **engine 内存态与 DB 状态双写的复杂度**：Engine 里十余个 sync.Map（started/paused/deleting/settling/deadline/inflight/steerBox...）与 DB 任务状态需精心同步，代码里大量竞态注释（claim→Execute 竞态守卫、delete barrier、CAS 状态冲突）证明这是持续的高维护成本来源。可考虑单一事实来源 + 事件日志。
6. **资源消耗型风险**：activity 每步写库 + 重试（FK 违反还要在线诊断查询，engine.go:649-663）、planner 心跳固定开销、冷压缩后台 LLM 调用，大规模任务下 PG 压力与 token 成本需要配额治理（系统有 token 统计但无硬预算熔断）。
7. **`llm_profiles.api_key` 明文存储**（DB 列明文、UI 掩码仅回显层面），且流量代理明文全录——部署环境的密钥与数据保护依赖运维自律。

---

## 附：与"AI 自主渗透测试系统"复刻直接相关的最小核心集

若要复刻该系统的骨架，最小路径为：norma 式 harness 主循环（流式 + 工具并发 + Settlement）→ ToolSet 式领域工具（add_intent/prove_goal/record_fact/insert_assets/report_finding/graph_overview 六件套即够跑通闭环）→ 双表探索图（nodes + edges，CAS 领取）→ planner/worker 双循环 + TriggerEvent → MITM 留痕（可选）→ 审批门（规则 + ask）。其余（LLM 池、归档、通知、复测、Auto 编排）均为增强件。
