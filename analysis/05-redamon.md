# RedAmon 深度源码分析报告

> 分析对象：`/home/mistyovo/桌面/MTxCC/refer/01-international/redamon/`（v6.26.2，MIT 协议）
> 分析方式：纯静态代码走读，只读不执行。所有引用为相对该仓库根目录的 `file:line`。

---

## 1. 一句话定位 + 项目背景

**RedAmon 是一个"从第一个数据包到合并 PR"的全链路自主 AI 渗透测试平台**：6 阶段并行侦察管线把目标攻击面沉淀为 Neo4j 知识图谱，LangGraph ReAct Agent 在"信息收集→漏洞利用→后渗透"三阶段模型下自主选择攻击技能与工具执行漏洞验证，随后 CypherFix Triage Agent 对数百条发现去重定级、CodeFix Agent 克隆仓库用 11 个代码感知工具实施修复并开 GitHub PR——所有高危动作均有人类审批卡点。

项目自述解决了 XBOW Web 安全基准 101/104（97.1%）全黑盒题目，并发布了一份 124 页的 397 类 bug bounty 发现自查审计（134 类完整、210 类在 backlog、44 类明确排除）。README.md:32-50。其免责声明明确限定"仅用于授权安全测试、教育与研究"（DISCLAIMER.md，27KB）。技术上是 polyglot monorepo，`redamon.sh`（228KB bash）作为构建/测试/部署控制平面。

对本研究最重要的价值：它是目前开源可见的**最完整的"LLM 渗透 Agent 工程化"参考实现**——不是 demo，而是在生产级多租户、审计、人机协同、注入防御层面做了大量取舍的实现。

---

## 2. 技术栈与代码规模

### 2.1 规模实测

| 目录 | 文件数 | Python 文件 | 角色 |
|---|---|---|---|
| `agentic/` | 452 | 374 | **核心**：LangGraph Agent、编排、prompt、技能、CypherFix |
| `recon/` | 429 | 360 | **核心**：6 阶段侦察管线（每扫描任务一个容器） |
| `recon_orchestrator/` | 60 | 54 | **核心**：扫描容器生命周期/资源治理 |
| `graph_db/` | 80 | 45 | **核心**：Neo4j schema、租户隔离、mixin 写入层 |
| `mcp/` | 45 | 27 | **核心**：MCP 工具服务器（kali-sandbox 内） |
| `scanners/` | 146 | 102 | **核心**：11 个独立扫描器容器（GVM、TruffleHog、supply-chain、capture_proxy、ai_attack_surface 等） |
| `services/` | 53 | 47 | 半核心：docker_broker、knowledge_base、postgres |
| `webapp/` | 1803 | 0 | **外围**：Next.js 16 / React 19 / Prisma 前端+API（约 175k 行 TS，不含测试） |
| `tests/` + 各组件 tests | 225+ | 172 | 外围（测试） |
| `skills/` | 18 | 0 | 外围（开发流程 skill，非运行时） |

核心 Python 约 **19.4 万行**（不含 tests），前后端合计约 37 万行。`CHANGELOG.md` 552KB、`README.md` 102KB、`.env.example` 22.8KB——配置面极大，是长周期演进的产物。

### 2.2 技术栈

- **Agent 层**：Python 3.11 + LangGraph（StateGraph + checkpointer）+ LangChain。Agent 以 **JSON 决策协议**驱动（非原生 tool calling），Pydantic 校验（`agentic/state.py:724` `LLMDecision`）。
- **工具层**：MCP（Model Context Protocol）——`mcp/servers/` 下 8 个服务器进程，跑在 `kali-sandbox` 容器内；另有进程内工具（query_graph/web_search/shodan/google_dork/tradecraft/fs_*/job_*）。
- **存储**：Neo4j（攻击面知识图谱 + 攻击链 EvoGraph）、Postgres（会话、checkpointer、fireteam、triage）。
- **LLM**：11 家 provider（openai/anthropic/openrouter/bedrock/deepseek/gemini/glm/kimi/qwen/xai/mistral）+ 自定义 endpoint（`agentic/orchestrator_helpers/model_providers.py`、`agentic/llm_builder.py`）。Anthropic prompt caching 有专门支持。
- **部署**：docker-compose 约 35 个服务（`docker-compose.yml`），三个隔离网络：`redamon`（信任面）、`pentest-net`（工具/目标面）、`codefix-net`。
- **外围**：Next.js webapp（UI + BFF + Prisma）、Playwright e2e、guinea_pigs 靶机。

---

## 3. 整体架构：模块划分与数据流

### 3.1 运行时拓扑（docker-compose.yml 实测）

```
webapp (Next.js, 唯一对外)
   │  X-Internal-Key / SCANNER_API_KEY
   ├── agent (FastAPI agentic/api.py + AgentOrchestrator)          [redamon 网，无 pentest-net]
   │      ├── MCP servers @ kali-sandbox (execute_* / kali_shell / metasploit / playwright)
   │      │        └── pentest-net → 目标
   │      ├── Neo4j (query_graph / chain 写入)
   │      └── Postgres (AsyncPostgresSaver checkpointer, fireteam 表)
   ├── recon-orchestrator → 按任务 spawn recon/ partial-recon 容器 → Neo4j
   ├── 11 个 scanner 容器 (gvm/trufflehog/wcvs/supply-chain/capture-proxy/ai-attack-surface...)
   ├── capture-proxy (mitmproxy) + traffic-ingest
   └── codefix-sandbox (CodeFix agent, codefix-net, 克隆仓库)
```

