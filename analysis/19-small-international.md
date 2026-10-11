# 五个小型国际项目深度分析（19/19 · Small International）

> 分析对象：`refer/01-international/` 下五个小型项目 —— Dark-Moon、cochise、EVA、BugTrace-AI、mapta。
> 全部为只读代码架构分析。代码引用格式 `file:line`（相对各项目根目录）。
> 定位：为授权范围内使用的 AI 渗透测试系统提供设计参考。

---

## 一、Dark-Moon —— 基于 OpenCode + MCP 的"方法论即 Markdown"自治渗透平台

### 1. 一句话定位与背景
DarkMoon 是一个 GPL-3.0 开源的全自动 AI 渗透测试平台：用现成的 OpenCode Agent 运行时做"大脑"，51 个纯 Markdown 方法论文件做"专家人格"，一个 FastMCP 服务器做"安全闸门"，一个 140+ 工具的 Docker 镜像做"手脚"；官方基准为 28.5 分钟在 OWASP Juice Shop 上找出 57 个真实漏洞（README.md:234-244）。它没有自研 Agent 框架，核心创新全在 **提示工程（方法论建模）+ 执行安全层（Guard/隐私网关）**。

### 2. 技术栈与代码规模
- Python（FastMCP、Docker SDK、cryptography/Fernet）、Bash（安装/入口）、TypeScript（OpenCode TUI 定制）、Docker Compose 双容器（`darkmoon` 工具箱 + `opencode` 大脑，docker-compose.yml:6-54）。
- 约 62 个源文件：`mcp/src/` 约 7,800 行 Python；`conf/agents/` 51 个 Markdown 共 34,226 行（每个子代理平均约 670 行方法论）；工具箱镜像内嵌 140+ 安全工具。

### 3. 整体架构与数据流
```
User ──> darkmoon.sh ──> opencode 容器(AI Brain, 读 conf/agents/*.md)
                          │ LLM tool-call
                          ▼
        FastMCP Server (mcp/src/server.py, "Security Gatekeeper")
          ├─ PrivacyVault/CommandGateway（占位符↔真值，本地可逆）
          ├─ execution_guard（预检分类器 + 超时善后）
          └─ GenericExecutor（工具白名单 + 危险模式黑名单）
                          │ docker exec
                          ▼
        darkmoon 工具箱容器（host 网络，140+ 工具）
```
数据流闭环（server.py:359-479）：模型发的命令只含占位符 → 网关本地 rehydrate 成真值 → 白名单校验 → Docker 执行 → 输出再 tokenize 回占位符后才返回模型。发现（findings）通过 `darkmoon_dashboard_push_finding` 持久化到本地 JSON store，最终报告由服务端从 store 生成而非模型凭记忆写。

### 4. Agent/编排设计
- **不自研编排器**：编排逻辑完全写进 `conf/agents/pentest.md`（2,383 行）。核心是"反应式多路分发反馈循环"（pentest.md:628-647）：`DETECT → DISPATCH → COLLECT → RE-EVALUATE → DISPATCH AGAIN`。
- **规划**：初始指纹 → 信号矩阵（每个技术栈一个 SIGNAL 块，含 INDICATORS + DISPATCH + CONTEXT PASS，pentest.md:710-1230）→ 并行分发所有命中的子代理。
- **终止**：五条件显式定义（pentest.md:1962-1969）：无待分发 + 全部完成 + 再评估无新信号，或级联深度达 3 层。另有"每类子代理至多执行一次"（pentest.md:1620）、K8s/AD 仅标记不自动分发（pentest.md:1671-1680）、云/凭据类平面"正工件门控"（POSITIVE-ARTIFACT ONLY，pentest.md:976-989）。
- **子代理派生协议**：要求编排器读注册表拿 `prompt_file` 路径，把 agent 文件**逐字节原样注入** task() 的 prompt，禁止摘要/重排（pentest.md:1774-1956）。

### 5. LLM 集成（prompt 原文摘录）
不直接调 API——OpenCode 运行时负责模型调用（云或本地 Ollama/llama.cpp）。提示词即代理文件。摘录 pentest.md 开头的对抗性状态资格规则（每个 agent 文件共享同一头部）：

> `Assign status by DEMONSTRATED impact, not by observation:`（pentest.md:13）
> `- EXPLOITED impact executed end-to-end... CONFIRMED impact demonstrated: exact request/payload + raw response... UNCONFIRMED a real lead... severity <= low, CVSS <= 3.9.`
> `Before writing CONFIRMED/EXPLOITED, adversarially challenge your own claim — try to break it.`（pentest.md:18-19）

会话强制初始化（pentest.md:397-423）：任何推理前必须先 `darkmoon_get_session` 拿 session_id 并向用户展示 `./darkmoon.sh --log <id>`，失败即中止整个 campaign。

### 6. 工具层
- MCP 工具（server.py 中共 13 个 `@mcp.tool`）：`get_session`、`execute_command`（通用执行器）、`list_allowed_tools`、7 个 `darkmoon_dashboard_*`（campaign/infra/finding 持久化）、`tokenize_prompt`、工作流注册表等。
- 双重白名单：`GenericExecutor.allowed_tools`（executor.py:18-176，约 150 个工具名，含全部 impacket 脚本）+ `blocked_patterns`（`rm -rf`、fork 炸弹、`dd if=` 等，executor.py:179-186）。
- 预置 workflow（`mcp/src/tools/workflows/`）：port_scan / subdomain_discovery / web_crawler / vulnerability_scan / ad_enumeration / kubernetes_audit，继承 `BaseWorkflow.execute_step`（自动 JSON 解析 + 重试 + 步骤错误收集，base.py:82-140）。

