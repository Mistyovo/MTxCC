# 11 · Xalgorix 与 Pentest-Swarm-AI 深度代码分析报告

> 分析对象（只读，未执行任何代码）：
> 1. `refer/01-international/xalgorix/` —— 单 Agent 深度验证型 AI 渗透平台（Go + React）
> 2. `refer/01-international/Pentest-Swarm-AI/` —— 群（Swarm）多 Agent 协同渗透系统（Go）
>
> 所有代码引用为相对各项目根目录的 `file:line`。同目录 `xalgorix-official` 为完全相同副本，已忽略。

---

# 第一部分 · Xalgorix

## 1. 一句话定位与项目背景

**Xalgorix 是一个自托管的"会证明漏洞"的自主 AI 渗透测试平台**：单个 LLM Agent 按完整 22 阶段方法论对授权目标做全流程测试，并由一个**独立验证器（Verifier）对每条候选发现重新利用复现**后才进入报告——"Most scanners detect. Xalgorix proves."（README.md:20）。Apache-2.0，Go 单二进制 + 内嵌 React 仪表盘，自带 hosted 云版本（同引擎），支持 OpenAI/Anthropic/DeepSeek/Gemini/Groq/Ollama/MiniMax 及任意 OpenAI 兼容网关（LiteLLM 等）。

工程背景：这是从 v4.x 一路迭代出的成熟工程（CHANGELOG、docs/ARCHITECTURE.md 详尽），代码中大量注释直接引用线上事故（"observed in production: a wedged channel returned TIMEOUT… 12 times in a row"），是一份**长期被真实扫描数据打磨过的 Agent 运行时**，对"自主攻击 Agent 如何不跑飞"这一主题的参考价值极高。

## 2. 技术栈与代码规模

| 层 | 技术 | 规模 |
| --- | --- | --- |
| 核心（Go） | Go 1.26、stdlib `net/http`、CDP（浏览器）、chromedp 风格 wsbridge | 435 个 `.go` 文件，约 **145,000 行** |
| 前端（外围） | React + TS + Vite，`//go:embed` 打进二进制 | 约 15,500 行 TS/TSX |
| 测试 | `_test.go` 233 个（约占文件数 53%） | 覆盖 agent 循环、守卫、解析器、恢复路径 |
| 部署（外围） | Docker（--privileged 扫描沙箱）、Helm chart、systemd 服务 | — |

核心代码集中在：`internal/agent`（约 100 文件，Agent 循环/规划/验证/守卫/hooks）、`internal/llm`（provider 抽象 + XML 工具调用解析）、`internal/tools`（约 20 个工具包）、`internal/scanctx`（会话状态/账本/覆盖）、`internal/web`（约 90 文件，编排与 API）。外围：`webui/`、`deploy/`、`internal/reporting`（PDF 报告）。

## 3. 整体架构：模块划分与数据流

五层结构（docs/ARCHITECTURE.md:9-27）：

```
cmd/xalgorix（运行时入口/服务化）
  → internal/web（HTTP API + WS 遥测 + 内嵌仪表盘 + 扫描编排/持久化）
    → internal/agent（自主循环：推理→调工具→观察→重复）
      → internal/tools（工具注册表 + shell/浏览器/HTTP/文件等全部能力）
        → internal/llm + internal/providers（模型路由、key 存储、provider 目录）
横切：scanctx（每扫描隔离）、reporting（findings+PDF）、sandbox+scopeguard（安全）、config
```

数据流（单次扫描）：

1. `POST /api/scan` → `internal/web/orchestrator.go:46 runMultiScan` 按模式分派（single/DAST/wildcard/multi）；wildcard 模式先跑子域发现再逐子域扫描（orchestrator.go:948 `runWildcardTarget`，两阶段：PHASE 1 子域发现 → PHASE 2 逐个扫描 + 内存健康检查）。
2. 每扫描一个 goroutine + 一个 `scanctx.ScanContext`（会话级隔离：vuln store、notes、terminal、browser、ledger、token 计数、rate policy）。
3. Agent 主循环（agent.go:1621 `Run`）→ LLM → 解析 XML 工具调用 → 多重守卫 → 异步执行工具 → hook 后处理 → 结果回填对话。
4. `report_vulnerability` 工具触发内联验证（Phase 20），验证通过才落 `vulnstore` → 事件广播到 WS → `reporting` 生成带 CVSS 的 PDF。
5. 检查点（`execution_checkpoint`）+ `queue_state` 支持崩溃/重启后续跑。

## 4. Agent / 编排设计

### 4.1 主循环（internal/agent/agent.go:1621-2585）

`Run(targets, instruction)` 是一个**单线程对话式循环**，每轮：

- 迭代门（agent.go:1772）：`scanBudget != nil || maxIter == 0 || iter < maxIter`，且未停止、ctx 未取消。
- 预算检查（agent.go:1780 `overBudget`：时长/token/工具调用上限）→ 语义活性检查（`semanticLivenessCheck`）→ 被动侦察完成守卫。
- **Hook 体系**（hooks.go）：`OnIterationStart` / `OnNoToolResponse` / `OnToolCall` / `OnStuckCheck` / `OnToolExecute` / `OnToolResult` / `OnHealthyResponse` / `OnEmptyResponse`，返回 `HookResult{Nudge, EmitMessage, ForceSkip, StopReason, PruneContext…}`——把"引导模型回正"的策略全部从主循环剥离。directives.go 专门修复了多 hook nudge 互相覆盖的结构性 bug（合并而非首胜）。
- **LLM 调用前有界上下文**（agent.go:1842-1847）：`ageOutToolOutputs()` 把超出活跃窗口的旧工具结果替换为"检索桩"（原文仍可经 `read_tool_output` 取回），再加 `shouldPruneBeforeLLM` 主动修剪。
- **自适应温度**（agent.go:1861-1865）：默认 `TempScanner=0.0` 全确定性；卡住/重试时升到 0.2 促使 payload 变化。

### 4.2 规划-执行：结构化 Plan + 覆盖缺口

- `Plan` 是**有依赖的任务图**（planner.go:131），`Task{ID,Title,Phase,VulnClass,Endpoint,Status,DependsOn,Disposition,DispositionSurface}`（planner.go:58-91）。粗粒度是刻意设计："'test /api/users for injection' 是一个任务；'发一个单引号'是模型在任务内的事"（planner.go:54-57 注释）。
- 模型可用 `build_plan` 提交 JSON 任务数组（agent_prompt.go:152-161）；不提交则 `AutoPlanFromState`（planner.go:445）依据端点清单+技术栈自动建图；每次表面修订后 `refreshPlanForSurface`（planner.go:723）重建。
- **覆盖率即证据**：`CoverageGaps`（planner.go:358）从 `EndpointClassCoverage` 端点×漏洞类矩阵推导缺口，引擎只在看到真实覆盖证据时自动把任务标 completed——模型无法靠嘴说"测过了"。
- **类型化终止理由**（planner.go:97-105）：`not_applicable / blocked_missing_auth / blocked_missing_second_identity / blocked_unreachable / blocked_policy / exhausted / superseded`；配 `vagueDispositionReasons` 黑名单（planner.go:110-129，"probably not applicable""seems fine"等敷衍话术不能清除引擎任务）。
- **Finish 门**：未完成的 Plan 会阻止 finish 并列出剩余任务；15 次拒绝后放行但最终状态被 `scanCompletionAssessment`（completion.go:46-93）诚实标记为 `completed / completed_with_blocked_work / incomplete`——"gate 放弃 ≠ 成功"。