关键点：**agent 容器不在 `pentest-net` 里**（docker-compose.yml:1297 注释"NO pentest-net -> no target egress at all"），所有目标流量必须经 MCP 工具进程发出，这使工具层成为天然的策略执行点（捕获代理路由、SSRF guard、确认门都挂在这层）。

### 3.2 数据流

1. **侦察**：`recon/main.py` 6 阶段管线（domain_discovery → port_scan → http_probe → resource_enum → vuln_scan + MITRE enrich，recon/main.py:10-16），并行 ThreadPool，结果 MERGE 进 Neo4j（带 `user_id/project_id` 租户键）。
2. **Agent 会话**：用户提问 → `AgentOrchestrator.invoke_with_streaming()`（agentic/orchestrator.py:1064）→ LangGraph 状态机 → think（JSON 决策）→ execute_tool/execute_plan/fireteam → 图谱写入（chain_graph_writer，fire-and-forget 后台线程）→ streaming 事件回 webapp WebSocket。
3. **发现回流**：工具输出 → LLM `output_analysis`（结构化提取 ports/services/vulns/credentials/sessions + chain_findings）→ `chain_graph_writer` 写 Neo4j EvoGraph（AttackChain/ChainStep/ChainFinding/ChainFailure/ChainDecision）。
4. **收尾**：CypherFix triage（静态 Cypher 评分 + LLM 复核 + 分组 + 修复建议，`agentic/cypherfix_triage/orchestrator.py:57-76` 的 R→A→B→C→D→E 六步）→ CodeFix agent 在沙箱克隆仓库、编辑、测试、开 PR。

### 3.3 模块职责清单（核心代码）

- `agentic/orchestrator.py`（1469 行）：图构建、路由、会话生命周期、MCP 热重载。
- `agentic/orchestrator_helpers/`（30+ 模块）：think/execute/approval/fireteam 节点、guardrail（硬+软）、LATS、productivity 循环检测、key rotation、streaming。
- `agentic/prompts/`：base.py（2235 行系统 prompt 工厂）、tool_registry.py（1132 行工具目录）、18 个按漏洞类别的 workflow prompt。
- `agentic/tools.py`（2657 行）：`PhaseAwareToolExecutor` 等 9 个 manager。
- `agentic/state.py`（2073 行）：AgentState（TypedDict，200+ 字段）与全部 Pydantic 模型。
- `graph_db/`：schema.py（幂等 DDL + 租户约束）、mixins/（按扫描器分类的写入器）、node_filters/。
- `mcp/servers/`：network_recon_server.py（1700 行，17 个工具）、metasploit_server.py（1243 行）、redamon.py（流量语料 redamon.search/replay/fuzz + JWT 工具）。

---

## 4. Agent/编排设计

### 4.1 状态机（非简单循环）

`_build_graph()`（agentic/orchestrator.py:605-813）构建 14 节点 LangGraph：

```
initialize ─┬─> process_approval / process_answer / process_tool_confirmation
            └─> think ─┬─> execute_tool ──> think            (单工具)
                       ├─> execute_plan ──> think            (并行波)
                       ├─> deploy_fireteam -> fireteam_collect -> think
                       ├─> await_approval / await_question / await_tool_confirmation -> END (暂停等人)
                       └─> generate_response -> END
```

这是 **ReAct + 人机协同中断**的混合体：三类暂停节点（阶段转换审批、反问用户、危险工具确认）直接 `add_edge(node, END)`（orchestrator.py:763/776/789），把图挂起在 checkpointer 上，用户答复后用 `resume_after_*_with_streaming()` 从 checkpoint 恢复（orchestrator.py:1156/1233/1308）。checkpointer 默认 MemorySaver，`PERSISTENT_CHECKPOINTER=true` 时换 `AsyncPostgresSaver`（orchestrator.py:159-196）——fireteam 多 agent 也依赖它。

### 4.2 决策协议：JSON 而非 function calling

think 节点每轮要求 LLM 输出严格 JSON（agentic/prompts/base.py:953-999）：

```json
{
  "thought": "...", "reasoning": "...",
  "action": "use_tool | plan_tools | deploy_fireteam | transition_phase | switch_skill | complete | ask_user",
  "tool_name": "...", "tool_args": {...},
  "tool_plan": {"steps": [{"tool_name","tool_args","rationale"}]},
  "skill_switch": {"to_skill": "...", "reason": "..."},
  "updated_todo_list": [...]
}
```

8 种 action 构成**升级阶梯**：`use_tool → plan_tools → deploy_fireteam`（"cheapest first"，base.py:336-340）。`plan_tools` 是单 LLM 调用内并行发射 N 个无依赖工具（一个 wave），`deploy_fireteam` 才是真正的多 agent。这种"先波后编队"的分级并行是控制 token 成本的显式设计。

### 4.3 Fireteam 多 agent

`fireteam_deploy_node`（agentic/orchestrator_helpers/nodes/fireteam_deploy_node.py:457）流程：校验计划（成员上限、**互斥组**——两个成员不能同时持有 metasploit 组，因 msfconsole 是单例）→ 写 Postgres fireteams/fireteam_members 行 → N 个 asyncio 任务各自跑预编译的 `fireteam_member_graph`（1672 行独立 think 节点）→ `Semaphore(FIRETEAM_MAX_CONCURRENT=5)` 限流 → `wait_for(FIRETEAM_TIMEOUT_SEC)` → collect 合并，findings 打 `(from <specialist>` 标签，同 scope 二次部署被 prompt 明确禁止（base.py:349）。成员每人在主 toolbox 外有 fallback 工具但需逐次说明理由（base.py:351-363）。成员的危险工具请求**升级回父 agent 的确认队列**（`_pending_escalations` 逐个 drained，state.py:1101-1106 注释引 FIRETEAM.md §20 Q3）。

