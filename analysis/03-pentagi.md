# PentAGI 深度技术分析报告

> 分析对象：`/home/mistyovo/桌面/MTxCC/refer/01-international/pentagi/`（vxcontrol/pentagi，main 分支，commit 843dfeb）
> 分析方式：纯源码静态阅读，未执行任何代码。所有引用为相对仓库根目录的 `file:line`。

---

## 1. 一句话定位 + 项目背景

**PentAGI（Penetration testing Artificial General Intelligence）是一个自托管的多 agent 自主渗透测试平台：用户用自然语言描述目标与意图，一个由 15 种角色 agent 组成的"虚拟安全公司"在隔离的 Docker 沙箱（Kali 镜像、20+ 专业工具）中自主规划、执行 nmap/sqlmap/Metasploit 等操作，并把结果沉淀为 pgvector 长期记忆与最终渗透报告。**

由 VXControl（迪拜安全工程公司）开发，社区版（CE）采用 EULA 许可。需要注意的一个关键事实：**本次检出的代码库已不是早期广为流传的 Python 版本，而是全面重写后的 Go + TypeScript 单体仓库**（后端 Go 761 个文件约 24.6 万行非测试代码 + 8.4 万行测试；前端 React/TS 约 9.1 万行）。早期 Python 版中的 RabbitMQ/IPC 总线、planner/assistant/attacker 命名等概念在当前代码中已被重新设计为"工具调用式委托 + 数据库消息链"架构（见第 4 节）。README 中的 "Current Capability Boundaries" 也明确说明它不是 CALDERA 式 BAS 产品。"组织/公司/工作流"概念在 CE 中体现为 flow → task → subtask 三级工作流；客户化组织/报告模板属于闭源 Enterprise 版。

## 2. 技术栈与代码规模

| 层 | 技术 | 规模 |
|---|---|---|
| 后端 | Go 1.x，Gin（REST）+ gqlgen（GraphQL + graphql-ws 订阅），sqlc（SQL 代码生成），goose（嵌入式迁移），moby/moby Docker SDK，vxcontrol/langchaingo（fork，带 reasoning/tool-call 扩展） | `backend/` 761 个 .go，非测试 246,468 行 |
| 前端 | React 18 + TypeScript + Vite，Apollo Client 4（订阅走 graphql-ws），Radix/shadcn UI，Playwright e2e + Vitest | `frontend/src` 527 个 ts/tsx，约 90,982 行 |
| 数据库 | PostgreSQL + pgvector（必需）；Neo4j（可选，经 Graphiti 服务 HTTP 间接访问） | 42 个 goose 迁移，嵌入式二进制内 |
| 可观测 | OpenTelemetry → VictoriaMetrics/Loki/Jaeger → Grafana；Langfuse（LLM 分析） | `observability/` |
| 部署 | docker-compose（pentagi 主服务 + pgvector + scraper 浏览器服务 + 可选监控栈），交互式安装 TUI（`cmd/installer`） | — |

后端包结构与职责（`backend/pkg/`，按代码量）：

| 包 | 行数（非测试） | 职责 |
|---|---|---|
| `graph/` | 56,327（大半为 gqlgen 生成） | GraphQL schema/解析器/订阅 |
| `observability/` | 60,626 | OTel + Langfuse 埋点 |
| `server/` | 46,408 | Gin 路由、REST DTO、会话/Token/OAuth 鉴权、权限 |
| `providers/` | 36,340 | LLM 适配层 + **agent 链执行引擎**（核心） |
| `tools/` | 28,233 | agent 工具调用层（terminal/file/browser/search/memory/flow_manager） |
| `database/` | 18,581 | sqlc 生成查询 + 租户辅助 |
| `controller/` | 11,325 | flow/task/subtask 运行时编排 |
| `cast/`+`csum/` | 6,342 | 消息链 AST 解析与**结构化摘要**（上下文管理核心） |
| `docker/` | 4,089 | 容器生命周期、worker 规格、沙箱隔离探测 |
| `templates/` | 2,398 | 内嵌 prompt 模板（40 个 .tmpl）+ 用户覆盖校验 |
| 其余 | — | flowfiles/resources（每 flow 文件树）、graphiti、config、password 等 |

## 3. 整体架构：模块划分、数据流、前后端 API

### 3.1 数据流（一次完整渗透的全链路）

```
用户(UI/API) ──createFlow──▶ GraphQL/REST ─▶ flowController.CreateFlow (controller/flows.go:276)
   └─异步▶ reserveFlow 落库 (controller/flow.go:142)
      └─▶ buildFlowWorker (flow.go:172)
          ├─ NewFlowToolsExecutor（绑定 DB/Docker/日志工作者）
          ├─ NewFlowProvider（LLM 选镜像/语言/起标题, providers/providers.go:299）
          ├─ 7 类日志 worker（msg/agent/search/term/vecstore/toolcall/screenshot）
          └─ go fw.worker() 常驻 goroutine (flow.go:1288)
              └─ fw.input channel 收用户输入 ▶ processInput (flow.go:1353)
                  └─ CreateTask：primary_agent 取标题 → generator 生成 subtasks
                  └─ taskWorker.Run 循环 (controller/task.go:341)
                      └─ PopSubtask → subtaskWorker.Run (controller/subtask.go:294)
                          └─ PrepareAgentChain + PerformAgentChain (providers/provider.go:636/708)
                              └─ performAgentChain 多轮工具循环 (providers/performer.go:58)
                                  ├─ terminal/file → Docker exec（Kali 沙箱）
                                  ├─ browser → scraper 服务
                                  ├─ web_search → 9 引擎编排 + fallback
                                  ├─ pentester/coder/installer/searcher/memorist/adviser
                                  │    └─ 嵌套子 agent 链（递归同引擎）
                                  └─ done/ask 屏障工具 → 终止/等待用户
                      └─ RefineSubtasks 重规划（每个 subtask 完成后）
                  └─ writeResult → reporter agent 汇总报告 (task.go:382)
UI ◀──GraphQL Subscriptions（pkg/graph/subscriptions 内存 pub/sub hub）── 每条日志/状态变更
```

### 3.2 前后端 API