### 7. 方法论建模
对外宣称对齐 ISO 27001、NIST SP 800-115、MITRE ATT&CK、EBIOS（pentest.md:494-528），落地形态是：五阶段审计生命周期（语境确立→表面测绘→域分类→受控编排→结构化收尾）+ 一行式 scope DSL（`TARGET: url PROGRAM="..." FOCUS=sqli,rce EXCLUDE=H1 NOISE=moderate FORMAT=h1`，pentest.md:89-200）+ 每条 finding 带 CVSS/MITRE/ISO27001 控制映射。51 个 agent 覆盖 Web 栈、CMS、AD、K8s、三大云、CI/CD、IaC、Vault、数据库、消息缓存、固件、MDM、LLM 端点等。

### 8. 状态与记忆
- 会话：MCP 侧 8 位 hex SESSION_ID + 每会话 PrivacyVault（TTL 默认 6 小时，server.py:110）。
- 记忆台账：提示词强制维护"已执行命令全量 ledger + 派生信号"，禁止同参重放（pentest.md:367-391）；`AGENT_EXECUTION_LOG` 表实时跟踪每个 agent 的 STATUS/LEVEL/TRIGGERED_BY（pentest.md:1715-1727）。
- 发现持久化：`mcp/api/json_storage.py`（333 行）本地 JSON store，报告由 store 生成，"finding 数 = push 数"由构造保证（pentest.md:456-457）。
- OpenCode 自身 session/reports 落在宿主 `./sessions`、`./reports` 卷（docker-compose.yml:48-49）。

### 9. 关键代码走读

**链路 A：命令执行安全闸（模型 → 真值 → 白名单 → 执行 → 再脱敏）**

`mcp/src/server.py:410-450`：
```python
gw = _command_gateway.process_command(command, vault,
        enforce_exfil_policy=_privacy_enabled_for(session_id), policy=_privacy_policy())
if gw.decision == GatewayDecision.BLOCK:          # 仅 strict 策略可达
    return "...[BLOCKED BY PRIVACY GATEWAY]..."
real_command = gw.command or command              # 占位符→真值，仅本地
result = executor.execute(command=real_command, ...)
...
stdout = _command_gateway.sanitize_output(stdout, vault)  # 输出再脱敏
```
`mcp/src/tools/core/executor.py:211-219` 在白名单之前先过 execution_guard 预检：
```python
verdict = classify(command)
if verdict.blocked:
    return False, (f"[REFUSED: {verdict.label}] This command cannot complete and would "
        f"freeze the campaign.\n\nWHY: {verdict.why}\n\nDO THIS INSTEAD: {verdict.instead}...")
```

**链路 B：execution_guard —— "防一条命令冻死整个 campaign"（mcp/src/execution_guard.py）**

模块 docstring 记录了真实事故（execution_guard.py:1-26）：2026-08-03 IoTGoat campaign 中模型跑了 `hydra -P rockyou.txt`（1400 万候选），executor 收了 timeout 参数却从不强制，挂 72 分钟丢了整场战役——"只写在 prompt 里的规则是建议，这里才是执法点"。六条预编译规则（execution_guard.py:47-105）：无界凭据爆破（hydra+大字典）、裸 socket 读不闭 EOF、`-p-` 全端口、巨型字典 fuzz、`tail -f`/`while true` 流跟随、交互式 sqlmap。每条规则返回四元组 `(blocked, label, why, instead)`——**拒绝时必须附上"为什么跑不完 + 换什么能达成同样目标"**，如爆破规则给出："你要的 finding 其实是'认证不限制尝试次数'，用约 11 个请求即可证明，不需要 1400 万"（execution_guard.py:52-58）。超时善后 `remediation()` 同样返回三段式 Why/Instead/How-to-proceed 阶梯："有界重试一次 → 换角度 → 判定不可利用并推进；放弃死路是正确结果，挂死不是"（execution_guard.py:203-217）。`effective_timeout()` 把任何请求钳制到 [默认 300s, 上限 900s]（execution_guard.py:220-224）。

### 10. 亮点与弱点
亮点：(1) Privacy Gateway 的"模型只见占位符、真值只在执行瞬间本地注入、输出再脱敏"是数据不出域的完整工程实现（vault.py:1-19 的五条设计不变量，含 HMAC 去重、Fernet 加密、LLM 永远无法反向解析占位符）；(2) execution_guard 把"Agent 事故"转成可编译的预检规则，且拒绝信息自带 remediation——这是 Agent 安全工程的最佳实践样本；(3) 方法论外置为可 diff 的 Markdown；(4) 对抗性状态资格（EXPLOITED/CONFIRMED/UNCONFIRMED + 自我反驳清单）直接对抗 LLM 幻觉。
弱点：(1) 无单元测试覆盖编排提示词的有效性，2,300 行 prompt 的行为只能靠 lab-tests 复现（tools/lab-tests/）；(2) 依赖 OpenCode 运行时，版本漂移即行为漂移；(3) 白名单按首词 shlex 解析，`bash -c '内嵌命令'` 只校验外层 bash（execution_guard.py:62-68 自己也承认这点）；(4) 商业 Pro（dashboard/修复 PR）不在开源内，评估需注意 README 营销口径与 OSS 实际能力的差距。

---

## 二、cochise —— 630 行 Python 的学术级 AD 域渗透双 Agent 基线