### 4.4 反僵化：三层元认知

1. **Productivity 评分 v2**（think_node.py:236-274）：每轮计算多维停滞分（verdict 窗口、`tested_axes` 会话级轴账本、`_iterations_since_state_grew`/`_iterations_since_chain_advance` 观测计数器），分绿/黄/橙/红/临界五档，触发不同干预。
2. **Deep Think**（think_node.py:345-465）：触发后用独立 prompt 让 LLM 产出 `DeepThinkResult`（含 **competing_hypotheses**，每个假设带 disambiguating_probe），下一轮 prompt 强制"下一个动作必须是区分性探针而非承诺偏好假设"（think_node.py:431-449）；用 **Jaccard 相似度** 拒绝换皮重试的旧计划（think_node.py:405-423，阈值 0.6）；有冷却期防 Deep-Think-every-turn。
3. **LATS**（agentic/orchestrator_helpers/lats.py，2109 行）：Language Agent Tree Search——把已执行探针组织为 UCT 树，价值函数是 productivity 分数的反转（lats.py:50-53），跨树持久化 `_lats_leads`（确认线索）与 `_lats_dead`（确认死亡的 class@target 对，永不重试）+ `_lats_probe_ledger`（硬去重账本）（state.py:1139-1153）。

另有 **recon 预算门**（见 §10 链路 2）与 **LLM 自评 productivity verdict**（new_info/confirmation/no_progress/blocked/duplicate/diagnostic_progress 六档，base.py:1111-1128，orchestrator 会"审计"并自动降级不诚实自评）。

---

## 5. LLM 集成

### 5.1 Provider 抽象

`_setup_llm()`（orchestrator.py:355-397）从项目设置解析 11 家 provider 的 key 传入 `setup_llm()`；模型 id 前缀路由（`llm-provider-integration` skill：prefix-routed model ids）。密钥只进 agent 容器，**扫描容器永不见 key**。`KeyRotator`（orchestrator_helpers/key_rotation.py）为 Tavily/Shodan/SerpAPI 做 rotate-every-N 轮换（orchestrator.py:214-218）。

### 5.2 Prompt 工程（核心资产）

主系统 prompt `REACT_SYSTEM_PROMPT`（base.py:887-1062）是**每轮按状态重组的模板**而非固定文本：静态前缀（人设 + untrusted 边界 + 阶段定义 + 工具表 + 当前攻击技能）以不透明哨兵 `<<REDAMON_CACHE_PREFIX_END>>` 分割（base.py:884），前缀打 `cache_control` 走 Anthropic prompt caching（project_settings.py:78-86）。

prompt 原文摘录（最能体现工程水平的两段）：

**失败认识论**（base.py:1042-1048，单段 ~450 词，逐句都是可执行规则）：

> "**On failure, VALIDATE before you theorize.** ... First reproduce your payload in a place you fully control ... Then separate three distinct causes ... **my technique is wrong** ... **my execution is wrong** ... **the environment is noisy** ... An error you have not reproduced is a clue, not a conclusion. ... **One failed attempt is not a negative result.** Before you conclude a vector or vulnerability class is absent ... vary the attempt along its obvious axes ... determine *where it lands* from its literal position in the raw response body — never from an error string ... probe one benign representative of each structural family — a raw HTML tag, an attribute break-out, and a script/expression ... never treat 'my first idea got filtered' as 'this class does not apply.' ... confirm you are reading the channel where success would actually surface ... never against the app's own narration of what it thinks it did ..."

**24 类响应分类法**（RESPONSE_CLASS_TAXONOMY_BLOCK，base.py:1254-1308）——要求 LLM 对 wave 中每个探针打一个 response_class：`exploit_confirmed / oob_callback / error_leak / outbound_fetch / boolean_differential / time_differential / reflected_unsanitized / server_error_5xx / filter_blacklist / partial_filter_bypass / encoding_normalization / waf_block / privilege_required / wrong_method / wrong_content_type / auth_required / rate_limited / size_limit / filter_whitelist / geo_legal_block / benign_no_signal / dead_endpoint / duplicate / inconclusive`，且 `boolean_differential/dead_endpoint/waf_block` **必须有基线对照请求否则判 inconclusive**（base.py:1264-1267）。这本质上是把渗透测试员的"响应解读启发法"编码为受约束的分类任务，供 LATS 价值函数与循环检测消费。

其他要点：graph-first 原则（"Never re-test what the graph already shows"，base.py:818）；TEXT_TO_CYPHER 系统 prompt 强制节点带 label、租户隔离说明、suppressed findings 语义（base.py:1539-1596）；工具参数四种 shape 的防错说明（base.py:1001-1027）；fireteam propensity 1-5 档操作员策略注入（base.py:376-407）。

### 5.3 会话内 LLM 辅助端点

`agentic/api.py`（5295 行 FastAPI）还暴露一批微调 LLM 端点：`/llm/waf-classify`、`/llm/nuclei-fp-filter`（误报过滤）、`/llm/takeover-classify`、`/llm/ffuf-extensions`、`/roe/parse`（自然语言 RoE 解析）等（api.py:651-1442），全部挂 `require_internal_auth`（`agentic/llm_guard.py`：常量时间比较 X-Internal-Key、令牌桶限流、日调用上限，llm_guard.py:9-30）。这展示了"重决策用大循环、轻分类用微端点"的双轨 LLM 用法。

---

## 6. 工具层

### 6.1 注册与分阶段可见性