- **GraphQL**（`pkg/graph/schema.graphqls`，gqlgen 生成 5.6 万行）：flow/task/subtask/provider/knowledge/resource/template 的查询与变更，以及 20+ 订阅事件（`FlowUpdated`、`MessageLogAdded`、`ToolCallLogUpdated`、`TerminalLogAdded`、`ScreenshotAdded` 等，`graph/subscriptions/controller.go:17-100`）。订阅由控制器侧的日志 worker 在每次落库时通过 `FlowPublisher` 发布，走进程内 channel + 超时（`defChannelLen=50`、`defSendTimeout=5s`）。
- **REST**（Gin + swaggo）：账户、资源上传、flow 文件下载等程序化路径；与 GraphQL 并列，Bearer API Token 鉴权。
- 前端不直接散调 Apollo：查询/订阅集中在 `src/providers/*` 挂在路由树外层；账户/资源/文件写入走 axios REST。

### 3.3 持久化即状态机

所有运行态都落 PostgreSQL：`msgchains`（完整消息链 JSON，可恢复）、`msglogs`（操作员可见消息流）、`toolcalls`（工具调用与结果）、`termlogs`（终端 stdin/stdout）、`containers`、`tasks`/`subtasks`（状态列）。进程重启后 `LoadFlows`（flows.go:253）→ `LoadFlowWorker` → `LoadTasks`/`LoadSubtasks` 按状态机恢复：running 的 subtask 重置为 created 重跑，waiting 的等待用户输入（`controller/subtask.go:89-110`）。

## 4. Agent/编排设计（重点）

### 4.1 角色清单与分层

权威列表是 `MSGCHAIN_TYPE` 枚举（`migrations/sql/20241026_115120_initial_state.sql:226-240` 及后续迁移）与 `agentByChain` 映射（`providers/agent_for_chain.go:8-24`）。每个角色绑定独立的模型配置槽位（`pconfig.ProviderConfig` 的 Simple/SimpleJSON/PrimaryAgent/Generator/Refiner/Adviser/Reflector/Searcher/Enricher/Coder/Installer/Pentester/Assistant 字段，`providers/providers.go:1119-1185` patch 逻辑），即**每个角色可以用不同的模型**（例如 generator 用廉价模型、pentester 用强模型）。

| 层 | 角色 | 职责 | 交互模式 |
|---|---|---|---|
| 编排层 | **primary_agent** | 子任务编排者："TEAM ORCHESTRATION MANAGER"，只做委派与整合，不直接跑命令 | 多轮工具链 + done/ask 屏障 |
| 规划层 | **generator** | 把用户请求拆成 ≤N 个顺序 subtask（`subtask_list` 工具） | 单轮结构化 |
| 规划层 | **refiner** | 每完成一个 subtask 后对剩余计划做增量重规划（`subtask_patch` 增/删/改/排序） | 单轮结构化 |
| 规划层 | **reporter** | 任务结束时汇总全任务执行状态生成报告（`report_result`） | 单轮 |
| 监督层 | **reflector** | 当 agent 空转（返回纯文本而非工具调用）时注入建议；模型调用连败 3 次时的"求助"兜底 | 单轮（advice 注入回主链） |
| 监督层 | **adviser + enricher** | "资深导师"：enricher 先检索记忆/上下文补充背景，adviser 再给战略建议；也被执行监控自动触发 | enricher→adviser 串联 |
| 监督层 | **tool_call_fixer** | 工具参数非法时，用 schema+错误信息让 LLM 修复参数 | 单轮 JSON |
| 监督层 | **summarizer** | 链摘要与超长工具结果压缩 | 单轮 |
| 执行层 | **pentester** | 渗透专家：持有 terminal/file/browser/web_search/记忆/图谱全套工具，在 Kali 容器内实战 | 多轮工具链 + `hack_result` 屏障 |
| 执行层 | **coder** | 开发/exploit 改写，terminal+file+代码记忆 | 多轮 + `code_result` |
| 执行层 | **installer** | 环境维护/装工具 | 多轮 + `maintenance_result` |
| 执行层 | **searcher** | 复杂检索（外部引擎+长记忆） | 多轮 + `search_result` |
| 执行层 | **memorist** | 长期记忆检索代理 | 多轮 + `memorist_result` |
| 交互层 | **assistant** | 挂在 flow 上的交互式聊天助手，与 flow 共享容器/Provider，额外持有 flow_manager 工具（get_flow_status/stop_flow/submit_flow_input/patch_flow_subtasks/wait_flow_completion），可以监督和接管自动化流程 | 流式多轮 |

### 4.2 通信机制："IPC"在 Go 重写版中的形态

当前版本**没有消息队列/进程间 IPC 总线**。agent 间通信由三个机制承担：

1. **工具调用即委托（同步嵌套调用）**：上层 agent 调用 `pentester`/`coder` 等工具（`tools/registry.go:105-107` 将其归类 `AgentToolType`），工具 handler 内部递归调用 `performAgentChain` 启动一个完整的子 agent 循环，子 agent 通过自己的屏障工具（如 `hack_result`）退出，结果经由闭包捕获的 struct 返回，再作为工具响应写回上层链。详见 10.3 链路走读。
2. **数据库为消息脊柱**：每轮消息链变更即 `updateMsgChain` 落库（`providers/performer.go:918-942`）；工具调用落 `toolcalls` 表；操作员可见叙事落 `msglogs`。任何组件崩溃后都可从 JSON 链恢复（`restoreChain`，`providers/helpers.go:469`）。
3. **进程内 pub/sub hub**（`pkg/graph/subscriptions`）：仅面向 UI 推送，不参与 agent 决策。

### 4.3 任务分解与重规划

- `NewTaskWorker`（`controller/task.go:50`）：LLM 生成任务标题 → 落库 → `GenerateSubtasks`（`controller/subtasks.go:70`）调用 generator agent（prompt 见 `templates/prompts/generator.tmpl`，要求 ≤{{.N}} 个 subtask，按 10% 环境侦察/30% 多路径实验/30% 评估选择/30% 聚焦执行的比例分布）。空计划（显式 `[]`）会降级为"原始输入作为单个 subtask"（`subtasks.go:81-88`）。
- `taskWorker.Run` 主循环（`task.go:341-376`）：`PopSubtask`（按序取一个 created 状态的 subtask，`subtasks.go:162`）→ `st.Run()` → 若任务未进入 waiting 则 `RefineSubtasks` 重新规划 → 直到队列空 → `writeResult` 生成任务报告。**subtask 严格串行执行**，循环上界 `TasksNumberLimit+3`（`provider.go:36`，=18）防失控。
- `RefineSubtasks`（`subtasks.go:106-160`）：refiner agent 基于 `<user_task><input>`、已完成 subtask 结果、剩余计划和执行状态产出 `subtask_patch` 增量操作；实现上删除所有 created 状态旧 subtask 再插入新计划。关键容错：模型类失败（`ErrAgentModelCall`）且仍有剩余 subtask 时降级为"沿用旧计划"（`subtasks.go:114-125`）。

