# 中文社区 AI 渗透测试项目分析（六）：BreachWeave / LuaN1aoAgent / Cairn / Viper / AboutSecurity / tinyctfer

> 纯代码架构分析，只读不执行。代码引用格式为相对于各项目根目录的 `file:line`（项目根 = `refer/02-chinese/<项目名>/`）。
>
> 背景说明：其中 4 个项目（BreachWeave、LuaN1aoAgent、Cairn、tinyctfer）均出自"腾讯云黑客松智能渗透测试挑战赛"（TCH）生态，是同一赛题下不同名次队伍的开源实现，非常适合做横向对比：同一约束条件下，四种截然不同的架构取舍。Viper 为知名红队平台的官方文档仓库快照，AboutSecurity 为知识库项目。

---

## 一、BreachWeave（tch-agent）

### 1. 一句话定位 + 项目背景

面向智能渗透测试场景的多 Agent 协作系统（Manager/Solver/Observer 三角色），腾讯云黑客松智能渗透测试挑战赛第二期线上初赛 + 线下决赛双料第一（1/613）。`main` 分支为 CTF 场景基础版，`pentest` 分支为授权渗透测试增强版（本地项目/资产管理、自动 Planner 调度、项目级 Memory/Idea 协作、按资产提交报告、Attack Flow 回放）。仓库自带完整的中文法律合规声明（README.md"法律免责声明 & 使用合规须知"章节）。

### 2. 技术栈与代码规模

- TypeScript + **Bun** monorepo（`package.json:1`，name 为 `tch-agent`），约 220 个 TS 源文件（不含 node_modules）+ 约 800 个内置 skill/字典数据文件。
- 分层：`apps/cli`（Commander 入口）→ `packages/ui-tui`（Ink 终端 UI）/ `packages/ui-web`（Bun.serve + React Web UI）→ `packages/core`（ConfigManager/ChallengeManager/RuntimeManager/Solver）→ `packages/libs/pi-mcp-adapter`。
- **底层 SDK 是 `@mariozechner/pi-coding-agent`（pi-mono 框架）**：agent session、工具定义（TypeBox schema）、extension 生命周期钩子、session compaction 都来自该 SDK，BreachWeave 的增值在于其上的多 Agent 编排与领域扩展（见 `packages/core/src/solver/session.ts:97` createSolverSession 直接组装 CreateAgentSessionOptions）。
- 容器运行时：dockerode；Solver 镜像为 Kali rolling 全家桶（`packages/core/src/runtime/assets/Dockerfile`）。

### 3. 整体架构与数据流

```
apps/cli (main.ts)
  └─ ChallengeManager (challenge/manager.ts, 1684 行) —— 控制平面
       ├─ 30s tick 的 planner 同步循环 (startSyncLoop, manager.ts:673)
       │    ├─ 拉取比赛 API 题目列表 (challenge/api-client.ts)
       │    └─ tickPlanner → runPlannerOnce (manager.ts:1307)：
       │         构建快照 → 注入占位符 → 一次性 planner 会话 → planner_* 工具执行调度
       ├─ RuntimeManager (runtime/runtime.ts, 882 行)
       │    └─ 每个 Solver 一个 Docker 容器 + JSONL-RPC 子进程 (solver/rpc/)
       │         容器内运行编译后的 solver 二进制，通过 host-bridge 回宿主请求状态
       └─ 文件态存储：~/.tch/challenges/<id>/{memory/, ideas/, logs, planner-last-round.json}
```

关键设计：**planner 不常驻**。每轮 `runPlannerOnce` 用 `SessionManager.inMemory()` 新建内存会话（manager.ts:1340-1345），把全局状态做成 Markdown 快照注入 system prompt，跑完即销毁；跨轮记忆靠 `planner-last-round.json`（snapshot_digest + actions + summary，manager.ts:176-181），digest 用 `Bun.hash` 计算以支持"状态没变就别乱动"的稳定性判断。

### 4. Agent / 编排设计

- **Manager（Planner）**：`CHALLENGE_PLANNER.md` 明确"你不解题，只负责稳定地调度 challenge 实例和 solver"，硬约束（最多 3 实例 / maxSolvers / 只能用快照中出现的 ID / stale=no 不许停）、调度顺序（先看新题→再看资源是否真紧张→再值得加码→才释放）、稳定性原则（"稳定优先于频繁变更"、"允许本轮什么都不做"）全部写成 prompt 规则（`packages/core/src/config/prompts/builtin/CHALLENGE_PLANNER.md:15-74`）。
- **Solver**：一题可挂多个 Solver 并行探索不同方向，每个是独立 Docker 容器里的完整 coding agent（bash/read/edit/write/grep + 安全工具）。启动时注入由 `buildSolverTask` 拼装的题面 + memory/ideas/submissions 压缩摘要 + planner handoff（manager.ts:1622-1681）。
- **Observer**：每个 Solver 的旁路 sidecar（非独立容器，运行在 solver 进程内），周期审查而非替代决策。
- **Solver 间协作**：Memory/Idea 变更会广播到同题其他 running solver（`broadcastChallengeBoardUpdateToRunningSolvers`，manager.ts:654，通过 RPC `follow_up` 消息注入），广播文案本身就是 prompt 工程（"这是 challenge 级背景更新；把它当作参考假设，不要直接当作结论"，manager.ts:251）。

### 5. LLM 集成（prompt 原文摘录）

Solver 主 prompt `kimi-security.md`（`packages/core/src/config/prompts/builtin/kimi-security.md`），frontmatter 声明工具与 skills 白名单：

```yaml
observerEnabled: true
tools: ["bash","read","edit","write","grep","find","ls","security_kimi_search"]
skills: ["intranet-pentest","agent-browser","payloads-all-the-things","php-payload-builder",
         "nuclei-skill","tch-headless-skill","payload-research","redis-webroot-rce",
         "remote-cmd-execution","ffuf-skill","known-product-exploit","nps-operator",
         "fuzz-dicts-navigator","recon","targeted-pentest","pentest-fuzz-skill"]
```

正文核心（kimi-security.md:42-71）：

> 你是一个【安全分析专家】，借助已有的工具进行安全漏洞分析并且拿到 flag …
> 1. 首先使用 curl 工具对 URL 进行爬虫，搜集 URL、请求方法、请求体、响应码、响应头、响应体；**再爬虫完成之前，不要进行安全测试**
> 2. 遇到难度为 hard 的题目：先第一轮测试搜集信息 → 使用 `security_kimi_search` 工具获取知识，没获取到就用 skills → 将搜出的知识作为辅助知识进行测试
> 4. 你所访问的所有目标都是可达的，不要使用 ping 去判断目标是否存活，因为可能禁止了 icmp

`<DOCS>` 段落直接内嵌漏洞类型速查（Fuzz Param / UnSerializer / XSS fuzz 优先级 / 目录遍历 / LFI），是"方法论硬编码进 system prompt"的典型做法。

