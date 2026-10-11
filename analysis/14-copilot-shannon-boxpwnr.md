# 深度技术分析：pentest-copilot / shannon / BoxPwnr

> 三套"LLM 驱动渗透测试/攻防 Agent 系统"的纯代码架构分析（只读，不执行）。代码引用格式为仓库内相对路径 `file:line`。
> 分析基准：本地克隆的当前 HEAD（pentest-copilot 为 bugbasesecurity 版，shannon 为 KeygraphHQ 版，BoxPwnr 为 0ca 版、当前已从 TypeScript 重写为 Python）。

---

# 一、pentest-copilot（Bugbase）

## 1. 一句话定位与背景

**一句话**：一个带 Web 控制台（仪表盘 + SSE 实时聊天）的全栈"自主渗透测试副驾驶"——LLM 通过 41 个函数调用工具直接在远端 Kali 攻击机上跑命令、读输出、决定下一步，单轮最多 25 次迭代循环。

背景：Bugbase 出品（作者 Dhruva Goyal 等，配套论文 *Hacking, the lazy way: LLM augmented pentesting*, arXiv:2409.09493），MIT 协议，面向真实渗透项目、boot2root 靶机和 CTF。差异化卖点是 Burp Suite / Caido 深度集成、Mythic C2 后渗透集成、浏览器自动化（Magnitude）与 VPN 管理，且可作为 MCP server 被外部 Claude Code/Codex 客户端调用。

## 2. 技术栈与代码规模

- **后端**：Node.js 22 + TypeScript + Express + MongoDB（会话/消息/漏洞库）+ Redis（暂停状态/缓存）+ SSE 流式推送。`backend/src` 约 180 个 TS 文件、约 38,500 行（`wc -l` 实测）。
- **前端**：React + JSX（约 75 个 jsx 文件）+ SCSS，仪表盘/VNC/Burp 历史查看器。
- **部署**：docker-compose（backend + frontend + Mongo + Redis + 可选内置 Kali 容器），SSH 工作主机模式挂载宿主 `~/.ssh` 只读。
- **核心文件规模**：`backend/src/services/agent.service.ts:1-1345`（Agent 主循环）、`backend/src/utils/llm/providers.ts:1-1589`（LLM 提供方抽象）、`backend/src/utils/copilot/prompts.ts:1-677`（系统提示词工厂）、`backend/src/services/swarm.manager.ts:1-1555`（多模型竞速）。

## 3. 整体架构：模块划分、数据流

```
浏览器(React/SSE) ──HTTP── Express 路由(controllers/)
                         │
                session.services / session.lifecycle（会话与 SSH ShellManager 生命周期）
                         │
                agent.service.runAgentLoop()  ←—— 核心 Agent 循环
                  ├─ context.service（上下文摘要/压缩）
                  ├─ engagement-state（结构化交战状态，注入 system prompt）
                  ├─ agent.tools.executeToolCalls → tools/registry（41 个工具）
                  │     └─ ExecutionContext: runCommand→ShellManager(SSH exec/PTY)
                  │        spawnSubagent / spawnSwarm / Burp / Caido / Mythic / Magnitude 浏览器
                  ├─ subagent.manager（后台并行子代理）
                  ├─ swarm.manager（多模型 racer 竞速 + 编排者模式）
                  └─ utils/llm/providers.invoke_llm_streaming（多 Provider 统一调用）
MongoDB 持久化消息/状态/待批准工具调用；Redis 存 agent:pause:<id>
```

数据流本质是"消息数组 + 工具结果回填"的经典 ReAct：每轮 `messages → LLM → toolCalls → 并发执行 → tool 消息回填 → 下一轮`，全部消息实时落 Mongo（`agent.service.ts:647-650` 注释解释了为何在循环顶部 flush——崩溃/重启不丢消息）。

## 4. Agent / 编排设计

**主循环**（`backend/src/services/agent.service.ts:441-1178`）：

1. 每轮先 flush 上一轮新消息到 Mongo（:647），检查 Redis 暂停位（:652）与 AbortSignal；
2. 收割已完成的 subagent/swarm 结果并注入为 user 消息（:665-698）；
3. 上下文超 70% 阈值则摘要压缩（:700-722，保留最近 8 条 + 修补孤儿 tool 消息边界 :91-99）；
4. 将结构化 `EngagementState` 以 `<engagement_state>` 块覆写进 system 消息（:725-735）；
5. 若有活跃 racer，切换为"竞速编排者模式"——注入专用 system prompt（:307-385），工具集收窄为 `get_solve_status / bump_racer / broadcast / read_racer_trace / wait` 五个（:761-763，`agentRole: "orchestrator"` 过滤）；
6. 流式调用 LLM（temperature 0.7），SSE 转发 reasoning/text/tool_call 增量（:779-821）；
7. `finishReason === "stop"` 且无工具调用 → 正常结束；编排者模式下文本-only 响应会被注入"必须调用工具"的 nudge 并自动 wait 15 秒防烧迭代（:887-903）；
8. 工具并发执行（`Promise.all`，`agent.tools.ts:341-355`）；命中审批边界的打包成 batch 挂起，状态置 `waiting_consent`，SSE `consent_required` 事件后 return（:977-1028）；用户批准后 `handleConsent` 执行并重入循环（:1221-1306）。

**角色分层**：main（全能主代理）→ subagent（同工具集并行工作者，不能再嵌套 spawn，`subagent.manager.ts:62-95` 的系统提示要求输出 FINDINGS/EVIDENCE/RECOMMENDATIONS/FILES 四段）→ racer（用户在设置里配置的多个模型副本，由 SwarmManager 竞速同一目标，编排者负责监控、 bump（注入跨 racer 情报）、广播、等 30 秒轮询）。编排者提示明确"你的迭代很贵，~80% 的回合应该是 wait"（`agent.service.ts:363-369`）。

**终止条件**：模型自然 stop、`ask_user` 工具触发（:1069-1073）、达到 `maxAgentIterations`（默认归一化，README 称 25）、审批熔断（连续 3 次或近 50 次 10 次 AI 审批拒绝即中断本轮，`tool-approval.service.ts:26-37`）、用户暂停/中止。

**反思机制**：无显式反思代理；反思体现为(a)状态管理指令要求记录"尝试过的方法及结果"，(b)摘要提示保留"为什么走这条路"的推理上下文，(c)racer 编排者读 racer trace 后 bump 纠偏。

## 5. LLM 集成

**Provider 抽象**（`backend/src/utils/llm/providers.ts`）：单一 `ProviderConfig`（provider/model/baseURL/apiKey/authMethod: api_key|oauth|subscription），`invoke_llm` / `invoke_llm_streaming` 统一入口。分支覆盖：

- Anthropic 原生 Messages（`anthropicBlocksToResult` :574-609，thinking/tool_use 块解析）；
- OpenAI Responses API（事件机 `response.output_text.delta` / `response.function_call_arguments.done` 等 :1109-1195）+ Chat Completions 兼容；
- Google / Mistral / Kimi（reasoning_effort 映射 :544-552）/ Bedrock / **codex-subscription / claude-subscription**——把已登录的 Codex CLI、Claude Code CLI 当作推理提供方（README:75-98：不复制/重放 OAuth token，官方 CLI 拥有登录态）；
- Langfuse 追踪可选（`getClient` :668-678），temperature 对固定温度模型钳制（:478-485），Anthropic thinking budget 按档位 4096→64000 映射（:449-455）。

