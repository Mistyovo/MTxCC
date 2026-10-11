# CyberStrikeAI 深度技术分析报告

> 分析对象：`refer/02-chinese/CyberStrikeAI/`（GitHub: Ed1s0nZ/CyberStrikeAI，Apache-2.0，404星链计划成员，腾讯 TCH 智能渗透 Top 项目）
> 分析方式：纯源码静态阅读，只读不执行。所有引用为仓库内相对路径 `file:line`。

---

## 1. 一句话定位与项目背景

**CyberStrikeAI 是一个 Go 语言单二进制部署的"AI 原生网络安全智能执行中枢"：把自然语言意图转化为受治理（RBAC + HITL + 审计）、可追溯（执行记录 + 攻击链图）的自主渗透测试行动，内置 MCP 工具层、多代理编排、RAG 知识库、WebShell 管理与 C2 框架。**

README 自述："将规划、执行、人工监督、证据与复盘连接在同一个可审计工作空间中……基于 Go 构建，融合 Eino 智能体、MCP 原生工具、RAG 知识、可视化工作流以及攻击链建模与分析能力，面向已获得明确授权的安全任务"（`README_CN.md:9-11`）。与多数"脚本 + LLM 胶水"项目不同，它是一个**平台化产品**：Web 控制台、多用户 RBAC、机器人接入（企微/钉钉/飞书/Telegram/Slack/Discord/QQ）、Burp Suite 插件与浏览器扩展、一键更新。其能力上限刻意做到了红队全栈（含 C2 Beacon、WebShell 虚拟终端），仅以"授权"文本约束。

## 2. 技术栈与代码规模

**语言与框架**（`go.mod`）：
- Go 1.25；Web 框架 gin v1.9.1；存储 SQLite（mattn/go-sqlite3）
- **Agent 框架：CloudWeGo Eino v0.9.14 + eino-ext（adk/local backend、agenticopenai、agenticclaude、markdown/recursive splitter、openai embedding）** —— 这是字节开源的 Go LLM 编排框架，CyberStrikeAI 是其较重的实战用户
- MCP 官方 Go SDK v1.3.1（modelcontextprotocol/go-sdk）
- tiktoken-go（token 计数）、OpenTelemetry（einoobserve）、zap（日志）、creack/pty（伪终端）、robfig/cron
- 前端为服务端模板 + 静态 JS（`web/templates`、`web/static`）；插件层含 Java（Burp）与 JS（浏览器扩展）

**代码规模**：744 个 Go 文件、约 15.97 万行 Go；另有 158 个 md、105 个 YAML 工具配方、62+41 个 JS/CJS、8 个 Java。

**核心 vs 外围**（internal 各包行数）：