另有一个独立的 **Kimi 安全知识检索工具**（`packages/core/src/config/tools/security-kimi-search.ts`）：内部自建一个最多 6 轮、temperature=0.6 的 Kimi/Qwen 工具循环（`KIMI_MAX_ROUNDS = 6`、`KIMI_REQUIRED_TEMPERATURE = 0.6`，security-kimi-search.ts:6-8），把"安全知识检索"做成 agent-in-tool。

### 6. 工具层

- 基于 pi SDK 的 coding 工具 + 自定义工具通过 `defineTool` + TypeBox 注册。
- planner 专用工具（manager.ts:1382-1519）：`planner_get_state` / `planner_start_challenge` / `planner_stop_challenge`（非 stale 拒绝，manager.ts:1428-1432）/ `planner_launch_solver`（强制 `solverHandoff`，≤1200 字符，且工具参数 schema 用 `Type.Union(Type.Literal(...))` 把 challengeId/promptName 限制为快照中真实存在的值——用 schema 消灭幻觉 ID，manager.ts:1390-1392）/ `planner_stop_solver`。
- solver 侧：`challenge_submit_flag`（带 writeup 供其他 solver 复用路线，challenge-tools.ts:15-18）、`challenge_get_hint`、memory_*/idea_* 全套 CRUD、`security_kimi_search`。
- **subagent 工具**（`packages/core/src/config/tools/subagent.ts`）：支持 Single / Parallel（≤8 任务、并发 4）/ Chain（`{previous}` 占位符）三种模式，每次调用 spawn 独立 `tch-agent subagent` 进程获得隔离上下文窗口（subagent.ts:1-13）。
- **Skills 体系**：Claude Code 的 SKILL.md 约定（`config/skills/index.ts`），内置 ad-pentest（10 篇 AD 攻击参考）、agent-browser、ffuf-skill、fuzz-dicts-skills（数万条中文场景字典：弱口令/目录/参数/XSS/RCE payload 等）、payloads-everything 等，prompt frontmatter 按名引用。
- pentest 分支增加结构化输出契约：`submit_sub_agent_output` 强制 subagent 以 schema（assets/hypotheses/candidate_findings/evidence_refs/coverage_gaps/goal）交付（`config/tools/pentest-output.ts:10-47`）。

### 7. 渗透方法论建模

两条线：

1. CTF 主线靠 prompt 中的步骤化 SOP（先爬虫后测试、hard 题知识检索三步走）+ 字典/payload skills。
2. pentest 分支显式建模为状态机与假设板：`RunState.current_phase ∈ {SCOPE, RECON, HYPOTHESIZE, TEST, DOCUMENT, REPORT}`，带 cycle 与 reentry 计数（`config/tools/pentest-workspace.ts:46-70`）；`HypothesisRecord` 含 statement/kind/entry_point/priority/confidence/why_plausible/next_test/status(candidate|verified|rejected|inconclusive)/attempt_count/evidence_refs（pentest-workspace.ts:72-94）。这是"假设驱动渗透 + 显式阶段流转"的教科书式落地，且 reentry 允许发现新攻击面后回流。

### 8. 状态与记忆

- **Idea / Memory 双板分离**（`challenge/memory.ts:4-5`）：`IdeaStatus = pending|testing|verified|failed|skipped`（方向），`MemoryKind = fact|evidence|failure|note|hint`（事实）。存储为 JSON 文件 + 目录锁（`withDirectoryLock`，锁超时 5s、陈锁 60s 回收，memory.ts:127-151），原子写（tmp+rename，memory.ts:120-125）。
- 注入控制：solver 初始上下文只带最近 memory 10 条 / ideas 8 条，每条 content 截断 220/120 字符，并注明"需要全量时再调用 memory_list"（manager.ts:62-67、286-319）。
- Observer 是**唯一的 Idea 写入者**（"主 Agent 对 ideas 是只读的；ideas 只能由你维护"，observer-agent.ts:157），solver 只读+验证——写权限分离避免主 agent 自我合理化。

### 9. 隔离与安全

- **每个 Solver 独立 Kali 容器**（Dockerfile 装了 nmap/sqlmap/hydra/hashcat/ffuf/nuclei/fscan/NetExec/Certipy/BloodHadoop 全家桶 + chromium + agent-browser），容器内留 `THIS_IS_DOCKER_CONTAINER_NOT_TARGET_MACHINE.txt` 防止 agent 把容器当靶机。
- **scope-guard 扩展**（`solver/extension/scope-guard.ts`）：audit/enforce 双模式；共享状态文件（assets.md/findings.md/run-state.json 等）只允许 main 角色写；监控 `curl/nmap/masscan/sqlmap/ffuf…` 14 类命令 token；main 角色"直连动作"默认限 6 次后强制走 subagent（scope-guard.ts:19-47）。
- **ralph 续跑循环**（`solver/extension/challenge-observer/ralph-loop.ts:58-87`）：`agent_end` 时若题目未完成则注入隐藏 continuation 消息（"继续当前任务。不要重复已经完成的步骤…直到比赛 API 明确显示题目完成"），错误重试上限 10 次、指数退避封顶 10s——**结束条件外置**，不交给模型主观判断。
- 上下文降噪：`large-tool-result.ts`（大输出外溢）、`pentest-compaction.ts` 的 compaction 指令（压缩时必须保留 Scope/资产/活跃假设/已确认发现/认证态/证据路径/当前阶段，可丢弃冗长 HTTP body，`solver/extension/pentest-compaction.ts:24-44`）。

### 10. 关键代码走读

**链路 1：Planner 调度循环**（manager.ts:1307-1380）
`runPlannerOnce` → `buildPlannerSnapshot`（把题目/约束/solver/prompt 历史战绩格式化为 Markdown 表格，manager.ts:393-454）→ `wrapPlannerResourceLoader` 把 `{{CHALLENGE_STATE}}/{{AVAILABLE_SOLVER_PROMPTS}}/{{PREVIOUS_PLANNER_ROUND}}/{{USER_STRATEGY}}` 四个占位符替换进 system prompt（manager.ts:489-512）→ 内存会话 `session.prompt("开始本轮比赛调度。")` → planner 调用 `planner_launch_solver` 等工具 → 结果写回 `planner-last-round.json`。工具调用与错误都通过事件订阅记录为 actions。

**链路 2：Observer sidecar 审查**（`solver/extension/challenge-observer/observer-loop.ts:234-376`）
`attachObserverLoop` 挂载 pi 扩展：`tool_execution_start/end` 事件累积每轮工具日志（args/result 截断 160 字符）；每条 assistant 消息结束 round+1，**每 6 轮或捕获到 `challenge_get_hint` 成功时强制触发**审查（observer-loop.ts:346-347）；`agent_end` 也触发。审查 payload = 题目状态 + 压缩后的 solver 背景（baseline 指令 + 最近 4 条用户注入，observer-loop.ts:189-216）+ 最近 10 轮活动。`runSolverObserverReview`（observer-agent.ts:331-388）用独立 session + 4 个 sidecar 工具（idea_*/memory_*/send_efficiency_reminder/query_solver_history）执行。**纠偏节流**是亮点：`shouldSendEfficiencyReminder`（observer-loop.ts:72-106）用消息指纹 + 活动指纹双重去重，冷却 6 轮、重复窗口 12 轮，防止 observer 变成噪音源；提醒通过 `pi.sendUserMessage(..., {deliverAs: "steer"})` 注入。