**Prompt 原文摘录**（`backend/src/utils/copilot/prompts.ts:604-676`）：

```
<role>
You are Pentest Copilot, an autonomous penetration testing agent ... You operate on a ${boxDesc}
with direct tool access via function calls. You make decisions independently — you do not ask
for permission to run commands (except when installing new tools).
</role>
<behavior>
- Execute tools autonomously to achieve the user's goal. Do NOT ask "should I run this?" — just run it.
- When you find something interesting (open ports, services, potential vulnerabilities), investigate deeper.
- Converge toward actionable findings: prioritize exploitable vulnerabilities over information gathering.
- When multiple tools can run independently, call them in parallel — but avoid parallelizing tools
  that require user consent ...
</behavior>
<guidelines>
- Start with reconnaissance unless the user provides recon data.
- For ANY Python code, ALWAYS use run_python_script — never run Python via run_bash ...
- Destructive system commands (rm -rf /, disk wipes, shutdowns) are blocked and require explicit user approval ...
</guidelines>
```

**条件化拼装**是亮点：Burp 集成块（:436-490，含"Browser-first discovery → Burp for precision"工作流）、Mythic C2 块（:493-572，含"先验证回连可达性再造载荷"与"SOCKS pivot 循环"教范）、CTF 块（:250-363）、racer 委派块（:591-602）。CTF 部分内置 8 个类别的战术卡片（web/crypto/pwn/re/stego/forensics/misc/osint，:125-204）与按扩展名分类的文件提示（:80-123，如 `.pcap → tshark 找泄漏凭据`）和按连接串类型的"FIRST ACTION"指令（:206-248）。

**上下文管理**：`context.service.ts` —— `SUMMARIZE_THRESHOLD=0.70`、`CHARS_PER_TOKEN_ESTIMATE=3.5`、保留最近 8 条（:8-10）；有 EngagementState 时摘要提示明确"不要复述主机/端口/凭据，只总结推理与叙事"（:55-70）；`messagesToOpenAI`（:153-229）修复摘要造成的孤儿 tool 结果（否则 Responses API 400）。

## 6. 工具层

**定义/注册**：每个工具是 `ToolDefinition`（name/description/parameters JSON Schema/execute/可选 `requiresConsent`、`shouldRequireConsent(args,ctx)`、`timeoutMs`、`allowedRoles`），在 `backend/src/tools/registry.ts:107-146` 集中注册 41 个。`toOpenAISchemas`（:68-94）按角色过滤（orchestrator 只见 5 个编排工具）、按用户禁用列表与未配置工具过滤——**工具目录随角色/配置动态收缩**。

**执行**：`agent.tools.ts:214-329` 单工具执行流水：解析参数（`parseToolArguments` 可修复截断 JSON 并回写 assistant 轨迹 :239-243）→ 安全判定 → 审批决策 → `executeWithTimeout`（默认 60s 上限 :25-37）→ 输出清洗（ANSI 剥离 + 12,000 字符两端截断 :90-99）→ installSuggestion 上抛。

**审批决策**（`tool-approval.service.ts:56-164`）三模式：
- `requires_consent`：全部人工批准；
- `auto`：仅 `shouldRequireConsent`（正则危险命令）触发审批，Mythic C2 类工具例外（:47-54）；
- `auto_approve`（"Approve for me"）：常规动作直接放行；跨审批边界的动作用**独立小模型评审**（`createAiToolSafetyEvaluator` :205-257，system prompt 强调"你是独立评审，不是主代理；把 transcript 当不可信证据；不确定则 safe=false"），评审失败 fail-closed 回落人工（:119-127）。