### 4.3 假设账本（Hypothesis Ledger）与专家分波

- `Hypothesis`（scanctx/ledger.go:110-149）：`(漏洞类 × 表面)` 假设 + 状态机 `queued→testing→proven/rejected/blocked/exhausted`（ledger.go:21-41）+ append-only `Evidence`（含 baseline/control、request/response、finding_ref）+ 置信度 + `AssignedTo` + `Attempts`。按 `class/endpoint/parameter/role` 归一化去重，原子持久化到 `ledger.json` 支持断点续扫。
- **确定性专家画像**（ledger_hooks.go:75-126）——5 个 data 驱动的非重叠 lane，各带**证据契约**与**停止规则**：
  - `recon-discovery`（早发 lane：扩展名爆破、参数挖掘、.git/.env、source map）
  - `authz-logic`（idor/bola/bfla/越权，要求双身份差分）
  - `business-logic`（竞态/重放/负数价格/批量赋值，单身份即可）
  - `injection-serverside`（rce/sqli/ssti/ssrf/xxe，要求具体利用产出或 OOB 回调归因）
  - `client-source`（xss/dom-xss/csrf/开放重定向，要求浏览器确认执行而非反射）
- **两级引擎委派**（agent.go:844-943 `maybeAutoDelegate`）：Stage E 在第 5 轮且 plan+ledger 就绪时**提前**拉起 recon-discovery（与根 Agent 的基线侦察并行，发现回流到 'Discovery Manifest' 笔记）；Stage W 在综合侦察完成后一次性拉起测试波。专家 spawn 时其任务书直接引用账本中**可调度的具体假设**（agent.go:1016 `specialistLaneBrief`："Test business logic everywhere" 变成 "H-18 business-logic POST /api/checkout"）。专家经共享 `agentsgraph.Graph` 管理（spawn/check/wait_agent 工具），finish 门在子 Agent 未收集完前保持阻塞。

### 4.4 反思与自愈

异常恢复是这个代码库最突出的部分（详见 §10 链路 B）。包括：上下文溢出强制修剪（agent.go:1890-1901）、限流渐进退避且**绝不停扫描**（agent.go:1903-1983，累计等待上限 6h 后 pause-for-resume）、协议硬重置（截断到 system prompt 重来，agent.go:2208-2226）、卡死循环检测（重复相同调用的 stuck hook）、exec 通道熔断（agent.go:113-124：连续 2 次 exec 工具硬超时后降级到 3 分钟短超时）、目标失联断路器（agent.go:137：10 分钟连续失联 + ≥3 次错误才终止，避免单次抖动误杀）。

### 4.5 终止条件（汇总）

| 条件 | 位置 |
| --- | --- |
| Plan 全部 completed/skipped（+账本无未闭环假设） | planner + completion.go:46 |
| 迭代/时长/token/工具调用预算 | agent.go:1780 `overBudget` |
| LLM 连续 25 次错误 / 12 次空响应 / N 次无工具调用 | agent.go:1986, 2017 |
| 目标失联 ≥10min 且 ≥3 连续错误 | agent.go:2434-2440 |
| 操作员停止 / ctx 取消 | 全循环 |

## 5. LLM 集成

- **Endpoint/Resolver 抽象**（llm/types.go:33-76）：`Endpoint{URL, Model, HeaderStyle(openai|anthropic|gemini), Auth(api_key|oauth_bearer), VendorOverride}`，由 `Resolver` 接口解析（catalog 感知 + legacy 回退）；web 层在扫描启动时**解析一次**并包成 `fixedResolver` 注入 per-scan client——"agent 出站流量携带的凭证恰是操作员请求所解析到的"（types.go:87-111）。
- **三协议原生适配**（client.go）：OpenAI chat-completions、Anthropic messages（含 `anthropicCacheControl` ephemeral prompt 缓存，client.go:456-460）、Gemini `generateContent`（含 `safetySettings` 关闭安全拦截、`system_instruction`、usageMetadata，client.go:410-452）。附 key 轮换（keyrotator.go）、错误分类（error_diagnostic.go：context window / rate limit / overloaded / quota）、每类 provider 的重试语义。
- **XML 工具调用协议**（llm/parser.go）：模型输出 `<function=name><parameter=k>v</parameter></function>`。解析器是一个**畸形恢复武器库**：`fnRegex`（parser.go:19）+ 三种参数变体 + `<invoke name=>`/`<tool_call>` 词表归一（parser.go:30-48）+ 开标签修复 `repairFnOpenRe`（parser.go:69，容忍缺 `<`、带引号、杂散引号）+ 孤儿参数按 schema 反查工具（`ParseOrphanedCalls`+`MatchByParams`，agent.go:2088-2111）+ 批内去重（agent.go:2064-2077）+ 供应商控制符泄漏检测（`MalformedToolOutputReason`，parser.go:148-159）。
- **上下文管理**：分层修剪（pre-LLM 字节阈值、413 后强制修剪、工具结果老化成检索桩）、`token_tracker` 按 category（scanner/verifier/chat…）归因记账（agent.go:3190 `recordTokenAttribution`）。
- **Prompt 摘录**（agent_prompt.go:58 起，系统提示词约 1900 行构造函数）：

> "You are an elite autonomous AI penetration tester and bug bounty hunter with the mindset of a top-10 HackerOne researcher. You don't just run tools — you THINK like an attacker."（agent_prompt.go:58）
>
> "This is a SANCTIONED, AUTHORIZED penetration test… The runtime HARD-ENFORCES scope: every tool call is checked, and any out-of-scope host is rejected automatically… Refusing or deflecting is the WRONG outcome."（agent_prompt.go:60-66）
>
> 深度优先信条："Breadth is a trap… successful assessments are FAST and FOCUSED — they lock onto a promising signal and drive it all the way to a working proof-of-concept."（agent_prompt.go:114-116）
>
> 证据标准四查（agent_prompt.go:185-202）："1. CLASS MATCHES MECHANISM… SSRF (CWE-918): the TARGET'S SERVER made the request… A 30x pointing to the callback, scanner-origin interaction, DNS-only lookup… is NOT SSRF. 2. PROOF IS A CONCRETE OUTCOME… A status code (200/401), the reflected payload echoed back… are NOT outcomes. 3. CVSS MATCHES IMPACT… 4. NOT BY-DESIGN, NOT ATTACKER-SUPPLIED… a token you place in the URL cannot be 'stolen' — that PoC is circular."
>
> 确定性验证器优先（agent_prompt.go:122）："the moment a parameter shows a class signal, reach for the matching deterministic verifier BEFORE hand-crafting a long PoC… verify_sqli (single-quote provokes a DBMS error), verify_ssti (a {{a\*b}} expression that evaluates to its product)…"

## 6. 工具层