`TOOL_REGISTRY`（agentic/prompts/tool_registry.py:34）是 prompt 侧单一事实来源（purpose/when_to_use/description/args_format/flag 示例），约 40+ 工具。`TOOL_PHASE_MAP`（agentic/project_settings.py:286-323）控制阶段可见性：如 `execute_hydra`/`metasploit_console`/`msf_restart` 仅 exploitation+，`google_dork` 仅 informational，`kali_shell`/`execute_code`/`execute_playwright` 全阶段。每轮 think prompt 只渲染当前阶段允许的工具表（base.py:241-269）——**用 prompt 收窄 + executor 硬校验双层实现**。

### 6.2 分发咽喉：PhaseAwareToolExecutor

`agentic/tools.py:1992` 起的 `PhaseAwareToolExecutor` 是唯一执行入口：MCP 工具经 `MultiServerMCPClient`（断线自动重连、generation 号防 ABA），进程内工具直接 callable；`set_tenant_context()/set_phase_context()` 用 contextvars 把 (user_id, project_id, phase) 传给工具闭包——**租户隔离在工具执行时生效而非仅靠 prompt**。

### 6.3 工具清单（按层）

- **网络/扫描**（MCP @ kali-sandbox，network_recon_server.py）：execute_curl/naabu/httpx/subfinder/gau/katana/arjun/ffuf/wpscan/amass/hydra/jsluice/masscan/osv_scanner/cve_intel、kali_shell（任意 Kali 命令）、execute_code（Python 沙箱执行）。
- **利用**：metasploit_console（1243 行服务器，progress 轮询 + session-chat 映射）、msf_restart。
- **浏览器**：execute_playwright（mcp/servers/playwright_server.py，341 行）。
- **图**：query_graph（NL→Cypher）+ graph_schema/graph_summary 伴生工具（一个开关三动词，project_settings.py:21-36）。
- **知识**：web_search（Tavily + 本地 FAISS+Neo4j 混合 KB，orchestrator.py:557-603）、shodan、google_dork、tradecraft_lookup（分层 fetch 外部攻击手册，tier2 阈值/TTL/section-picker-LLM）。
- **文件系统/工作区**（进程内）：fs_read/write/edit/grep/glob/mkdir——限定 `/workspace/<projectId>/`（notes/、tool-outputs/、jobs/、uploads/ 四固定子目录，base.py:38-146 有完整的 workspace 心智模型 prompt）。
- **后台作业**：job_spawn/status/wait/cancel——>60s 的工具强制后台化（base.py:96-146 给出完整 spawn 政策与"必 spawn 清单"如 `execute_hydra any call`）。
- **流量**：proxy_brain（单工具替代 10 个 proxy_* 工具；redamon.search/get/sitemap/params/grep/diff/to_curl/query/replay/fuzz + jwt forge，mcp/servers/redamon.py:124-318；replay **钉死 host/scheme/port** 防跨域重放）。
- **用户 MCP**：`reload_mcp_manifests()`（orchestrator.py:492-555）热合并用户自建 MCP server，declared_tool_names 白名单 + registry prompt 同步。

### 6.4 输出治理

工具输出 >20KB 自动 offload 到 `tool-outputs/<utc-iso>-<tool>.txt` 并返回 head/tail stub（base.py:66-82）；MCP 包装器 `success=True` 但正文含 `[ERROR]`/`Page.goto: Timeout` 等 7 种模式时由 `_detect_embedded_tool_error` 翻转 success 并写 ChainFailure（execute_tool_node.py:22-52, 268-277）；`error_class` 分类器区分引用错误/4xx/5xx/秒级崩溃（execute_tool_node.py:283-289）。

---

## 7. 渗透方法论建模

这是 RedAmon 最有参考价值的部分之一：**把渗透方法论做成了"分类路由 + 技能文档 + 分阶段 workflow prompt + 参数化攻击策略"四层**。

### 7.1 攻击路径分类（入口路由）

会话开始时 LLM 把请求分类为 attack_path_type（cve_exploit / brute_force_credential_guess / xss / sql_injection / ssrf / rce / path_traversal / xxe / crypto_attack / deserialization / http_request_smuggling / denial_of_service / phishing_social_engineering / user_skill:* / *-unclassified），每类有专属 `build_attack_path_behavior()` 行为块（base.py:467-586）。未分类路径的兜底规则写明"**发现具体向量立刻 switch_skill，不许继续扫描**"（base.py:575-582）。`switch_skill` 是一等 action：不换阶段、即时重绑 workflow（base.py:984-988）。

### 7.2 漏洞类别 workflow prompt（18 个文件）

`agentic/prompts/sql_injection_prompts.py`（541 行）示例——"GATE A"硬门（sql_injection_prompts.py:26-48）：

> "On a LOGIN / AUTH form, auth-bypass IS the objective; DB-extraction is FORBIDDEN until the bypass sweep is exhausted and on record. ... **Mandatory auth-bypass matrix — fill EVERY cell before any extraction**: Fields (rows): test EVERY submitted field ... the SECRET field frequently interpolates into a LATER query ... **Skipping the secret field is the single most common reason a solvable login is wrongly declared 'not bypassable.'** ... Run the whole bypass matrix as ONE scripted sweep — do NOT hand-fire probes across iterations."

这把资深测试员的战术纪律（登录页先穷尽认证绕过矩阵、逐字段×逐形状×应用自身响应差异为 oracle、脚本化一次性扫完）直接编码为阻塞门。每类还有**工程化参数**：SQLI_LEVEL/RISK/TAMPER、SSRF_INTERNAL_RANGES/OOB_PROVIDER/CLOUD_PROVIDERS、RCE_AGGRESSIVE_PAYLOADS（默认 False=只读证明）、DESERIALIZATION_EXEC_GADGETS_ENABLED（默认关）等（project_settings.py:358-400）——攻击烈度是配置面不是 prompt 面。