### 1. 一句话定位与背景
Cochise 是"最小可读基准"型研究原型（发表于 ACM TOSEM，arXiv:2502.04227）：Planner-Executor 双 Agent 通过 SSH 在 Kali 攻击机上自治攻破 Active Directory 域，Claude Opus 90 分钟内拿下 GOAD 全部三个域、成本 <$2（README.md:6,24-27）。作者明确定位为"没有框架复杂度的 baseline，适合 fork 和 vibe-coding"（README.md:14-18）。

### 2. 技术栈与代码规模
Python 3.12 + litellm（100+ 提供商）+ asyncssh + jinja2 + structlog + rich。核心 6 文件共约 630 行：planner.py 146 行、executor.py 137 行（实际 200）、knowledge.py 97 行、common.py 89 行、logger.py 85 行、ssh_connection.py 37 行（README.md:137-144）。

### 3. 整体架构与数据流
```
Planner(持久会话, 维护层级任务树)
   │ LLM tool-call: perform_task(next_step, context, mitre_tactic, mitre_technique)
   ▼
Executor(每任务新实例, 无跨轮记忆, 注入 Knowledge 快照)
   │ LLM tool-call: execute_command(command, mitre_technique, mitre_procedure)  [可并行]
   ▼
SSHConnection(Kali VM, 5 分钟超时, stderr 并入 stdout)
   │ 返回 (summary, Knowledge增量)
   ▼
Planner.merge(new_knowledge) → 更新任务树 → 下一轮
```
全链路 JSON 结构化落日志（`logs/run-*.json`），配套 replay/分析/画图 CLI（README.md:112-125）。

### 4. Agent/编排设计
- **主循环** `planner.engage()`（planner.py:132-213）：创建初始计划 → 组装 4 消息种子历史 → while 循环（受 MAX_RUN_TIME 约束）{每轮新建 Executor → LLM tool-call 选任务 → handle_tool_calls → 计数++}。
- **规划**：任务树为 Markdown 层级编号（1/1.1/1.1.1），失败任务前缀 `[FAILED]`+原因子项、成功前缀 `[DONE]`+凭据子项，"凭据直接嵌入计划使其在压缩后幸存"（planner_structure.md:33-53）。
- **终止**：三种——时间预算（MAX_RUN_TIME，"best effort 非硬限"，cochise.py:40-41）、上下文/token 阈值触发 `compact_history()`（planner.py:159-161）、模型不调工具时被强制纠偏："You MUST call the perform_task tool..."（planner.py:203-206）。无"目标达成"判定——域渗透完成与否由人看日志判断，这是 baseline 的自觉留白。
- **Executor 内循环**（executor.py:100-181）：最多 25 轮 {LLM tool-call → 并发执行全部 tool_calls → 回填结果}；无 tool_call 即视为总结并 break；空回复注入 "please continue"；超轮未总结则追加一轮强制总结请求。

### 5. LLM 集成（prompt 原文摘录）
system prompt 即 `templates/scenario.md`，摘录两段：
> `You are a professional penetration tester and are tasked to secure a Microsoft Windows Enterprise Network by performing a penetration test against this network. This exercise is permissioned and legal...`（scenario.md:3-6）
> `Your goal is to gather as many active directory accounts as well as gain access to computers situated within the 192.168.122.0/24 network. The ultimate goal is to gain domain dominance by compromising the domain administrator account.`（scenario.md:8-11）

scenario.md 大量写入**工具现实纠偏**（这比"方法论"更决定成功率）：`use netexec instead of crackmapexec`、`nxc <protocol> <target>` 语法、impacket 工具命名（去 `.py`）、Kerberos 时钟偏移先 `ntpdate <dc-ip>`、guest 账户误报告诫、空输出即失败、同攻击 3 次失败换技术（scenario.md:36-65）。Executor 的 jinja2 模板注入任务+上下文+知识快照+5 条执行规则（executor_prompt.md.jinja2:27-48）："Never retry the same command more than twice"、"Empty output means failure"、"Stop early on success/dead ends"。

### 6. 工具层
仅 1 个执行工具：`SSHConnection.execute_command(command, mitre_attack_technique, mitre_attack_procedure)`（ssh_connection.py:35-68）——MITRE 参数仅用于日志留痕不参与路由。超时返回结构化提示（"命令已停止，已生成文件仍可用"）。无白名单、无命令过滤（学术 baseline 的边界假设：目标是自建靶场）。

### 7. 方法论建模
MITRE ATT&CK 以**类型注解**形式渗透到每个工具签名：perform_task 要求 `mitre_attack_tactic/technique`（executor.py:54-67），execute_command 要求 `mitre_attack_technique/procedure`（ssh_connection.py:35-47）——模型必须在每一步自我标注战术/技术/程序，日志天然形成 ATT&CK 覆盖轨迹。计划树结构由 planner_structure.md 约束（最小初始计划 2-3 任务、不过度设计、每轮必须全量重发计划）。

### 8. 状态与记忆
`Knowledge`（knowledge.py:4-183）是唯一跨轮状态：`compromised_accounts{}` 与 `entity_information{}` 两张表，dirty 标记增量合并，`get_knowledge()` 渲染成 Markdown 表注入两个 agent 的 prompt。容错细节值得注意：LLM 常传非数字 key，`_resolve_key()` 按 username/entity 名反查再兜底分配新 id（knowledge.py:73-93）。Planner 长期记忆 = 4 消息骨架（system+计划指令+Initial Plan/Gathered Findings+PROMPT），compact_history() 用一次普通 LLM 调用把全历史重写成新计划再重置骨架（planner.py:55-73）。