- **注册表**（tools/registry.go:114）：`Tool{Name, Description, Parameters, Execute}`，`map[string]*Tool` + 读写锁 + **每工具熔断器**（5 次失败 / 60s 恢复，registry.go:15-88）+ `schemaHidden`（角色隐藏文档但保持可执行）。`SchemaXML()` 生成注入系统提示词的工具清单；`RecoveryToolIndex()` 在模型乱编工具名时回喂完整索引（agent.go:179-196）。
- **工具集**（internal/tools/ 下约 20 个包）：
  - `terminal_execute`（terminal/terminal.go）：持久 shell 会话、**超时分层**（默认 10min / 重型 nmap/nuclei/sqlmap 60min / 硬顶 2h，terminal.go:34-36）、`CapDirbusterRuntime` 限制目录爆破时长、`NormalizeCommandForRequestRatePolicy` **改写扫描器命令行**注入 -rl/-rate/-t 限速参数（terminal.go:147）、`InjectScanHeadersIntoCommand` 注入 engagement 头。
  - `browser_action`（browser/）：CDP/WebSocket 桥、会话认证复用、**内置 XSS 确认**（`verify_xss`：nonce 真在浏览器执行才算，xssverify.go）。
  - `http_request`（httpclient/）：`sendraw`、会话认证注入、响应字节上限。
  - 确定性验证器套件：`verify_sqli/verify_ssti/verify_xxe/verify_csrf/verify_path_traversal/verify_timing/verify_oob`（各自带 control+probe 差分设计；verify_oob 要求回调与原语归因）。
  - `add_note/read_notes`（notes，"Endpoint Inventory"/"Discovery Manifest" 是引擎解析的结构化笔记）、`read_ledger/claim_next_hypothesis/add_hypothesis_evidence/update_hypothesis`（ledger_tools.go）、`update_plan/build_plan`（plan_tools.go）、`spawn_agent/check_agent/wait_agent`（agentsgraph/）、`report_vulnerability`（reporting/，含 FP 拒绝：fabricated/host-bypass/oauth/smuggling 等误报模式）、`read_skill`（skills/，方法 论技能目录）、`web_search/cve_search/exploit_search`、`oob_poll`（OOB/interactsh）、`python_action`、`file_edit`、`codesearch`、`agentmail`、`proxy`。
- **执行**：`executeToolAsync`（agent.go:1497）带心跳监控与硬超时看门狗；工具结果经 hook（WAF/技术检测）后截断回填对话。

## 7. 渗透方法论建模

- **22 阶段注册表**（methodology/registry.go:31-54）是全系统唯一权威定义（规划器、报告、web、UI 同源消费），并明确："The phase NUMBER is taxonomy, not chronology"——引擎按**表面适用性**调度而非顺序执行。阶段覆盖侦察→手工发现→目录→CORS/cookie→认证会话→注入→SSRF/XXE→IDOR→API/GraphQL→上传→反序列化/RCE→竞态/业务逻辑→子域接管→开放重定向→邮件安全→云→WebSocket→CMS→BLH/内容伪造→**利用验证(20)**→**新漏洞探索(21)**→**终报(22)**。Phase 20/22 是引擎派生态，不允许作为 plan 任务（agent_prompt.go:161）。
- `XALGORIX_CHECKLIST=full|professional` 两档深度（agent_prompt.go:31-39）；Web UI 可勾选阶段子集，空集=全量（methodology/registry.go:84-97）。
- 方法论→代码的落点：① Plan 任务的 `Phase` 字段与 `classPhase`（planner.go:1121）；② 阶段限制守卫 `shouldBlockForPhaseRestriction`（agent.go:2294）把不属于所选阶段的工具调用直接拒绝；③ 报告的 methodology disposition（reporting/methodology.go）；④ skills 目录按漏洞类提供方法知识。

## 8. 状态与记忆

- **ScanContext**（scanctx/context.go）：每扫描一个全局注册的会话对象，聚合 vulnstore / notestore / ledger / terminal / browser / coverage / token_tracker / toolarchive（工具输出归档供检索桩取回）/ rate policy / request rate policy。并发扫描间零串线。
- **三层记忆**：① 对话内（有界窗口+检索桩+prune）；② 结构化笔记 notes（端点清单被 planner 解析为覆盖矩阵 ground truth）；③ 假设账本 ledger（原子持久化，重启恢复）。
- **执行检查点**（execution_checkpoint.go）：`Run` 进入即 `RestoreExecutionCheckpoint`，退出 defer 保存——崩溃后可续。
- 令牌归因（token_attribution）按 scanner/verifier/chat 分类记录，支撑预算与 UI。

## 9. 隔离与安全