**链路 3：Observer prompt 的认识论约束**（observer-agent.ts:11-179）
`OBSERVER_SYSTEM_PROMPT` 是全项目最精彩的 prompt：默认动作排序 `NO_CHANGE > update existing > delete superseded > add new`；把 idea 判 failed 前必须连续自问三个问题（"否定的是整条路线还是某个 payload？是否仍有合理变体？是否更适合写 failure boundary？"）；看板体积预算（memory ≤12、ideas ≤8，超限时"压缩本身就是优先动作"）；`send_efficiency_reminder` 四前提同时满足才可用。这实际是把科学方法中的"证伪保守性"写成了操作规程。

### 11. 亮点与弱点

亮点：
1. **Observer 的认识论设计**：写权限分离（solver 只读 ideas）、failed 三问、纠偏指纹节流、看板体积预算——可直接移植到任何长程渗透 agent。
2. **用 JSON Schema 消灭调度幻觉**（planner 工具参数只接受快照中存在的 Literal ID）。
3. 状态分层（Idea/Memory）+ 截断注入 + 广播协作的完整闭环；snapshot digest 支撑的"无变化不调度"。
4. 结束条件外置的 ralph 续跑 + 指令化 compaction 保留清单。

弱点：
1. 强绑定 TCH 比赛 API（challenge/api-client），通用渗透场景需要重写接入层；pentest 分支的资产管理补足了这一点但两套代码有重复。
2. Observer/Planner 每轮全量快照注入，题目多时 prompt 线性膨胀（有截断但无结构化检索）。
3. 大量攻击知识以字典/payload 文件硬编码，更新依赖人工；kimi-security.md 的 DOCS 段落与 skills 内容存在职责重叠。
4. scope-guard 的 enforce 模式主要按文件前缀和命令 token 匹配，绕过面较大（如 base64 编码命令），安全边界实质上仍依赖容器隔离。

---

## 二、LuaN1aoAgent v2

### 1. 一句话定位 + 项目背景

"Cognitive-Driven Autonomous Security Agent"：基于 TypeScript + Pi SDK 完全重写的 P-E-O（Planner-Executor-Observer）自主安全研究系统，以**证据约束的因果推理图（Reasoning Graph）+ 任务图（Task Graph）+ 作战图（Operation Graph）三图模型**为核心，同样是 TCH 赛事头部项目（AGPL v3）。v2 明确声明与 Python v1 运行时无关，是全新实现。

### 2. 技术栈与代码规模

- Node.js 25+（依赖 `node:sqlite` 内置运行时）/ TypeScript，src 约 30 个文件、**约 19900 行**，其中 `controller.ts` 单文件 6829 行。
- 前端：React/Vite Web workbench（`web/`），含 live trace 与因果图可视化。
- 网络侧为独立工程：`traffic-proxy/`（Go，显式 HTTP(S) 前向代理 + SQLite + 内容寻址 body 审计 + CA 管理 + replay）、`network-image/`（Python + Go：mitmproxy 分段抓包、conntrack 遥测、scope DNS、`gateway-tun` 基于 **gvisor-ligolo** 用户态 TCP/IP 栈的透明协议网关、socks-connector、chisel）。
- 沙箱：per-task Docker（`executor-image/`）+ 独立 gateway 容器。

### 3. 整体架构与数据流

```
CLI (cli.ts) → SecurityAgentController (controller.ts)
  ├─ 持久层：.agent-runtime/sessions/<id>/
  │    state.sqlite（三图+事件+artifacts+watermark）
  │    execution.jsonl（append-only 归一化事件审计镜像）
  │    graph-deltas.jsonl（可重放图增量镜像）
  │    artifacts/（大输出内容寻址外溢） sandboxes/ executor-sessions/ traffic/
  ├─ Planner（fresh session/轮）──planner_submit──▶ Task Graph 变更
  ├─ Executor（per-task Docker 容器内 pi 会话，epoch 制）
  │    └─ 每次工具事件归一化 → ExecutionLog；大输出 → artifacts
  ├─ Observer-Supervisor（热路径，窗口=8 turns）──control_submit──▶ continue/redirect/handoff/stop_executor
  └─ Observer-Projector（异步，窗口=16 工具事件，并发≤2）──graph_delta_submit──▶ 推理图/作战图增量
```

`runUntilDone`（controller.ts:788）外层循环：`ensureRootGraph` → `runReadyTaskGraph`（依赖就绪任务进入确定性 admitted wave 并行执行，controller.ts:5557-5571）→ `runOnce`（planner 决策周期）→ 直到 goal:root completed / 预算耗尽 / 无进展轮数超限。所有运行参数（projector 窗口、supervisor 窗口、task 预算）在 `run_started` 事件中全量落审计（controller.ts:808-842）。

### 4. Agent / 编排设计

- **Planner**：不做技术调查，只维护任务图。命令语言仅 5 个：`create_tasks / patch_task / replace_dependencies / set_task_status / set_node_status`（types.ts:209-234），通过终止工具 `planner_submit` 交付；输入默认是 compact 的 `PlannerDecisionView`（taskLedger/taskOutcomes/epochOutcomes/reasoningDigest/operationDigest/blockers/graphSummary），且第二轮起只发 **delta**（`plannerDecisionViewDelta`，prompts.ts:250-282）。运行时对命令做**冲突检测与原子批应用**（`applyPlannerCommands` + `assertPlannerRuntimeTransitions`，controller.ts:1399/1661），非法状态转移直接打回并带 repairFeedback 重试。
- **Task 语义**是整个设计最精细的部分：Task = "同一条因果工作流"而非技术阶段；新必要结果用 `patch_task.appendObjectives` 追加而**不得覆盖历史 goal**；跨任务复用 workspace/session 必须走 `continueFromTaskRef` 单一后继（防上下文分裂）；`budget.maxTurns` 是"已分配预算"而非硬上限，用 `additionalTurns` 分段续拨。
- **Executor**：接收有界 `TaskEnvelope`（TaskDefinition & SchedulingPolicy & RuntimeContext，types.ts:148），epoch 制执行，`task_result_submit` 交出 completed/partial/blocked/failed + evidenceRefs/artifactRefs/blockerReason/checkpointReason。同一 Task 跨 epoch 复用同一 Pi session lineage 与 workspace，不同 Task 完全隔离。
- **Observer 双模式**：Supervisor（热路径、只判 continue/redirect/handoff/stop_executor）与 Projector（异步、只产图增量）职责正交，且都"每次调用新 session、不共享隐藏模型历史"。

### 5. LLM 集成（prompt 原文摘录）