| 分类 | 包 | 规模 | 职责 |
|---|---|---|---|
| 核心 | `internal/multiagent` | 199 文件 / 28,049 行 | Eino 编排、中间件群、流式事件映射 |
| 核心 | `internal/handler` | 136 文件 / 47,777 行 | HTTP/SSE API（产品功能面） |
| 核心 | `internal/mcp` | 22 文件 / 7,494 行 | 内置 MCP Server、异步执行服务、外部 MCP 联邦、调用拦截 |
| 核心 | `internal/security` | 29 文件 / 5,035 行 | 工具执行器、进程作用域、RBAC |
| 核心 | `internal/database` | 60 文件 / 17,726 行 | 40+ 张表的 SQLite 持久层 |
| 核心 | `internal/agent` / `agents` | ~2,300 行 | 单代理循环 + agents/*.md 加载 |
| 重要 | `internal/c2`（5,444）、`workflow`（5,774）、`knowledge`（3,864）、`attackchain`、`project(prompt)`、`hitl`、`toolguard`、`processguard`、`vision`、`update` | | |
| 外围 | `tools/`（105 YAML）、`roles/`（13 YAML）、`skills/`（24 目录）、`agents/`（16 md）、`mcp-servers/`（Python）、`plugins/` | | 知识资产层，零代码即可扩展 |

## 3. 整体架构

### 3.1 模块划分与数据流

```
用户(Web SSE / 机器人 / Burp插件 / 浏览器扩展 / MCP stdio)
   │ HTTPS
   ▼
cmd/server → internal/app（路由装配，app.go:964 起 /api 路由组）
   ▼
internal/handler（136 文件；chat、hitl、c2、asset、knowledge、workflow…）
   ▼
internal/multiagent（编排入口四选一）
   ├─ RunEinoSingleChatModelAgent  单代理 ReAct（eino_single_runner.go:28）
   └─ RunDeepAgent（runner.go:65）
        ├─ deep：主代理 + task 子代理（Eino adk/prebuilt/deep）
        ├─ plan_execute：Planner→Executor→Replanner（eino_orchestration.go:50）
        └─ supervisor：transfer/exit 专家路由（adk/prebuilt/supervisor）
   ▼
einomcp.ToolsFromDefinitions（OpenAI 风格工具定义 → Eino InvokableTool 桥，einomcp/mcp_tools.go:28）
   ▼
internal/agent.Agent → internal/mcp.Server（工具注册/分发）
   ├─ builtin 工具（漏洞/资产/黑板/知识库/视觉/执行控制/WebShell/C2/批量任务，builtin/constants.go）
   ├─ internal/security.Executor → 本地命令（YAML 配方驱动）
   └─ ExternalMCPManager → HTTP/stdio/SSE 外部 MCP（含联邦）
   ▼
mcp.ExecutionService（阻塞工作交给 worker，产生 execution_id）
   ▼
SQLite（data/）＋ SSE 进度流（process_details / partial_output）
```

关键设计：**Agent 面向 Eino，工具面向 MCP**——所有能力统一注册进自建 MCP Server，再经 `einomcp` 桥接为 Eino 工具，单代理（自研 OpenAI 循环）与多代理（Eino ADK）共享同一工具池（`runner.go:1` 包注释、`einomcp/mcp_tools.go:28-46`）。

### 3.2 前后端 API

- REST 分组：`/api/auth`、`/api/robot`（app.go:967-982）、受保护组含 knowledge（app.go:1179）、c2（app.go:1371）等；对话主流为 `/api/multi-agent/stream` SSE（README_CN.md:106 提及）与单代理聊天流。
- SSE 事件类型丰富：progress / response_start / response_delta / tool_call / tool_result / eino_trace_*（排障）等；`RunResult`（runner.go:35-47）把 `MCPExecutionIDs`、`LastAgentTraceInput/Output`、`PendingExecutionIDs` 回写消息表，供续跑与攻击链使用。
- 另暴露 `cmd/mcp-stdio` 把整个平台作为外部 MCP Server 供 Claude/其他 Agent 调用。

## 4. Agent / 编排设计

### 4.1 四种编排模式（配置 `multi_agent.*`，config.example.yaml:320-372）

| 模式 | 机制 | 终止 |
|---|---|---|
| eino_single | `adk.NewRunner.Run` + TypedChatModelAgent，历史切片多轮 | 模型自然停 + max_iterations |
| deep | 主代理通过 `task` 工具委派子代理（子代理看不到父对话，仅看 description） | 同上 |
| plan_execute | Eino `adk/prebuilt/planexecute`：Planner（newLenientPlan 宽松计划解析）→ Executor（Agentic 模型 ReAct）→ Replanner 循环 | loop_max_iterations（默认 10，eino_orchestration.go:94-97） |
| supervisor | `transfer_to_agent`/`exit` 专家路由 | exit 或 transfer 断链 |

主代理系统提示解析链：`agents/orchestrator*.md 正文 > config 覆盖字段 > 内置默认`，且 plan_execute/supervisor **不**回退 Deep 提示避免混用（orchestrator_instruction.go:249-298）。

### 4.2 角色体系（三层）

1. **编排主代理**（`agents/orchestrator.md`，164 行）：规划、委派、汇总。核心行为规则：委派优先（"把任务拆成至少两类阶段并分别委派"，orchestrator.md:27）；**task 交接包强制**（"把子代理当作刚走进房间的同事——它没看过你的对话"，orchestrator.md:33）；**派单前目标完整性校验**（目标标识/测试范围/任务目标/成功标准四字段缺失则禁止委派，orchestrator.md:38-43）；矛盾结果需补充 task 二次验证直至自洽（orchestrator.md:51）。
2. **16 个杀伤链子代理**（`agents/*.md`）：recon、intel-collection、vulnerability-triage、penetration、privilege-escalation、lateral-movement、persistence-maintenance、opsec-evasion、impact-exfiltration、reporting-remediation、cleanup-rollback 等——即把渗透阶段显式映射为专家角色。子代理有硬约束："默认不拥有父代理完整上下文，仅以本次 task.description 为准……禁止自行猜测目标"（penetration.md:27-32），frontmatter 可声明 `max_iterations`、`tools`、`bind_role`。
3. **13 个角色配置**（`roles/*.yaml`）：Web应用扫描/渗透测试/API安全/云安全/二进制分析/CTF 等，本质是**工具白名单 + 提示前缀**，通过 `ag.ToolsForRole(roleTools)` 过滤（runner.go:155、213）。

### 4.3 循环、反思与终止

- **中间件群**（multiagent 包最厚的部分）：summarization（上下文压缩）、reduction（大输出落盘）、tool_search（动态工具解锁）、orphan_tool_pruner（修补悬空 tool_call）、tool_pair_reconciler、HITL、RBAC tool filter、continuation_user_dedup、model_output_recovery 等 30+ 个，全部以 Eino `TypedChatModelAgentMiddleware` 挂载，主/子代理按配置分级挂载（runner.go:218-240）。
- **反思**：plan_execute 的 Replanner 每轮执行后重写计划；supervisor 依赖专家输出互证；摘要压缩时维护"原始用户输入与约束账本"（`summarization_user_intent_ledger_max_runes`，config.example.yaml:356）防目标漂移。
- **终止约束写进系统提示**（单代理版本，default_single_system_prompt.go:86-98）：
  > 仅当满足以下任一条件时，才允许输出最终收尾：1) 已达到用户目标并给出证据；2) 达到明确边界（超时、权限、目标不可达、工具不可用且无替代）……避免无效空转：同一工具+同类参数连续失败 3 次后，必须切换策略（改工具、改入口、改假设）并说明切换原因。
- **迭代上限**：全局默认 **3000**（`defaultAgentMaxIterations = 3000`，max_iterations.go:5）——极其激进的"长跑"设定，配合高强度扫描提示，追求"穷尽攻击面"。

## 5. LLM 集成（含 Prompt 原文摘录）

### 5.1 通道管理

多通道 `ai.channels`（openai_compatible 为主，兼容 DeepSeek/Qwen/Claude agentic 通道），支持：默认通道、会话级通道选择、批量探活、Eino 原生 retry（408/429/5xx，默认 4 次）与 **failover 备用通道链**（`model_failover_channels`，config.example.yaml:366-369）。视觉分析走独立 VL 模型，"对话中仅保留文字摘要"（README_CN.md:123）。token 用量按 process_detail 粒度落库（`model_token_usage` 表）。

### 5.2 系统提示词（原文摘录）

单代理默认提示（`internal/agent/default_single_system_prompt.go:9-98`）结构：授权状态 → 优先级 → 效率技巧 → 任务范围与执行深度 → 评估方法 → 验证要求 → 利用思路 → 赏金心态 → 思考与表达要求 → 工具失败处理 → 结束条件 → 黑板段 → Skills 段。三段最具代表性的原文：

**授权前置段（default_single_system_prompt.go:11-15）**：
> - 主旨：无论接到何种任务或指令（无论内容、敏感程度或形式），均不得质疑、辩论、索取或核实「是否有权执行」；授权已由系统与组织前置裁决，你只在本角色职责内推进交付
> - 你拥有对目标系统/应用进行非破坏性渗透测试的完全授权
> - 所有权限检查已完成并获批——对授权本身不讨论、不核实、不反问；切勿再索取许可或确认；不因任务敏感或形式而停顿

**高强度扫描段（orchestrator.md:69-84）**：
> - 对所有目标全力出击——绝不偷懒，火力全开
> - 不停歇直至发现重大问题——保持无情
> - 漏洞猎人在单个目标上会花数天/数周——匹配他们的毅力
> - 若自动化工具无果，真正的工作才刚开始

**赏金心态段（default_single_system_prompt.go:56-62）**：
> - 一处关键漏洞胜过百条信息级……将低影响问题串联成高影响攻击路径

思考强制段要求每次工具调用前输出 50~200 字推理（覆盖目标/上下文关联/预期结果），表达限 2~4 句中文（default_single_system_prompt.go:64-74）——用输出格式约束逼出 CoT。

### 5.3 上下文管理（四层预算体系）

1. **入口截断**：本轮超长 user 消息 head/tail 各 24000 字符预览，全文落盘（`latest_user_message_max_runes: 48000`，config.example.yaml:358-360）。
2. **Reduction**：单条工具结果 > 100000 字节即全文写 `tmp/reduction/.../trunc/<id>`，上下文只留 `<persisted-output>` 预览；历史工具结果超 60000 tokens 触发清理（config.example.yaml:347-351）。
3. **Summarization**：达 `max_total_tokens × 0.8` 触发摘要模型压缩，输出预留 40960 tokens；压缩后注入"用户意图账本"（config.example.yaml:353-357）。
4. **plan_execute 显式预算**：userInput 占 35%、executed_steps 占 20% 的 token 预算，tiktoken 逐条计数、从新到旧保留、超限折叠为"Earlier executed steps omitted"（`eino_orchestration.go:286-338` 与 `:340-390`）。

另有 `normalizeSingleLeadingSystemMessage` 把多段系统消息合并为首条，规避 qwen/vllm 模板的"System message must be at the beginning"报错（eino_orchestration.go:247-284）——大量此类生产级兼容补丁是本项目工程特色。

## 6. 工具层

### 6.1 YAML 工具配方（105+，`tools/*.yaml`）

以 `tools/nmap.yaml` 为例：`command` + 固定 `args`（默认 `-sT -sV -sC`）+ `parameters` 列表。参数声明五种格式：`positional`（含 position 序号）、`flag`（`-p` value）、`template`（`-T{value}`）、`combined`（`--flag=value`）、`additional_args`（引号感知的空格分词，executor.go:627-696）。描述本身就是给模型的使用文档（含"必须 root""全端口很慢"等提示）。

### 6.2 注册与 Schema 生成

`security.Executor.RegisterTools`（executor.go:284-351）遍历启用工具，`buildInputSchema`（executor.go:1402-1472）把 YAML 参数自动转 JSON Schema（类型映射、array 补 items、enum、default、required），注册进自建 MCP Server；工具描述支持 short/full 两档（`tool_description_mode`）控制上下文占用。

### 6.3 执行链（无 shell 包装）

`ExecuteTool`（executor.go:122-281）→ `buildCommandArgs`（executor.go:354-624，含 nmap scan_type 整段替换、position 0 子命令前置等特判）→ `exec.CommandContext(cmd, args...)`——**直接 argv 数组执行而非 shell 拼接**，天然免疫 shell 注入（仅 `exec` 工具例外，走 `sh -c`）。执行期特性：

- 流式读取 stdout/stderr，2048B/200ms 批量回调 SSE（executor.go:1127-1254）
- **PTY 自动重试**：检测 "inappropriate ioctl for device"/"not a tty" 时换 `pty.Start` 重跑（executor.go:1288-1302, 1306-1381）
- 输出字节上限 + 超限 spill 落盘并返回带路径截断预览（executor.go:970-1099）
- 无输出空闲超时（默认 300s）终止进程树（executor.go:1205-1234）
- 退出码白名单（`allowed_exit_codes`，如 nuclei 非 0 仍算成功）
- 后台命令 `&` 检测 → `StartManagedBackground` 由 ProcessScope 托管，任务结束自动清理（executor.go:752-770, 840-847）

### 6.4 内置 MCP 工具与异步执行

内置工具 40+（builtin/constants.go:8-107）：漏洞 CRUD、资产 CRUD、黑板七件套、知识库检索、`analyze_image`、**执行控制三件套**（get/wait/cancel_tool_execution）、WebShell 五件套、批量任务九件套等。

**异步执行模型**（本项目工具层最大亮点）：`mcp.ExecutionService`（execution_service.go:79-103）把阻塞工具调用交给 worker，状态机为 `queued/running/completed/blocked/failed/cancelled/hard_timeout/orphaned`（execution_service.go:18-28）。Agent 侧只做**有界等待**（Wait + timeout，execution_service.go:381），超时则向模型返回 execution_id + partial_output 尾部预览，模型用 `wait_tool_execution`（每次 ≤600s 有界等待，可多轮）或 `cancel_tool_execution` 续接（execution_control_tools.go:57-115）。支持单 server 熔断、并发限制、主动取消、服务重启后 orphaned 对账。

### 6.5 tool_search 动态解锁

工具数 ≥ 20 时仅前 12 个（+ 白名单 40 余个内置工具）常驻上下文，其余进动态池，模型用 `tool_search` 正则按需解锁（config.example.yaml:341-344）——显著降低百工具规模下的 schema token 开销与误选率。

## 7. 渗透方法论建模

CyberStrikeAI 没有把渗透方法论硬编码成状态机，而是**分层外置为知识资产 + 图谱化记忆**：

1. **方法论提示层**：评估方法六步（范围定义→广度优先发现→自动化扫描→定向利用→持续迭代→影响文档，default_single_system_prompt.go:36-43）+ "先基础后顶级（前 0.1% 黑客）技术、链接多个漏洞"的利用思路。
2. **技能层**（24 个 skill，agentskills.io 规范，SKILL.md + 渐进披露）：核心哲学在 `skills/pentest-agent-os/SKILL.md`：
   > **渗透不是执行脚本，是搜索路径。** 不给自己预设固定路径/角色/已知利用链。给定起点与目标，路径未知 → 在状态空间里搜索……不问"它有没有已知CVE"，而问"基于项目黑板上已掌握的事实，下一步最有效的动作是什么"。
   
   套件按杀伤链全覆盖：attack-surface-recon、initial-access-phishing、web-attack-methods、source-code-hunting、post-exploitation、active-directory-attack、cloud/blockchain/ai-llm/wireless-hardware-attack、zero-day-discovery（0day 自主发现引擎）、redteam-opsec、pentest-verification（防幻觉铁律）、capability-primitive-search（能力原语凑链）。
3. **强触发器**（SKILL.md 末尾）：识别到组件→搜洞；请求被拒→代理换路（proxy-tool-bootstrap）；新凭据/域名→立即横向。
4. **攻击链图谱**：`attackchain.Builder.BuildChainFromConversation`（builder.go:86）用 LLM 把"本轮 last_react 轨迹 + 工具结果"一次性解析为 target/action/vulnerability 三类节点的**树状图**（强调分支/汇聚、失败节点标 `failed_insight`、"失败如何为成功提供线索"），vulnerability 节点按 critical(90-100)/high(80-89)/medium(60-79)/low(40-59) 打 risk_score（builder.go:468-560 的完整构建 prompt）。落库 `attack_chain_nodes/edges`，前端有图谱视图与步骤回放。
5. **项目黑板**（见下节）：把"已知事实图"作为搜索算法的状态空间。

## 8. 状态与记忆（数据库 Schema）

SQLite，40+ 表（`internal/database/schemas/application.sql`）。核心表：

- **会话与跨轮记忆**：`conversations`（application.sql:243-256）除常规字段外存 `agent_mode`、**`last_react_input` / `last_react_output`**（序列化的模型面消息带 JSON，续跑时按 role/tool_calls/tool_call_id/reasoning_content 逐字段还原，见 runner.go:692-787 的 historyToMessages/normalizeRestored*）；`messages` 带 `mcp_execution_ids` 与 `reasoning_content`；`process_details` 存 SSE 事件流。
- **项目黑板（图谱记忆）**：`project_facts`（application.sql:366-382）：`(project_id, fact_key)` 唯一、`category`、`summary`（索引行）、`body`（完整复现上下文）、`confidence`(tentative/confirmed)、`pinned`、`related_vulnerability_id`；`project_fact_edges`（application.sql:352-364）：9 种有向边 `discovered_on/depends_on/leads_to/enables/exploits/contains/part_of/supports`。系统只注入"黑板索引"（fact_key+摘要），正文须调 `get_project_fact` 取回——**索引/正文两级取回控制上下文成本**（orchestrator.md:133）。
- **漏洞台账**：`vulnerabilities`（application.sql:518-544）：title/severity/status/vulnerability_type/target/**preconditions/reproduction_steps/evidence/impact/recommendation**/retest_notes/retest_log/project_id/owner_user_id + `vulnerability_retests` 复测表——字段即渗透报告模板。
- **执行审计**：`tool_executions`（application.sql:490-507）：arguments/status/result/error/duration_ms/**partial_output**（含 truncated 标记）/owner_user_id/conversation_id；`tool_stats`、`skill_stats` 统计。
- **治理**：`hitl_interrupts`（application.sql:268-284，payload/decision/decision_comment/decided_by）+ `hitl_conversation_configs`（按会话开关/模式/敏感工具/超时/reviewer）；RBAC 五表（rbac_users/roles/role_permissions/permissions/resource_assignments，application.sql:396-449）；`audit_logs`；C2 全套（listeners/profiles/sessions/tasks/events/files/payload_artifacts）。
- **RAG**：`knowledge_base_items` / `knowledge_embeddings` / `knowledge_retrieval_logs`（knowledge.sql）。

## 9. 隔离与安全

### 9.1 调用前拦截（toolguard）

`internal/toolguard`：规则化正则拦截器，**默认启用政府域名保护**（.gov/.gov.cn/*.gov.* 通配，toolguard.go:24, 62-73）。检查覆盖工具名 + 全部参数字符串/键名，**最多 3 轮 percent-decode 反逃逸**（toolguard.go:196-255）；规则上限 100 条、校验失败保留旧策略（fail-safe）。命中时返回结构化 MCP error（含规则名/匹配文本），并在结果 meta 打 `cyberstrike.ai/blocked` 标记（mcp/tool_call_guard.go:27-41）。包注释诚实声明局限："不解析 DNS/重定向/文件/任意编码，不是穷尽的目标授权检查"（toolguard.go:1-5）。

### 9.2 HITL 人机协同

- 按会话配置：模式 off/approval、敏感工具集、超时、reviewer（human 或**审计 Agent**）（handler/hitl.go:27-33, hitl_conversation_configs 表）。
- 实现为 Eino `compose.ToolMiddleware`，**同时挂 Invokable 与 Streamable**——因为 Eino filesystem execute 是流式工具，只挂 Invokable 会漏拦直接执行（multiagent/hitl_middleware.go:45-52，注释原话）。
- 人工可**编辑参数**后放行：编辑后的 arguments 替换执行，并在工具结果前注入 "[HITL] Human reviewer approved this tool call with edited arguments. Original…/Executed…" 通知模型（hitl_middleware.go:61-78）。
- 拒绝是**软结果**（soft tool result）让模型继续迭代而非硬错误；并对 `transfer_to_agent` 特判清除 returnDirectly，防止拒绝后监督者空转终止（hitl_middleware.go:108-116，三条注释解释了框架级坑）。
- 崩溃一致性：启动时把上一进程遗留的 pending interrupt 全部置为 cancelled/reject（handler/hitl.go:79-88）。
- **C2 危险任务共用同一审批通道**：`C2HITLBridge` 写 hitl_interrupts 轮询等待，超时"安全起见拒绝执行"（app/c2_hitl_bridge.go:47-131）。

### 9.3 进程治理

- `processguard`：OS 级 containment，auto/required/process_group 三模式；Linux 用 **cgroup**（memory_max_bytes 默认 2GiB、cpu_quota、max_processes 256），带 watchdog 与 `-check` 自检命令（guard.go:8-60, check.go:9）；`Configure` 失败即拒绝（fail-closed）。
- `security.ProcessScope`：任务级进程所有权——所有 ShellSession 注册入 scope，`Seal()` 后拒绝新进程（`ErrProcessScopeClosed`），后台命令托管其中，任务结束统一清理；Start 与 Seal 同锁串行，"没有进程能在最终快照与任务完成之间启动逃逸清理"（process_scope.go:16-47 注释）。
- 平台 RBAC：用户/角色/权限/资源归属，`rbac_tool_middleware.go` 在**工具层**再做一次角色过滤（工具白名单随角色/子代理收窄）。

### 9.4 输出与证据治理

"数据库保存与 Agent 实际看到的同一份兜底后工具结果，恢复路径会再次防御历史超大输出"（README_CN.md:131）——即持久化的是 post-reduction 视图，审计所见即模型所见。另有登录保护、审计日志、TLS 默认（run.sh 以 `--https` 自签证书启动）。

### 9.5 安全短板（客观）

- **授权只靠提示词与正则**：`projects.scope_json` 存在但未见机器可读的强校验链；提示词反而主动压制模型的授权质疑（见 §5.2），与"审计 Agent 复核"存在哲学冲突。
- **无容器/VM 沙箱**：工具直接运行在宿主机，cgroup 是资源限制不是隔离；`exec` 工具（sh -c 任意命令）+ `eino_skills.filesystem_tools`（read/write/edit/execute 本机文件）组合的爆炸半径大。
- 默认 max_iterations=3000 + "火力全开"提示，长跑失控与目标过载风险由人扛。

## 10. 关键代码走读（5 条核心链路）

### 链路 A：多代理运行装配（RunDeepAgent）

`internal/multiagent/runner.go:65-240`。入口做七件事：准备模型面 user 消息（入口截断）→ 加载 agents/*.md 与 YAML 子代理合并（runner.go:92-103）→ 归一化编排模式（orchestrationOverride 优先，runner.go:104-107）→ 准备 Skills 中间件与本地 FS 工具（runner.go:123）→ 构建 MCP 执行监控回调与 execution_id 记录器（runner.go:131-152）→ 逐个子代理构建模型/工具/中间件链（runner.go:176-240）：

```go
// runner.go:213-215  子代理工具 = 角色过滤后的 MCP 桥接
subDefs := ag.ToolsForRole(roleTools)
subTools, err := einomcp.ToolsFromDefinitions(ag, holder, subDefs, recorder, nil, toolInvokeNotify, id)
// runner.go:222-227  子代理独立迭代上限与摘要中间件
subMax := resolveMaxIterations(appCfg, sub.MaxIterations)
subSumMw, err := newEinoAgenticSummarizationMiddleware(ctx, subModel, appCfg, &ma.EinoMiddleware, conversationID, db, projectID, logger)
```

每个子代理独立挂 summarization / reduction / skill / filesystem 中间件——上下文治理是**每代理一份**而非全局一份。

### 链路 B：HITL 拦截（工具执行前的人工闸门）

`internal/multiagent/hitl_middleware.go:98-134`。拦截器经 context 注入（`WithHITLToolInterceptor`）；中间件在真实工具前调用：

```go
// hitl_middleware.go:104-123
if fn, ok := ctx.Value(hitlInterceptorKey{}).(HITLToolInterceptor); ok && fn != nil {
    originalArgs = input.Arguments
    edited, err := fn(ctx, input.Name, input.Arguments)
    if err != nil {
        if IsHumanRejectError(err) {
            msg := HitlRejectToolResult(input.Name, err.Error()) // 软结果
            hitlClearReturnDirectlyIfTransfer(ctx, input.Name)   // 防 supervisor 空转
            return &compose.ToolOutput{Result: msg}, nil
        }
        return nil, err
    }
    if edited != "" { editedArgs = edited; input.Arguments = edited } // 参数编辑
}
```

管理端 `HITLManager`（handler/hitl.go:41-53）维护 per-conversation 运行时配置与 `pending map[string]*pendingInterrupt`（decideCh 通道），审批通过后经 `approvedExec` FIFO 队列回写 tool_result。

### 链路 C：YAML 工具执行（nmap 为例）

`internal/security/executor.go:122-281`。查索引 → 构建 argv → 流式执行：

```go
// executor.go:187-203
cmd := exec.CommandContext(ctx, toolConfig.Command, cmdArgs...) // 无 shell
applyDefaultTerminalEnv(cmd); attachNonInteractiveStdin(cmd)
if cb, ok := ctx.Value(ToolOutputCallbackCtxKey).(ToolOutputCallback); (ok && cb != nil) || mcp.MCPExecutionIDFromContext(ctx) != "" {
    output, err = streamCommandOutput(ctx, cmd, cb, ResolveShellNoOutputTimeoutSeconds(...), e.toolOutputMaxBytes, spill)
    if err != nil && shouldRetryWithPTY(output) { /* PTY 重试 */ }
}
```

argv 构建的 nmap 特判在 executor.go:367-373（scan_type 整段替换默认 `-sT -sV -sC`）与 579-583（选项须贴值、置于全部参数前，附单测 `nmap_argument_order_test.go`）。

### 链路 D：异步执行与多轮等待（execution_id 模式）

`internal/mcp/execution_service.go:125 Submit → :205 runWorker → :381 Wait`。Submit 建 entry（含 cancel、done chan、PreRun 闸门）入队并持久化 queued；worker 取出后 `markEntryRunning`、执行 `entryResultRecover`（panic 恢复）、`finishEntry` 落库并回调 OnDone（Agent 已停止等待也会收到，保证 late completion 落账）。模型侧续接工具定义在 `execution_control_tools.go:57-90`：

```go
// wait_tool_execution：Description 原文（execution_control_tools.go:57）
"继续等待一个后台工具 execution 完成。每次等待都有 timeout_seconds 上限；
若仍未完成，会返回当前状态，模型可稍后再次调用。"
```

### 链路 E：上下文预算（plan_execute 输入裁剪）

`internal/multiagent/eino_orchestration.go:286-338`。tiktoken 从新到旧累加，超 `max_total_tokens × 0.35` 即截断，至少保留最新一条 user：

```go
// eino_orchestration.go:311-336（节选）
n, err := tc.Count(modelName, string(msg.Role)+"\n"+msg.Content)
if err != nil { n = (len(msg.Content) + 3) / 4 } // tiktoken 失败退化为字节/4
if used+n > budget { break }
...
if len(out) == 0 { return []adk.Message{input[len(input)-1]} } // 至少保留最新 user
```

executed_steps 渲染同构，超预算时写入"Earlier executed steps omitted due to context budget: N steps"（eino_orchestration.go:383-385）。

### 链路 F（附）：toolguard 反逃逸检查

`internal/toolguard/toolguard.go:118-145`：规则序即优先级；`candidateTexts`（:196-255）递归展开 JSON 参数树（深度 10001 对齐 encoding/json 上限）收集全部字符串与键名，每个候选做最多 3 轮 percent-decode（:201-213）；消息模板单次替换防注入（:138-140）。

## 11. 亮点与弱点

### 可借鉴设计（Top 7）

1. **execution_id 异步工具执行模型**：阻塞型 MCP 调用交 worker，Agent 有界等待 + `wait_tool_execution` 多轮续接 + partial_output 尾部预览 + 主动取消 + 服务重启 orphaned 对账（§6.4）。解决"长扫描拖死 Agent 循环"这一 AI 渗透系统的普遍痛点，且对模型暴露的是自然语言友好的状态而非裸 future。
2. **"所见即所存"的证据治理 + 三层上下文预算**：入口截断 / reduction 落盘 / summarization+用户意图账本，且数据库持久化的是与模型所见相同的兜底后结果，审计与回放不二义（§5.3、§9.4）。
3. **项目黑板（Fact 图谱）与漏洞台账分离**：`project_facts`（key 唯一、索引/正文两级、9 种有向边、confidence）记复现上下文，`vulnerabilities` 记可交付 findings；"边渗透边记录"作为强制节奏写入所有层级的系统提示（projectprompt/blackboard.go:13-17），把 Agent 记忆外置为可检索图谱，抗上下文压缩丢失。
4. **HITL 的工程完备度**：Invokable+Streamable 双通道拦截（防流式工具漏拦）、参数编辑回写并告知模型、拒绝为软结果保持迭代、transfer 工具 returnDirectly 特判、崩溃孤儿审批自动拒绝、C2 危险操作复用同一审批表（§9.2）——每一处都是踩过坑的框架级补丁。
5. **任务级进程治理**：ProcessScope Seal 语义（关闭后拒绝新进程）+ cgroup 资源上限 + watchdog + 后台命令托管 + PTY 降级 + 无输出空闲超时（§9.3、§6.3）。
6. **task 交接包纪律**：把"子代理无父上下文"这一多代理最常见故障源升格为提示词硬约束——四字段完整性校验缺失即禁止委派、交接必须自带增量共识（orchestrator.md:33-43），并要求协调者对矛盾结果做补充委派直至自洽。
7. **攻击链 LLM 后处理成图**：把 last_react 轨迹转为 target/action/vulnerability 树（失败节点标 failed_insight、强调失败线索价值），实现"过程可教学化回放"而非仅结论输出（builder.go:468-560）。

### 弱点

1. **授权治理前置但不可验证**：提示词明令"不得质疑/核实授权"（§5.2），scope 只有正则 guard 与 RBAC 工具白名单兜底，无机器可读目标范围强制（无 IP/CIDR 级执行校验）；对构建合规系统而言这是应当反做的部分——授权应做成策略引擎而非提示词豁免。
2. **无真正沙箱**：工具直跑宿主机；cgroup 是限额非隔离；`exec`（sh -c）+ 本机文件读写工具组合爆炸半径大。自建 C2/WebShell 能力使误用后果远超扫描器类项目。
3. **激进的自主性参数**：默认 3000 次迭代 + "无情/火力全开"提示 + "若不足以赚到 $500+ 继续挖"，缺乏对应的预算/速率熔断，成本与目标保护依赖人工 HITL 开启。
4. **单体重构压力**：handler 47k 行、multiagent 28k 行、单 SQLite 文件；中间件体系虽好但文件粒度极细（199 文件），上手成本高。
5. **提示词中文为主且高度重复**：四份编排提示 80% 重复段落，维护易漂移（虽有 projectprompt 抽取公共段）。

### 与同类项目差异一句话

相比 ReCon-style 单循环扫描器，CyberStrikeAI 的差异在"平台 + 治理 + 记忆"：Eino 多编排、MCP 工具面、黑板图谱、HITL/审计/RBAC、攻击链回放一应俱全；其 prompt 工程（授权豁免 + 高强度自主）与隔离短板（无沙箱）分别是构建授权范围内 AI 渗透系统时**最值得警惕与最需要替换**的两块。

---

*报告基于 2026-10 本地快照（go.mod: eino v0.9.14 / mcp-sdk v1.3.1），共抽读 30+ 核心文件。*