### 7.3 技能库（.md，46 个）

`agentic/skills/` 13 类（active_directory/api_security/cloud/frameworks/mobile/network/post_exploitation/protocols/reporting/social_engineering/technologies/tooling/vulnerabilities），如 `jwt_attacks.md`：frontmatter + 工具调色板表 + 侦察清单（.well-known 端点矩阵）+ token 解剖 + **攻击矩阵表**（alg=none/RS256→HS256/kid 注入/jku/x5u/claim inflation/OIDC mix-up/PKCE downgrade/…每行带 quick probe）+ proxy_brain 流量工作流。技能经 `skill_loader` 按阶段注入 prompt，`builtin-agent-skill` skill 文档说明一个内置技能要过 9 层（含 KNOWN_ATTACK_PATHS 门）。另有 volume-mounted 的 community-skills（.md 攻击工作流，无需重建镜像）。

### 7.4 三阶段杀伤链模型

informational（默认，图优先、轻探测允许）→ exploitation（需用户审批 + 已确认漏洞）→ post_exploitation（需审批 + 活跃会话）——升级需审批、降级自动批（base.py:905-908）。模式矩阵区分 Statefull（meterpreter 会话）与 Stateless（输出即证明）（base.py:858-871）。完成判定标准显式列出（"uid=0(root)、[+] Success: 'user:pass'、session X opened"，base.py:1130-1135），并配"成功后 STOP"规则（base.py:1040）。

---

## 8. 状态与记忆

### 8.1 AgentState（agentic/state.py:974-1178）

200+ 字段的 TypedDict，可归为七类：

1. **会话骨架**：messages（add_messages reducer）、iteration/max、phase + phase_history、attack_path_type。
2. **执行轨迹**：execution_trace（ExecutionStep 列表，thought/tool/output_analysis 全量）——**这是唯一能到达下一轮 think LLM 的通道**（think_node.py:104-109 注释：节点每轮重建无状态 prompt、从不回放 messages）。
3. **LLM 自管 todo_list** + 多目标（conversation_objectives/objective_history）。
4. **攻击链记忆**：chain_findings_memory / chain_failures_memory / chain_decisions_memory / chain_waves_memory（含零发现 wave，防重复部署同 scope 编队）。
5. **目标情报**：target_info（ports/services/technologies/vulnerabilities/credentials/sessions，由 output_analysis.extracted_info 持续合并）。
6. **人机协同**：三类 awaiting/pending/response 字段 + qa_history。
7. **元认知簿记**：tested_axes（语义轴→尝试账本）、_iterations_since_state_grew/_chain_advance、_deep_think_cooldown_until、_previous_priority_order、LATS 全套（_exploit_tree/_lats_leads/_lats_dead/_lats_probe_ledger/_lats_tree_digest）、fireteam 全套、token 计量。

### 8.2 三级持久化

- **checkpointer**：MemorySaver 或 AsyncPostgresSaver（跨重启续跑，`resume_execution_with_streaming` orchestrator.py:1388）。
- **Neo4j EvoGraph**：chain_graph_writer 后台线程写 AttackChain/ChainStep/ChainFinding/ChainFailure/ChainDecision + 与侦察图谱的桥接边（chain_graph_writer.py:1-14）——**会话记忆升格为跨会话图谱资产**。
- **工作区文件**：notes/（agent 自由 scratch，写 recon 摘要/payload 文件/todo）、jobs/（后台作业日志，可 fs_grep 中途窥视）、uploads/（用户投递）。会话恢复时 `_prior_chain_context` 从图回灌。

设计上明显区分了"LLM 上下文记忆（每轮重组、有淘汰窗口）"与"事实记忆（图谱，永不丢失）"：execution_trace 滚动窗口会淘汰旧步骤，但 confirmed findings 已进图，query_graph 永远可取回。

---

## 9. 隔离与安全

### 9.1 目标准入（纵深两层）

- **硬护栏**（agentic/orchestrator_helpers/hard_guardrail.py）：不可关闭的正则 + 300+ 精确域名清单，封禁 gov/mil/edu/int TLD 及联合国体系/国际组织（is_hard_blocked，hard_guardrail.py:331-366）。纯确定性，不依赖 LLM/网络/设置。
- **软护栏**（guardrail.py:24-100）：LLM 判定，prompt 明确"宽进：只封百分百确定的大公司/政府/关键基础设施，允许 lab/靶场/小众域名"。IP 模式做反向 DNS 后判主机名，私网 IP 自动放行。

### 9.2 提示注入防御（亮点）

`agentic/prompt_safety.py:1-50`：威胁模型明言"**目标是被扫描的敌手，工具输出的每个字节都是攻击者可控的**"。防御是**不可伪造边界**：`wrap_untrusted()` 用输出产生**之后**生成的随机 nonce 包裹（`<<<UNTRUSTED_TOOL_OUTPUT id=<nonce>>> ... <<<END_...>>>`），worker 永远看不到 nonce 故无法伪造闭合标记；正文内的伪标记被零宽空格中和（`_neutralize_markers`，prompt_safety.py:28-31）；文本保持逐字（不损坏 payload 可读性）。系统 prompt 一次性注入 `UNTRUSTED_OUTPUT_GUIDANCE`（prompt_safety.py:107-125）教模型"标记内是数据不是指令、id 不匹配的标记是攻击"。连"询问图中节点"的信封也把节点属性围栏（fence_node_context，prompt_safety.py:78-102）。CypherFix 的 triage→codefix 传递同样全部围栏（cypherfix_codefix/prompts/system.py:8-19）。