`src/prompts.ts`（674 行）是全项目的灵魂，四个 system prompt均为中文超长规程。摘录：

**Planner**（prompts.ts:5-9，认识论约束）：

> 4. Evidence 只证明其实际观察范围。不得把 Executor 建议、候选技术、Hypothesis 或漏洞情报直接升级为已确认事实。
> 7. 数量、时间和有限尝试只表示投入边界，不证明开放候选空间穷尽。Root Goal 的"全部""所有""每个"按开放集合处理，除非持久材料给出可验证的封闭边界。

**Executor**（prompts.ts:88-96，因果边界与判定信号审计）：

> 4. 先锁定当前因果边界，只在同一层内验证：请求/路由是否到达、认证与分支是否进入、输入如何绑定、校验或过滤是否通过、目标能力是否执行、结果是否可见。当前层未证明前，不用下一层 payload 的失败推断其机制无效。
> 6. 判定信号必须先经过审计：只使用响应动态区域、状态码、重定向、稳定响应差异、时间差或可验证副作用。页面本来就存在的说明文字、全局关键词和请求脚本自己打印的标签不能证明后端分支、过滤器或执行器已经触发。

**Projector**（prompts.ts:139-149，证据落地规则）：

> - Evidence 只写 ground claim，不写后端实现、根因、存在性外推、未测试分支或能力泛化。
> - Hypothesis status 只能是 open、inconclusive、confirmed、refuted、superseded；不存在 contradicted 状态，反对该假设的 Evidence 用 contradicts 边表达。
> - 只有完整、可绑定的正向结果证明受控输入突破安全边界时创建 Vulnerability；只有实际读取敏感数据、执行代码、创建会话或完成目标时创建 succeeded Exploit。一次固定成功只证明该固定能力。

Executor 每轮工具调用前强制输出 ≤80 汉字的公开行动理由（因果层/实验模式/唯一变量/动态判定信号，prompts.ts:112）——这是把"可审计性"写进输出协议。

### 6. 工具层

Executor 工具面（README + controller.ts:4933）：`read/grep/find/ls/bash`、`web_fetch`、`web_search`（Brave API，无 key 时 HTML fallback）、`vulnerability_search`（NVD，"检索空结果是弱反证"）、`browser_render`（JS 渲染后 DOM 作为客户端行为证据）、`artifact_read/artifact_write`（跨任务材料，完整文件不经过模型上下文）、`route_open/route_status/route_stop/route_reconnect`（Runtime 托管 SSH/Chisel 内网路由；operator 的透明代理不暴露给 agent）。Planner 侧只给 `graph_query/graph_trace` 只读视图。**关键设计：运行时按上下文动态裁剪工具**——如 finalization 阶段只留 `artifact_write` 和 `task_result_submit`（controller.ts:4933-4941）。

### 7. 渗透方法论建模

方法论没有显式 phase 机（对比 BreachWeave），而是内化为**证据认识论 + 因果边界纪律**：

- 三图分工：作战图（Host/Port/Service/WebEndpoint/Parameter/Credential/Session/Route/File/Process）承载资产状态；推理图（Evidence→supports/contradicts→Hypothesis→confirms→Vulnerability→exploited_by→Exploit）承载结论链；任务图承载规划。边词汇表固定且方向受校验（prompts.ts:163-178）。
- **Provenance 强制**：confirmed Vulnerability / succeeded Exploit 节点必须顶层 `evidenceRefs` 引用本批 observation，"写入 properties 或描述不算节点依据"（prompts.ts:186-188）——由 schema 校验兜底（`normalizeProjectorGraphDelta`，controller.ts:5696）。
- 负面知识复用：refuted/superseded Hypothesis 带 reopenConditions，等价条件下的新实验必须复用旧结论（Executor 规则 9，prompts.ts:103）。
- 枚举 manifest 制：批量枚举结果写 workspace manifest，"数量达到阈值只表示本轮停止扩大，不表示攻击面不存在"（prompts.ts:105）。

### 8. 状态与记忆

- SQLite 单会话库 + 双 JSONL 镜像（事件与图增量皆可重放）；Projector 有 watermark 机制异步追平（`projectionDegradations` 字段显式告诉 Planner"图未追平，别把旧图缺失当否定事实"，prompts.ts:33）。
- 大输出即刻外溢为内容寻址 artifact（带 preview + provenance ref），模型上下文只留引用。
- 跨 epoch 状态：同 Task 复用 Pi session lineage；`resumeCursor/lastEventId` 由 Runtime 填充支持 checkpoint 续跑（types.ts:154-168）。
- Supervisor 的"相关负面知识"压缩注入（`compactSupervisorRelevantKnowledge`，controller.ts:6463）。

### 9. 隔离与安全

- **Per-task Docker**：UID 1000、`--cap-drop ALL`、`no-new-privileges`、只读 rootfs、`/tmp` tmpfs（rw,exec,nosuid,nodev,size=512m）、pids-limit 512、mem 4g / cpu 2、独立内部网络 + 专用 DNS（executor-sandbox-docker.ts:132-160）。容器配置有**启动后断言校验**（CapDrop/Tmpfs/NetworkMode 逐项核对，executor-sandbox-docker.ts:729-745）。
- **Gateway 容器是任务网唯一出口**：仅 NET_ADMIN/SETUID/SETGID，PID 1 配 TUN/策略路由后以独立 UID 跑 gvisor 用户态网关并清空 capability bounding set；任意 TCP 端口的 HTTP/HTTPS 识别与透明捕获，其余 TCP 原样转发（network-image/Dockerfile + gateway-tun/protocol_gateway.go）。
- **Scope 机械执行**：`scope.ts` 解析授权 CIDR/域名为规范集合，`configureAuthorizedScope` 下发到网络层；**AI 从 goal 文本推断的 scope CIDR 必须逐字出现在 --goal 中否则拒绝**（scope.ts:75-82）——防 prompt 注入扩权的关键细节。域名 scope 支持通配符，做了 IDNA/全数字 TLD 校验。
- 控制面数据（源码、ExecutionLog、GraphStore、.agent-runtime）对 Executor 不可读；跨任务事实只能经 evidence/artifact 显式引用。

### 10. 关键代码走读

**链路 1：运行主循环与预算**（controller.ts:788-1040）
`runUntilDone` 先 `configureAuthorizedScope`（scope 落网络层）→ 记录 run_started（全量配置审计）→ `ensureRootGraph` 创建 goal/scope 根节点 → `runReadyTaskGraph` 调度就绪任务 → while 循环调 `runOnce`（planner 决策）+ `drainReadyTaskGraph`，带 maxPlannerCycles 无进展熔断、全局时间预算、planner 不可用时的指数退避 + 连续失败熔断（controller.ts:916-930）。