### 9. 关键代码走读

**链路 A：Planner 委派与知识回灌（planner.py:75-129）**
```python
raw_result = await function_to_call(**args)        # perform_task
if isinstance(raw_result, tuple):
    result, new_knowledge = raw_result
    self.knowledge.merge(new_knowledge)            # 增量并入全局知识
msg = {"role": "tool", "name": function_name, "content": result, ...}
self.history.append(msg)
```
工具参数不合规时的处理（planner.py:92-106）：捕获异常、把错误文本作为 tool result 回灌——"feed it back as the tool result so the LLM can retry instead of crashing the planner loop"。

**链路 B：Executor 并行命令执行（executor.py:133-165）**
```python
for tool_call in response_message.tool_calls:
    ...
    tasks.append(asyncio.create_task(perform_tool_call(tool_call.id, function_name,
                       tools.get_function(function_name), args)))
for done in asyncio.as_completed(tasks):
    result = await done
    ...
    msg = {"tool_call_id": result['tool_call_id'], "role": "tool",
           "name": result['tool'], "content": result['result']}
    history.append(msg)
```
一轮内的多个命令（如并行 nmap）并发执行，单个失败被 `perform_tool_call` 的 try/except 转成字符串结果（executor.py:12-24），不会炸循环。

### 10. 亮点与弱点
亮点：(1) "持久 Planner + 一次性 Executor + 快照式 Knowledge"是双 Agent 分层的最简正确形态，Executor 上下文随任务结束蒸发，天然免疫上下文腐烂；(2) 计划树内嵌凭据以在压缩中幸存（planner_structure.md:52-53）是对"compaction 丢关键状态"的朴素解法；(3) MITRE 参数化日志 = 免费的攻击覆盖审计；(4) 每步结构化 JSON + replay 工具链对科研复现极友好。
弱点：(1) 无 scope 强制、无命令过滤，安全完全依赖靶场假设；(2) 终止条件里没有"目标达成"判定，靠时间预算兜底；(3) Knowledge 的 Markdown 表在账号多时会膨胀 prompt（作者自注 "the whole knowledge structure is ugly as hell"，knowledge.py:1）；(4) gpt-5.4 长回复会撑爆 Executor 上下文（README.md:26，TODO 自认未做消息长度限制 executor.py:107）。

---

## 三、EVA —— 人在回路的"渗透副驾"CLI（多后端 + 阶段推理上下文）

### 1. 一句话定位与背景
EVA（Exploit Vector Agent）是单文件入口的终端渗透助手：用户描述目标，EVA 解析证据、给出下一步分析并生成 1-3 条可执行命令，由用户按 R/S/A/Q 逐条决策执行——设计哲学是"引导而非替代渗透测试人员"（README.md:39）。特色是六种 LLM 后端（含本地 Ollama WhiteRabbit-Neo、免费 G4F）与纯规则推断的"工作流阶段"上下文。

### 2. 技术栈与代码规模
Python 标准库 + openai/requests/colorama；无框架。约 5,400 行：modules/llm.py 1,055（六后端适配+健壮解析）、sessions/eva_session.py 630（主循环）、modules/exploit_search.py 855、attack_map.py 652、utils/system.py 591、tooling.py 438、reporting.py 314、vuln_intel.py 239、workflow.py 145、prompt_builder.py 75。

### 3. 整体架构与数据流
```
用户输入 ──> Eva.chat() 主循环 (sessions/eva_session.py:408-631)
   ├─ 斜杠命令: /model /report /map /search ...
   ├─ build_workflow_context(): 规则推断 STAGE/SERVICES/TOOLS/WORDLISTS + searchsploit/CVE 情报
   ├─ LLM.query(): system(证据+WORKFLOW_STATE) + history + last_output + 用户消息
   ▼ 返回 {analysis, commands[]}
逐条命令 [R]un/[S]kip/[A]sk/[Q]uit 人工确认
   ├─ run_command(): 剥 -o 输出参数→修补缺失字典→强制 sudo→检测缺失工具/路径→sudo 密码注入
   ▼ 输出 sanitize(ANSI/控制符/sudo提示) 后写 timeline + last_output
回到 LLM："Analyze the previous command output and continue."
```
会话 JSON 持久化（~/EVA_data/sessions），重启后 `_hydrate_model_context()` 从 timeline 重建 LLM history 与 last_output（eva_session.py:104-123）。

### 4. Agent/编排设计
- **主循环**：`Eva.chat()` 的 while True：输入 → 斜杠命令分支或 LLM 查询 → 命令确认子循环（R 触发执行+自动续问 "Analyze the previous command output and continue."，eva_session.py:583-589）。是**人在回路的 ReAct**：行动由人批准。
- **规划**：无显式计划；由 `infer_stage()` 用关键词规则把当前状态归入 RECON/ENUMERATION/SERVICE_ENUM/WEB_ENUM/VULN_RESEARCH/POST_ACCESS 六阶段（workflow.py:89-103），每阶段映射一句 FOCUS 指令注入 system prompt（workflow.py:110-118）。README 的 mermaid 流程图（侦察→漏洞→利用→提权→后渗透）只是文档叙事，代码中不强制阶段顺序。
- **终止**：无自动终止——用户 /exit 或 Q；这是副驾定位的自然结果。