### 9.3 出网与 SSRF

`fetch_guard.py`：抓取外部内容（tradecraft 爬虫）时拒绝 loopback/RFC1918/link-local/169.254.169.258/CGNAT/非全局地址，DNS 解析后逐 IP 判定；`llm_url_guard.py` 是自托管模型场景的反向例外。文档承认 connect-time DNS-rebinding TOCTOU 为残留风险（fetch_guard.py:12-15）——诚实标注残余风险本身是好实践。

### 9.4 容器与网络

- 三网隔离：agent 在 `redamon` 网无目标出网；工具在 `pentest-net`；扫描器容器 `cap_drop:[ALL]` + `read_only` + `tmpfs` + `no-new-privileges`（recon_orchestrator/container_manager.py:711-722, 1175-1178, 4082-4083；bind-mount 源码目录的容器因需 DAC_OVERRIDE 而显式推迟 cap_drop 并注释原因，:1073）。
- capture-proxy 路由：开启抓包时 curl 参数**追加** `-x proxy -k -H X-Redamon-Ctx`（append 保证用户参数不能覆盖 enforced 路由泄 tag，network_recon_server.py:172-185）。
- LLM 端点防滥用：`llm_guard.py` 常量时间密钥比较 + (user,ip) 令牌桶 + 日上限。
- RoE（Rules of Engagement）系统：客户端/联系人/起止日期/时间窗/排除主机/禁止工具与类别/阶段上限/全局 RPS/数据处理级别/保留期/合规框架/应急流程，LLM 解析原始 RoE 文本（/roe/parse），执行层在 execute_tool/execute_plan 双处硬拦截（execute_tool_node.py:120-133、execute_plan_node.py:47-77 的 `_check_roe_blocked`）。

### 9.5 危险动作的人机门

`DANGEROUS_TOOLS`（project_settings.py:41-55）列 18 个需逐次确认的工具（含 execute_curl、kali_shell、execute_code 等一切能发真实流量的）。think_node.py:2125-2165：decision 命中即置 `awaiting_tool_confirmation` 并构造 `ToolConfirmationRequest`（single/plan 两种 mode，plan 模式只列出波内危险子集），图中断在 END，操作员 approve/modify/reject 后恢复；modify 可改参数。另有会话参数缺失强制 ask_user（LHOST/LPORT，think_node.py:2100-2123）与 fireteam 成员升级队列。

### 9.6 租户隔离

Neo4j 每节点带 user_id/project_id，约束键统一为 (业务键, user_id, project_id)——schema.py:50-70 的 G2 注释详细记录了全局唯一约束曾导致"两项目扫同一目标时一方静默丢失/被劫持"的事故与修复。Cypher 生成侧强制 label-in-pattern 以便逐 pattern 注入租户过滤（base.py:1546-1557）。suppressed findings（Muted）对 agent 查询不可见且禁止提及（base.py:1559-1566）。

---

## 10. 关键代码走读（6 条链路）

### 链路 1：ReAct 主循环（invoke → think → execute → loop）

入口 `invoke_with_streaming()`（agentic/orchestrator.py:1064）：`_apply_project_settings(project_id)` 热加载每项目设置（LLM/tool key/rotation/RoE/tradecraft 资源，orchestrator.py:206-330，含大量"从共享 stamp 改为 per-session 读取以修并发竞态"的注释），LLM 未配置则 fail-fast（orchestrator.py:1101-1105）；输入经 `fence_node_context` 围栏后 `graph.astream(stream_mode="values")` 逐状态转发给 streaming callback。

`think_node`（orchestrator_helpers/nodes/think_node.py:160）每轮：contextvars 设置租户/阶段（:187-190）→ 计算生产力分（:249-259，`compute_productivity_score` 带 churn-aware novelty 饱和度）→ 判定 Deep Think 触发（:277-343，LATS 驾驶中则压制）→ 重组系统 prompt（静态前缀 + 当前状态：chain_context/todo/target_info/qa_history/objective_history）→ `retry_llm_call` 调 LLM → `try_parse_llm_decision` 解析 JSON → 更新 todo / 写 chain 记忆 / 设置 `_decision`。

`execute_tool_node`（nodes/execute_tool_node.py:55）：RoE 阶段上限检查（:120-133）→ **recon 预算门**（:142-177）→ 长 MSF/hydra 命令走 `execute_with_progress` 逐行流式（:182-212）→ 工具任务包成可取消 future 支持单工具 Stop（:218-249）→ 嵌入错误检测（:273-277）→ error_class 分类（:283-289）→ flag 捕获事件（:316-327）→ msf session ↔ chat session 映射注册（:330-354）。回到 think，周而复始直至 complete / max_iterations（路由 _route_after_think，orchestrator.py:844-914）。

### 链路 2：侦察预算门——"强制提交"机制

execute_tool_node.py:142-177（摘录）：

```python
if get_setting('RECON_BUDGET_GATE_ENABLED', True) and phase == "informational":
    recon_budget = get_setting('RECON_MAX_INFORMATIONAL_ITERS', 15)
    discovery_tools = set(get_setting('RECON_DISCOVERY_TOOLS', [...]))
    if iteration >= recon_budget and tool_name in discovery_tools:
        msg = (
            f"RECON BUDGET EXCEEDED ({iteration} informational iterations; limit "
            f"{recon_budget}). ... STOP scanning and COMMIT now: (1) emit "
            f"action='switch_skill' to the vulnerability class the live evidence "
            f"implies ..., then (2) emit action='transition_phase' to exploitation ..."
```