**链路 2：Executor 输入装配**（prompts.ts:422-478）
`renderExecutorInput` 把 TaskEnvelope（taskId/goal/scopeRef/约束/成功条件/累计新增目标）、environmentFacts（Runtime 探测的可写路径等）、**operation_graph 与 reasoning_graph 的图切片 JSON**（Runtime 按 task 相关性选定，而非全图）、available_sessions、runtime_budget、execution_brief、dependency_outcomes 一次性注入，末尾固定指令"成功条件满足后立即调用 task_result_submit"。图切片而非全图是控制 token 的核心手段。

**链路 3：沙箱容器构建与验证**（executor-sandbox-docker.ts:105-200）
`createDockerRunArgs` 生成完整 `docker run` 参数（如上第 9 节）；`buildContainerEnvironment` 只透传 CA 证书路径相关 env（SSL_CERT_FILE/REQUESTS_CA_BUNDLE/CURL_CA_BUNDLE/NODE_EXTRA_CA_CERTS 指向网关 CA，保证 MITM 抓包对工具透明）；`assertContainerPath` 强制容器内路径白名单（workspace + 显式只读根）。

### 11. 亮点与弱点

亮点：
1. **认识论工程化**：证据/假设/漏洞/利用的严格分层、"一次固定成功只证明该固定能力"、负面结论不超实验范围、判定信号审计——这是六个项目中把"agent 何时允许下结论"建模得最严谨的。
2. **三图 + watermark + delta 视图**解决了长程任务的状态膨胀与"图未追平"竞态；Projector 异步化不阻塞执行。
3. **网络层隔离达到准产品级**：gvisor 用户态网关、scope CIDR 与 goal 逐字校验、容器配置事后断言、CA 密钥权限校验（traffic-proxy 拒绝 group/world 可读密钥）。
4. append-only 事件 + 可重放图增量，天然支持审计与回溯。

弱点：
1. `controller.ts` 6829 行单体，Planner/调度/校验/沙箱胶水全部耦合，可维护性差；prompt 与代码校验规则两处维护（如 Hypothesis 状态枚举在 prompt 与 normalize 函数各一份）。
2. 系统复杂度极高（四个 agent 角色 + 三图 + 双 observer），单任务成本与延迟显著高于 BreachWeave/tinyctfer 这类轻量方案；README 自述 v2 成绩需重跑验证。
3. 强依赖 Node 25 内置 sqlite 与 Docker 双镜像，部署门槛高。
4. Supervisor 窗口 8 turns / Projector 并发 2 等参数为静态 env 配置，缺乏自适应。

---

## 三、Cairn（衍迹）

### 1. 一句话定位 + 项目背景

"基于事实图的协作探索协议"：把目标导向探索建模为有向无环图（Fact 节点 + Intent 边），Server 是协议真相源，Dispatcher 是客户端执行器，Agent（Claude Code / Codex / pi / mock）是可插拔 worker。设计文档自我定位为黑板架构（Hearsay-II）、蚁群 stigmergy、OODA 循环、任务式指挥的现代化重构（docs/specs/server-protocol.md:31-45）。同为 TCH 赛事背景作品。

### 2. 技术栈与代码规模

Python 3.12 + FastAPI + uvicorn + docker SDK + click，43 个 py 文件约 6430 行（cairn/src/cairn/）。依赖极少（pyyaml/requests/docker），工程分为 server（协议 API）与 dispatcher（执行器）两个子包，prompts 以 markdown 随代码分发。

### 3. 整体架构与数据流

```
Cairn Server (FastAPI, 真相源)
  Project / Fact / Intent / Hint + intent 认领/心跳/结论 + 项目级 reason lease
       ▲ HTTP（Dispatcher 是唯一协议写入者）
Dispatcher (scheduler/loop.py, 935 行主循环)
  ├─ 按项目状态选择任务类型：初始态→bootstrap；新态势→reason；否则→explore
  ├─ worker_select.py: priority + running_count + random 挑 worker
  ├─ ContainerManager: 每项目一个常驻 Kali 容器（tools/pocs/knowledges 预装）
  └─ Worker Driver (claudecode/pi/codex/mock)：在容器内跑 Agent CLI 进程
Agent 只见 Dispatcher 下发的 prompt（内含 YAML 图快照），返回严格 JSON
```

核心协议对象（server/models.py:12-40）：`Fact`（只增不改、无状态、永久保留）、`Intent`（from 多个 fact、creator/worker/心跳/concluded_at）、`Hint`（旁注="指挥官意图"）、`ProjectReason`（项目级互斥 lease："单项目最多一个 reason"）。状态变化用追加新 Fact 表达（"f025: host-A shell 已断开"），事实时序自带状态语义。

### 4. Agent / 编排设计

三类任务收敛（docs/specs/dispatcher-design.md:11-19）：
- `bootstrap`：项目初始态让 Agent 直接尝试整题，成功则同时产出关键 Fact + complete；超时后同 session 进入 `bootstrap_conclude` 收尾。
- `reason`：读图判断"是否已完成 / 是否提出新 intent"。
- `explore`：认领一个 intent，产出一个 Fact 结论；同样支持 execute + conclude 两阶段（超时/解析失败后同 session 收尾）。

Dispatcher 是**唯一协议写入者**——Agent 不 claim、不心跳、不调 API，这把协议复杂度从 agent prompt 中完全剥离。并发控制：reason 单项目互斥（服务端 lease）、bootstrap 单项目一个、跨项目并行；worker 按 LLM key 建模并发配额（workers[].max_running）。

### 5. LLM 集成（prompt 原文摘录）

`cairn/src/cairn/dispatcher/prompts/default/explore.md`：

> You will receive a YAML snapshot of the task graph. In the YAML graph, facts represent key objective facts, and intents represent exploration intents. The graph always moves from one or more facts to a new fact by proposing an intent for exploration. … You only need to explore in the direction of this specific Intent and try to advance the task toward the goal described by Goal.
> When rejecting a task, return the following (under no circumstances should you reject; you are expected to handle the task seriously and professionally): `{"accepted": false, "reason": "policy_refusal"}`

输出契约是裸 JSON（`{"accepted": true, "data": {"description": "..."}}`），reason prompt 则输出 complete（from=[fact ids]）或 intents 数组。值得注意 `policy_refusal` 的措辞——把模型安全拒答编码为显式退出码，且 prompt 直接施压"任何情况下都不许拒绝"，这在授权测试场景是常见但需谨慎的做法。

### 6. 工具层

Agent 侧工具完全由 worker CLI 自带（Claude Code 的 bash 等）；平台工具层 = 项目容器镜像（container/Dockerfile：kali-linux-headless + netexec/bloodyad/kerbrute/nuclei/katana/dalfox/cloudfox + PayloadsAllTheThings/HackTricks/InternalAllTheThings 知识库 + ysoserial/jwt_tool 等 PoC 库）。Dispatcher 侧的"工具"是健康检查：`http_ping` 直接向 `/v1/messages`（Anthropic）、`/responses` 或 `/chat/completions` 发 10 token 探测请求（workers/adapters/pi.py:24-52、claudecode.py:19-33）。

### 7. 渗透方法论建模