### 4.4 循环与终止（多层防线）

`performAgentChain`（`providers/performer.go:58-280`）是所有多轮 agent 共用的引擎，其防御体系：

1. **调用预算**：general 类 agent（primary/pentester/coder/installer/assistant）默认 100 次迭代，limited 类默认 20 次（`performer.go:99-114`；`config/config.go:316-317` 的 `MAX_GENERAL_AGENT_TOOL_CALLS`/`MAX_LIMITED_AGENT_TOOL_CALLS`），超限报 `ErrAgentModelCall`。
2. **优雅收尾**：距上限还剩 `maxAgentShutdownIterations=3` 轮时不再调模型，直接注入"接近迭代上限"的固定消息，引导 agent 用屏障工具收尾（`performer.go:124-136`）。
3. **屏障终止**：`executor.IsBarrierFunction`（`performer.go:247`）——primary_agent 的 `done`、可选 `ask`；各专家的 `*_result`。屏障执行后链正常结束。
4. **重复检测**：`repeatingDetector`（`helpers.go:86-117`）按"工具名+去 message 后的参数"判连续重复，阈值 3 次触发软警告——把上一次真实工具响应原样回贴并明说"它不会变"（`helpers.go:126-136`）；累计 3+4 次后硬中止链（`performer.go:324-328`）。
5. **执行监控（导师介入）**：`executionMonitor`（`helpers.go:165-197`）统计同一工具连续调用数与总调用数，越过阈值（`EXECUTION_MONITOR_SAME_TOOL_LIMIT`/`TOTAL_TOOL_LIMIT`）即调用 adviser 生成 `<mentor_analysis>`，与原始结果包成增强响应回给 agent（`performer.go:397-412`；格式见 `primary_agent.tmpl:238-260`），随后计数复位。这是专为小模型设计的防打转机制。
6. **空转反射**：模型返回纯文本无工具调用时触发 `performReflector`（`performer.go:611-774`），reflector 建议以 human 消息注入重试；连续 3 次封顶；用 context 标记 `markReflectorRetry` 防递归（`performer.go:763-765, 798-807`）。
7. **模型调用重试**：`callWithRetries`（`performer.go:417-609`）3 次重试 + 5 秒间隔；确定性错误立即失败；重试耗尽走 caller-reflector 兜底。工具参数非法则走 `fixToolCallArgs`（`handlers.go:947`）让 tool_call_fixer agent 修参后重试（≤3 次，`performer.go:355-395`）；`ErrFlowStateGuard` 类错误（状态不匹配，非参数问题）跳过修参直接上抛（`performer.go:368-375`；哨兵定义 `tools/flow_manager.go:22`）。

### 4.5 人工介入点

- **ask 工具**（`ASK_USER` 开关，`tools.go:1061-1065`）：primary_agent 调 `ask` → 屏障返回 `PerformResultWaiting`（`provider.go:892-905`）→ subtask/task/flow 状态逐级 back-propagate 为 waiting（`subtask.go:215-235`、`task.go:274-297`）→ UI 提示 → 用户输入经 `PutInput` → `PutInputToAgentChain` 把回答写回链中 ask 工具的响应位置（`provider.go:930-946` 调 `updateMsgChainResult`）→ 恢复执行。
- **中途输入/停止/报告**：flow worker 的 input channel 语义精确区分"已接收未完成"与"未接收"（`ErrInputAccepted`/`ErrInputNotAccepted`，`flow.go:124-135`）；`Stop` 有 60 秒任务排空超时并 kill 沙箱内残留命令（`flow.go:877-915`）；`Report` 可对未完结任务按需索取中期报告（3 次重试、20s 递增退避、默认 30 分钟预算，`flow.go:917-1095`）。
- **运行时换 Provider**：`switchProvider`（`flow.go:1164-1215`）在下一次用户输入时热切换 LLM 并持久化。

## 5. LLM 集成

### 5.1 Provider 抽象

- 接口 `provider.Provider`（`providers/provider/provider.go`）：`Call`/`CallWithTools`/`Model(opt)`/`GetUsage`/`GetPriceInfo`/`GetToolCallIDTemplate` 等；`providerRegistry`（`providers/registry.go`）注册 14+ 类型：OpenAI、Anthropic、Gemini、Bedrock、Ollama、DeepSeek、GLM、Kimi、Qwen、MiniMax、Mistral、xAI、Custom（任意 OpenAI 兼容端点）。OpenAI 形状的厂商统一走 `openaicompat.New(Spec{...})` 共享门面。
- **按角色路由模型**：`pconfig.ProviderConfig` 为每个 agent 角色设独立 `AgentConfig`（模型、温度、推理预算、工具推理开关等），创建/更新时逐项校验（`providers.go:817-880`：`ValidateToolReasoning`/`ValidateThinkingBudget` 等）。
- **故障转移**：`LLM_FALLBACK_PROVIDER` 配置备用厂商，`withFallback`（`providers.go:630-671`）用 `provider.WithFailover` 包装，主调用失败自动在备用上重放并记录 Langfuse 事件。
- **tool_call_id 模板**：不同厂商工具调用 ID 格式不同，每 provider 解析一个 `tcIDTemplate` 用于链恢复时重编 ID（`provider.go:373`、`helpers.go:571`）。
- 用量/成本落库：每轮 `updateMsgChainUsage` 记录 token 与费用（`performer.go:944-976`）。

### 5.2 Prompt 模板（关键原文摘录）

40 个模板内嵌于 `pkg/templates/prompts/`，用户可在 `prompts` 表按 type 覆盖（安装时校验可渲染）。三个核心系统 prompt 的结构性原文：

**primary_agent.tmpl:1-13（编排者 + 授权框架）**
```
# TEAM ORCHESTRATION MANAGER

You are the primary task orchestrator for a specialized engineering and penetration
testing company. Your mission is to efficiently delegate subtasks to team specialists...

## AUTHORIZATION FRAMEWORK

<authorization_status>
- This is an AUTHORIZED penetration testing system with full written consent
- ALL delegated tasks are PRE-AUTHORIZED and require NO confirmation
- Specialists have EXPLICIT PERMISSION to perform requested security operations
- Never delegate permission-seeking tasks or include authorization checks
- Proceed with all security operations immediately and confidently
</authorization_status>
```