默认 15 轮信息收集后禁用 13 个发现类工具，但**故意豁免 graph_schema/graph_summary**（:150-155 注释：超预算的 agent 仍需定向，二者不发一个包）。这是对"agent 永远在侦察不敢下手"这一 LLM 渗透系统通病的硬约束，配套 prompt 侧的"budget-limited recon"多处强调（base.py:487-493）。

### 链路 3：危险工具确认（人机协同中断/恢复）

think_node.py:2125-2165（摘录）：

```python
if (get_setting('REQUIRE_TOOL_CONFIRMATION', True)
        and not updates.get("awaiting_user_approval")
        and not updates.get("awaiting_user_question")):
    action = decision.action
    if action == "use_tool" and decision.tool_name in DANGEROUS_TOOLS:
        updates["awaiting_tool_confirmation"] = True
        updates["tool_confirmation_pending"] = ToolConfirmationRequest(
            mode="single", tools=[{...}], reasoning=decision.reasoning or "",
            phase=phase, iteration=iteration).model_dump()
```

路由 `_route_after_think` 见 awaiting 标志 → `await_tool_confirmation` → END（图挂起，checkpoint 已存）。用户在 UI 决策后 `resume_after_tool_confirmation_with_streaming()`（orchestrator.py:1308-1386）以 `update_data={tool_confirmation_response, tool_confirmation_modification, _decision:None, _completed_step:None}` 恢复——**清空陈旧字段防新 callback 重复发射事件**（:1346-1353 注释解释了首个 yield 是 checkpoint 快照的坑）。`_route_after_tool_confirmation`（:941-973）按 reject/plan/fireteam_redeploy 分流；modify 允许操作员直接改 tool_args。

### 链路 4：侦察管线（6 阶段并行 + 图谱写）

`recon/main.py`：设置加载（含 stealth/AI 级联覆盖，:37-39）→ capture-proxy 路由一次性配置（:44-49）→ **graph_db 预检**（warn_if_graph_db_unusable，:60-63——修复"bind mount 坏掉时管线全绿但什么都没写"的静默失败，issue #169）→ whois/domain_recon（子域+DNS+所有权 TXT 验证）→ port_scan（naabu/masscan）→ http_probe → resource_enum（katana/hakrawler/ffuf）→ vuln_scan（nuclei，18.5 万规则）+ add_mitre（CWE/CAPEC 富化）。每工具输出经 `graph_db/mixins/recon_mixin.py` 等 MERGE 进图；域批次模式（DOMAIN_BATCH_GROUPS）支持操作员分组审批。`partial_recon.py` 是按需单工具运行入口（14+ tool_id），输入可**从图谱反推**（`_build_recon_data_from_graph`，partial_recon_modules/graph_builders.py）——工具间解耦靠图，不靠管道顺序。

### 链路 5：CypherFix（triage → codefix → PR）

TriageOrchestrator（cypherfix_triage/orchestrator.py:57-76，docstring 即架构说明）：**A-E 五步中只有 E 写库**——[A] 静态 Cypher 事实集为每个 finding 算 BASE 分（无 LLM）+ 读已存 review/decision 层；[B] 确定性键分组（共享同一修复的 finding 归组）；[C] LLM 仅对"无仍有效 review"的 finding 批量复核（12 条/30k 字符/20 分钟预算，超时发布为"not reviewed"而数学分保留，:24-28）；combine_layers 是唯一合成点（对应仓库 `priority-board-triage` skill 的"nobody writes a score"规则）；[D] 每组一条 remediation 草案；[E] claim→分批(500/事务)发布→upsert，节点锁下重读尊重并发写。CodeFix agent（cypherfix_codefix/）：克隆仓库入 codefix-sandbox（独立网），ReAct + 11 个 github_* 工具（read/edit/write/bash/glob/grep/list_dir/symbols/find_definition/find_references/repo_map——tree-sitter 代码导航），系统 prompt（prompts/system.py:21-122）注入修复任务时**所有 triage 产出字段逐个 wrap_untrusted**（标题/CVE/资产/建议方案/evidence，:8-19——triage 的散文来自扫描器输出，对 codefix 是不可信输入），运行 6 种语言运行时验证测试，产出 diff 并开 PR。

### 链路 6：LLM 输出分析 → 图谱写入（发现回流）

每次工具执行后，下一轮 think prompt 注入 `PENDING_OUTPUT_ANALYSIS_SECTION`（base.py:1069-1174），强制 LLM 返回结构化 `output_analysis`：interpretation、extracted_info（**固定字段名** primary_target/ports/services/technologies/vulnerabilities/credentials/sessions，"wrong names will break connections"，base.py:1241）、chain_findings（19 种 finding_type 枚举 + severity/evidence/related_cves/related_ips/confidence）、exploit_succeeded 判定标准、productivity verdict。think_node 解析后：extracted_info 合并进 target_info；chain_findings 追加 chain_findings_memory 并经 chain_graph_writer（后台线程、驱动单例带健康检查、fire-and-forget 不阻塞主循环，chain_graph_writer.py:15-21, 34-60）写 Neo4j；deserialization 技能还定义了 finding 晋升协议——确认证据必须填 `related_finding_ids=[捕获的候选 id]` 形成 CONFIRMS 边并回查验证（base.py:556-564）。

---

## 11. 亮点（可借鉴设计）与弱点

### 11.1 亮点（按可迁移价值排序）