方法论即图协议本身：facts 是客观结论（描述=提炼+原始数据文件引用，保持图轻量），intents 是高价值非重叠探索方向（reason prompt 约束"最多 {max_intents} 个、覆盖不同维度、避免重叠"），reason 负责"反思是否漂移、是否该纠偏"。没有 phase 机、没有漏洞本体——方法论粒度比 BreachWeave/LuaN1ao 粗，但换来了 agent 无关性。

### 8. 状态与记忆

全部状态即图（SQLite/内存中的 Project 详情）：fact_count/intent_count/working/unclaimed/hint_count 汇总（server/models.py:53-60）。无独立 memory 系统；Agent 的会话状态由 Dispatcher 用 `--session-id`（claudecode.py:36-48：`claude --session-id <s> --dangerously-skip-permissions -p -- <prompt>`）跨 execute/conclude 两阶段保持，"同 session 收尾"保证了超时前的上下文不丢。

### 9. 隔离与安全

- 每项目一个 Kali 容器（非每任务），容器生命周期由 Dispatcher 管理；项目 stopped/deleted 是硬停止：取消运行任务、停止/删除容器（dispatcher-design.md 要点 9、12）。
- Intent 认领 + 心跳租约（HeartbeatLease）防多 worker 重复劳动；reason lease 防并发推理冲突。
- Agent 以 `--dangerously-skip-permissions` 全权限运行——隔离完全依赖容器，与 tinyctfer 同级。

### 10. 关键代码走读

**链路：explore 任务全流程**（`cairn/src/cairn/dispatcher/tasks/explore.py:29-140`）
`run_explore_task`：取 driver → `HeartbeatLease.for_intent(...).start()`（认领+心跳）→ `container_manager.ensure_running(project_id)` → 可选健康检查（失败即释放 intent 返回 unhealthy）→ `render_prompt(load_prompt(group, "explore.md"), {graph_yaml: 快照写入容器内文件并引用, intent_id, intent_description})` → `run_worker_process` 在容器内执行 → `parse_json_output` + `validate_explore_payload` → 写回 Fact / 释放 intent；超时走 `write_conclude_result` 同 session 收尾。图快照写进容器再引用（`write_graph_snapshot_reference`）而非直接塞 prompt，是控制上下文的细节。

### 11. 亮点与弱点

亮点：
1. **协议/执行分离的最小内核**：Agent 无关（claudecode/codex/pi/mock 四驱动即插即用）、协议只有 4 个对象、"Dispatcher 是唯一写入者"让 agent prompt 极简——多 agent 协作系统的低耦合范本。
2. Fact 只增不改 + Intent 保留完整因果链 = 知识库与审计日志合一；reason lease 的项目级互斥是廉价而正确的并发原语。
3. execute/conclude 两阶段超时收尾（同 session）解决了"agent 跑超时但成果丢失"的普遍痛点。

弱点：
1. 方法论建模浅：无假设/证据区分、无漏洞结构化表示，结论质量完全靠 worker agent 自身。
2. 裸 JSON 输出契约脆弱（prompt 反复强调转义），依赖 output_parser 容错；拒绝语义（policy_refusal）被 prompt 压制存在滥用风险。
3. 单 Dispatcher 实例设计（文档明言不支持多实例共调）；每项目容器常驻，资源占用高。

---

## 四、Viper

### 1. 一句话定位 + 项目背景

FunnyWolf/Viper：知名开源红队平台（MSF 二次封装的多平台 C2，对齐 Cobalt Strike 功能矩阵），本目录快照是其**官方文档站仓库**（vitepress，1862 个文件：docs/{zh,en}、release notes、131 篇模块文档、30+ 实战教程），不含平台源码。需要注意：任务描述中"19 个 py 文件 + 前端"与本快照不符，本仓库实际为纯文档工程（`package.json` 仅有 vitepress 依赖）；平台本体经 Docker 分发（docs/public/install.sh）。

### 2. 技术栈与代码规模

文档站：VitePress + Vue 组件（自定义 Hero/Pricing/KeyFeatures）+ lunaria i18n。文档内容覆盖：模块手册（按 MITRE ATT&CK 战术前缀命名：Discovery_*/Execution_*/DefenseEvasion_*/Persistence_*/LateralMovement_*/CredentialAccess_*/PrivilegeEscalation_*/Web_*/ResourceDevelopment_*，131 篇）、agent 手册（6 个 AI_Agent_* 模块）、MCP 服务器手册、release notes（3.1.x 系列）。

### 3-4. 架构与 Agent 设计（据文档）