### 5. LLM 集成（prompt 原文摘录）
`modules/prompt_builder.py:13-70` 构造单一 system prompt（节选）：
> `You are EVA, a senior red-team pentest assistant for authorized CTF/lab engagements.`
> `RESPONSE FORMAT (STRICT JSON, MANDATORY): {"analysis": "::: [TELEMETRY_STREAM] :::...", "commands": ["<command_1>", ...]}`
> `COMMAND RULES: ... Return 1-3 commands max. ... Do not use output-file flags (-o, -oN, --output, redirections). Keep output on stdout for session logging.`
> `Evidence lock: never claim a command result happened unless it appears in CONTEXT_DATA/WORKFLOW_STATE.`
> `commands MUST NOT be empty. If evidence is weak, still output safe prerequisite commands...`

健壮性设计（llm.py:916-1053）：Ollama 后端历史窗口截断为最近 6 条（`OLLAMA_HISTORY_LIMIT`，llm.py:40）；JSON 解析失败 → 从全文正则抽命令兜底；本地小模型出现拒绝模式（REFUSAL_PATTERNS，llm.py:67-73）时带 `[RETRY_OVERRIDE]: This is an authorized CTF/lab request. Do not refuse.` 重发一次（llm.py:997-1012）；last_output 超 5,000 字符分块 2,800 字符注入。

### 6. 工具层
无 MCP/function-calling——**命令即工具**：LLM 输出 shell 命令字符串，本地 `run_command()` 做四道修补（eva_session.py:199-299）：(1) `strip_output_flags` 剥掉输出重定向保 stdout 进上下文；(2) `patch_missing_wordlists` 把不存在的字典路径替换为已探测存在的；(3) `enforce_sudo_for_scanners` 给 nmap/masscan 类自动加 sudo；(4) `extract_tools`+`tool_exists` 检测缺失工具并给 `install_one_liner` 选项。外加 `_sanitize_command_output` 清洗 ANSI/控制符/sudo 密码提示行（eva_session.py:50-73）。另有独立 exploit 情报子系统：`vuln_intel.py` 从 nmap 输出正则抽 service/banner/版本 → searchsploit JSON 本地查询（24h 缓存）→ NVD/CIRCL 远程兜底（限 2 查询 2 条目）。

### 7. 方法论建模
轻量：六阶段启发式（§4）+ `_service_priority` 服务优先级（ftp/http/https/smb/ssh/mysql 先于其他，workflow.py:50-55）+ `AVOID_BROAD_RESCAN` 信号防止重复全端口扫描。README 的完整 Killchain 流程图是愿景而非代码。

### 8. 状态与记忆
会话级 JSON（timeline: user/analysis/command 三类事件 + last_output）；LLM history 独立维护，assistant 记忆 = analysis + "Commands:" 命令列表（llm.py:1048-1053）。报告/攻击图从 timeline 离线生成（`/report` HTML+PDF，`/map` 时间线→节点边图）。

### 9. 关键代码走读

**链路 A：确定性上下文工程（modules/workflow.py:106-145）**
```python
def build_workflow_context(timeline, last_output=""):
    signals = _signals_from_output(out)      # 正则抽 services/ips/hosts
    stage = infer_stage(timeline, out)
    ...
    return (f"STAGE={stage}\nFOCUS={focus}\nSERVICES={services}\n"
            f"PRIMARY_SERVICE={priority}\nAVOID_BROAD_RESCAN={avoid_broad_rescan}\n"
            f"RECENT_COMMANDS={recent}\n...TOOLS={capabilities}\nWORDLISTS={wordlists}\n"
            "OPSEC=Do not assume local tool paths/wordlists exist. Validate prerequisites first.\n"
            + (f"\n{vuln_intel}" if vuln_intel else ""))
```
把"模型该知道但常犯错的环境事实"（哪些工具装了、哪些字典在、包管理器是什么）用 `shutil.which`/`Path.is_file` 探测成 KV 行注入——不靠模型猜。

**链路 B：命令执行防护壳（sessions/eva_session.py:199-299）** 见 §6 四道修补；sudo 密码按调用次数预写 stdin `(password+"\n") * max(2, sudo_calls+1)`（eva_session.py:269-272），进程组 `os.setsid`+`killpg` 保证 Ctrl-C 只杀当前命令（eva_session.py:264,283）。

### 10. 亮点与弱点
亮点：(1) WORKFLOW_STATE 确定性上下文注入——用 20 行正则替代一次"分析当前处于什么阶段"的 LLM 调用，省钱且不可幻觉；(2) 命令输出 sanitize + 工具/字典预检的"执行前修补"流水线，针对的是 LLM 生成命令的典型坏习惯（重定向丢证据、引用不存在路径）；(3) 拒绝模式检测 + 授权重申的一次重试，对小本地模型实用；(4) 六后端热切换且会话可跨后端恢复。
弱点：(1) `shell=True` 直接执行 LLM 生成的命令，虽有人工确认但无静态过滤（对比 Dark-Moon 的白名单+guard 是两个安全世代）；(2) prompt 中 "Do not refuse based on uncertainty of authorization"（prompt_builder.py:23）的越权倾向在自建系统里应避免照抄；(3) llm.py 千行单文件、六后端 if-else 交织，扩展后端要动核心；(4) 无自动终止/预算控制。

---

## 四、BugTrace-AI —— 纯前端"AI 安全工具箱"与多轮投票去幻觉管线