1. **不可伪造的不可信内容边界（prompt_safety.py:34-50）**：输出产生后生成 nonce 的围栏 + 伪标记中和 + 一次性系统级指引，是对"目标即敌手"威胁模型下提示注入的最优雅开源解之一，任何把工具输出回灌 LLM 的渗透 agent 都应照抄。
2. **Productivity 评分 + Deep Think 竞争假设 + LATS 树搜索的三层反僵化栈**（§4.4）：用 LLM 自评 verdict 但**用观测计数器（state-grew/chain-advance）交叉审计并自动降级不诚实自评**；Deep Think 强制"下一动作必须是区分性探针" + Jaccard 拒绝换皮计划；LATS 的 `_lats_dead` 跨树死亡账本保证确认死的 class@target 永不重试。这是对 agent 死循环这一工程痛点的最完整解法。
3. **危险工具逐次确认 + 阶段转换审批 + 图中断/checkpoint 恢复的人机协同骨架**（§4.1、链路 3）：把"人在环上"做成了 LangGraph 一等公民（await_* 节点 → END → resume API），且 modify 语义（改参数/改计划）而非仅 yes/no；fireteam 成员的危险请求升级回父队列逐个审批。
4. **升级阶梯式并行：use_tool → plan_tools（单次 LLM 的工具波）→ deploy_fireteam（真子 agent）**，配 propensity 1-5 操作员策略、互斥组（msfconsole 单例）、"同 scope 不二次部署"规则——对"何时值得多 agent"给出了清晰经济学。
5. **渗透方法论的分层编码**（§7）：分类路由（switch_skill 一等动作）+ 18 个带硬门的漏洞类 workflow（如 SQLi 的 GATE A 认证绕过矩阵）+ 46 个 .md 技能库 + 攻击烈度参数化到设置层（RCE_AGGRESSIVE_PAYLOADS 默认只读证明）——"资深测试员的纪律"被结构化而不是埋在一段长 prompt 里。
6. **24 类 response_class 分类法 + 基线强制**（base.py:1254-1308）：把"这个响应说明什么"变成受约束分类任务，供 LATS 价值函数消费；"无基线不得判 differential"防 LLM 幻觉确认。
7. **图作为记忆中枢**：侦察管线工具间解耦靠图、agent"graph-first 不重测"、会话发现升格为 EvoGraph、partial recon 从图反推输入、triage 事实集纯 Cypher 无 LLM。
8. **工具输出治理**：20KB offload + head/tail stub + jobs/ 后台化 + fs_grep 中途窥视 + 嵌入错误检测翻转 success——长扫描不撑爆上下文。
9. **安全纵深清单化**：硬/软双护栏、三网隔离、cap_drop+read_only+no-new-privileges、append 式代理路由防覆盖、llm_guard 限流限额、RoE 全维硬拦截、密钥不进扫描容器、(键,user,project) 三元约束——且大量注释记录事故与残留风险（G2 租户碰撞、TOCTOU、bind-mount 不可用静默失败）。
10. **失败认识论文本**（base.py:1042-1048）："先复现再归因、三因分离、单次失败非否定、读字面位置不读应用自述、确认成功只认目标本证"——可直接复用的 prompt 资产。

### 11.2 弱点与风险

1. **体量与复杂度失控风险**：核心 19 万行 Python + 17 万行 TS，单文件 5295 行（api.py）、2657 行（tools.py）、2235 行（base.py）；配置项数百个（.env.example 22.8KB）；技能/设置/prompt 存在 9 层穿透（仓库自己的 `project-settings-cascade`、`builtin-agent-skill` skill 都在承认这点）。二次开发门槛极高。
2. **JSON 决策协议的脆弱性**：自研 JSON-mode 而非原生 function calling，需要 `try_parse_llm_decision`、参数 shape 四桶说明（base.py:1001-1027）、大量"WRONG 示例"prompt 纠偏——这是对弱模型兼容的妥协，强模型下原生 tool calling 更稳。
3. **软护栏的宽进策略**：guardrail.py 明确"When in doubt, ALLOW"——白名单式授权（scope 文件）缺位于目标准入，硬护栏只封政府/国际组织，恶意/误配的自有域名仍可被打；RoE 主要靠 prompt 纪律 + 少数硬拦截点。
4. **residual risks 已知未解**：DNS-rebinding TOCTOU（fetch_guard.py:15）、bind-mount 容器保留 CAP_DAC_OVERRIDE（container_manager.py:1073）、kali_shell 本质是任意命令执行（靠确认门 + 容器隔离兜底）。
5. **每轮全量 prompt 重组的成本**：think 每轮重建状态化系统 prompt（执行轨迹/chain context 全渲染），虽有 Anthropic cache 前缀切分，非 Anthropic provider 下长会话 token 成本高；execution_trace 淘汰窗口外的推理过程不可恢复（事实已进图，但"为什么"会丢）。
6. **LLM 依赖面广**：分类、护栏、Cypher 生成、triage、微端点全是 LLM——`/llm/nuclei-fp-filter` 等误判会静默改变图内容（虽有 evidence 引用约束）。
7. **测试对 Docker 强绑定**（host pytest 会错配依赖集），本地迭代摩擦大（AGENTS.md CRITICAL 规则）。

### 11.3 对本项目的适配建议（简）

- 直接借鉴：untrusted 围栏、三阶段+确认门骨架、productivity/Deep Think 反僵化、response_class 分类法、DANGEROUS_TOOLS + TOOL_PHASE_MAP 双层工具治理、workspace+offload+job_spawn 输出治理。
- 谨慎借鉴：fireteam（复杂度高，plan_tools 波已覆盖 80% 场景）、自研 JSON 协议（建议原生 tool calling）。
- 避免：配置项爆炸；应收敛为少量带 schema 的策略对象。

---

*报告基于 v6.26.2 全量源码静态走读，2026-10-12。*