平台分层（docs/zh/guide/*）：Docker 化服务端（内嵌 metasploit-framework、nuclei）+ Web 控制台（主机/会话/路由/监听/模块/凭据管理，pivotgraph 内网拓扑图）+ 多 handler。LLM 集成是"**平台能力 Agent 化**"路线：
- **MCP 服务器**（docs/zh/guide/mcpserver.md）：容器内 `python3.12 /root/viper/Worker/mcpserver.py` 起 SSE 服务，暴露 15 个平台工具：`get_host_info / get_session_info / list_handler / list_session / list_host / list_route / query_route_by_ipaddress / query_port_forward_config / session_meterpreter_command_run / session_shell_command_run / read_loot_file / msf_module_execute / msf_module_search / msf_module_info / msf_module_target_compatible_payloads`——即把 C2 的读态与执行原语 MCP 化，供 Cursor 等外部 agent 接入。
- **LangGraph 智能体族**（docs/zh/module/）：`AI_Agent_Session_LangGraph_Pentest`（nmap/wafw00f/subfinder/nuclei/ipgeo/whois/msf function 工具集，自我定位"帮助工程师填参数、分析结果、给下一步计划"而非全自动）、`_Analysis`（查询已有数据分析并给行动计划）、`_ViperOperation`（平台操作）、`_Mail`（钓鱼模拟）、`_AwarenessTraining`（安全意识培训演示）。
- 模型配置支持多档（Easy/Reasoning/Function_calling 标签，docs/zh/guide/openai_api.md），兼容任意 OpenAI 格式 API（Ollama/百炼等），用"多配置组合"平衡成本/效果/速度；3.1.7 起平台迁移 Python 化（"Python Is All Your Need"）。

### 5-10. 要点与走读（文档级）

无源码可走读，可确认的关键工程事实：
- `docs/zh/release/3_1_2_Pentest_Agent_and_MCP_server.md`：明确产品判断——"因为当前 LLM 和工程上的限制，一个全自动的 Agent 效果并不理想。如果 Agent 能帮助渗透测试工程师输入繁琐的工具参数，分析工具的结果，给出下一步渗透计划，这个目标是完全可以实现的"——**助手路线而非自主路线**的代表性表态。
- `docs/zh/training/use_viper_with_skills/`：已有将 Claude Code skills 与 Viper 结合的官方教程（知识库消费侧）。
- ATT&CK 战术作为模块命名空间（131 篇模块文档的前缀分类）= 平台能力的分类法即 agent 工具目录的天然 schema。

### 11. 亮点与弱点

亮点：成熟 C2 平台以 MCP 暴露受控原语（会话执行/模块执行）而非暴露 shell，权限粒度合适；LangGraph agent 定位克制（人机协同）；ATT&CK 前缀的模块目录是现成的"工具语义层"。
弱点（就 AI 渗透参考价值而言）：本快照无源码，prompt/编排细节不可审计；平台重 C2 操作，漏洞发现侧的 agent 能力弱（依赖 nuclei 等工具）。

---

## 五、AboutSecurity（wgpsec）

### 1. 一句话定位 + 项目背景

"The world's largest structured pentest knowledge base"（wgpsec/AboutSecurity）：把渗透测试方法论改写为 **AI Agent 可执行格式**的知识工程仓库——不是 agent 框架，而是给任何 agent 供弹药的"知识层"标准范本。

### 2. 技术栈与代码规模

纯内容 + 轻脚本：248 个 `skills/*/SKILL.md`（17 大类）、600+ 篇 `Vuln/` 漏洞条目、`Dic/` 字典库（每目录 `_meta.yaml` 元数据）、`Payload/` 分类 payload、`Doc/`（报告写作/检查表/速查表）；脚本仅 7 个（bash + python）：`scripts/sync-claude-skills.sh`、`generate-index.py`、`bench-skill.py`、`grade_eval.py` 等。

### 3-6. 结构、方法论与工具层

- **Skills**：Claude Code SKILL.md 约定，frontmatter 含 name/description/**tags（中英 + MITRE ATT&CK 编号）**/category/mitre_attack。description 是**触发式写法**——"当目标有表单提交、登录页面、搜索功能、数据查询接口时使用"（skills/exploit/web-method/sql-injection-methodology/SKILL.md:4），这是让模型自主检索调用的关键。正文分 Phase，内嵌"⛔ 深入参考（Phase 3 截断时必读！）"指向 references/ 分卷——渐进披露（progressive disclosure）控上下文。
- **Vuln 条目**：frontmatter 结构化（id/title/product/vendor/version_affected/severity/tags/**fingerprint**），正文为"验证请求对（不带 UA 预期 403 / 带 UA 预期 200）→ 利用步骤 → 原始 HTTP 报文"（Vuln/middleware/nacos/CVE-2021-29441.md）——fingerprint 字段可直接驱动"识别产品→匹配漏洞库"的 agent 流程。
- **方法论编码质量**以 sql-injection-methodology 为例：Phase 0 强制 POST 参数完整性（含 hidden/submit）、ORDER BY 二分法并强调"必须找到 N+1 失败边界"、UNION 六变体穷尽后才许转报错注入——把人类专家的"何时允许放弃一条路线"写成硬规则，与 BreachWeave Observer 的 failed 三问异曲同工。
- **工具脚本**：`sync-claude-skills.sh` 把嵌套 skills 目录 symlink 成 Claude Code 要求的扁平 `.claude/skills/<name>/`（脚本头注释：11-13）；`generate-index.py` 解析全部 frontmatter 生成 `skills/index.json` 统一索引（供 agent 检索）；`bench-skill.py/grade_eval.py` 支持技能基准评测（Doc/Skill-Benchmark-Guide.md）。

### 7-10. 状态/隔离/走读

无运行时状态与隔离（纯知识库）。走读示例：`skills/exploit/web-method/sql-injection-methodology/SKILL.md:33-58`（ORDER BY 二分 + UNION 六变体）与 `Vuln/middleware/nacos/CVE-2021-29441.md:1-9`（frontmatter 指纹 + 双请求验证对）。

### 11. 亮点与弱点

亮点：description-as-trigger + tags + ATT&CK 编号的检索三元组；Vuln 条目的 fingerprint/version_affected/验证请求对是 agent 可直接执行的"机器可读 PoC 卡"；_meta.yaml 使字典/_payload 资产也带语义元数据；渐进披露的 references 分卷。
弱点：方法论以 CTF/Web 视角为主，企业内网/AD 深度依赖外部知识库；无版本化指纹库（fingerprint 是自由文本）；部分内容中英混杂影响检索一致性。

---

## 六、tinyctfer

### 1. 一句话定位 + 项目背景

"100 行 Baby Runtime"：l3yx 与 m09ic 的 TCH 智能渗透挑战赛第 4 名（238 队）核心代码抽离版，哲学口号"**Intent is All You Need**"——用约 1500 元 token（kimi k2）拿到名次。设计文章：《Intent is All You Need》《Intent Engineering》。与重编排路线相反，它把全部赌注押在"给 Claude Code 一个完美工具环境 + 一份强 prompt"上。

### 2. 技术栈与代码规模

极小：`tinyctfer.py` 99 行（docker SDK 启动沙箱）+ `meta-tooling/`（`python_executor_mcp.py` 240 行、`browser.py` 38 行、toolset 库约 380 行）+ `claude_code/` 配置（agent 定义 + .mcp.json + wait.sh）。沙箱镜像 `l3yx/sandbox`（Ubuntu 桌面 + Claude Code + MCP + toolset + 安全工具 + VNC）单独分发。

### 3. 整体架构与数据流

```
tinyctfer.py: docker run l3yx/sandbox（挂载 claude_code 只读配置 + 可写 workspace，
              注入 ANTHROPIC_* env，映射 VNC 5901 供人观察）
  → wait.sh 轮询 http://localhost:8000 直到 Python Executor MCP 就绪
  → container.exec_run(["claude","--dangerously-skip-permissions","--print", task])
  → Claude Code 主 agent 按 .claude/agents/security-ctf-agent.md 委派 SubAgent
  → SubAgent 用 MCP 工具 execute_code 在有状态 Jupyter kernel 里写 Python，
    Python 里 import toolset 驱动 浏览器/终端/代理/笔记
  → flag 拿到即结束