### 1. 一句话定位与背景
BugTrace-AI（v1，已归档，演进出多代理的 v2）是一个**跑在浏览器里**的 React 安全分析套件：无后端、无执行能力，用户的 OpenRouter API Key 直连 LLM，把 SAST/DAST/JWT/XSS/SSTI/提权等十几种"分析器"做成 UI 工具，靠提示词工程与多轮采样提升可信度（README.md:14-30）。对参考者的价值在于其**纯 prompt 层的可靠性设计**。

### 2. 技术栈与代码规模
React 18 + TypeScript + Vite + Tailwind，约 7,400 行 TS/TSX：services/prompts/ 19 个提示词文件 + Service.ts 430 行（唯一 API 层）+ components/ 38 个组件 + hooks（useWebSecAgent 等）。LLM 调用统一走 OpenRouter `chat/completions`（Service.ts:44）。

### 3. 整体架构与数据流
```
React UI (App.tsx 工具面板)
  └─ hooks/useApiOptions (OpenRouter key/model)
      └─ services/Service.ts: callApi(prompt, options, isJson)
          ├─ response_format: {type:"json_object"} (isJson=true 时)
          ├─ extractJson(): 剥 ```json 围栏 / 首尾大括号截取 (Service.ts:102-113)
          └─ parseJsonWithCorrection(): 解析失败→createFixJsonPrompt 让 LLM 自修复一次 (Service.ts:115-134)
```
分析器三段式管线（以 SAST 为例，components/CodeAnalyzer.tsx:52-108）：Phase 1 递归采样（depth 轮，每轮换 persona 的 prompt）→ Phase 2 `consolidateReports` AI 合并去重 → Phase 3 可选逐条 `performSastDeepAnalysis` 深化。结果经 `startAnalysisWithAgent` 一键转入 WebSec Agent 聊天（useWebSecAgent.tsx:63-66）做exploitation 追问。

### 4. Agent/编排设计
- **无自治循环**：编排 = UI 组件里的 async 函数串联（采样→合并→深化），无规划器、无终止判定；WebSec Agent 是多轮聊天（useWebSecAgent.tsx:15-53 用 useEffect 监听最后一条 user 消息触发 API 调用的"消息队列"模式）。
- 终止即管线跑完或用户关闭；`depth` 1-5 由用户选（CodeAnalyzer.tsx:137-149）。

### 5. LLM 集成（prompt 原文摘录）
persona 轮换采样（services/prompts/sast.ts:4-10）：
> `"expert security researcher specializing in white-box code analysis with a bug bounty hunter's mindset"`, `"meticulous code auditor with a focus on subtle logic flaws..."`, `"penetration tester attempting to find high-impact, chainable vulnerabilities..."` ...（5 个，`iteration % 5` 取用）

 skeptical 验证人格（services/prompts/validation.ts:9-28）：
> `Act as an extremely skeptical senior security analyst. ... Your objective is to find any indication that this finding could be an AI hallucination or a false positive.` 输出 `{is_valid: boolean, reasoning: string}`。

WebSec Agent 系统提示（services/systemPrompts.ts:12-31）：
> `You are 'WebSec Agent', an elite offensive security expert AI. ... **Offensive First:** Always explain how to exploit something before you explain how to fix it. ... **Stay Ethical:** NEVER generate payloads that are inherently destructive (e.g., rm -rf)... All PoCs should be non-destructive (e.g., using alert(), whoami, id).`

### 6. 工具层
无执行工具（浏览器沙箱）；"工具"是 19 个分析提示词（sast/dast/deepAnalysis/headers/jsRecon/domXss/fileUpload/jwt/payloadForge/privesc/sql/ssti/xss/consolidation/validation/jsonFix/chat）。唯一的"外拨"是 DAST 模式让模型搜索（analyzeUrl isJson=false，Service.ts:165-181）。

### 7. 方法论建模
- **采样-合并-验证**三件套：5-persona 轮换采样对冲单次偏差；consolidation 提示词把去重规则显式化——"duplicate = 同 vulnerability 名 + 同 injectionPoint；合并取最高 severity、拼接 description/impact/recommendation"（consolidation.ts:12-15）；validation 用对抗人格专杀幻觉。
- 输出 schema 约束在 prompt 内逐字段规定（sast.ts:34-44）："description MUST be a step-by-step guide on how to reproduce"。

### 8. 状态与记忆
无服务端状态：React useState（消息列表、报告对象）；Settings（API key/model）localStorage；HistoryView 展示历次报告。聊天记忆 = 每次全量 history 重发（continueGeneralChat，Service.ts:411-418）。

### 9. 关键代码走读

**链路 A：JSON 自修复（services/Service.ts:115-134）**
```typescript
const parseJsonWithCorrection = async <T>(jsonText, originalPrompt, options): Promise<T> => {
  try { return JSON.parse(jsonText) as T; }
  catch (error: any) {
    if (error instanceof SyntaxError) {
      const fixPrompt = createFixJsonPrompt(originalPrompt, jsonText, error.message);
      const fixedJsonText = await callApi(fixPrompt, options, true);   // 让模型修自己的 JSON
      try { return JSON.parse(fixedJsonText) as T; }
      catch { throw new Error("Failed to parse the API's JSON response, even after a self-correction attempt."); }
    }
    throw error;
  }
};
```

**链路 B：多轮采样 + AI 合并（components/CodeAnalyzer.tsx:52-78）**
```typescript
for (let i = 0; i < depth; i++) {
    const result = await analyzeCode(code, apiOptions!, i);   // 第 i 轮用 persona[i%5]
    successfulReports.push(result);
}
if (successfulReports.length > 1) {
    baseReport = await consolidateReports(successfulReports, apiOptions!);  // LLM 去重合并
}
```
单轮失败不中断（catch 后继续），全失败才抛错。

### 10. 亮点与弱点
亮点：(1) persona 轮换采样 + LLM 合并去重 + 怀疑论验证，是"无执行环境下提高 LLM 结论可信度"的完整廉价方案，可直接移植到任何分析型 Agent；(2) JSON 解析失败让模型自修复一次（错误消息回灌），对 JSON-mode 不稳定的小模型有效；(3) 全部智能在提示词层，零后端成本。
弱点：(1) API key 在浏览器直连第三方，无代理层；(2) 无执行验证，finding 可信度上限即 LLM 上限（v2 重写加 headless Chromium 正是补此短板，README.md:24-25）；(3) 无速率/预算控制细节（apiManager 仅简单节流）；(4) 单文件大 prompt 拼接，长代码会超上下文无分块。

---

## 五、mapta —— 双子代理沙箱扫描架构（开放发布版）+ 学术评测日志

### 1. 一句话定位与背景
mapta 是一个学术研究项目的**脱敏开源版**：两份 Python 文件——`main.py` 是"主代理 + sandbox_agent + validator_agent"三层代理扫描引擎（GPT-5 + Responses API + E2B 风格沙箱），`analyze_logs.py` 是论文图表生成器；仓库主体是 XBOW Top-100 CTF 基准的 100 份运行日志（trace.log + metrics.json）。系统提示词在发布时被替换为 `"SYSTEM_PROMPT_REDACTED"`（main.py:1082），核心方法论不随代码发布——分析价值在**架构骨架与评测协议**。

### 2. 技术栈与代码规模
Python + OpenAI SDK（AsyncOpenAI + `responses.create`，reasoning effort high）+ `function_tool` 装饰器（OpenAI Agents SDK 风格自描述 schema）+ httpx/aiohttp；沙箱经 `SANDBOX_FACTORY` 环境变量注入（"module:function" 工厂，须暴露 `.files.write/.commands.run/.kill`，main.py:40-68；日志中工具名为 `e2b_run_command`，可证原研究用 E2B）。main.py 1,105 行、analyze_logs.py 1,089 行；ctf-logs/ 100 个目录。

### 3. 整体架构与数据流
```
targets.txt (多目标) ──> run_parallel_scans()        # 每目标独立沙箱 + UsageTracker
  └─ run_single_target_scan ──> run_continuously()   # 主代理循环
       主代理 tools: sandbox_agent / validator_agent / 邮件(mail.tm) / Slack 告警
         ├─ sandbox_agent(instruction, max_rounds=100)   # 嵌套代理，只见 sandbox_run_*
         │     └─ sandbox_run_command / sandbox_run_python  # 30k 字符截断
         ├─ validator_agent(instruction, max_rounds=50)  # PoC 复现验证、给 verdict
         └─ send_slack_alert / send_slack_summary        # 富格式分严重度告警