- **scopeguard**（scopeguard/scopeguard.go）：`IsLocalOrListener` 的**最小阻塞原则**——只阻断"可证明属于本机"的地址（loopback/未指定/本机网卡/自监听 host:port），**刻意放行 RFC1918 与 169.254.169.254**（"agent 是扫描器，需要测 SSRF 打云元数据——那是目标的内部 IP，不是操作者的"，scopeguard.go:14-33）。`AllowLoopbackPorts` 支持 provision 模式（本机起目标应用再测），但**仪表盘自身端口永不豁免**（scopeguard.go:79-109）；`AllowLocalTargets` 全局开关在多租户部署必须关闭。
- **范围外目标守卫**（agent.go:2314-2328）：所有工具调用无条件过 `shouldBlockForOutOfScope`——从 DNS、端口扫描、关联基础设施发现的第三方主机一律拒绝并回喂"OUT-OF-SCOPE TARGET BLOCKED"。
- **sandbox/policy.go**：文件系统写允许清单（workspace/`~/.xalgorix`//tmp）+ 读拒绝清单（`~/.ssh`、`~/.aws`、`/etc/shadow`），读默认放行以便用系统字典。
- **运行时安全**：仪表盘默认绑定 127.0.0.1，外部绑定强制要求认证（server 拒绝启动）；API token 机器凭证（SHA-256 常时比较，仅 /api/* 与 WS）；登录限流+CSRF；破坏性命令提示词禁令（rm -rf/DROP TABLE 等，agent_prompt.go:178-184）+ 终端层拦截；`Path_Policy` 写拒绝计数（path_rejections）进入健康端点；`XALGORIX_LLM_MAX_INFLIGHT` 全局并发 LLM 上限。
- **provision 白盒模式**只在单一回环端口起目标应用，其余全部出范围。

## 10. 关键代码走读

### 链路 A：一次工具调用的完整守卫链（agent.go:2249-2392）

```go
for _, tc := range toolCalls {                       // agent.go:2249
    if tc.Name == "terminal_execute" {                // 命令规范化前置
        normalized, note := terminal.NormalizeCommandForRequestRatePolicy(contextID, command)
        normalized = terminal.InjectScanHeadersIntoCommand(normalized)
        normalized = terminal.CapDirbusterRuntime(normalized)   // :2259-2261
    ...
    if blocked, reason := a.shouldBlockForActivityPolicy(tc.Name, tc.Args); blocked { ... }   // :2284 主动/被动模式
    if blocked, reason := a.shouldBlockForPhaseRestriction(tc.Name, tc.Args); blocked { ... } // :2294 所选阶段
    if blocked, reason := a.shouldBlockForMissingAuthPrerequisites(...); blocked { ... }      // :2304 认证前置
    if blocked, reason := a.shouldBlockForOutOfScope(tc.Name, tc.Args); blocked { ... }        // :2320 范围硬墙
    a.state.ConsecutiveBlockedCalls = 0               // :2333 通过即清零（只有持续阻塞才升级）
    toolCallHook := a.hooks.Fire(OnToolCall, a.state, toolArgs)   // :2335 工作跟踪
    stuckResult := a.hooks.Fire(OnStuckCheck, a.state, toolArgs)  // :2352 卡死检测
    a.hooks.Fire(OnToolExecute, a.state, toolArgs)    // :2372 覆盖率只在实际执行时记账
    result, err := a.executeToolAsync(tc.Name, tc.Args)           // :2381 异步+心跳+硬超时
    toolResultHook := a.hooks.Fire(OnToolResult, a.state, resultArgs) // :2418 WAF/技术检测
```

要点：四道阻塞守卫全部**以用户消息形式回喂拒绝原因**（模型可学习），覆盖率记账刻意放在所有守卫之后（agent.go:2368-2372 注释：被 force-skip 的请求不算端点/类证据）。

### 链路 B：畸形输出的自愈阶梯（agent.go:2032-2241 + parser.go）

```go
toolCalls := llm.ParseToolCalls(responseClean)        // agent.go:2041
if len(tc.Args) == 0 && a.registry.RequiresParams(tc.Name) { continue }  // :2057 丢空参碎片
if deduped := dedupeBatchCalls(toolCalls); ...        // :2074 批内去重（防误杀健康扫描）
if len(toolCalls) == 0 {                              // :2088 孤儿参数恢复
    for _, oc := range llm.ParseOrphanedCalls(responseClean) {
        if oc.NameHint != "" { if _, exists := a.registry.Get(oc.NameHint); exists { name, ok = oc.NameHint, true } }
        if !ok { name, ok = a.registry.MatchByParams(oc.ParamNames) }   // :2104 参数集反查
```

无工具输出时（agent.go:2186-2240）：`OnNoToolResponse` hook 的 `PruneContext` 触发**协议硬重置**——对话截断到只剩 system prompt + 恢复指令（附完整工具索引，"call read_notes and read_ledger to recover saved progress"，agent.go:179-189），计数清零给模型完整恢复窗口；malformed 输出被丢弃不进历史（"persisting malformed tool-call XML teaches it to keep malforming"，agent.go:2165-2174 注释——健康轮次则重写为**规范形**再持久化，靠 few-shot 自模仿维持格式）。

### 链路 C：独立验证器（verifier.go:51-214）

```go
verifierMaxTurns = 8; verifierDeadline = 3 * time.Minute   // :44-45 有界确认，非第二次全扫
vreg := tools.NewRegistry()                                 // :60 受限只读注册表
terminal.Register(vreg); httpclient.Register(vreg); browser.Register(vreg)
notes.Register(vreg); websearch.Register(vreg); oobtool.Register(vreg)
a.registerVerifyPathTraversalTool(vreg)                     // :71 原生 ../ 段重放工具
vreg.Register(&tools.Tool{ Name: "submit_verdict", ...      // :74 唯一出口工具
  {Name: "verdict", Description: "'confirmed'…'rejected' (you can POSITIVELY show it is NOT…)…or 'inconclusive'"} })
...
res := a.execVerifierToolGuarded(vreg, tc.Name, tc.Args, deadline)  // :156 与主 Agent 同一 scope guard + 看门狗
if res.Error == "" && ... { case "http_request", "terminal_execute", ...: hasRetest = true }  // :159-161
...
if verdict.Confirmed && !hasRetest {                        // :202 防口头确认
    return …{Inconclusive: true, Reason: "verifier submitted confirmation without a successful independent re-test"}
}
```

预算耗尽还有**强制判决轮**（:178-193）：不给工具，要求基于已观察内容立刻提交 verdict，避免把真发现丢成"预算耗尽 inconclusive"。

### 链路 D：专家分波（agent.go:844-943）

```go
if !a.state.ReconLaneLaunched && a.state.Plan != nil && a.state.PlanBuilt &&
    a.state.LedgerSeeded && a.state.EndpointInventorySaved && a.state.Iteration >= 5 &&
    a.agentGraph.DelegationCount() == 0 {                  // :878-880 Stage E 早发发现 lane
    …return "🔭 ENGINE DISCOVERY LANE STARTED: … YOU continue the baseline recon NOW"  // :889
if a.state.WaveLaunched || !a.reconPhaseComplete() || a.state.Iteration < 5 || … { // :903 Stage W 门
    a.noteDelegationDefer("comprehensive recon not complete (missing: …)")           // :906 延迟原因可观测
profiles := a.eligibleSpecialistProfiles()                  // :918 authz-logic 需 ≥2 身份（:953）
…return "🚀 ENGINE DELEGATION STARTED: launched %d non-overlapping specialists … specialist completion is NOT a finish signal" // :935-942
```

配合 `spawnSpecialistProfile`（:967）把假设 `MarkAssigned` 给子 Agent（软所有权，finish 门可见 assigned-but-never-claimed 工作）。

## 11. 亮点与弱点

**亮点**
1. **验证即架构**：独立 Verifier（受限工具集 + submit_verdict 单出口 + hasRetest 反口头确认）+ 七个确定性 verify_* 工具 + 证据四查提示词 + report_vulnerability 服务端 FP 拒绝——四层防误报纵深，直接对准 LLM 渗透最大的痛点（幻觉发现）。
2. **生产级自愈运行时**：XML 工具调用的畸形修复族、协议硬重置、批内去重、exec 熔断、目标失联断路器、限流永终断策略——每条都有真实事故注释佐证，是难得的"Agent 可靠性工程"样本。
3. **引擎持有真相、模型不能自证完成**：覆盖率由工具执行证据驱动，skip 需类型化理由且敷衍话术黑名单无效，finish 有门且有诚实终态（incomplete 可见可续）。
4. **scopeguard 的克制设计**：只封"可证明是本机"的地址，放行目标的内网/云元数据——安全边界精确到语义而非一刀切私网。

**弱点**
1. 单进程单机：所有扫描共享一个 OS 用户/工作区，文件系统层只有路径策略没有强隔离（无每扫描容器/ns）；Docker `--privileged` 的推荐姿势本身就宣告了隔离靠"一次性沙箱容器"而非代码。
2. 工具调用走自定义 XML 文本协议而非 provider 原生 function-calling，兼容性靠海量正则修补维持，长期维护成本高（尽管修补本身做得极好）。
3. `internal/agent` 单包 100+ 文件、agent.go 3200 行，ScanState 字段爆炸，新人上手成本高；委派/finish/覆盖逻辑耦合较深。
4. 22 阶段方法论与漏洞类映射基本面向 Web/API，内网/主机侧（AD、横向）几无覆盖。

---

# 第二部分 · Pentest-Swarm-AI

## 1. 一句话定位与项目背景

**Pentest-Swarm-AI 是一个以"真 swarm"为卖点的开源自主 API/Web 渗透系统**：数十个 Agent 通过**共享信息素黑板（stigmergic blackboard）**协作，发现即时唤醒相关 Agent 并发工作，"attacking at machine speed"（README.md:65）。定位为开源版 XBOW（自托管、任意模型、真群体而非单 Agent 流水线）。AGPL-3.0，Armur-Ai 出品，明确致谢 PentestGPT/PentAGI/Strix/CAI 等前身。

核心主张（README.md:93-108）：多数"多 Agent"工具其实是固定顺序流水线（recon→classify→exploit→report）；本项目用三个群体智能原语区分自己——**Stigmergy**（经黑板读写协调，信息素权重偏置并衰减）、**Emergence**（攻击链无人预先编排，由黑板状态涌现）、**Decentralization**（每个 Agent 自带触发谓词，加 Agent 无需改编排器）。

## 2. 技术栈与代码规模

| 层 | 技术 | 规模 |
| --- | --- | --- |
| 核心（Go） | Go 1.24、cobra CLI、bubbletea TUI、uuid/zap | 396 个 `.go` 文件，约 **60,400 行** |
| 黑板（可选） | Postgres 16 + pgvector（SQL 内算信息素） | postgres.go 407 行；内存版 memory.go 359 行 |
| 工具链 | ProjectDiscovery Go 库（subfinder/httpx/nuclei/naabu/katana/dnsx/gau）+ nmap 子进程 | internal/tools 38 个适配器 |
| LLM | Claude/Together/OpenAI 兼容/Gemini/Ollama/LM Studio/OrcaRouter/OpenRouter/Bedrock/MuseSpark | internal/llm 10 个 provider |
| 外围 | Next.js 15 仪表盘（约 800 行，alpha）、Docusaurus 站点、chains/playbooks YAML、training/ 微调数据脚本、MCP server、GitHub Action | — |
| 测试 | `_test.go` 150 个（含 e2e、llm_eval、cybench/speed bench） | — |

核心在：`internal/swarm`（调度器+黑板+Agent 契约）、`internal/engine`（runner 与 swarm_runner）、`internal/agent/{recon,classifier,exploit,report}`（四专家）、`internal/tools`（适配器+协调器）、`internal/scope`、`internal/pipeline`（状态机/清理/FP 缓存/NVD 校验）。

## 3. 整体架构：模块划分与数据流

```
cli/（cobra 命令 + TUI launcher）
  → internal/engine.Runner.Run（顺序 5 阶段，默认 stable）   runner.go:170
  → internal/engine.Runner.RunSwarm（--swarm，黑板群体）      swarm_runner.go:111
       ├─ internal/swarm.Scheduler（并发/预算/静默看门狗）
       │    ├─ agents.ReconAgent      ← 触发: TARGET_REGISTERED
       │    ├─ agents.ClassifierAgent ← 触发: SUBDOMAIN/PORT_OPEN/SERVICE ∧ 信息素≥0.2
       │    ├─ agents.ExploitAgent    ← 触发: CVE_MATCH/MISCONFIG/HTTP_ENDPOINT/PLAYBOOK ∧ ≥0.3
       │    └─ agents.ReportAgent     ← 触发: CAMPAIGN_COMPLETE
       ├─ blackboard.MemoryBoard / PostgresBoard（findings+信息素+游标+预算）
       ├─ tools.Coordinator（38 个扫描工具并发执行，scope 挂 ctx）
       └─ exploit.Executor（scope→allowlist→safe-mode→执行 httpreq/jwt 内建动词或子进程）
外部：jev（FP 过滤+路径评分）、poc（PoC 自验证）、chains/playbooks（YAML 知识库）、cleanup registry
```

数据流：`Seed` 写入 `TARGET_REGISTERED`（swarm_runner.go:594）→ recon 展开 SUBDOMAIN/PORT_OPEN/HTTP_ENDPOINT/TECHNOLOGY/EXPLOIT_PLAYBOOK/工具直出漏洞（各带类型化信息素）→ classifier 对高信息素原始发现做 LLM 判读（tier 折扣+corroborator）→ exploit 确定性 playbook 或 LLM 攻击链（httpreq 步骤链 + `--capture` 变量穿透）→ 自适应 BOLA 扫荡（OBJECT_REF 回放）→ CAMPAIGN_COMPLETE（时间预算 20min/成本帽/静默 60s/killswitch 任一触发）→ report 查询黑板按 `publishThreshold` 过滤渲染 MD/PDF/SARIF + ROI 页脚 + PoC。

## 4. Agent / 编排设计（swarm 核心）

### 4.1 Agent 契约（swarm/agent.go:19-36）

```go
type Agent interface {
    Name() string                          // 游标/预算归因的唯一标识
    Trigger() blackboard.Predicate         // 调度器订阅的触发谓词
    MaxConcurrency() int                   // Handle 并发上限（0=1 串行）
    Handle(ctx, f Finding, board Board) error  // 处理一条 finding；error → AGENT_ERROR finding，不中止战役
}
```

注释明确要求 Handle 无状态："any state that must persist across iterations belongs on the blackboard"——**黑板即记忆，Agent 即纯函数**，这是可插拔性的来源。

### 4.2 调度器（swarm/scheduler.go）

Scheduler 自称"thin coordinator"："It does NOT plan — each agent's trigger predicate decides what it picks up. The scheduler only enforces concurrency caps, budget caps, and clean shutdown"（scheduler.go:18-20）。`Run`（:149）起四类 goroutine：

1. **完成观察者**（:162-181）：订阅 `CAMPAIGN_COMPLETE`，收到即 cancel 全体。
2. **预算执行器**（:191-223）：每 5s 轮询；超限**不直接 cancel**，而是写 `CAMPAIGN_COMPLETE`——"研究者拿到部分报告而非空目录，可以提高预算再跑"（pause-on-budget）。
3. **静默看门狗**（:232-271）：`sawActivity && inFlight==0 && 空闲≥60s` 时自行写完成信号——无中央规划器的系统必须有静默终止。
4. **每 Agent 一个分发循环**（:299-418 `runAgent`）：从上次提交游标续订（`pred.SinceID = cursor`，:302-303，exactly-once 语义）；每条 finding 依次过**每 Agent token 预算门**（超限跳过分发且**不推进游标**，加预算可续，:331-343）、**每 Agent 限速器**（防"agent 唤醒自己快过 LLM provider 排水"的病态反馈环，:97-111/353-360）、并发信号量；Handle 后**无论成败都提交游标并计费时长**（:385-387）；错误写成 `AGENT_ERROR` finding（信息素 0.3，10min 半衰期）让其他 Agent 可反应（:394-407）。

### 4.3 触发谓词与信息素门（涌现的编排）

各 Agent 的 Trigger 即编排规则：

- classifier（agents/classifier.go:51-67）：`{Types:[SUBDOMAIN,PORT_OPEN,SERVICE], MinPheromone:0.2}`，并注释**刻意不**分类 HTTP_ENDPOINT/TECHNOLOGY（"classifying every crawled URL turned the report into raw-JSON noise"）。
- exploit（agents/exploit.go:113-123）：`{Types:[CVE_MATCH,MISCONFIG,HTTP_ENDPOINT,EXPLOIT_PLAYBOOK], MinPheromone:0.3}`。
- "the instant a finding lands it wakes whichever agent it's relevant to"——阅读顺序不预设，链由黑板状态涌现（README 的核心卖点，代码兑现方式就是这两个谓词 + Write 时的订阅者 fan-out，memory.go:156-163）。

### 4.4 规划-执行-反思（exploit 侧）

- **规划**：`ExploitAgent.BuildPlan`（agent/exploit/agent.go:34-93）双源——规则链（pathBuilder）+ LLM 增强链，`mergePaths` 合并且 **LLM 失败降级到纯规则链**（:61-68）。系统提示词要求"Produce 2-4 DISTINCT candidate attack paths…NOT minor variations"（:240-246），供下游评分器择优。
- **反思/重规划**：`AdaptPlan`（:138-166）把最新执行结果回喂 LLM 决定"continue / pivot / adjust"（自适应闭环的雏形，roadmap 中的 pillar ②）。
- **自适应评分**：`JevScorer`（--jev-adaptive）实时给候选攻击路径打分，按分排序执行并给信息素定级；失败开放回退启发式排序（exploit.go:58-64）。
- **终止条件**：CAMPAIGN_COMPLETE（时间预算 `DefaultSwarmTimeBudget=20min` swarm_runner.go:649 / 成本帽 `MaxCostUSD` :263 / 静默 60s / killswitch / ctx）+ 每 Agent token 帽（默认 500k，memory.go:63）。

## 5. LLM 集成

- **Provider 接口**（llm/provider.go:11-29）：`Complete/Stream/HealthCheck/ModelName/ContextWindow/SupportsToolUse`。`CompletionRequest` 带 `CacheSystemPrompt + CacheTTL("5m"/"1h")`（:43-48），Claude 侧落 `anthropic.CacheControlEphemeralParam`（llm/claude.go:205-210）。Usage 含 cache read/creation token（provider.go:95-100）。
- **工厂**（llm/factory.go:100-287）：10 个 provider 分支；`together`/`openrouter` 有**按角色的模型混合**：`openrouterModelFor`（swarm_runner.go:67-74）——推理重的 classifier/exploit 走 `z-ai/glm-5.3-flash`，批量 recon/report 走 `deepseek-v4.1-flash`；显式 per-agent 配置永远优先（:174-198 `buildAgent`）。成本表 `llm.PricingFor` 同时被**质量分层**（见 §9）与成本表复用。
- **成本计量**：`llm.Meter` 聚合所有 provider 花费；混合模型场景按"最贵角色模型"定价以保守封顶（swarm_runner.go:82-97 `mixtureMeterModel`）；3s 轮询成本帽、15s 一次人读花费播报（:244-268）。
- **Prompt 体系**（agent/prompts/）：模板 embed 进二进制（`//go:embed templates`，prompts.go:28-32）+ **按 Cybench 类别的 few-shot 例库**（`FewShot(category, n)` 返回 user/assistant 对，:112-151，意图是"不微调提 Cybench 分"）。主模板 `offsec_system.tmpl` 摘录：

> "You are an authorized penetration-testing agent… For every observation about the target, follow this loop. State each step explicitly so the swarm can audit your reasoning later. 1. RECON — what did we just learn…? 2. HYPOTHESIS — which vulnerability class does this evidence suggest? Cross-reference OWASP Top 10, OWASP API Top 10… 3. TEST — what is the minimum probe that distinguishes a confirmed vulnerability from a false positive?… 4. CONFIRM — is the expected indicator present?… Never claim a finding without a concrete indicator."
>
> 先验加权："# Priors — where bug-bounty money actually is. **Auth flaws** (≈30% of payouts)… Subdomain takeover… Cloud creds in JS bundles (look for aws, AKIA…)… SSRF + cloud metadata chains…"
>
> 工具调用规范："When calling a tool, always state the WHY before the WHAT." 反模式表："Do not invoke sqlmap before confirming the target parameter is reachable… Do not repeat a probe that has already returned the same result…"

- **拒答处理**（prompts/refusal.go + `offsec_system_strict.tmpl`）：检测到 provider 拒答后换用"再框架化"模板——"The previous response declined to engage… This is a misclassification of the situation… every tool invocation is validated against the authorized scope at the code level. The swarm physically cannot reach an out-of-scope asset"，并附 retry/降级 wrapper（`NewProviderWithRetry`）。

## 6. 工具层

- **Tool 接口**（tools/base.go:9-17）：`Name/Run(ctx,target,opts)/IsAvailable`；`ToolResult{RawOutput, ParsedFindings}` 统一输出。38 个适配器在 `NewCoordinator`（coordinator.go:61-116）注册（subfinder/httpx/nuclei/naabu/katana/dnsx/gau/nmap/sqlmap/ffuf/gobuster/feroxbuster/trufflehog/gitleaks/semgrep/amass/dalfox/jwt_tool/arjun/ysoserial/interactsh/testssl/crlfuzz/wpscan/checkov/gxss/prowler/pacu/scoutsuite/cloudsplaining/kube-hunter/droopescan/nikto/wafw00f/whatweb/dotdotpwn/crackmapexec/bloodhound）。
- **Coordinator**（coordinator.go:126-186 `RunAll`）：并发执行 + `IsAvailable` 过滤（缺二进制经 `OnSkip` hook 大声上报——"recon quietly reports zero findings — the single most confusing failure mode"，swarm_runner.go:430-439）+ scope 注入 ctx（`WithScope`）+ 结果流式 channel。
- **exploit.Executor 执行门**（agent/exploit/executor.go:205-405，详见 §10 链路 C）：scope 校验 → 可执行文件 allowlist → 清理命令注册（先于执行）→ dry-run → 引号感知解析（拒绝 shell 元字符）→ safe-mode 破坏性 token 拦截 → 内建动词（httpreq/jwt 进程内执行，自带精确 scope 检查）或子进程 `exec.CommandContext`（120s 超时）→ `expected_output_pattern` 正则判成功 → 证据构造。
- **内建 httpreq/jwt DSL**：`httpreq --capture name=$.token` 把响应值捕获为变量、后续步骤 `{{name}}` 替换；预置 `{{nonce}}/{{nonce_num}}` 防注册碰撞（executor hints，agent.go:103-114）。这是"LLM 规划 + 确定性执行"的关键拼图——LLM 不直接跑 shell，而是产出声明式步骤。

## 7. 渗透方法论建模

- **双运行时**：顺序 5 阶段状态机（`internal/pipeline/statemachine.go`，planned→recon→classify→exploit→report，默认 stable）与 swarm 模式（alpha）并存，`--swarm` 切换且 API 兼容（swarm_runner.go:102-104 注释）。
- **知识库三层 YAML**：
  - `chains/*.yaml`（13 条 CVE 链）：`{id,name,cves,fingerprint(paths,signals),links[]{enable/verify},remediation,cvss}`。示例 log4shell（chains/log4shell-jndi-rce.yaml:23-28）："Inject a benign JNDI lookup pointing at your own OOB/collaborator host… A DNS/LDAP callback confirms the lookup fired = vulnerable. Do NOT host a malicious class or execute code — the callback alone is proof."——**指纹→安全验证→报告**的结构化方法论。
  - `playbooks/*.yaml`（8 个场景剧本：bug-bounty/external-asm/ci-cd/internal-network/ctf-solver/api-security/graphql-audit/cloud-exploitation）。
  - `config/pheromones.yaml`：方法论注意力分配的量化表达（见 §8）。
- **MITRE ATT&CK**：攻击步骤带 `technique_id`，报告侧映射 kill-chain/技术矩阵（agent/report/mitre.go、attackgraph/）。
- **方法论文本先验**：系统提示词的 "Priors"（§5）+ exploit 提示词的 BOLA/BFLA/批量赋值/JWT/SSRF-元数据/GraphQL/注入/竞态 recipes（agent.go:103-114）——把 OWASP API Top 10 的打法直接写进工具使用说明。

## 8. 状态与记忆

- **黑板即状态**（blackboard/types.go:59-74）：`Finding{ID,CampaignID,AgentName,Type,Target,Data(JSON),PheromoneBase,HalfLifeSec,SupersededBy,CreatedAt,Pheromone(读时算)}`。16 种类型横跨侦察（SUBDOMAIN/PORT_OPEN/HTTP_ENDPOINT/TECHNOLOGY）、分类（CVE_MATCH/MISCONFIG）、利用（EXPLOIT_CHAIN/RESULT/PLAYBOOK/OBJECT_REF/SESSION）、元（CAMPAIGN_COMPLETE/AGENT_ERROR）。
- **信息素衰减**：`pheromone = base * 0.5^(age/halfLife)`（memory.go:348-354）；Postgres 版落成 SQL 函数 `swarm_pheromone(pheromone_base, half_life_sec, EXTRACT(EPOCH FROM (NOW()-created_at)))`（postgres.go:137,151-152）——查询时计算、无需后台刷新。写入即 clamp 到 [0,1]（memory.go:121-130，防 MINJA 式记忆投毒用 9999 权重霸占谓词）。
- **类型化半衰期调参**（config/pheromones.yaml）：TARGET_REGISTERED 24h、PORT_OPEN 1h、SESSION **15 分钟**（token 过期快）、AGENT_ERROR 10min、EXPLOIT_RESULT 成功时升 1.0。`--exploration-bias low/med/high = ×0.7/1.0/1.3`（tuning.go:47-62）——广度 vs 深度的全局旋钮。
- **游标（cursor）**：每 (campaign,agent) 一个 `SinceID`，崩溃重启后从最后提交处续订——at-least-once 语义的群体版。
- **跨战役记忆**：`memory.MemoryStore`（memory/store.go:14-23）存 tech_stack/attack_chain/false_positive 模式（keyword 匹配召回）；Postgres 黑板带 1536 维 embedding 向量相似搜索（types.go:119-123）。`memorygraft/detector` 检测记忆投毒（MINJA/MemoryGraft 防御，roadmap Wave 3 已部分落地）。
- **Supersedes**：新发现可标记旧 finding 被取代，查询自动过滤——假设修正不产生重复噪声。

## 9. 隔离与安全

- **scope 三层**：① 域名/CIDR/端口定义 + 通配符（scope/validator.go:23-127）；② `ValidateCommand` 从命令文本**提取所有 IP/域样子串逐一校验**，"called before every command execution — no exceptions"（:143-168；@ 前缀豁免 payload 里的 email，:154-160）；③ 内建动词（httpreq）做精确 host 级校验（executor.go:208-232 注释解释了为何对 builtin 跳过宽扫——`$.posts.0.author.vehicleid` 会被误判成域名）。
- **可执行文件 allowlist**（executor.go:413-441）：LLM 只能调用注册工具适配器对应的二进制，"Blocks shell wrappers (bash, sh -c, python -c)…that a prompt injection could try to reach as a bridge to RCE"。主命令与 cleanup 命令同受约束。
- **safe-mode**：破坏性 token（rm/DROP/kill…）逐词拦截（executor.go:301-315）。**assist 模式**：每步人工 y/N（:319-365）。**dry-run**：全链路只打印不执行。
- **清理先注册**：cleanup_command 先过同一策略再注册，SIGINT/崩溃/预算取消都触发逆序清理（executor.go:256-262；swarm_runner.go:270-279 用 detached ctx 保证取消后仍清理 2 分钟）。
- **异构群体防上下文投毒**（swarm/quality/quality.go）：模型可靠性 tier 由**输出单价推导**（"frontier ≥$8 → ×1.0；economy <$1 → ×0.6"，:33-48，"self-updating proxy for capability… we only ever discount, never inflate"）；`GatedPheromone` 只折扣**模型判断**，工具观测事实不折扣——廉价模型的猜测要么被佐证要么衰减出强模型上下文。再叠 `verify.Corroborator`（classifier→exploit 交接处的主动佐证：免费黑板证据或有限付费强模型复核，swarm_runner.go:548-561）。
- **成本护栏**：战役级 hours+tokens 预算、每 Agent token 帽（500k 默认）、`MaxCostUSD` 成本帽、限速器。

## 10. 关键代码走读

### 链路 A：黑板写入与信息素（blackboard/memory.go:109-166）

```go
func (b *MemoryBoard) Write(ctx context.Context, f Finding, opts ...WriteOption) (uuid.UUID, error) {
    o := writeOpts{pheromoneBase: 1.0, halfLifeSec: 3600}
    ...
    // Clamp pheromone to [0, 1]. Defends against MINJA-style memory
    // injection where a malicious caller writes PheromoneBase=9999 to
    // dominate trigger predicates that rank by pheromone.
    if o.pheromoneBase > 1.0 { o.pheromoneBase = 1.0 }        // memory.go:125-130
    ...
    f.ID = uuid.New(); f.CreatedAt = b.now()
    b.findings = append(b.findings, f)
    if o.supersedes != nil { … b.findings[i].SupersededBy = &id }   // :146-153 假设修正
    for _, s := range b.subs {                                       // :156-163 写入即唤醒
        if b.matches(f, s.pred) { select { case s.ch <- b.withPheromone(f): default: } }
    }
```

读时衰减（:348-354）：`return f.PheromoneBase * math.Pow(0.5, age/float64(f.HalfLifeSec))`——整个群体的注意力经济学由这一个公式 + 谓词 MinPheromone 构成。

### 链路 B：调度器分发循环（scheduler.go:299-418）

```go
cursor, _ := s.board.Cursor(ctx, s.campaignID, agent.Name())
pred := agent.Trigger(); pred.SinceID = cursor          // :301-303 断点续订
ch, err := s.board.Subscribe(ctx, pred)
...
case f, ok := <-ch:
    bud, _ := s.board.AgentBudget(ctx, s.campaignID, agent.Name())
    if bud.Exceeded() { …continue }                       // :335-343 不推进游标，可续
    if lim := s.agentLimits[agent.Name()]; lim != nil { lim.Take(ctx) }  // :355-360 自激限流
    sem <- struct{}{}; inflight.Add(1); atomic.AddInt64(&s.inFlight, 1)  // :361-363
    go func(finding blackboard.Finding) {
        err := agent.Handle(spanCtx, finding, s.board)    // :382
        _ = s.board.CommitCursor(ctx, s.campaignID, agent.Name(), finding.ID)  // :386 成败都提交
        _ = s.board.UpdateBudget(ctx, s.campaignID, duration.Hours(), 0)       // :387
        if err != nil { … board.Write(…Type: TypeAgentError, PheromoneBase: 0.3 …) } // :399-407
    }(f)
```

### 链路 C：exploit 执行门（agent/exploit/executor.go:205-342）

```go
if !isBuiltinVerb(firstToken(step.Command)) {
    if err := scope.ValidateCommand(step.Command, *e.scopeDef); err != nil {  // :221 scope 硬停
        return …{Output: fmt.Sprintf("BLOCKED: %s", err), Success: false} …
if err := e.checkExecutableAllowed(step.Command); err != nil { … }           // :239 allowlist（防注入→RCE 桥）
if step.CleanupCommand != "" { e.cleanup.Register(ctx, campaignID, step.CleanupCommand, step.Name) } // :258-260 先注册
if e.dryRun { return …"[DRY RUN] Command would be: " + step.Command … }      // :265-275
parts, err := parseCommand(step.Command)   // :285 引号感知，拒绝 | > < & ; $() 反引号 换行
if e.safeMode { for _, word := range strings.Fields(part) { if _, bad := destructiveTokens[...]; bad { … } } } // :301-315
if isBuiltinVerb(parts[0]) { … return e.runHTTPReq(execCtx, parts, step, campaignID, start, vars) }  // :320-341 进程内动词
cmd := exec.CommandContext(execCtx, parts[0], parts[1:]...)                  // :367 无 shell 直执行
if step.ExpectedOutputPattern != "" { matched, _ := regexp.MatchString(...); result.Success = matched } // :387-389
```

### 链路 D：自适应 BOLA 扫荡（agents/exploit.go:226-312）

```go
// pillar ①: reuse the session this chain captured, harvest the object ids it saw,
// and replay those ids across every id-bearing endpoint — surfacing BOLA/IDOR
// the playbook never scripted.
refs := exploitpkg.HarvestObjectRefs(blob.String())           // :256 从响应收割 uuid/email/数字 id
for i, ref := range refs { if i >= 24 { break }; board.Write(…Type: TypeObjectRef…) }  // :263-272 共享状态
templates := exploitpkg.DeriveBOLATargets(a.apiEndpointURLs(ctx, board))  // :275 从黑板取 id 承载端点
hits := a.executor.SweepBOLAWithProbe(ctx, authHeader, templates, refs, a.campaignID, a.probeSink)  // :281-286
for _, hit := range hits {
    cf := pipeline.ClassifiedFinding{ Title: "Adaptive BOLA: cross-user object access via harvested id",
        Description: "…found adaptively (no scripted playbook for this endpoint).", …
        Evidence: []pipeline.Evidence{{Type: "http_response", Content: hit.Evidence, …}} }  // :288-305
    board.Write(…Type: TypeMisconfig, PheromoneBase: 1.0 …)
```

会话来源 `bearerFromVars`（:317-330）：按常见捕获名（jwt/token/auth…）找链变量，兜底识别"三段点分、>20 字符"的 JWT 形状——没有会话就干脆不扫荡（有界、best-effort）。

## 11. 亮点与弱点

**亮点**
1. **信息素黑板是真群体原语**：触发谓词 + 类型化半衰期 + 读时衰减 + Supersedes + MinPheromone 门，共同构成一个**可调参的注意力分配系统**（pheromones.yaml + exploration-bias），把"广度 vs 深度"从架构问题变成运维旋钮；游标/预算/静默看门狗解决了无中央规划器系统的终止与恢复。
2. **异构模型群体的信任经济**：按价格推导的 tier 折扣（只降不升）+ 工具观测免折扣 + 交接处 Corroborator 佐证 + 每写 clamp 防投毒——"廉价模型判断必须挣得进入强模型上下文的资格"是可直接复用的多 Agent 设计。
3. **LLM 规划与确定性执行的干净分离**：LLM 产出声明式步骤（httpreq --capture/{{var}} 链），执行器以 scope→allowlist→safe-mode→无 shell 子进程的固定门序列落地；已验证 playbook（EXPLOIT_PLAYBOOK）完全绕过 LLM 确定性重放；pause-on-budget 保证任何终止路径都产出部分报告。
4. **工程完整度**：成本计量按混合模型取最贵定价、OnSkip 大声报缺工具、清理先注册、TUI/仪表盘/事件流三视角、chains/playbooks 知识库可 PR 扩展。

**弱点**
1. swarm 模式自标 alpha：默认运行时仍是 5 阶段流水线；黑板默认内存版（Postgres 已迁移但未接主线，swarm_runner.go:281-283 注释"Memory-backed for now"），崩溃丢状态。
2. 验证深度不如 Xalgorix：exploit 结果靠 `expected_output_pattern` 正则 + 报告期可选 Jev FP 过滤，没有独立的"再利用复现"验证器；recon 工具直出的漏洞按严重度映射代表 CVSS 分（agents/recon.go:198-211）而非精确打分。
3. 触发谓词表达力有限（类型+前缀+信息素+游标四元组，exploit.go:110-112 注释自认"per-type gating and the API cap live in Handle"），复杂路由逻辑漏回 Handle 内的 ad-hoc 代码，部分抵消"加 Agent 零改编排"的主张。
4. scope 的命令文本正则提取是启发式（isCommonNonTarget 硬编码白名单 github.com/google.com 等），`ValidateCommand` 对非内建动词仍有绕过面（如十六进制 IP、重定向 DNS）；对比 Xalgorix 的解析层守卫略粗。

---

# 两项目对照速览

| 维度 | Xalgorix | Pentest-Swarm-AI |
| --- | --- | --- |
| 拓扑 | 1 协调者 + ≤5 确定性专家 lane（共享 agentsgraph+ledger） | N 个触发谓词 Agent 围绕黑板（stigmergy） |
| 编排真相 | 引擎持有的 Plan+Coverage 门，模型不能自证完成 | 无中央规划；信息素+谓词涌现 |
| 验证 | 独立 Verifier Agent + 7 个确定性 verify_* 工具 | expected_output_pattern + 可选 Jev/PoC 自验证 |
| 工具协议 | 自定义 XML 文本（重修复） | Provider 原生 tool-calling（httpreq/jwt DSL） |
| 记忆 | ledger.json + notes + 检索桩（扫描内） | 黑板（信息素/游标/Supersedes）+ pgvector（跨战役） |
| 安全模型 | scopeguard 最小阻塞 + 写允许/读拒绝路径策略 | scope 三层 + 可执行 allowlist + safe/assist/dry-run |
| 成熟度 | 单 Agent 深度验证，生产打磨充分 | 群体广度，swarm 侧 alpha |

两者恰好互补：Xalgorix 回答"怎么让一个自主攻击 Agent 可靠、诚实、不误报"，Pentest-Swarm-AI 回答"怎么让一群异构 Agent 自组织地把大攻击面嚼完并把注意力花在值钱的地方"。