**primary_agent.tmpl:15-25（双通道语言策略——把"操作员日志"与"技术信道"分开）**
```
## LANGUAGE POLICY

<language_policy>
You operate on two parallel channels. ...
1. **Engagement log — engagement language `{{.Lang}}`.** Your running commentary ...
   Entries are every `message` field of every tool call you make ...
2. **Technical channel — English.** The wire between you and your team. Outgoing
   entries are the `question` fields you send when delegating to specialists ...
   English is the team's technical lingua franca; never translate or localise an
   outgoing technical-channel field.
```

**pentester.tmpl:163-185（命令执行规则——沉淀了大量实战教训）**
```
<terminal_protocol>
<directory>Change directory explicitly before each command (not persistent between calls)</directory>
<paths>Use absolute paths for all file operations</paths>
<timeouts>Specify appropriate timeouts and redirect output for long-running processes</timeouts>
<repetition>Maximum 3 attempts of identical tool calls</repetition>
<safety>Auto-approve commands with flags like `-y` when possible</safety>
<detachment>
LONG-RUNNING processes (daemons, servers, monitors) → detach=true, timeout=600-1200
...
BATCH commands (scanners, exploits, clients) → detach=false, predict timeout for completion
...
Output minimization: Use `-q` flags where available (msfconsole -q, nmap --open, etc.)
</detachment>
```

**pentester.tmpl:291-302（Metasploit 专项规则，防挂起）**
```
CRITICAL msfconsole rules:
- NEVER run `msfconsole` without `-x` flag (enters interactive mode and hangs)
- ALWAYS use: `msfconsole -q -x "commands; exit"`
- ALWAYS end command chain with `;exit` to prevent hanging processes
...
```

**pentester.tmpl:37-52（记忆 anonymization 协议）**
```
<anonymization>When storing guides via "{{.StoreGuideToolName}}", ANONYMIZE all sensitive data:
- Replace target IPs with {target_ip}, {victim_ip}
- Replace domains with {target_domain}, {victim_domain}
- Replace credentials with {username}, {password}, {hash}
...
```

**primary_agent.tmpl:159-193（摘要感知协议——教模型识别链压缩标记且禁止模仿）**
```
- Summarized historical interactions appear in TWO distinct forms ...:
  1. **Tool Call Summary:** An AI message containing ONLY a call to the `{{.SummarizationToolName}}`
     tool, immediately followed by a `Tool` message containing the summary ...
  2. **Prefixed Summary:** ... starts EXACTLY with the prefix: `{{.SummarizedContentPrefix}}`.
...
- NEVER mimic or copy the format of summarized content ...
- NEVER call the `{{.SummarizationToolName}}` tool yourself; it is exclusively a system marker ...
```

模板变量注入面很宽：工具名、Docker 镜像、容器端口、当前时间、执行上下文（flow/task/subtask XML）、用户文件清单、`{{.ToolPlaceholder}}`（工具 schema 占位）等，见 `provider.go:672-691` 的渲染调用。

### 5.3 上下文管理

- **链结构化摘要（csum/cast）**：`cast.ChainAST` 把消息链解析为 Section（human 头 + body 对）AST；`csum.SummarizeChain`（`csum/chain_summary.go:98-146`）三步走：① 除最后 N 节外每节压缩为单摘要对（并发执行）；② 滚动控制活跃节大小（末节预算 50KB、单 body 对 16KB、预留 25% 余量）；③ QA 对摘要（最多 10 节/64KB）。摘要以 `SummarizationToolName` 伪工具调用或 `**summarized content:**` 前缀写入链，配套上面的"摘要感知协议"。每轮工具循环后执行（`performer.go:256-278`）。
- **工具结果压缩**：terminal/browser 结果超 16KB 先 LLM 摘要、失败则头尾截断（`tools/executor.go:353-372, 425-446`），摘要 prompt 带 schema 与参数（`executor.go:511-542`）。
- **窗口预压实**：调用模型前 `compactChainForWindow`（`providers/window_compaction.go`）按模型窗口预算再压。
- **恢复时再摘要**：`restoreChain`（`helpers.go:469-630`）重写 system 消息、剥除悬挂的未应答工具调用、规范化 tool_call_id、清 reasoning，再用收紧参数摘要一遍——保证换模型/重启后链仍可送入新窗口。

## 6. 工具层

### 6.1 注册与分发

- **schema 即 Go struct**：`registryDefinitions`（`tools/registry.go:174-458`）用 `jsonschema.Reflector` 从参数 struct 反射出 JSON Schema，字段级 `jsonschema_description` 直接成为函数描述的一部分。例如 `TerminalAction`（`tools/args.go:186-192`）的 `detach` 字段描述详细到枚举了哪些命令该 detach。新增工具 = 定义 struct + 注册表条目 + 类型映射（`registry.go:98-141`）。
- **七类工具类型**：environment（terminal/file/flow_manager）、search_network（browser/web_search/各引擎）、search_vector_db、agent（委托类）、store_agent_result、store_vector_db、**barrier**（`registry.go:56-88`）。类型决定 Langfuse 观测形态与日志分类。
- **每角色一个 executor 视图**：`GetPrimaryExecutor`（`tools/tools.go:997-1068`）只给 primary_agent 7 个委托类工具 + done（+可选 ask）；`GetPentesterExecutor`（`tools.go:1296`）给 terminal/file/browser/web_search/记忆/图谱 + hack_result；coder/installer/searcher/memorist/enricher/generator/refiner/reporter/assistant 各有定制集。**工具可见性即权限边界**。
- 分发管线 `customExecutor.Execute`（`tools/executor.go:283-414`）：未知工具返回可用列表引导自纠（`executor.go:263-281`，并特判摘要标记的模仿）→ 参数反序列化 → 写 msglog（`message` 字段作为操作员叙事）→ 写 toolcalls（received）→ handler 执行 → 结果 sanitize/摘要/截断 → 更新 toolcalls → 可记忆工具异步入 pgvector → 更新 msglog result。取消后落库走 `WithoutCancel` + 10s 超时保证日志不半写（`executor.go:398-409`）。

### 6.2 外部工具的"命令构造-执行-解析"