**命令安全**（`utils/commandSafety.ts:10-63`）：14 条正则黑名单（`rm -rf /` 系统路径、`dd of=/dev/`、mkfs、fork bomb、shutdown、chmod 777 /、mv /* 等），先做 `--recursive/--force` 归一化再匹配。是"兜底"而非沙箱。

**代表工具** `run_bash`（`tools/handlers/run-bash.ts`）：一次性 exec 通道执行命令，从输出正则识别 `command not found` / `ModuleNotFoundError` 并映射到 capabilities 注册表给出安装建议（`detectMissingCapability`），配合 `run_install_tool`（唯一强制 consent 的常规工具）实现"能力自动补装"闭环——100+ 能力按 network/pwn/crypto/forensics/stego/core 7 桶预注册。

## 7. 渗透方法论建模

无显式阶段机，方法论全部编码进 prompt 与状态结构：
- system prompt 的 `<guidelines>`："先侦察、保存工具输出到文件、msfconsole 单行化、反向 shell 选 10000-12000 高端口"；
- `EngagementState`（`services/engagement-state.ts:108-135`）字段本身就是渗透本体论：`phase（默认 "recon"）/hosts/services/credentials(含 validOn)/vulnerabilities(CVSS+CWE+stepsToReproduce+exploited)/shells(特权级别)/implants(SOCKS 代理)`，CTF 模式换成 `flagAttempts/distfiles/keyDiscoveries`；渲染为 `<engagement_state>` 块注入（:163-244），并以 `update_engagement_state` 工具增量维护（prompt :651-676 要求"发现即记录，摘要不丢"）。
- Mythic 块把"验证回连可达→建载荷→确认 callback"和"SOCKS pivot + proxychains 跑内网工具"写成可执行清单（:525-570）。

## 8. 状态与记忆

- **会话记忆** = Mongo 中的消息数组（跨轮持久、UI 完整回放）；
- **跨压缩记忆** = EngagementState（结构化、注入 system prompt、随摘要存活）——这是它对抗上下文遗忘的核心手段；
- **Shell 状态**：`buildShellStatusMessage`（`agent.service.ts:171-191`）在每次摘要后重注入活跃 shell 列表（id/label/类型/状态），防止模型忘记持久会话句柄；
- 漏洞库（`vulnerability.service.ts`）+ findings-bus 供 UI 与报告复用；
- 无跨会话/跨目标记忆。

## 9. 隔离与安全

- 攻击面隔离：所有命令经 SSH 到远端攻击机执行，工作目录约束（prompt: "Do NOT delete or write outside this workspace"），可选内置 Kali 容器；
- 三层审批（模式 → 工具静态 `requiresConsent` → 动态 `shouldRequireConsent` 正则 → AI 评审），AI 评审 fail-closed + 熔断器；
- MCP 暴露带 bearer token，README 明示"等同本地管理员权限"（:138-140）；Docker 模式默认绑 127.0.0.1；
- 弱点：正则危险命令检测易绕过（编码/变量拼接）；"auto" 模式下普通命令零审查；AI 评审本身可被 transcript 注入影响（虽提示词声明不可信，仍非硬边界）。

## 10. 关键代码走读

**链路 A：一次完整 Agent 轮（含审批挂起与恢复）**
1. `agent.service.ts:779` `invoke_llm_streaming`（消息 + 工具 schema + temperature 0.7 + reasoningMode）；
2. `agent.service.ts:932` `executeToolCalls` → `agent.tools.ts:256-278`：`safetyTriggered = toolDef.shouldRequireConsent?.(args)` → `decideToolConsent`；若 `needsConsent`，返回空结果并标记；
3. `agent.service.ts:994-1027`：取第一个需审批项组装 `pendingConsent`（含 batch）写入 Mongo，SSE `consent_required`，状态 `waiting_consent`，循环退出；
4. 用户批准 → `agent.service.ts:1284-1304` 逐个 `executeConsentedTool` 执行并回填 tool 消息 → `runAgentLoop` 重入。

**链路 B：竞速编排者（多模型 swarm）**
1. 主代理调用 `spawn_swarm`（`agent.tools.ts:155-190`：配置的 racer 花名册是权威，编排策略附加到每个 racer 的 task）；
2. `agent.service.ts:737-757` 检测到活跃 swarm → `upsertRacerOrchestratorPrompt` 注入编排者 system（:324-380："你绝不自己解题；每回合必须调用工具；wait(30)→get_solve_status→决策"）；
3. 工具集切换为 orchestrator 角色 5 工具（:761-763）；编排者通过 `bump_racer`（注入洞察）/`broadcast`/`read_racer_trace` 管理竞速者；
4. 结束时 `appendSwarmResultMessages`（:410-437）把各 racer 结果与 winner 摘要以消息形式并入主对话。

**链路 C：上下文压缩 + 状态保持**
1. `context.service.ts:26-36` `shouldSummarize`：以最近一次真实 `prompt_tokens`（无则 3.5 字符/token 估算）对比模型上下文 × 0.70；
2. `summarizeMessages`（:73-151）：切分点回退避免孤儿 tool 消息（:91-99），LLM 摘要（temperature 0.3），保留 system + summary + 最近 8 条；
3. 回到主循环后 `agent.service.ts:717-721` 重注入 shell 状态消息，:725-735 重注入 engagement_state 块。

## 11. 亮点与弱点

**亮点**
1. "Approve for me" 独立 AI 审批员：fail-closed、只审边界动作、带熔断——人机权限谱系（全人工 → 自动 + 边界审批 → AI 代审）设计完整，可直接借鉴；
2. EngagementState 跨压缩结构化记忆 + 摘要后自动重注入 shell 状态，解决了长渗透会话"忘事"这一共性痛点；
3. 能力注册表 + command-not-found 反向推荐安装，把"环境准备"自动化且保留在审批边界内；
4. 订阅型 CLI（Codex/Claude Code）作为一等推理 Provider，与 API key 模式同契约。

**弱点**
1. 正则黑名单式命令安全易绕过，"auto" 模式下缺乏纵深；
2. 单一全能代理 + prompt 编码方法论，无阶段化/专业化分工，复杂目标收敛性依赖模型能力；
3. 工具输出硬截断 12k 字符可能丢关键证据；subagent/swarm 结果以纯文本注入，无结构化合并/去重。

---

# 二、shannon（Keygraph）

## 1. 一句话定位与背景

**一句话**：面向 Web 应用与 API 的白盒自主渗透测试引擎——先用 agentic 代码分析（SAST）+ 动态侦察生成"可利用性队列"，再由 5+1 个并行利用代理对运行中的目标实施真实利用，**"无利用不报告"（No exploit, no report）**，最终产出 PDF/Markdown/JSON/SARIF 报告。

背景：Keygraph 开源版（AGPL-3.0），命名致敬 Claude Shannon（"渗透的本质是信息问题：每个探测都在削减系统状态的不确定度"，README:95）。`npx @keygraph/shannon` 一条命令启动，GitHub Action/GitLab CI 集成可做发布门禁。早期基于 Claude Agent SDK，现改为 **pi harness**（`@earendil-works/pi-coding-agent`）+ **Temporal** 持久编排。

## 2. 技术栈与代码规模

- **TypeScript monorepo**（pnpm + Turborepo + Biome）：`apps/cli`（发布到 npm 的启动器，约 40 文件）+ `apps/worker`（Temporal worker + 流水线逻辑，约 160 文件），合计约 206 个 TS 源文件；
- **Temporal.io**：durable workflow 编排、崩溃恢复、可查询进度、重试策略；
- **pi harness**：agent 会话/工具/模型运行时（Node ≥22.19）；**playwright-cli**：浏览器自动化；**Typst**：PDF 渲染；
- 每次扫描一个临时 Docker worker 容器（Chaingorg Wolfi 运行时），专属 task queue 与预选 workflow ID；
- 关键文件：`apps/worker/src/temporal/workflows.ts:1-1485`（主编排）、`apps/worker/src/temporal/activities.ts`（2068 行，薄包装）、`apps/worker/src/ai/pi/pi-executor.ts:1-476`、`apps/worker/src/session-manager.ts:1-247`、提示词目录 `apps/worker/prompts/`（30+ 模板 + shared partials）。

## 3. 整体架构：模块划分、数据流

```
apps/cli（docker.ts: 起 Temporal 基础设施 + docker run 临时 worker；
         temporal-client.ts: describeScan/queryProgress/stop）
        └─ Temporal workflow: pentestPipeline（workflows.ts:361）
             ├─ preflight / validate-authentication / validate-model（短重试活动）
             ├─ Capella（agentic SAST）子工作流 —— 与渗透测试并行启动，按类 join（:936）
             ├─ Phase1 pre-recon（源码→架构基线 deliverable）
             ├─ Phase2 recon（攻击面映射 deliverable）
             ├─ Phase3/4 五类流水线并行（并发上限 5，:186）：
             │     每类 = vuln 分析代理 → reconcileClass(SAST SARIF + 动态候选 → 去重合并队列)
             │            → exploit 利用代理（读 <class>_exploitation_queue.json）
             ├─ miscellaneous 利用通道（接住五类之外的发现，:1090）
             ├─ 报告流水线：renumber → compaction → report 代理 → report.json
             │            → markdown/PDF(Typst)/SARIF 渲染 → 呈现到 run 根目录
             └─ 终态：completed | partial（带有序原因集）| failed | cancelled
代理执行栈：activities（心跳/错误分类薄包装）→ services/agent-execution.ts
            （git 检查点 + 验证 + 渲染）→ ai/pi/pi-executor.runPiPrompt（createAgentSession）
```

**代理间通信媒介是文件系统**：每个代理把交付物写到目标仓库 `.shannon/deliverables/`（如 `recon_deliverable.md`、`injection_exploitation_queue.json`），下游代理的 prompt 直接引用这些路径作为输入（见 vuln/exploit 提示词的 `<starting_context>`）。每个代理执行前后打 git 检查点（`agent-execution.ts:231-245`），支持断点续扫（resume 检测已完成代理、恢复检查点、清理不完整交付物）。

## 4. Agent / 编排设计

**固定 15 代理注册表**（`session-manager.ts:21-123`）：`pre-recon`、`recon`、5 个 `*-vuln`（injection/xss/auth/authz/ssrf）、5 个 `*-exploit`、`miscellaneous-exploit`、`report`。每项声明 `prerequisites`（文档性依赖图）、`promptTemplate`、`deliverableFilename`，外加每代理验证器（vuln 类验证 `{class}_exploitation_queue.json` 存在，:153-163；report 验证报告文件，:236-246）。

**阶段编排**（`workflows.ts:1407-1425`）：

```ts
await runSequentialPhase('pre-recon', 'pre-recon', a.runPreReconAgent);
await runSequentialPhase('recon', 'recon', a.runReconAgent);
// 五类流水线并发（MAX_CONCURRENT_PIPELINES = 5），Capella SAST settlement 并行 join
const pipelineResults = await runWithConcurrencyLimit(pipelineThunks, MAX_CONCURRENT_PIPELINES);
await miscellaneousLane;          // 第六通道
await finalizeReportPipeline();
```

每类流水线内部（:748-781）：`runVulnAgent() → reconcileClass(vulnType, settled.sarif) → runExploitAgent()`，失败计 `failedPipelines`（最多容忍 32 个非致命失败，:187），单类失败不拖垮全局，终态降级为 `partial` 并携带原因投影。

**单代理内部**（pi 会话）：主代理是"规划者/审阅者"，被提示词强制把重活委托给 `task` 子代理（见 vuln 提示词 :82-92"NEVER use the read tool for application source code analysis—delegate every code review to the task agent"）；子会话工具白名单 `CHILD_TOOLS = ['read','grep','find','ls','write','bash']`（`ai/pi/task-tool.ts:47`），禁止递归 task、禁止触碰 collector 工具（结构化输出所有权留在顶层会话）。子会话用量回传父级记账（`pi-executor.ts:285-302`）。

**终止条件**：每个提示词有 `<conclusion_trigger>` 硬性清单（todo 全 completed + 必需 collector 工具各调用一次 + 宣布 "XX COMPLETE" 后立即停止）；会话层由 pi 的 `submit_result` 类工具 `terminate: true`（`ai/submit-tool.ts:58`）显式终结；流水线层由 Temporal 重试上限与非重试错误类型（`workflows.ts:97-105`：认证/配置/目标无效/登录失败/供应商 cyber 门控）控制。

**反思**：exploit 代理的"Bypass Exhaustion Protocol"（先穷尽多种绕过技术才允许分类，见提示词 :238-254）与"假设非漏洞直到证明"的默认怀疑立场（:256）是行为级反思约束；reconciliation 则是系统级反思——静态假设必须经动态利用验证才入报告。

## 5. LLM 集成

**Harness-first**：不直接调 SDK，而是 `createAgentSession`（`pi-executor.ts:333-346`）：`model: selection.model`、`thinkingLevel: PI_THINKING_LEVEL（'high'）`、内置工具 `['read','bash','edit','write','grep','find','ls']`（:60）+ 自定义工具、`SessionManager.inMemory()`、`SettingsManager.inMemory({ retry: PI_RETRY_SETTINGS, compaction: {enabled:true} })`——**重试分层**：pi 的 agent 级循环关闭，Temporal 拥有代理重启；provider 级传输重试保留（transport 故障在会话内吸收，不付出整代理重跑的代价）。上下文压缩由 pi 内建 compaction 兜底。

**模型解析**（`ai/models.ts`）：单一模型贯穿全流程，`SHANNON_AI_MODEL=<provider>:<model-id>`（首个冒号切分，Bedrock ID 保留自身冒号）；四个策展 Provider（anthropic/openai/xai/amazon-bedrock，各用厂商原生环境变量）+ pi 目录内任意 Provider + `--models-config` 自定义模型描述文件 + `SHANNON_AI_BASE_URL` 网关覆盖；`RuntimeCredentialStore` 把凭据留在内存（临时容器中绝不落盘）；目录刷新 10 秒限时、超时回落静态目录。`SHANNON_USE_PI_AUTH=1` 可复用宿主 pi 登录（ChatGPT Plus/Pro、Grok 订阅）。

**Prompt 摘录**——`apps/worker/prompts/vuln-injection.txt:1-9`：

```
<role>
You are an Injection Analysis Specialist, an expert in **white-box code analysis and data flow
tracing** for SQLi, Command Injection, LFI/RFI, SSTI, Path Traversal, and Deserialization ...
Your primary function is to analyze how untrusted user input travels to security-sensitive sinks ...
</role>
<objective>
... Success criterion: Complete source-to-sink traces detailing path, sanitizers, sink context,
and vulnerability reason with a minimal proof-of-concept payload.
</objective>
```

`apps/worker/prompts/exploit-injection.txt:19-32`：

```
### CRITICAL: EXPLOIT DEFINITION
An exploit is executing an unauthorized or unintended action against the target application {{WEB_URL}}.
An exploit must only be considered successful if it meets this definition.
### PROOF-BASED EXPLOITATION REQUIREMENTS
- Claims require evidence ... Default to skepticism: Assume NOT vulnerable until proven otherwise
Remember: An unproven vulnerability is worse than no finding at all - it wastes resources and
creates false confidence.
```

提示词工程特征：XML 标签分区（role/objective/scope/critical/methodology/conclusion_trigger）；`@include(shared/...)` 共享分部（rules-of-engagement、code-path-rules、_shared-session 浏览器会话恢复）；变量替换 `{{TARGET_URL}}/{{LOGIN_INSTRUCTIONS}}/{{WEB_URL}}/{{PLAYWRIGHT_SESSION}}`（`services/prompt-manager.ts`）。

## 6. 工具层

pi 无内建 JSON-schema 输出与 Task/TodoWrite，shannon 全部自建：

- **task 工具**（`ai/pi/task-tool.ts:80-103`）：子会话委托，参数仅 `prompt + description`，执行模式 `parallel`（单消息多调用可并行）；子代理命名器防重名（:65-78）；
- **todo_write / glob**（`ai/pi/session-tools.ts`）：进度自管理；
- **collector 工具**（`collectors/*.ts`）：TypeBox `defineTool` 强类型结构化输出。vuln 代理 4 个一次性分节工具（`set_findings_summary/set_strategic_intelligence/set_safe_vectors/set_blind_spots`，重复调用返回 "already called" 幂等）；exploit 代理 `add_exploit`（每漏洞一次，`DuplicateError` 拒重，ID 必须来自队列否则拒绝并列出合法 ID——提示词 :353）；report 代理 `add_finding`（schema 见 `finding-collector.ts:29-56`：severity/status:exploited|out_of_scope|blocked_by_constraints|false_positive/confidence/结构化 steps( prose|code )/OWASP 2025 类目/code_locations( file+start_line+role:source|sink|guard )）；
- **submit 工具**（`ai/submit-tool.ts`）：`submit_exploitation_queue` 捕获队列 JSON，`terminate: true` 结束会话，pi 先按 schema 校验参数再执行（"captured payload is already schema-valid"，:31-33）——结构化输出 = 工具调用 + 类型校验，而非解析自由文本；
- **bash 超时扩展**（`BASH_TIMEOUT_EXTENSION_DIR`，`pi-executor.ts:84-94`）：强制有界 bash 超时，防无界命令挂死代理；
- **权限系统扩展**（`ai/pi/permission-system.ts`）：把配置的 `code_path` avoid 规则写入 `@gotgenes/pi-permission-system` 全局 deny 配置，跨所有工具与子 task 会话生效。

## 7. 渗透方法论建模

方法论同时编码在三个层面：

1. **流水线结构**：侦察 → 分类专业分析 → 调和去重 → 实证利用 → 报告，即"假设生成（SAST + 动态）→ 假设验证（利用）"的科学方法结构，README 的 mermaid 图（:263-278）直言 "No exploit demonstrated → Discard"；
2. **提示词内嵌领域方法论**：vuln-injection 的 7 步法（为每个注入源建 todo → 溯源到汇点的每条独立数据流路径 → 记录变换/净化器序列/拼接点 → 标注 slot 类型（SQL-val/like/num/enum/ident、CMD-argument、FILE-path…）→ 净化器与 sink 上下文匹配判定 → 出队或记入"已确认安全" → 置信度评分），并附 witness payload 库、假阳性清单（"参数绑定不保护标识符/关键字"、"黑名单≠安全"、"sanitize-then-concat 视为无效净化"）；exploit-injection 的 OWASP 四段攻击流（确认 → 指纹/枚举 → 定向提取）+ 利用证明 Level 1-4（Level 3 实际取数才算 exploited）+ 证据清单（payload/DB 版本/当前用户/表清单/敏感表前 5 行）；
3. **结构化 schema**：exploitation queue 的字段（source/combined_sources/path/sink_call/slot_type/sanitization_observed/concat_occurrences/verdict/mismatch_reason/witness_payload/confidence/code_locations，提示词 :100-120）本身是一套污点分析数据模型。

## 8. 状态与记忆

- **代理间记忆 = 交付物文件**：`.shannon/deliverables/*.md` 与 `*_exploitation_queue.json` 是唯一权威媒介，prompt 显式声明"single source of truth"（vuln 提示词 :42）；false positives 记到 `workspace/injection_false_positives.md`（防复测）；
- **断点续扫**：`session.json` 记录已完成代理；`loadResumeState()`（`temporal/activities.ts`）校验交付物存在性、恢复 git 检查点、清理不完整交付物；报告已有 durable 草稿则跳过重跑（`workflows.ts:1396-1399`）；
- **认证状态**：`auth-state.json`（浏览器 cookie/storage）由 preflight 单次真实登录生成，各代理 `@include(shared/_shared-session.txt)` 恢复会话、失败则自行走完整登录流；workflow 结束时删除（防 cookie 落盘残留）；
- **审计**：crash-safe 追加日志（`audit/log-stream.ts`），每代理独立日志文件投影（`actor-projection.ts`），CLI `shannon logs --agent <name>` 可 tail；顶层只有报告文件对"客户"可见，其余藏于 `.shannon/` 内部目录；
- 无跨扫描记忆（每次都是新仓库/新容器）。

## 9. 隔离与安全

- 每扫描一个临时 `docker run --rm` worker，专属 task queue + 预选 workflow ID 标签（活动永不错挂到挂了错误仓库的 worker）；目标仓库**只读挂载**；
- 出站模型流量直达用户配置的端点（"Keygraph never proxies your model traffic"），凭据仅存内存/0600 TOML；
- `rules.avoid/rules.focus`（URL 与 code_path 双维度）+ 自由文本 rules_of_engagement 注入每个提示词；code_path deny 由权限系统扩展硬执行（非仅提示）；
- 文档明示威胁模型：**不可信代码库的 prompt injection**（README:326 "Do not scan untrusted or adversarial codebases"）；Anthropic/OpenAI cyber 安全门控的预检（`services/cyber-access-verification.ts`）与 `--validate-model` 预检模式；
- 利用是真实且有副作用的（建用户、提交表单、外联请求），定位 staging/预发环境。

## 10. 关键代码走读

**链路 A：一个 vuln 代理从 Temporal 活动到 pi 会话**
1. `workflows.ts:761` `runVulnAgent()` → activities 心跳包装 → `services/agent-execution.ts:231` 为该代理路径打 git 检查点；
2. `ai/pi/pi-executor.ts:273-312`：`resolveModelSelection()` 解析模型 → `buildResourceLoader`（bash 超时扩展 + 权限系统扩展 + 非浏览器代理 noSkills）→ 组装 customTools（task/todo_write/glob + 调用方传入的 4 个分节 collector + submit 工具）；
3. `pi-executor.ts:333-346` `createAgentSession`（thinkingLevel high、pi retry 设置、compaction on）→ :356-404 订阅事件（turn_end/tool_execution_start/end/compaction_end）做审计与进度 → :407 `session.prompt(fullPrompt)` 跑到 agent_end；
4. `pi-executor.ts:413-437`：汇总父+子用量，读 `submitTool.getCaptured()` 得结构化队列 → `validateAgentOutput`（`session-manager.ts:221-225` 验证 `injection_exploitation_queue.json` 落盘）→ 成功后提交 deliverables git commit（`agent-execution.ts:328-334`）。

**链路 B：五类流水线 + SAST join + 调和**
1. `workflows.ts:936` `executeChild(capellaWorkflow)` 启动 agentic SAST 子工作流（与渗透并行，`settleCapella()` 单次结算，:200-207 注释：五个类 join 同一个 outcome，避免重复等待）；
2. `workflows.ts:774` 每类分析完成后 `reconcileClass(vulnType, settled.sarif)`：合并 SAST 候选与动态候选、去重、生成 exploitation queue（调度预算由 `reconciliationActivityOptions` :273-311 按类剩余时限动态计算）；
3. 队列非空才跑 exploit 代理（空则 `markSkipped`，:497-504）；TaskFormation 模型失败时仅对封闭的接受原因集合走单例降级（`shouldUseSingletonFallback` :242-247，防止基础设施故障被伪装成空队列成功）。

**链路 C：exploit 代理的闭环证据流**
1. 读 `injection_exploitation_queue.json` 建 todo（提示词 :140-147）→ 逐漏洞走 OWASP 三阶段；
2. 每个定论调 `add_exploit`（ID 必须命中队列，收集器拒绝非法 ID，:353；重复 ID 拒绝）；假阳性只写 `workspace/injection_false_positives.md`；
3. 渲染器把 `add_exploit` 调用组装为 `injection_exploitation_evidence.md`（提示词 :347 "The host renderer assembles ... You do NOT write the Markdown file directly"），未处理队列 ID 会出现在 "Unprocessed Vulnerabilities" 节供下游可见（:357）。

## 11. 亮点与弱点

**亮点**
1. **Temporal durable 编排 + 每扫描临时容器/队列**：崩溃恢复、可查询进度、精细到活动类型的重试/取消语义（agent 活动 TRY_CANCEL 可杀，git 状态类活动 WAIT_CANCELLATION_COMPLETED 防半写）——工程化程度三者最高；
2. **"无利用不报告"的验证闭环 + 结构化工具调用输出**：收集器 schema 即契约（ID 对齐、幂等、拒绝幻觉 ID），报告只收 `status: exploited`，CI 门禁只认实证发现；
3. **文件系统作为代理间记忆 + git 检查点续扫**：交付物即接口、可审计、可恢复；
4. 提示词里的领域方法论深度（slot 类型学、净化器-上下文匹配、利用证明分级、绕过穷尽协议）几乎是一份可教学的渗透知识库。

**弱点**
1. 单一模型贯穿全流程（无按阶段选模），弱模型会拖垮利用阶段；整跑 1-1.5 小时、成本不低；
2. 固定五类 + miscellaneous 通道，覆盖面受类目限制（无 SSRF 之外的云配置/业务逻辑等深挖类目）；
3. 基础设施重（Temporal + Docker + 临时容器），本地调试门槛高；AGPL 对二次开发约束强；
4. 白盒假设依赖源码可用（黑盒是商业版能力）。

---

# 三、BoxPwnr（0ca）

## 1. 一句话定位与背景

**一句话**：一个"即插即用"的 LLM 自动解题/打靶研究平台——用 `platform × executor × solver` 三维解耦的架构让任意 LLM（或 Claude Code/Codex/Grok/Cursor 等 CLI agent）在 Kali Docker 容器里自主解决 HackTheBox、PortSwigger、picoCTF 等 16 个平台的 4,700+ 挑战，并沉淀 10,000+ 可回放 trace 作为基准。

背景：作者 0ca，2025-01 起源于"LLM 能否自主解 HTB 机器"的实验（README "Why Now"），现在是多平台 agentic 架构的对比试验台（`--solver` 10 种 vs `--platform` 16 种），boxpwnr.info 发布解题主 trace 与排行（HTB Labs 51%、picoCTF 99.8%、XBOW 98.1% 等）。当前版本已从 TypeScript 完全重写为 Python（uv + LangChain）。

## 2. 技术栈与代码规模

- **Python 3.10+**，`src/boxpwnr` 约 90 个 py 文件、约 39,600 行；LangChain（bind_tools 工具调用）、Jinja2 提示模板、Pydantic 工具 schema、Docker（自建 Kali 镜像）、asyncio 命令执行、asciicast PTY 录制、pytest；
- 大文件：`core/llm_manager.py`（2428 行）、`solvers/claude_code.py`（2029 行）、`core/reporting.py`（1978 行）、`platforms/htb/htb_client.py`（1673 行）、`executors/docker/docker_executor.py`（1330 行）、`tools/tools.py`（1208 行）、`core/orchestrator.py`（1203 行）；
- 提示词全部 YAML 化外置：`prompts/generic_prompt.yaml` + `prompts/solvers/*.yaml` + `prompts/platforms/*/*.yaml` + `prompts/reporting/*.yaml` + `prompts/compactation/*.yaml`（Jinja2 模板继承式拼装）。

## 3. 整体架构：模块划分、数据流

四个正交概念（CLAUDE.md 原文）："**Solver 定义 agent 怎么思考，Executor 定义命令在哪跑，Platform 定义打什么目标，Orchestrator 是中央控制器**"。

```
cli.py（571 行参数解析）
  └─ Orchestrator（core/orchestrator.py:38）
       ├─ platform: BasePlatform 子类（16 个：initialize_target / validate_flag / cleanup_target）
       ├─ executor: docker(Kali) | ssh | platform(WebSocket attackbox)
       ├─ solver: LLMSolver 子类（single_loop* / hacksynth / claude_code / codex / grok /
       │          cursor_cli / kiro_cli / external）
       └─ reporting: ReportGenerator（trace 目录: config.json/stats.json/report.md/
                    conversation.json/notes.md/commands/*.txt/BoxPwnr.log）

主循环（orchestrator.py:444-645）：
  while True:
    _check_limits()（turns / cost / Deadline 时间）
    reporting.save_attempt_files()（每次昂贵 LLM 调用前落盘防丢失）
    action = solver.get_next_action()
      ├─ "flag"  → platform.validate_flag → 成功且 target_complete → 结束；
      │             部分成功（HTB user flag）→ feedback 回灌继续找 root flag（:526-536）
      ├─ "command" → executor.execute_command → solver.handle_result 回灌
      ├─ "none"(in_progress) → 工具已在 solver 内部执行，直接续跑
      └─ "error" → 按 metadata（cost/turn/time limit、no_solution）分类终态
```

## 4. Agent / 编排设计

- **single_loop 家族**（默认）：LLM 原生 function calling，一轮 = 一次 LLM 调用 + 并行工具执行（`solvers/single_loop.py:249-310` 用 `ThreadPoolExecutor` 并发执行同回应中的多个 tool_calls，结果按原序回填 ToolMessage）；`flag_found` 工具短路返回（:209-230，发现 flag 立即提交不等本轮其他工具）；非法工具参数捕获 Pydantic 校验错误并回灌"常见问题"反馈（:164-195）；无工具调用时回灌"No valid tool call was received"（:312-321）。
- **single_loop_xmltag**：旧式 `<FLAG>` XML 标签解析；**single_loop_compactation**：加双历史上下文压缩（见 §8）；**hacksynth**：planner-executor-summarizer 三段式；**claude_code/codex/grok/cursor_cli/kiro_cli**：把系统提示经环境变量 `BOXPWNR_PROMPT` 交给外部 CLI agent，BoxPwnr 退化为"目标管理 + 流式解析 `<FLAG>` 标签 + 计费监控"；**external**：任意命令（如 `claude --dangerously-skip-permissions -p "$BOXPWNR_PROMPT"`），配 `--external-timeout`。
- **终止条件**（`orchestrator.py:194-218`）：turns 上限、cost 上限（`llm_solver.calculate_cost()`，精确到 token 计费含 cache）、`Deadline` 时间上限（初始化后才起算，:429-432，且深入 LLM 重试循环内部强制——`llm_manager.py:1411-1443` 的 tenacity `wait` 前回调暂停 deadline，防止"卡在 API 重试几小时"）；平台侧 `abort_on_wrong_flag` 可一次错旗即终止（:542-545）。
- **反思机制**：`add_try_harder_message`（`llm_manager.py:2072-2085`）——"没有检测到具体可执行步骤。你不该放弃。试试更努力……" 这种硬编码 nudge；`_detect_alternating_loop`（:1977）检测模型陷入重复循环；`_detect_refusal`（:2018-2066）识别拒答模式（早期对话用更宽的 "i cannot help" 集合）。失败后 `--analyze-attempt` 用 TraceAnalyzer 写 notes.md 复盘；`--generate-progress` 生成 progress.md 供下次续跑（跨 attempt 记忆）。

## 5. LLM 集成

**LLMManager**（`core/llm_manager.py:307`，2428 行）是"交互力学层"，模型解析覆盖：直连 Claude/OpenAI/Gemini/DeepSeek/Grok、`openrouter/`、`nvidia/`（NIM）、`nvidia-web/`（Playwright 免 key）、`z-ai/`、`kimi/`、`cline/`、`kilo/`、`ollama-cloud/`、`ollama/` 前缀路由；上下文窗口三级发现（OpenRouter API → 已知模型表 → models.dev 抓取，:126-262）；计费含 cache_creation/cache_read 分类记账（`core/llm_cost_tracker.py`，543 行）。

Provider 特化处理密集：Claude thinking 配置（`_get_claude_thinking_config` :1013）、OpenAI reasoning_effort（:947）、OpenRouter reasoning 支持（:1068）、**ChatOpenCode 自定义子类**（:2087-2168，解决 Kimi K2-thinking 的 `reasoning_content` 跨轮保留与 Cline 环境注入）、孤儿 tool_call 修复（`_repair_orphaned_tool_calls` :1165）、配额耗尽解析与无限/有限重试分流（:1240-1396）。

**系统提示拼装**（`core/orchestrator.py:409` `self._get_system_prompt()`）：Jinja2 渲染 `generic_prompt.yaml`（角色 + 目标信息 + 环境 + **完整性规则** + 方法论）→ 叠加 solver 专属 yaml → 用户自定义指令 → 可选 `progress_content`（上次尝试的交接文档）。原文摘录（`prompts/generic_prompt.yaml:11-18`）：

```
# INTEGRITY RULES (CRITICAL)
You MUST solve this challenge using only your own analysis, reasoning, and the tools available
inside this environment.
- Do NOT search the web, browse the internet, or use any external services to look up writeups,
  solutions, flags, or source code for this challenge.
- Do NOT use curl, wget, or any network tools to fetch published solutions or flags from the internet.
- Using external solutions, writeups, or pre-published answers is considered cheating and
  invalidates any results.
```

以及 single_loop 求解器提示（`prompts/solvers/single_loop_prompt.yaml:10-15`）：

```
# TOOL USAGE - CRITICAL REQUIREMENT
- YOU MUST ALWAYS start your response with an explanation of your reasoning and approach
- NEVER call tools without first providing a clear explanation of what you plan to do and why
- If you discover a flag use the flag_found tool immediately
```

**上下文管理**：默认求解器不压缩（靠模型大窗口 + 精简提示）；compactation 求解器做双历史压缩（§8）；llm_manager 层做消息清洗（`_sanitize_messages` :893）。

## 6. 工具层：命令执行封装与笔记/记忆系统（重点）

**命令执行双层封装**（Codex 架构对齐，`tools/tools.py`）：

1. **一次性层**：`shell_command`（:229-307）——非交互脚本，固定 `bash -lc '<script>'` 内层登录 shell（:274，故意不暴露 login 开关"省 token 减少决策负担"），workdir 用 `shlex.quote` 防注入（:266），超时后附提示"若需持续交互请用 exec_command"（:287-297），非零退出码显式回显（:300-301）。另有 `execute_command`（旧接口）与 `python_code`（:80-150）——**把 Python 代码 base64 编码写入容器内临时文件再执行**（:119-130），彻底规避引号/反斜杠/换行的 shell 转义问题，执行后清理临时文件并保留退出码。
2. **PTY 会话层**：`exec_command`（:330-404）在 PTY 里启动命令，`yield_time` 秒后让出并返回 session ID；`write_stdin`（:425-520）向会话写字节/轮询输出——**auto-Enter 启发式**（:459-473）：若输入只含可打印字符且无换行、无控制字节，服务端自动补 `\n`（"很多模型无法在工具参数里可靠地带上换行符"）；进程退出即修剪会话防 list 膨胀（:506-512）；`list_sessions` / `close_session` 管理生命周期。提示词（single_loop_prompt.yaml:4-7）明确分工："Prefer one-shot commands via shell_command ... Use exec_command + write_stdin only for interactive or long-running programs ... start a persistent program (e.g., exec_command('bash'))"。
3. **执行器层**（`executors/docker/docker_executor.py:925-1043`）：`docker exec -t <container> bash -c <command>`（-t 支持 telnet/ftp 等交互工具），`AsyncProcessManager` 异步读双流，**输出超约 100KB 截断**（:111，防内存问题），**每条命令输出落盘**到 `attempt_dir/commands/<cmd>_<timestamp>.txt`（`_save_command_output` :1045-1089，含命令/时间戳/时长/退出码/状态头）；PTY 由 `executors/pty_manager.py` 的 `PtySessionManager` 管理（每会话 asciicast 录制，:313-434，产出可回放 .cast 文件）。

**笔记/记忆系统**（文件即记忆，三层）：

1. **attempt 内过程记忆**：`conversation.json`（全量消息 + canary 串防训练污染，`reporting.py:1239-1246`）+ `commands/*.txt` 每命令输出 + `BoxPwnr.log`；`notes.md`（`reporting.py:1246-1252`）是**留给人类研究者的空白笔记模板**（"Remove everything in this file and add your notes about this attempt ... You can reference issues with #ISSUENUMBER"），`TraceAnalyzer.analyze_attempt`（`utils/trace_analyzer.py:115-160`）在 `--analyze-attempt` 时用 LLM 复盘失败尝试并写入 notes.md（编排器 :1198 提示 "See notes.md for details"）。
2. **跨 attempt 记忆（progress.md 交接）**：失败/中断时 `--generate-progress` 用 `prompts/reporting/progress_handoff.yaml` 的结构化交接提示让 LLM 生成八节交接文档（Target Information / Discoveries / Vulnerabilities Identified / Attack Vectors Attempted / **Dead Ends (DO NOT RETRY)** / Current State / Recommended Next Steps / Key Insights，含"写实际值不写占位符"、"无内容写 None identified 不许省略节"等格式规则）；`--resume-from progress.md` 把它注入下次系统提示的 `PREVIOUS ATTEMPT CONTEXT` 块（generic_prompt.yaml:31-37："Use this information to build upon previous progress and avoid redundant work. You may re-evaluate approaches if you have new insights"）。
3. **compactation 求解器的双历史**（`solvers/single_loop_compactation.py`）：`full_history`（完整不删，供报告）与工作历史（送 LLM）分离；token 用量达上下文 60%（且消息 >20）触发压缩（:122-140）；**关键消息关键词保全**（:157-186）：内容含 `<flag>`、`password/credential/root/shell/access gained/vulnerability found/exploit successful/key found/login successful/authenticated` 的消息永不被压缩；压缩实现为"全量摘要进单条 HumanMessage"（:238-312，system + `[SUMMARY OF PREVIOUS CONVERSATION]` + "Please continue from where we left off"），从根上避免孤儿 ToolMessage 破坏 API 交替约束；压缩事件本身作为带 metadata 的标记消息进入 full_history 供报告呈现 token 前后对比（:292-309）。

**辅助工具**：`read_file`（cat + 行号，:606-645）、`grep`（-rnC2 -I，exit 1 视为无匹配，:653-694）、`file_search`（find）、`web_search`（DuckDuckGo，**反作弊查询过滤**：封禁 `writeup/write-up/solution/walkthrough/ctf/challenge/htb` 词 + 平台别名 + 目标名按 PascalCase/snake_case 拆词匹配，命中即拒答 "Do not cheat or search for solutions/writeups"，:770-846）、`apply_patch`（Codex 补丁格式，含模糊匹配容错，:853-1170，**默认禁用**——工厂函数注释 :1196 "LLMs struggle to use this one"）。

## 7. 渗透方法论建模

方法论极简（研究平台取向，让模型自己发挥）：generic prompt 只有"分解问题、逐组件测试、偏好简单可调试命令"；平台指令补目标语义（如 `prompts/platforms/htb/machine_instructions.yaml`："Your goal is to find and submit the user.txt and root.txt flags"，注入目标 IP/攻击机 IP/平台备注）。solver 提示补执行纪律（先解释再调工具、并行独立操作、超时意识、flag 即交）。真正的"方法论承载"在 **trace 沉淀**：reporting 生成攻击图（mermaid，`prompts/reporting/attack_graph_*.yaml`）、成功/失败/中断三态摘要、progress handoff——把"方法论"外化为可回放数据而非提示词约束。这与 shannon（提示词内嵌厚方法论）形成两极。

## 8. 状态与记忆（汇总）

| 层 | 载体 | 说明 |
|---|---|---|
| 会话内 | llm_manager.history | LangChain 消息列表，孤儿修复 + 清洗 |
| 会话内（compactation） | full_history vs 工作历史 | 双历史，报告完整性不受压缩影响 |
| attempt 内 | conversation.json / commands/*.txt / *.cast / BoxPwnr.log | 全量过程证据，boxpwnr.info 可回放 |
| attempt 内（人类） | notes.md | 空白模板 + TraceAnalyzer 自动复盘 |
| 跨 attempt | progress.md | 八节结构化交接，注入下次系统提示 |
| 计费 | llm_cost_tracker | 输入/输出/cache 分账，max-cost 硬限 |
| 环境 | 容器内文件系统 | 会话间共享（--keep-container 加速重试） |

## 9. 隔离与安全

- **Docker 隔离为主**：自建 Kali 镜像（Dockerfile 哈希变更检测自动重建，:668-713）、专用网络、架构校验、挂载校验；`--executor ssh` 走远程主机、`--executor platform` 走平台自带 attackbox WebSocket；
- 容器内以 root 运行且无命令审查层（对比 pentest-copilot 的审批）——定位是受控靶场研究，靠平台 ToS 与 `--max-cost/--max-turns/--max-time` 资源围栏代替权限围栏；
- 凭据经 `SecretManager`（960 行，首次交互式录入 `.env`）；CLI 求解器在容器内安装并配置 auth（claude_code 求解器 :494-600，写 `~/.claude/anthropic_key_helper.sh` 辅助脚本与 `~/.claude.json`），外部求解器模式明确 `IS_SANDBOX=1` 才允许 root 跳过权限（README:322-324）；
- 学术诚信约束：INTEGRITY RULES 提示 + web_search 反作弊过滤双保险（软硬两层）。

## 10. 关键代码走读

**链路 A：single_loop 一轮工具循环**
1. `orchestrator.py:462` `action = solver.get_next_action()` → `single_loop.py:160` `llm_manager.get_llm_response(model=self.llm_with_tools)`（重试/拒答检测/缓存计费都在这层）；
2. `single_loop.py:201-230`：扫 `response.tool_calls`，`flag_found` 优先短路：立即 `_run` 提交、回填 ToolMessage、返回 `LLMAction(type="flag")`；
3. `single_loop.py:249-296`：其余工具 `ThreadPoolExecutor(max_workers=len(tools_to_execute))` 并发执行、按原序回填；
4. 返回 `LLMAction(type="none", metadata={"status":"in_progress"})` → `orchestrator.py:573-602` 直接 `continue`（不加任何额外反馈，节流日志每 5 秒一条）。

**链路 B：PTY 交互会话（exec_command → write_stdin）**
1. `tools.py:377` `executor._pty_session_manager.create_session(command=final_cmd)`（workdir 时包成 `bash -c 'cd dir && exec cmd'`，:371-375）；
2. `tools.py:380` `session.collect_output_until_deadline(yield_seconds)` 取首批输出；仍在跑则返回 "Process running with session ID N"；已退出则立即 `close_session` 修剪（:393-398）；
3. 后续 `write_stdin(session_id, chars, yield_time)`：auto-Enter 判定（:466-470 `if (not has_newline) and (not has_control_bytes): chars_to_send += "\n"`）→ `session.send_input(..., press_enter=False)` → 轮询输出并带会话上下文回显（:483-514）。

**链路 C：compactation 双历史压缩**
1. `single_loop_compactation.py:374` 每次 `get_next_action` 前查 `_should_trigger_compaction()`（cost_tracker 累计输入 token ≥ 窗口 × 0.60 且消息 >20，:128-134）；
2. `_compact_conversation`（:238-312）：全量（除 system）送临时 `LLMManager` 用 `prompts/compactation/conversation_compaction.yaml` 摘要 → 重建为单条 `HumanMessage`（system 内容 + `[SUMMARY OF PREVIOUS CONVERSATION]` + "Please continue from where we left off"）→ 清空并替换 `llm_manager.history`；
3. `get_messages()` 覆写返回 `full_history`（:356-365）——报告与 trace 永远看到未压缩全量；压缩标记消息（含 tokens_before/after 与百分比 metadata）追加进 full_history（:292-309）。

## 11. 亮点与弱点

**亮点**
1. **platform × executor × solver 三维解耦**是同类项目中最干净的架构抽象：换模型/换执行环境/换靶台互不侵入，天然成为 agentic 架构 A/B 试验台（10,000+ trace 基准是副产品复利）；
2. **命令执行封装的工程细节**：base64 中转执行 Python、auto-Enter 启发式、退出即修剪会话、超时引导切 PTY、每命令输出落盘——都是踩过真实模型行为坑之后的补丁，可直接移植；
3. **progress.md 跨尝试交接 + Dead Ends (DO NOT RETRY) 节**：把"跨会话记忆"做成了轻量、可人读、可注入的结构化文档，而非向量库黑盒；
4. 反作弊双层设计（提示层 INTEGRITY RULES + 工具层查询过滤）保证基准有效性。

**弱点**
1. 关键消息保全靠关键词匹配（password/root/shell…），粗糙且语言限定英文，遗漏即永久丢失于压缩；
2. 无权限/审批层，容器内 root 全权，直接用于真实渗透需自建围栏；
3. 方法论几乎完全外包给模型，弱模型表现断崖；提示词体系单薄（generic 仅 36 行）；
4. 单循环无规划/反思分工（hacksynth 除外），长链任务收敛靠重试与 try-harder nudge。

---

# 四、横向对比与可借鉴设计速览

| 维度 | pentest-copilot | shannon | BoxPwnr |
|---|---|---|---|
| 形态 | Web 全栈副驾驶（人机协同） | CLI 自主引擎（CI/CD 门禁） | 研究基准平台（架构试验台） |
| 编排 | 单主代理 + subagent + racer 编排者 | Temporal 五阶段固定流水线（15 代理） | Orchestrator + 可换 solver |
| 方法论载体 | prompt + EngagementState | 阶段结构 + 厚领域提示词 + schema | 极薄提示 + trace 沉淀 |
| 记忆 | 结构化状态跨压缩存活 | 交付物文件 + git 检查点续扫 | 双历史 + progress.md 交接 |
| 权限 | 三模式审批 + AI 代审 + 熔断 | 只读挂载 + 规则引擎 + ROE 注入 | 无（资源围栏代替） |
| 隔离 | SSH 攻击机 / Kali 容器 | 每扫描临时容器 + 专属队列 | Kali 容器 / SSH / 平台 attackbox |
| 结构化输出 | 工具参数 + 状态工具 | TypeBox collector 工具 + terminate | flag_found / `<FLAG>` 标签 |

三套系统分别代表三种产品哲学：**copilot（人在环上、权限谱系完备）**、**autopilot（流水线化、验证闭环、可恢复）**、**laboratory（解耦、可测量、可复现）**。对构建授权范围内的 AI 渗透系统，最有价值的组合是：shannon 的阶段化流水线与"无利用不报告"验证闭环 + pentest-copilot 的分层审批与跨压缩结构化状态 + BoxPwnr 的执行器解耦与命令封装细节。