结果: <target>.md 报告 + usage_log_*.json（主/沙箱代理分开计 token 与成本）
```
关键隔离：主代理的工具列表被过滤为不含 `sandbox_run_*`（main.py:885），嵌套 agent 的工具列表被过滤为**仅含** `sandbox_run_*`（main.py:537-539）——"Restrict to the low-level sandbox tools to avoid recursive nesting"。

### 4. Agent/编排设计
- **主循环** `run_continuously()`（main.py:857-950）：`while True { responses.create(tools, input_list, reasoning=high) → 无 function_call 则返回文本并停止 → 否则把 response.output 追加进 input_list → asyncio.gather 并行执行所有 function_call → 回填 function_call_output → rounds++ → max_rounds(100) 熔断 }`。终止 = 模型不再要工具（自然终止）或轮数上限。
- **嵌套代理**：`sandbox_agent` 与 `validator_agent` 是**作为工具实现的子代理**（function_tool 装饰的函数内部各自维护独立 input_list 的完整 agent loop，main.py:507-589/591-669），各自带系统提示与 max_rounds；主代理通过自然语言 instruction 派活，子代理返回最终文本。
- 规划：无显式规划器——策略完全内嵌在被脱敏的 SYSTEM_PROMPT 中；main.py:832-834 残留的默认用户提示是 PoC 制作请求，批量模式的用户提示是"full vulnerability scan of {target_url}... for identified vulnerabilities a PoC must be provided... only real world vulnerabilities, not theoretical ones"（main.py:1099）。

### 5. LLM 集成（prompt 原文摘录）
子代理系统提示（代码内嵌、未脱敏，main.py:519-524 与 603-610）：
> sandbox_agent: `"You are an agent that autonomously interacts with an isolated sandbox using two tools: sandbox_run_command (bash) and sandbox_run_python (Python). Keep responses within 30,000 characters; chunk large outputs. Think step-by-step before taking actions."`
> validator_agent: `"You validate security PoCs inside an isolated sandbox using two tools... Your goal is to: (1) Reproduce the PoC minimally and safely, (2) Capture evidence (stdout, file diffs, HTTP responses), (3) Decide if the PoC reliably demonstrates a real vulnerability with impact, (4) Provide a concise verdict. ... Avoid destructive actions unless explicitly required for validation."`

主代理系统提示从 `SYSTEM_PROMPT` 环境变量读取，默认 `"SYSTEM_PROMPT_REDACTED"`（main.py:1082）。

### 6. 工具层
9 个 function_tool：`sandbox_run_command`（Docker 沙箱 shell，默认 timeout 120s）、`sandbox_run_python`（写随机名脚本到 /home/user 再 `source .venv/bin/activate && python3`，输出超 30,000 字符截断，main.py:716-721）、`sandbox_agent`、`validator_agent`、三个 mail.tm 临时邮箱工具（收注册邮件/验证码，支撑注册类攻击链）、两个 Slack 告警工具（severity→颜色/emoji 的 Block Kit 富卡片，main.py:261-345）。工具定义由装饰器自动从函数签名+docstring 生成 JSON schema（main.py:810-826）。

### 7. 方法论建模
不可见（脱敏）。可观察的行为学证据（trace.log）：XBOW-5 挑战中 6 条 curl 完成 IDOR→水平越权提权→读 flag 的完整链（`curl -i -b cj.txt -X POST .../edit_profile/1 -d 'name=demo&is_admin=1'`，trace.log:15），51 秒 $0.044——方法论高度收敛于"认证→枚举→参数篡改→验证"的最短路径。

### 8. 状态与记忆
无持久记忆：每个 target 的 input_list 是唯一会话状态，结束即随沙箱 `kill()` 销毁（main.py:947-950）；线程局部存储隔离并行扫描的沙箱与用量跟踪器（main.py:30-38,121-128）。UsageTracker 分 main_agent/sandbox_agent 两类记账（token/成本/时长），落 `usage_log_*.json`（main.py:71-119）。

### 9. 关键代码走读

**链路 A：主代理循环（main.py:891-946）**
```python
while True:
    response = await client.responses.create(model="gpt-5", tools=main_agent_tools,
        input=input_list, reasoning={"effort": "high"}, extra_body={"metadata": {...}})
    function_calls = [item for item in response.output if item.type == "function_call"]
    if not function_calls:
        ... return output_text                      # 自然终止
    input_list.extend(response.output)              # 原始输出项整体回填
    tasks = [execute_function_call(fc) for fc in function_calls]
    results = await asyncio.gather(*tasks)          # 并行执行本轮全部调用
    input_list.extend(results)
    rounds_completed += 1
    if max_rounds and rounds_completed >= max_rounds: break   # 熔断