PentAGI 不为 nmap/sqlmap 等做逐工具封装，而是**给 agent 一个真实 shell + 大量 prompt 层使用规范**：

- **terminal**（`tools/terminal.go:196-261`）：`docker exec`（TTY 模式）在 flow 专属容器内执行；命令包装 `docker.SandboxCommand/FlowCommand` 以便 Stop 时整组击杀（`terminal.go:204-207`）；`detach=true` 时 500ms 后返回"后台已启动"且用 `WithoutCancel` 保证委托超时不杀进程（`terminal.go:239-258`）；超时返回部分输出 + 结构化 HINT（建议 `timeout N` 包裹或 detach，`terminal.go:313-327`）；输出附 `[exit code: N]`，exit>128 且容器已停时标注 `[interrupted: the sandbox container stopped]`（`terminal.go:346-350`）。stdin/stdout 带 ANSI 着色写 termlogs，前端渲染为终端视图。
- **file**（`terminal.go:399-595`）：读=从容器 tar 拷出（100MB 上限）；写=打包 tar 拷入；编辑=统一 diff 在内存整体应用，任何 hunk 不匹配则不改文件（`ApplyUnifiedDiff`）。路径校验拒绝 `$()`/反引号等 shell 展开并明示"此字段不过 shell"（`terminal.go:381-397`）。
- **browser**：独立 `vxcontrol/scraper` 服务（compose 中 `SCRAPER_IMAGE`，`browser.go:337-341`），返回渲染后页面供摘要。
- **web_search**（`tools/web_search.go`）：统一入口，agent 只给 `query + mode`（links/answer/research/exploit），引擎选择对 agent 透明；`fallbackStrategy`（`web_search.go:82`）为每种 mode 定义 9 引擎有序链（exploit 模式 Sploitus 打头），瞬时错误重试、按链降级。searcher 原语层要求返回类型化错误（`searchers.Retryable/Fatal/ErrNotConfigured`）。
- **外部自定义函数**：用户可在 flow 创建时注册 HTTP 函数（`ExternalFunction`，`tools/tools.go:60-66`，含 URL/timeout/JSON Schema/适用上下文），并按 agent 上下文禁用内置工具（`DisableFunction`）。
- **prompt 层工具使用规范**：`pentester.tmpl:279-365` 内置完整工具目录（network_recon/web_testing/password_attacks/metasploit/windows_ad/post_exploit/traffic_analysis/reverse_eng/osint_search 九类）+ `cli_argument_protocol`（防幻觉 flag、跨工具 flag 假设、结构化输出、payload 引号转义）+ `tool_management_protocol`（装不上就换等效工具，最多 2 次安装尝试）。

## 7. 渗透方法论建模：从用户输入到报告

1. **立项**（flow）：LLM 三次轻量调用定语言、起标题、按任务选 Docker 镜像（`providers.go:316-371`，`image_chooser.tmpl`；策略类 `newImagePolicy` 可否决模型选择并回退默认 Kali 镜像 `providers.go:52`）。
2. **分解**（generator）：渗透特化规划原则——"Prioritize reconnaissance and information gathering early / Include explicit vulnerability identification phases / automated tools + manual verification / documentation throughout"（`generator.tmpl:153-158`）；比例化分布（10/30/30/30）。
3. **执行**（primary_agent → pentester）：pentester 收到带完整上下文的 `question`，先查 Graphiti 历史与 guide 记忆避免重复，再独立尝试，必要时再向 searcher/coder/installer/adviser 二次委托（`pentester.tmpl:262-269` 要求"Attempt to solve tasks independently BEFORE delegating"）。所有操作在 per-flow Kali 容器内。
4. **证据沉淀**：工具结果（terminal/file/搜索/委托结果）经 anonymizer 替换敏感值后分块入 pgvector（`executor.go:597-683`），agent 显式 `store_guide` 存方法论；可选 Graphiti 把每条 agent 响应与工具执行写成知识图谱 episode（`performer.go:1011-1197`）。
5. **重规划**（refiner）：每 subtask 完成后以 `<user_task><input>` 为锚点 + 已完成结果 + 执行状态重写剩余计划（`subtasks_refiner.tmpl`）。
6. **汇报**（reporter）：任务级报告渐进增强——先任务/子任务信息，不够 `msgReporterSizeLimit` 再注入 primary 链执行摘要，仍不够再注入 msglogs 摘要（`provider.go:586-616`）；产出 `report_result`（success 布尔决定 task finished/failed），写入 `tasks.result` 与 msglogs（`task.go:382-422`）。UI 支持 Markdown/PDF 导出。
7. **中断恢复**：任一环节崩溃，重启后按 DB 状态机续跑（running→created 重跑该 subtask，waiting→等用户）。

## 8. 状态与记忆

### 8.1 数据库 schema（核心表，`migrations/sql/`）