```

### 4. Agent / 编排设计

无自建编排：Claude Code 原生 SubAgent 机制 + 一份 200 行的 agent 定义。task 文案（tinyctfer.py:90-95）只有意图与边界："obtaining the Flag completes the task, you can end work immediately…You don't need to scan ports or IP segments, all IP and ports needed for solving are already listed"——把比赛先验直接写进任务意图。

### 5. LLM 集成（prompt 原文摘录）

`claude_code/.claude/agents/security-ctf-agent.md` 是唯一的 prompt 资产，要点：

> **Web Penetration Standard Operating Procedure (SOP) - No Steps Can Be Skipped:**
> 1. Open browser to access login/homepage → Inspect traffic
> 2. View page source code → Extract JS/API/comments
> 3. Test normal functionalities (login, search, upload) → Inspect traffic at every step
> 4. Only after confirming no hidden logic in traffic, proceed to run automated tools (nuclei/ffuf)
> 5. If automated tools find no vulnerabilities, continue exploring website functionalities…（agent md，SOP 节）

> **CRITICAL: Immediate Note-Taking Rule** - You MUST save notes IMMEDIATELY when you discover: Any leaked credentials… **Save the note BEFORE attempting to exploit** - this ensures valuable information is preserved even if exploitation fails or times out. Notes should only contain objective facts, not plans or guesses.

> 3. The CTF environment and your sandbox environment **cannot access the internet**…When encountering no-echo RCE or SQL injection, use **error-based** or **time-based blind injection** techniques.

以及工程纪律："**Timeout = Retry Signal**"、"Each code execution should be SMALL and FOCUSED…under 20-30 lines"。prompt 中直接内嵌 toolset 各模块的调用示例代码（browser/proxy/terminal/note 各一段可复制代码块）——**用文档化 API 示例替代工具 schema 描述**。

### 6. 工具层（Meta-Tooling，全项目核心创新）

传统：Agent → 工具 A → 解析 → 工具 B → 解析…（上下文污染）。tinyctfer：**Agent 意图 → 写一段 Python → 执行 → 最终结果**。

- 对 Claude Code 只暴露 3 个 MCP 工具：`execute_code / list_sessions / close_session`（python_executor_mcp.py:198-233）。`execute_code` 背后是 **jupyter_client KernelManager 的有状态会话**（同名 session 跨调用保留变量/导入；超时 interrupt 但会话存活，python_executor_mcp.py:130-160），每次执行自动持久化为 .ipynb notebook（天然审计轨迹，python_executor_mcp.py:105-108）。
- Python 内的 `toolset` 库提供四类原语：
  - `toolset.browser`：拿到 Playwright BrowserContext（browser.py 起的 Chromium 挂 Caido 代理 + remote-debugging）；
  - `toolset.proxy`：`list_traffic(filter='req.host.like:"%example.com" and req.method.like:"GET"')`（**CAIDO HTTPQL 过滤语法查流量**）；
  - `toolset.terminal`：tmux 会话（new_session/send_keys/get_output/kill，支持 C-c/C-Up 等按键，terminal.py:88-108）——长跑安全工具（ffuf/sqlmap）后台化 + 事后取输出；
  - `toolset.note`：markdown 笔记读写（note.py，"只记客观事实"）。
- 效果：上下文中只有"代码 + 结果"，中间工具输出全部留在 kernel/文件系统里。

### 7. 渗透方法论建模

SOP 五步（浏览器+流量 → 源码 → 功能测试+流量 → 自动化工具 → 功能逻辑推理）+ "漏洞多源于业务逻辑而非扫描器"的显式提醒 + 比赛环境先验（无外网→禁 OOB/反弹，改用报错/时间盲注）。无阶段机、无假设板——方法论完全内化为 prompt 纪律与笔记规范。

### 8. 状态与记忆

- Jupyter kernel 变量空间（会话内最強记忆）+ workspace 文件系统 + notes/*.md（跨步骤客观事实）+ .ipynb 全量审计。
- 无跨 challenge 记忆（单题一次性容器）。

### 9. 隔离与安全

单容器全信任（`--dangerously-skip-permissions`），隔离 = Docker 边界 + 比赛网络（无外网）。VNC 端口供人实时观察（可视化对齐 agent 行为， debugging 利器）。挂载：claude_code 只读、workspace 可写。

### 10. 关键代码走读

**链路 1：意图注入**（tinyctfer.py:88-97）
task 字符串 = agent 名 + 意图 + 题目 URL + 边界先验，`claude --print` 单次执行；等待逻辑 wait.sh 仅 `while ! curl -s --head http://localhost:8000` 轮询 MCP 就绪。

**链路 2：有状态代码执行**（meta-tooling/service/python_executor_mcp.py:69-196）
`execute_code(session_name, code, timeout)`：kernel 不存在则创建 → code 追加为 notebook cell 并立即写盘 → `client.execute(code)` 后循环收 iopub 消息（stream/execute_result/display_data/error）直到 idle → 超时则 `interrupt_kernel` 并清空残留消息、**会话状态保留** → 输出格式化为结构化 list[dict] 返回给 MCP。Jupyter 而非裸 exec 是关键：变量持久 + 魔法命令（%pip/!cmd）+ 中断不杀会话。

**链路 3：终端即工具**（meta-tooling/toolset/src/toolset/terminal/terminal.py:22-66）
tmux session 的 `send_keys + capture_pane(start, end)` 模型：`new_session` 起 tmux 并（有视口时）开 xfce4-terminal 窗口供 VNC 观察（`AI-Terminal-<id>` 标题）；`get_output` 支持负数行号取历史——长时任务"启动→干别的→回来取结果"的异步模式。

### 11. 亮点与弱点

亮点：
1. **Meta-Tooling（工具的元化）**是六项目中性价比最高的单项创新：MCP 工具数从 N 收敛到 1，上下文污染归零，且 Python 图灵完备让 agent 自由组合工具链（含写扫描脚本、并发请求、解析响应）。
2. 有状态 Jupyter kernel = 天然工作记忆；.ipynb 自动落盘 = 免费的行动审计。
3. VNC 可视化 + 流量代理（HTTPQL 检索）+ tmux 终端三件套构成"AI 友好沙箱"的完整定义，值得直接复刻。
4. 成本数据公开（~1500 元打完比赛）证明工具环境质量 > 编排复杂度。

弱点：无编排/并行/重试，单题单发；prompt 与 SubAgent 深度耦合 CTF（作者自述）；无 scope 强制（依赖容器网络边界）；kernel 长会话内存泄漏与 notebook 无限增长无治理。

---

## 七、横向对比与可借鉴清单（速览）

| 维度 | BreachWeave | LuaN1aoAgent | Cairn | Viper | AboutSecurity | tinyctfer |
|---|---|---|---|---|---|---|
| 编排粒度 | Manager+多Solver+Observer | P-E-O+双模Observer | 协议化图+Dispatcher | LangGraph 助手族 | 无（知识层） | 无（单 agent） |
| 状态模型 | Idea/Memory 双板 | 三图+证据强制 | Fact/Intent DAG | 平台 DB | — | kernel+notes |
| 方法论 | 阶段机+假设板 | 认识论规则内化 | 图生长协议 | ATT&CK 模块目录 | SKILL 渐进披露 | SOP prompt |
| 隔离强度 | solver 容器+scope-guard | per-task 容器+网关+scope 机械执行 | 项目容器+租约 | 平台自身 | — | 单容器 |
| 代码量级 | ~220 TS | ~20k 行 TS+Go | ~6.4k 行 Py | 文档 | 内容库 | ~750 行 |

若构建授权范围内的 AI 渗透系统，最值得直接借鉴的六个单项：BreachWeave 的 Observer 认识论规程与 Idea/Memory 写权限分离；LuaN1aoAgent 的证据强制 provenance（无证据不得确认漏洞）+ scope 与 goal 逐字校验；Cairn 的"协议唯一写入者 + execute/conclude 双阶段超时收尾"；Viper 的 MCP 化 C2 原语（受控执行而非裸 shell）；AboutSecurity 的触发式 description + fingerprint 漏洞卡格式；tinyctfer 的 Meta-Tooling（单一 execute_code 收敛工具面）。