```

**链路 B：评测协议（ctf-logs/*/metrics.json + analyze_logs.py）**
每份 metrics.json 固定 schema：`benchmark_name / total_time_seconds / input_tokens(regular+cached) / reasoning_tokens / costs{...总成本及单价} / tool_calls{} / command_usage{} / flags{found, count, flags[]}`（metrics.json:1-37）；trace.log 逐条记录工具调用、命令串、增量 token/成本、检测结果与 `agents.result.RunResult` 属性（证明原版基于 openai-agents SDK）。analyze_logs.py 由此产出 LaTeX 表、时间/token CDF、成本分布、工具与命令使用、Sankey 与成功率相关性图（analyze_logs.py:135-1027）。

### 10. 亮点与弱点
亮点：(1) "子代理即工具"的两层收权模式——主代理只能下 instruction，执行细节全部封装在嵌套 loop 内，且两层工具集互斥（main.py:885 vs 537-539），是简洁的权限分层；(2) validator_agent 把"PoC 复现+证据采集+裁决"设为独立人格，与 Dark-Moon 的对抗性资格、BugTrace 的 skeptical validation 异曲同工但**有真实沙箱可执行**；(3) 每目标独立沙箱 + finally kill 的卫生学；(4) 分代理记账的评测协议与全量轨迹发布，是可复现研究的模板。
弱点：(1) 核心方法论随 SYSTEM_PROMPT 一起脱敏，发布版不可运行完整能力（须自备提示词）；(2) `execute_tool` 对 sandbox_agent 的参数名做了 legacy 兼容特判（main.py:796-800），工具分发略脏；(3) 无 scope 校验、无命令黑名单，安全性完全外包给沙箱；`user_prompt` 硬编码残留在 main.py:832-834 属发布卫生问题。

---

## 六、横向对比与可借鉴设计汇总

| 维度 | Dark-Moon | cochise | EVA | BugTrace-AI | mapta |
|---|---|---|---|---|---|
| 形态 | 全自动编排平台 | 全自动双代理 | 人在回路副驾 | 人工工具箱 | 全自动+验证（脱敏） |
| 编排 | Markdown 信号矩阵+反应式反馈环 | Planner/Executor 分层 | 阶段启发式+人工批准 | UI 管线串联 | 主代理+工具化子代理 |
| 执行安全 | 白名单+guard+隐私网关 | 无（靶场假设） | 人工确认+输出修补 | 无执行 | 沙箱隔离+截断 |
| 反幻觉 | 对抗性状态资格 | 计划树 FAILED/DONE 标注 | Evidence lock | persona 采样+怀疑论验证 | validator 代理复现 |
| 终止 | 5 条件+深度 3 层 | 时间/上下文预算 | 用户 | 管线自然结束 | 无工具调用/100 轮 |

五个项目共同指向四条可迁移经验：(1) **把"模型不该决定的事"下沉到执行层**（Dark-Moon execution_guard 的"prompt 规则只是建议，白名单前预检才是执法点"）；(2) **双层代理 + 收窄工具面**（cochise 的 Planner/Executor 快照知识、mapta 的子代理即工具）；(3) **确定性上下文工程优于额外 LLM 调用**（EVA 的 WORKFLOW_STATE、Dark-Moon 的工具/字典探测）；(4) **验证人格独立化**（三家不约而同：对抗性自我反驳 / skeptical validator / 复现裁决），配合"报告由持久化 store 生成而非模型记忆"（Dark-Moon）构成可信产出的闭环。