- **身份与权限**：`users`（type local/oauth、role）、`roles`/`privileges`（细到 `flows.create`、`msglogs.subscribe` 等 `<area>.<action>`，初始迁移 33-65 行）、`api_tokens`、`user_preferences`。
- **工作流**：`flows`（status/title/model/model_provider/language/functions JSON/tool_call_id_template/trace_id）→ `tasks`（input/result/status）→ `subtasks`（title/description/result/**context**——渲染好的执行上下文快照，20250412 迁移）。
- **消息与日志**：`msgchains`（type/model/usage_in/usage_out/**chain JSONB**——完整消息链）+ 后续迁移补 usage_cache/cost/duration；`msglogs`（操作员叙事，type=thoughts/terminal/file/search/advice/ask/input/done/report + result_format）；`toolcalls`（call_id/status/args JSON/result，call_id 有 hash 索引）；`termlogs`（stdin/stdout/stderr）；`searchlogs`（engine/query/link/title）；`vectorstorelogs`（记忆读写审计）；`screenshots`；assistant 专属日志表。
- **配置与资产**：`prompts`（用户级模板覆盖，type+user 唯一）、`providers`（用户自定义 LLM 配置）、`user_resources`（上传文件，hash 去重）、`knowledges`（知识库）、`flow_templates`、`containers`（primary/secondary、local_id/local_dir）。
- 高 churn 表（msglogs/toolcalls 等）专门做了 autovacuum 调参迁移（20260721）。

### 8.2 凭证/发现管理

CE 版**没有独立的凭证保管库或 findings 表**。凭证与发现以三种形态存在：① 原文留在 msgchains/toolcalls/termlogs（操作员可在 UI 审计）；② 入长期记忆前经 `anonymizer.Replacer`（`vxcontrol/cloud/anonymizer`）替换 IP/域名/凭证为占位符（`executor.go:609`），日志侧另有 `GetSecretPatterns()` 掩码 provider 密钥；③ 可选 Graphiti 图谱用固定分类法建模实体（node_labels：Host/Port/Service/Vulnerability/Credential/ValidAccess/PrivChange/AttackTechnique 等；edge：DETECTED_VULNERABILITY→CONFIRMED_VULNERABILITY→HAS_VULNERABILITY 状态升级边，`pentester.tmpl:72-76`）——这是最接近"发现管理"的结构化形态。检索侧 memorist/search_guide/`search_in_memory` 都带 flow/task/subtask 元数据过滤。

## 9. 隔离与安全

- **per-flow 容器**：`RunContainer`（`docker/client.go:242`）为每个 flow 创建命名容器（`pentagi-terminal-<flowID>`，多租户加前缀，`terminal.go:597-610`）；工作目录挂卷（命名卷或 host dir）；镜像拉取失败回退默认镜像；重启策略 OnFailure×5；json-file 日志 10m×5。
- **能力最小化**：`CapDrop: ALL` + 显式允许列表（`docker/worker_spec.go:18-44`）——Docker 默认 14 项减 MKNOD（块设备逃逸向量），加 SYS_PTRACE，NET_ADMIN 按需；注释明确禁止 SYS_ADMIN/SYS_MODULE/SYS_RAWIO/SYS_BOOT。`PidsLimit=2048` 防 fork 炸弹（`client.go:374-380`）。评估过 no-new-privileges 但因破坏 SUID 提权测试而刻意不加（`client.go:368-372` 注释）。
- **端口分配**：每 flow 按 `portsBase + (flowID*ports + i) % limit` 分配监听端口段（`client.go:119-128`），reverse shell/handler 用。
- **DinD 沙箱强验证**：启用 agent 内 Docker（`DOCKER_INSIDE=true`）时强制指向独立 daemon（`DOCKER_INSIDE_HOST`），启动时跑 `sandbox_probe.sh` 实测：daemon ID 必须不同于编排节点、拒绝一切 host-escape 请求且放行合法请求；任一不过则**关闭 agent Docker 访问并继续运行**（fail-open 业务、fail-closed 权限，`docker/sandbox.go:226-296`）。
- **网络**：支持 host 或 bridge + 自定义网络 + publicIP 绑定。
- **应用安全**：会话 Cookie（`COOKIE_SIGNING_SALT`×`TENANT_ID` 派生、HttpOnly 常开）+ Google/GitHub OAuth + API Bearer Token 三通道统一过 `auth_middleware`；授权与认证分离，路由级 `PrivilegesRequired("<area>.<action>")`；登录限速；前后端共享密码策略（bcrypt 72 字节上限）；多租户 `TENANT_ID` 贯穿容器命名/DB schema/会话。
- **局限**：目标范围（scope）约束基本停留在 prompt 层（授权框架、restrictions），terminal 工具没有 IP/域名白名单硬校验，容器网络出口不限——防越权测试主要靠部署形态（内网/专用 daemon）而非代码。

## 10. 关键代码走读（7 条核心链路）

### 10.1 链路一：Flow 创建与 worker 启动（用户输入如何变成跑起来的 agent）

1. `flowController.CreateFlow`（`controller/flows.go:276-308`）：先 `reserveFlow` 落库占位（status=created），`reserveEntry` 注册内存表，发 `FlowCreated` 订阅事件，然后 `go fc.prepareFlow(...)` **异步构建**——HTTP 请求立即返回 flowID。
2. `buildFlowWorker`（`controller/flow.go:172-337`）：取用户 → 建 Langfuse trace → `newUserPrompter`（用户 prompt 覆盖）→ `tools.NewFlowToolsExecutor` → `provs.NewFlowProvider`。
3. `NewFlowProvider`（`providers/providers.go:299-409`）内三次轻量 LLM 调用定运行参数：镜像选择（`image_chooser.tmpl` + `imagePolicy.resolve` 否决回退，providers.go:316-342）、语言（344-356）、标题（357-371），并解析 `tcIDTemplate`。
4. 组装 `flowProvider`：callCounter（工具调用流 ID）、summarizer + LRU 缓存、调用上限、执行监控构造器（399-405）。
5. 回到 buildFlowWorker：把 7 个日志 worker 注入 executor 与 provider（`flow.go:252-265`）→ `executor.Prepare`（拉起主容器）→ `commit()` → `go fw.worker()`（325-326）→ `PutInput` 送入首条输入（329）。
6. `worker()`（`flow.go:1288-1351`）：先恢复未完任务（"continue after loading"），再进入 `select { fw.input <- ... }` 循环；`processInput`（1353-1407）优先把输入投给 waiting 的任务，否则 `CreateTask` 并 `execTask`。

### 10.2 链路二：任务分解与 subtask 执行循环（编排主干）

1. `NewTaskWorker`（`controller/task.go:50-122`）：`GetTaskTitle`（LLM）→ `CreateTask` 落库 → `TaskCreated` 事件 → 写 input msglog → **同步** `GenerateSubtasks`（102 行；这是创建期内最慢的一步，代码注释专门说明 Stop 需先建好 cancel 才能打断它，见 `flow.go:1372-1380`）。
2. `GenerateSubtasks`（`controller/subtasks.go:70-104`）→ `provider.GenerateSubtasks`（`providers/provider.go:353`）→ `performSubtasksGenerator`（`performers.go:96`）：generator agent 用 `subtask_generator.tmpl`（含 previous_tasks/previous_subtasks、执行环境、用户文件）产出 `subtask_list` 工具调用，闭包捕获 `subtasks` 变量（`performers.go:120-135` 附近的 HackResult 式捕获）。
3. `taskWorker.Run`（`task.go:341-376`）：
```go
for len(tw.stc.ListSubtasks(ctx)) < providers.TasksNumberLimit+3 {
    st, err := tw.stc.PopSubtask(ctx, tw)   // 取队首 created subtask
    if st == nil { break }                    // 队列空 = 任务完成
    if err := st.Run(ctx); err != nil { ... } // 执行一个 subtask
    if tw.IsWaiting() { return nil }          // ask 屏障 → 等用户
    if err := tw.stc.RefineSubtasks(ctx); err != nil { ... } // 重规划
}
return tw.writeResult(ctx)
```
4. `subtaskWorker.Run`（`controller/subtask.go:294-353`）：置 running（状态 back-propagate 到 flow）→ `EnsureChainConsistency` → `Provider.PerformAgentChain` → 按 `PerformResultWaiting/Done/Error` 三态落 subtask 状态。
5. `PerformAgentChain`（`providers/provider.go:708-928`）：读出 msgchains 中的链 JSON → 取 6 个委托 handler + 屏障闭包 → `GetPrimaryExecutor` → `performAgentChain` 进入通用引擎。

### 10.3 链路三：一次 agent 间协作的完整时序（primary_agent 委托 pentester）

```
primary_agent 链 (performer.go:58 performAgentChain, optAgentType=primary_agent)
 │  iteration N: callWithRetries → CallWithTools(chain, 7个委托工具)
 │    模型返回 toolCall: pentester(question="...target 192.168.1.5, find RCE...")
 ▼
execToolCall (performer.go:282) → executor.Execute (executor.go:283)
 │  AgentToolType → Langfuse Agent 观测；message 字段写 msglogs
 ▼
handlers.go GetPentesterHandler 返回的闭包 → performPentester (performers.go:609)
 │  组装子工具集：adviser/coder/installer/memorist/searcher + HackResult 闭包
 │  ctx = tools.PutAgentContext(ctx, MsgchainTypePentester)  // 标记当前 agent
 │  GetPentesterExecutor (tools.go:1296)：terminal/file/browser/web_search/
 │    search_guide/store_guide/graphiti_search + hack_result(屏障)
 │  (可选) performPlanner：planner agent 先产出执行计划注入 user prompt
 │  restoreChain：新建 pentester 型 msgchain（system=pentester.tmpl, user=question）
 ▼
performAgentChain (递归同一个引擎, optAgentType=pentester)
 │  pentester 多轮：graphiti_search → terminal("nmap -sV ...") → web_search(mode=exploit)
 │    → terminal("searchsploit ...") → coder(再嵌套) → ...
 │  每轮：updateMsgChain 落库 / 超16KB结果摘要 / repeatingDetector+monitor 监控
 │  最终调用屏障 hack_result(result=英文技术报告)
 ▼
HackResult 闭包 (performers.go:654-660)：json.Unmarshal 捕获到 hackResult 变量
 │  → 返回 "hack result successfully processed" → 链正常结束
 ▼
performPentester 返回 hackResult.Result → putAgentLog(parent=primary, current=pentester)
 ▼
executor.Execute 把该字符串作为工具响应写回 primary_agent 链
 (performer.go:232-241 组装 ChatMessageTypeTool 消息) → 下一轮迭代
```

要点：委托是**同进程同步嵌套**，深度由各层调用预算独立限制；子 agent 的全部中间过程都落在自己的 msgchain/日志表中，返回给上层的是子 agent 自己撰写的结构化结果（`HackResult.Result` 要求英文技术报告 + `Message` 要求 engagement 语言日志，`tools/args.go:343-346`）。

### 10.4 链路四：终端命令执行（工具层最核心路径）

`terminal.Handle`（`terminal.go:125-194`）→ `ExecCommand`（196-261）：
```go
containerName := PrimaryTerminalName(t.tenantPrefix, t.flowID)   // :202
cmd := docker.SandboxCommand(command)                            // :204 打标签便于整组击杀
createResp, err := t.dockerClient.ContainerExecCreate(ctx, containerName,
    client.ExecCreateOptions{Cmd: cmd, AttachStdout: true, AttachStderr: true,
        WorkingDir: cwd, TTY: true})                              // :228-234
if detach {                                                       // :239-258
    detachedCtx := context.WithoutCancel(ctx)                     // 委托超时不杀后台进程
    go func() { ... getExecResult(detachedCtx, ...) }()
    select { case <-time.After(500ms): return "Command started in background..." }
}
return t.getExecResult(ctx, createResp.ID, timeout)               // :263
```
`getExecResult`（263-353）：attach 后 io.Copy 收集输出；ctx 超时时把**部分输出写 termlogs 再返回错误**（错误文本内嵌部分输出 + HINT，298-327）；完成后 `ContainerExecInspect` 取 exit code 拼进结果（346）。

### 10.5 链路五：打转检测与导师自动介入（监督回路）

1. 每次工具调用进 `execToolCall` 先过 `detector.detect(toolCall)`（`performer.go:323`；实现 `helpers.go:95-117`：比对去掉 `message` 字段后的工具名+参数序列）。
2. 第 3 次相同 → 不执行，返回 `repeatingToolResponse`（把上次真实响应截 4KB 回贴 + "It will not change. Alter the arguments..."，`helpers.go:126-136`）；第 7 次仍相同 → 硬错误终止链（`performer.go:324-328`）。
3. 执行成功后 `monitor.shouldInvokeMentor(toolCall)`（`performer.go:397`；实现 `helpers.go:175-190`：同工具连续 ≥ sameThreshold 或总调用 ≥ totalThreshold）→ `performMentor`（`performers.go:887`）调 adviser（前置 enricher）→ `formatEnhancedToolResponse` 把原始结果与 `<mentor_analysis>` 包成增强响应（`helpers.go:1130`）→ `monitor.reset()`。

### 10.6 链路六：ask 屏障 → 人工介入 → 恢复

1. primary_agent 调 `ask(message)` → 屏障 handler 置 `performResult = PerformResultWaiting`（`provider.go:892-905`）→ 屏障语义使链结束（`performer.go:247-254`）。
2. `subtaskWorker.Run` 据此 `SetStatus(waiting)`（`subtask.go:332-337`）→ back-propagate：task waiting → flow waiting（`task.go:282-285`）→ 订阅推 UI。
3. 用户在 UI 回复 → `flowWorker.processInput` 找到 waiting 任务 → `task.PutInput` → `subtask.PutInput`（`subtask.go:260-292`）→ `PutInputToAgentChain`（`provider.go:930-946`）用 `updateMsgChainResult(chain, AskUserToolName, input)` 把回答写成 ask 工具的工具响应（`helpers.go:266`）→ `runTask` 重跑该 subtask，链从中断点继续。

### 10.7 链路七：报告生成（含渐进上下文增强与重试）

1. 队列空后 `writeResult`（`task.go:382-422`）→ `GetTaskResult`（`provider.go:543-634`）：渲染 reporter 模板，**若长度不足阈值则逐级注入** primary 链执行摘要（`getTaskPrimaryAgentChainSummary`）与 msglogs 摘要（604-615）——报告质量优先的上下文装配。
2. `performTaskResultReporter`（`performers.go:22-95`）单轮链产出 `report_result`（success/result）。
3. success 布尔决定 task finished/failed（`task.go:389-394`），结果写 `tasks.result` 并以 Markdown 报告消息发布（406-415）。
4. 按需报告 `flowWorker.Report`（`flow.go:964-993`）异步起 goroutine，占任务槽、可被 Stop 打断；`runReport`（996-1095）3 次尝试、20s×n 退避、30 分钟总预算，失败原因也写成 msglog 报告告知操作员（1097-1107）。

## 11. 亮点（可借鉴设计）与弱点

### 亮点（8 条，前 5 条最值得借鉴）

1. **"agent 即工具"的统一委托原语**：所有专家都是注册表里的一个工具（`tools/registry.go` AgentToolType），委托 = 嵌套调用同一个 `performAgentChain` 引擎，返回 = 各自的屏障工具 + 闭包捕获结构化结果（`performers.go:609-704`）。不需要独立 agent 进程/消息路由，天然获得统一的重试、摘要、监控、落库与 Langfuse 追踪；新增一种专家只需 prompt 模板 + 参数 struct + executor 视图三件套。
2. **generator/refiner 动态重规划 + 屏障协议**：初始计划（比例化方法论）+ 每步后增量 patch（`subtasks.go:106`），并以 `done/ask/*_result` 屏障工具统一"如何结束"这一 agent 工程最难的问题；配合三态 `PerformResult` 与 subtask/task/flow 状态 back-propagation，人工介入（ask）与自动恢复（waiting 链回填）闭环完整。
3. **多层防打转监督栈**：重复检测（回贴真实响应而非抽象警告，`helpers.go:126`）→ 执行监控自动注入导师 `<mentor_analysis>` 增强响应（小模型救星）→ reflector 空转反射（带递归防护 context 标记）→ 每链调用预算 + 距上限 3 轮的优雅收尾消息。四层机制互补且都有真实故障案例注释支撑。
4. **结构化上下文工程**：chainAST 分节摘要（字节预算 + 保留最近 N 节 + QA 对压缩）+ 工具结果 16KB 摘要/截断 + 恢复时重摘要 + **prompt 侧"摘要感知协议"**（教模型识别压缩标记、禁止模仿/调用）——把"压缩"做成了系统与模型的双向契约；执行上下文（flow/task/subtask 状态 XML）从 DB 渲染并作为 subtask.context 快照持久化。
5. **按角色路由模型 + 故障转移 + 参数自修复**：每个 agent 角色独立 AgentConfig（可为 generator 配廉价模型、pentester 配强模型），`LLM_FALLBACK_PROVIDER` 全局热备，`tool_call_fixer` 用 schema+错误让 LLM 修参重试（`handlers.go:947`）并区分"参数错可修"与"状态错不可修"（`ErrFlowStateGuard`）。
6. **记忆分层与匿名化**：pgvector 过程记忆（工具结果自动入库，anonymizer 先替换 IP/域名/凭证）+ agent 主动 guide 存储（prompt 强制匿名化规范）+ 可选 Graphiti 时序知识图谱（固定攻击面分类法 + 漏洞状态升级边），检索均带 flow/task/subtask 元数据过滤。
7. **可验证的沙箱隔离**：worker 容器 CapDrop ALL + 白名单能力 + PidsLimit；启用 agent 内 Docker 时启动期实测独立 daemon、拒绝 host-escape 请求，验证失败则关闭该能力而非拒绝启动——fail-closed 权限 + fail-open 业务的取舍明确。
8. **全程可观测与可恢复**：msgchains 存全量链 JSON 使任意 agent 会话可精确恢复重放；toolcalls/msglogs/termlogs 三视角审计；Langfuse trace（含每次 prompt 渲染的 evaluator span）+ OTel；用量成本逐链记账。

### 弱点

1. **subtask 严格串行**（`task.go:341` 单循环 + `PopSubtask` 单取）：无法并行探测多路径，长任务墙钟时间被规划结构锁死；flow 之间也共享单进程 goroutine，无分布式队列，横向扩展名不副实。
2. **msgchains 整链 JSON 重写**：每轮迭代 `json.Marshal` 全链 + UPDATE 单行（`performer.go:918-942`），长链下 DB 写放大严重（仓库自己加了 autovacuum 调参迁移 20260721 来缓解症状）。
3. **scope 约束停留在 prompt 层**：授权框架、目标范围、禁 UDP 扫描等全是提示词约束，terminal/file 工具无目标白名单硬校验，容器出口网络不受控——对"授权范围内测试"的合规要求缺乏代码级强制。
4. **CE 无凭证/发现管理**：拿到的凭证散落在日志与链 JSON 中，无 vault、无加密存储、无生命周期管理（图谱的 Credential 节点只是可选建模）；日志掩码只覆盖 provider 密钥。
5. **委托深度与上下文复制**：每次委托都重新渲染完整 system prompt + 执行上下文并新建 msgchain，深层委托（pentester→coder→installer）token 成本层层放大；嵌套同步调用也使一个 specialist 的超时直接占住上层链。
6. **代码量与生成物负担**：gqlgen 生成 5.6 万行、依赖 fork 的 langchaingo（reasoning/工具调用扩展无法上游同步），`pkg/providers` 曾堆积 43 个测试文件（CLAUDE.md 自述），维护面大。
7. **文档与实现的边界澄清成本**：README 需专门声明"BAS 式攻击脚本属概念性未来工作"，历史 Python 版资料（IPC、planner/attacker 命名）与现 Go 版差异大，二次调研易被旧资料误导。

---

### 附：与任务预期的差异说明

任务描述中"Python、约 1000+ 源文件、IPC 总线、planner/assistant/attacker 角色、组织/公司概念"对应的是 PentAGI 早期 Python 版（v0.1，架构为 core ipc + rabbitmq）。本次磁盘上的检出版本已是官方 Go 重写版：语言为 Go/TS，通信为"工具调用式同步委托 + DB 消息链 + GraphQL 订阅"，角色体系如第 4.1 节所述。本报告以磁盘代码为准；若需对照 Python 版设计，需另行获取旧 tag（如 v0.1）。
