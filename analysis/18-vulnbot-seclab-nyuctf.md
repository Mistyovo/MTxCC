# 参考项目深度分析 18：VulnBot / seclab-taskflow-agent / nyuctf_agents

> 分析对象：三个 AI 安全 agent 开源项目（只读代码架构分析，未执行任何代码）。
> 用途：为授权范围内使用的 AI 渗透测试系统提供架构参考。
> 代码引用格式：相对路径 `file:line`（相对各自项目根目录）。

---

# 一、VulnBot

## 1.1 一句话定位 + 项目背景

**一句话定位**：一个用 LLM 复刻"人类渗透测试团队"工作流的多 Agent 协作自主渗透测试框架（论文 *VulnBot: Autonomous Penetration Testing for a Multi-Agent Collaborative Framework*, arXiv:2501.13411，Kong et al. 2025）。

**背景**：由中科院信息工程研究所相关团队发布（KHenryAegis/VulnBot）。核心思路是把渗透测试拆成"信息收集 → 漏洞扫描 → 漏洞利用"三个阶段，每个阶段交给一个专职 Agent 角色，角色间通过"计划-执行"循环串联，底层通过 SSH 在远程 Kali Linux 上执行真实渗透命令，并可选接入 RAG 知识库（渗透测试案例库，基于 Milvus 向量检索 + 重排序）。其 RAG 子系统直接借鉴 Langchain-Chatchat 的实现。

## 1.2 技术栈与代码规模

| 维度 | 内容 |
|---|---|
| 语言 | Python 3.11.11 |
| 规模 | 93 个 .py 文件，约 7,100 行（另有 Streamlit WebUI、FastAPI server） |
| Agent/LLM | openai 1.53 + ollama 0.3（双 provider） |
| 持久化 | SQLAlchemy 1.4 + MySQL（Session/Plan/Task/Conversation/Message 五张表） |
| RAG | langchain 0.3 + pymilvus（Milvus HNSW）+ sentence-transformers CrossEncoder 重排 |
| 执行 | paramiko（SSH 到远程 Kali，invoke_shell 交互式 shell） |
| CLI/UI | click + prompt_toolkit + rich；streamlit WebUI；FastAPI 知识库 API |
| 建模 | pydantic v2（Role/Planner/Plan/Task 全部 BaseModel） |

## 1.3 整体架构：模块划分、数据流

```
pentest.py (CLI 入口)
   │  初始化/恢复 Session（MySQL）
   ▼
roles/ ──── Collector(侦察) ──┐  每个 Role 内部：
   │                         │   _plan() → Planner → WritePlan(LLM 生成 JSON 计划)
   │  角色链式传递            │   _react() → WriteCode(LLM 生成命令) → ExecuteTask
   ├──── Scanner(扫描) ◄──────┘            │ <execute> 标签解析
   │                                       ▼
   └──── Exploiter(利用)          actions/shell_manager (单例 SSH)
                                           │ paramiko invoke_shell
server/chat/chat.py (_chat 统一 LLM 网关)   ▼
   │  ├─ 可选 RAG: rag/kb 检索 + reranker   远程 Kali Linux
   │  └─ MySQL 会话历史(截断窗口)          (nmap/sqlmap/msf...)
db/ (Session/Plan/Task 持久化)
```

数据流：`用户任务描述 → Session → Role._plan（生成含依赖的 JSON 任务列表）→ 逐任务 _react（LLM 写命令 → SSH 执行 → 结果判定 → 计划修订）→ 阶段结束生成摘要 → 下一角色接管`。所有 LLM 交互都走 `_chat()`，带会话 ID 的历史存 MySQL。

模块划分：`roles/`（角色定义）、`actions/`（计划与执行动作）、`prompts/`（提示词）、`server/chat/`（LLM 网关）、`db/`（持久化）、`rag/`（向量检索知识库）、`web/`+`server/api/`（知识库管理 WebUI/API）、`experiment/`（对照实验用的 PentestGPT 基线）、`config/`（pydantic-settings YAML 配置）。

## 1.4 Agent/编排设计

**角色**（`roles/role.py:16-27`）：`Role` 是 pydantic BaseModel，字段含 `name/goal/tools/prompt/max_interactions/planner/previous_summary` 及两个会话 ID（`plan_chat_id` 计划会话、`react_chat_id` 推理会话——**规划与执行用独立对话上下文**，是典型的"双记忆"设计）。三个子类只是换了 goal、工具清单与提示词：

- **Collector**（`roles/collector.py:10-32`）：目标"全端口/服务扫描"，工具清单 Nmap/Curl/Dmitry/Enum4linux/Amass 等 15 个；
- **Scanner**（`roles/scanner.py`）：Nikto/Dirb/Wapiti/Sqlmap/WPScan 等；
- **Exploiter**（`roles/exploiter.py`）：Hydra/Metasploit/Impacket/Mimikatz 等。

**主循环**（`roles/role.py:84-90`）：

```python
def run(self, session):
    next_task = self._plan(session)
    while self.chat_counter < self.max_interactions:
        next_task = self._react(next_task)
        if next_task is None:
            break
    self.put_message(session)
```

终止条件：`chat_counter >= max_interactions`（CLI `-m` 传入，默认 5）或 `update_plan` 返回空（LLM 认为本阶段无事可做）。

**规划-执行**：`_plan`（`roles/role.py:58-82`）在阶段开始时把历史阶段的 LLM 摘要（`PlannerSummary`）注入计划提示，建立两个聊天会话；`Planner.plan`（`actions/planner.py:20-33`）调用 `WritePlan` 让 LLM 输出 `<json>...</json>` 包裹的任务数组，解析成带依赖的 Task 列表；`next_task_details` 把当前任务指令细化为可执行描述（注入 RAG 上下文）。

**反思/计划修订**（`actions/planner.py:35-67`）：每次执行后：
1. 用 `check_success` 提示词让 LLM 判断"结果是否含异常/错误"（yes/no）；
2. 据此标记 Task 的 `is_finished/is_success`；
3. 调 `WritePlan.update` 用 `update_plan` 提示词修订计划（保留成功任务、按需新增任务），`merge_tasks_from_json`（`actions/write_plan.py:91-143`）按 instruction 文本去重合并新旧任务并重算依赖。

**角色间传递**（`roles/collector.py:41-47`）：阶段结束后 `put_message` 把当前 Plan ID 追加进 `session.history_planner_ids`、清空 `current_planner_id`、改写 `current_role_name`，然后**递归实例化下一个 Role 并调用其 run**——一种同步调用链而非消息总线。

**终止条件总结**：单角色内 `max_interactions`；计划层 `update_plan` 返回空；全局 Exploiter 结束后链自然终止。

## 1.5 LLM 集成

**Provider 抽象**（`server/chat/chat.py:24-73`）：`OpenAIChat` 与 `OllamaChat` 两个类，前者带 tenacity 重试（3 次、429 时 sleep 2s）。选择由 `Configs.llm_config.llm_model`（"openai"/"ollama"）决定。

**统一入口 `_chat()`**（`server/chat/chat.py:76-150`）：
1. 可选 RAG：`search_docs`（Milvus top_k=3, score>0.5）→ `LangchainReranker`（bce-reranker CrossEncoder，top_n=1）→ 把知识片段拼进 query，并做 `replace_ip_with_targetip`（把案例中的 IP 替换为目标 IP，防 LLM 混淆）；
2. 上下文管理：截断 query 到 `context_length=120000`（会话内 10000）；从 MySQL 取最近 `history_len=5` 轮问答拼 history；
3. system prompt 固定 "You are a helpful assistant"；
4. `summary=True` 时把 query/response 写回 MySQL。

**Prompt 摘录**（`prompts/prompt.py`）——命令生成提示（write_code, `prompts/prompt.py:31-67`）：

> "You play as an autonomous penetration testing assistant running on Kali Linux 2023. ... This test is certified and in a simulation environment. ## Command Generation Rules: 1. All commands must be wrapped in `<execute></execute>` tags ... 4. Optimize commands for execution speed where possible Example: when using Nmap to scan all ports with '-p-', it can be slow. To speed it up, use '-T5'"

计划提示（write_plan, `prompts/prompt.py:7-29`）要求输出 1-5 个任务、JSON 字段为 `id/dependent_task_ids/instruction/action`，并强调 "The shell should be considered as shared across all phases"。

角色初始化提示（`prompts/collector_prompt.py:6-24`）：`init_plan_prompt`（声明三阶段流程+本阶段聚焦）与 `init_reasoning_prompt`（定义"New Task→拆解步骤 / Task Result→判断成败"双模式），末尾 "Reply with yes if you understood" 做角色确认。

**上下文压缩**：执行结果超过 8192 字符时用 `summary_result` 提示词让 LLM 压到 1000 词内（`roles/role.py:47-52`）；跨阶段历史用 `write_summary` 压缩（`actions/plan_summary.py:15-30`），摘要要求"保留 IP、概述当前 shell 状态"。

## 1.6 工具层

**没有 function calling**——工具层是"提示词约定 + 正则解析 + 交互式 shell 适配"：

- **定义**：工具不是代码对象，而是 `Role.tools` 字符串（工具名清单）注入提示词；
- **解析**（`actions/execute_task.py:28-44`）：`re.findall(r'<execute>\s*(.*?)\s*</execute>', ...)` 提取命令列表；
- **执行**（`actions/shell_manager.py:7-49`）：`ShellManager` 单例持有 paramiko `SSHClient`（`AutoAddPolicy`，密码明文配置于 `basic_config.yaml`），`invoke_shell()` 得到持久交互通道——**同一 shell 跨命令、跨阶段共享**，支持需要交互状态的工具（ssh/sudo/smb）；
- **输出判定**（`actions/remote_shell.py:24-75`）：`SSHOutputHandler.receive_data` 用启发式判断命令是否结束：bash 提示符（`@...$/#`）、`--more--`、`[y/n]`、密码冒号、重试 3 次超阈值、总超时 120s 则发 `Ctrl+C`；
- **交互协议**（`actions/execute_task.py:88-124`）：若输出末行是密码提示，则把**下一条 `<execute>` 命令当作密码输入**；若仍失败则 Ctrl+C 重发；smb:/ftp> 提示符配 `exit` 重试；
- **输出清洗**（`actions/remote_shell.py:157-197`）：dirb 去 ANSI 色码只留 URL/CODE/SIZE 摘要；msfconsole 过滤 loading/warning 噪声——针对具体工具的 tokenizer 优化；
- **执行模式**（`actions/execute_task.py:46-59`）：`Auto`（全自动）/`SemiAuto`（Shell 自动、Web 手动）/`Manual`（全部人工输入结果）。

## 1.7 渗透方法论建模

- **三阶段流水线**直接映射行业标准渗透流程（recon → scan → exploit），每阶段的 `goal` 与工具清单写死在代码里（`roles/collector.py:14-32` 等）；
- **任务 DAG**：计划任务的 `dependent_task_ids` 解析为 `dependencies`（`actions/write_plan.py:83-84`），`Plan.get_sorted_tasks`（`db/models/plan_model.py:34-60`）做 Kahn 拓扑排序并检测环，`current_task` 取第一个未完成任务；
- **action 类型**只有 `Shell` 和 `Web` 两种（`prompts/prompt.py:8`）；
- **共享 shell 语义**贯穿提示词与代码（阶段间不重建会话，`update_plan` 提示词显式说明"若前任务已进入不同 shell，无需重执行前置命令"，`prompts/prompt.py:86`）；
- **RAG 方法论外挂**：渗透案例库（含 writeup 的文档解析入 Milvus）在计划与任务细化两个点注入，检索 query 用任务 instruction 本身。

## 1.8 状态与记忆

- **MySQL 持久化**：`Session`（当前角色、计划 ID 链、初始描述）→ `Plan`（goal、双 chat_id、任务序列）→ `Task`（instruction/code(JSON 命令列表)/result/is_success/is_finished/dependencies）；`Conversation/Message` 存 LLM 历史。支持**会话恢复**（`pentest.py:22-47` 列出历史 session 供选择继续）；
- **三层记忆**：① 会话内 LLM history（窗口 5 轮）；② 阶段摘要（`PlannerSummary`，LLM 压缩所有历史阶段为 <1000 词 + shell 状态）；③ RAG 知识库（外部长期知识）；
- **计划状态机**：`update_task_status` 标记成败，`merge_tasks` 合并修订。

## 1.9 隔离与安全

- **执行隔离**：所有命令在远程 Kali（默认 10.10.0.5 root）执行，本机只做编排——攻击面与控制面分离；
- **人工介入**：`Mode.SemiAuto/Manual` 提供分级的 human-in-the-loop（`config/config.py:15-21`）；
- **命令黑名单**：`FORBIDDEN_COMMANDS = {'apt', 'apt-get'}`（`actions/remote_shell.py:81`），注释写的是"network tunneling tools are restricted"（名不符实，实为防破坏环境依赖）；
- **提示词层安全声明**："This test is certified and in a simulation environment"（`prompts/prompt.py:34,103`）——纯提示级越权缓解；
- **弱点**：SSH `AutoAddPolicy` + 明文密码；无目标范围（scope/授权目标 IP 段）校验，LLM 生成的命令直接进 shell，命令内容无白名单/审核；`_chat` 异常时返回 `**ERROR**` 字符串可能被当作正常结果进入下一步判断。

## 1.10 关键代码走读

**链路 1：计划生成与演化**（`actions/write_plan.py:17-23` → `actions/planner.py:35-67`）

```python
# write_plan.py:17-23 —— LLM 输出 JSON 计划的解析
def run(self, init_description) -> str:
    rsp = _chat(query=DeepPentestPrompt.write_plan, conversation_id=self.plan_chat_id,
                kb_name=Configs.kb_config.kb_name, kb_query=init_description)
    match = re.search(r'<json>(.*?)</json>', rsp, re.DOTALL)
    if match:
        return match.group(1)
```

```python
# planner.py:37-44 —— 用独立 LLM 调用判定任务成败（反思）
check_success = _chat(
    query=DeepPentestPrompt.check_success.format(result=result),
    conversation_id=self.current_plan.react_chat_id)
if "yes" in check_success.lower():
    task_result = self.update_task_status(..., True, True, result)
```

成败判定（`prompts/prompt.py:118-124`）："If the Task Execution Result contains any exceptions or errors, it will be considered unsuccessful. Please reply with 'yes'/'no'"。判定结果驱动 `update_plan` 重写任务列表，成功任务被强制保留（提示词约束 + `merge_tasks_from_json` 代码级兜底）。

**链路 2：命令生成 → SSH 执行 → 交互修复**（`actions/write_code.py:17-26` → `actions/execute_task.py:67-129`）

```python
# execute_task.py:101-114 —— 密码提示的自动配对处理
if any(prompt in last_line for prompt in PASSWORD_PROMPTS):
    if i + 1 < len(self.code):
        result += f'Action:{self.code[i + 1]}\nObservation: '
        next_output = shell.execute_cmd(self.code[i + 1])   # 下一条命令当密码
        result += next_output + '\n'
        skip_next = True
        if any(prompt in next_output.strip().split('\n')[-1] for prompt in PASSWORD_PROMPTS):
            shell.shell.send('\x03')                        # 仍要密码 → Ctrl+C
            ...
```

配合 `RemoteShell.execute_cmd`（`actions/remote_shell.py:117-134`）的 `[y/n]` 自动答 yes、dirb/msfconsole 输出清洗，构成对真实交互式渗透工具链的完整适配。这层"交互式 shell 语义适配"是 VulnBot 最有工程含金量的部分。

**链路 3：角色链交接**（`roles/collector.py:41-47`）

```python
def put_message(self, message):
    super().put_message(self)
    if message.current_role_name == RoleType.COLLECTOR.value:
        message.current_role_name = RoleType.SCANNER.value
        message.history_planner_ids.append(self.planner.current_plan.id)
        message.current_planner_id = ''            # 下一角色重新规划
        Scanner(console=self.console, max_interactions=self.max_interactions).run(message)
```

Session 是角色间唯一共享状态：角色名推进 + 计划 ID 归档。Scanner 完成后同样交接给 Exploiter（`roles/scanner.py:42-48`）。配合 `preload_session` 的会话恢复，中断后可从任意角色继续。

## 1.11 亮点与弱点

**亮点**
1. **双会话规划/执行分离** + 阶段摘要压缩，控制了长程渗透任务上下文膨胀；
2. **交互式 shell 深度适配**（密码配对、提示符检测、超时中断、按工具输出清洗），让 msfconsole/ssh/smbclient 这类交互工具可被 agent 驱动；
3. **计划为可演化的任务 DAG**（拓扑排序 + LLM 修订 + 成功任务保留），而非线性 TODO；
4. **RAG 注入点选择在"计划"与"任务细化"两处**而非每次命令生成，并做 IP 归一化防串目标；
5. MySQL 全量持久化 + 会话恢复，长任务可断点续跑。

**弱点**
1. 无 function calling，`<execute>` 正则解析脆弱（嵌套/格式偏差即失败）；
2. 角色链硬编码为单向三段，不能迭代回退（如利用失败回到扫描）；
3. 无目标授权范围/命令审计机制，Auto 模式下 LLM 输出直通 root shell；
4. 工程细节粗糙：异常吞掉返回错误字符串、`preprocess_json_string` 只修 `@!` 转义、`_chat` 里 asyncio.run 嵌套线程池调用等；
5. 成败判定依赖单次 yes/no LLM 调用，无多重校验。

---

# 二、seclab-taskflow-agent（GitHub Security Lab Taskflow Agent）

## 2.1 一句话定位 + 项目背景

**一句话定位**：GitHub Security Lab 官方维护的 **MCP 驱动、YAML 声明式多 Agent 工作流框架**，主要用于 AI 代码审计与漏洞分诊（agentic security research）。

**背景**：GitHub Security Lab（securitylab.github.com）实验性框架，设计哲学是"以提示词级捕捉漏洞模式为中心，随前沿模型能力进化可规模化复用"（README:230-234）。主用场景：C/C++ 代码审计（内置 CodeQL MCP server 提供代码导航工具）、code scanning 告警分诊。它不执行渗透，而是把"安全研究工作流"抽象成 GitHub-Actions 风格的 YAML taskflow，由框架解释执行。附带完善的工程化设施：lint 离线校验、JSON Schema 导出、会话 checkpoint/resume、运行 manifest 审计、多模型对比评测。

## 2.2 技术栈与代码规模

| 维度 | 内容 |
|---|---|
| 语言 | Python ≥3.10，hatch 构建，MIT 协议，SPDX 标头 |
| 规模 | 85 个 .py（src 核心 ~5,500 行，最大 runner.py 1,419 行）+ 54 个 YAML（personality/taskflow/toolbox/model_config/prompt 五类文档）+ 30 个测试文件 |
| Agent SDK | **三后端**：OpenAI Agents SDK（默认）/ GitHub Copilot Python SDK / Anthropic Python SDK，统一 `AgentBackend` Protocol |
| 语法/校验 | Pydantic v2（五类 YAML 文档的语法模型）+ jsonschema（outputs 校验）+ Jinja2（模板） |
| 工具协议 | MCP 全覆盖：stdio / SSE / streamable HTTP 三种 transport，内置 FastMCP 实现的 CodeQL / memcache / logbook / echo 四个 MCP server |
| 其它 | Typer CLI、platformdirs、watchdog 线程、Docker 发布链（release_tools/） |

## 2.3 整体架构：模块划分、数据流

```
CLI (cli.py, Typer: -p personality / -t taskflow / --resume / --lint / --schema)
  ▼
runner.py  run_main(): 加载 taskflow → 逐 task 循环
  │   ├─ Jinja2 渲染 user_prompt (globals/inputs/outputs 上下文)
  │   ├─ repeat_prompt/over → 展开 prompt 列表；models → 多模型扇出
  │   ├─ run_prompts() → _fan_out_deploys() (分支并发 + completion 策略)
  │   │      ▼ deploy_task_agents()
  │   │        ├─ build_mcp_servers() → mcp_lifecycle 连接/清理 MCP
  │   │        ├─ mcp_system_prompt() 拼装系统提示 (personality+server_prompt+guidelines)
  │   │        └─ backend (openai_agents/copilot_sdk/anthropic_sdk) 流式驱动
  │   ├─ 结果捕获: ResultStore (tool result 流 + 命名 outputs, schema 校验)
  │   └─ session.record_task() checkpoint + manifest
  ▼
session.py  TaskflowSession (JSON checkpoint, 断点 resume)
```

支撑模块：`models.py`（Pydantic 语法模型）、`available_tools.py`（YAML 资源加载缓存，用 `importlib.resources` 解析 dotted path）、`template_utils.py`（Jinja2 环境 + 表达式求值）、`mcp_utils.py`/`mcp_transport.py`/`mcp_lifecycle.py`（MCP 参数解析/命名空间/重连 transport/生命周期）、`results.py`（ToolResult 规范化与解码）、`capi.py`（GitHub CAPI endpoint/token 管理）、`_stream.py`（后端流驱动 + 限速退避重试）、`_watchdog.py`（事件循环卡死守护）、`linting.py`（离线校验）。

数据流：`taskflow YAML → Pydantic 校验 → 逐任务（模板渲染 → prompt×model 分支矩阵 → 每 branch 独立 agent 运行 + 工具结果入私有 sink → 投影/命名输出捕获 → checkpoint）`。

## 2.4 Agent/编排设计

**角色 = personality**（YAML）：系统提示级指令 + 任务 + 默认 toolboxes。示例 `src/seclab_taskflow_agent/personalities/c_auditer.yaml:8-21`：personality "Your name is Ronald. You are a C programming language security expert..."，task "Find and call out security vulnerabilities in any provided C code..."。内置 personalities：`assistant`（通用）、`c_auditer`（C 审计）。

**编排 = taskflow**（声明式，非自主规划）：任务序列由 YAML 写定，框架按序执行。任务字段（`models.py:108-242`）包括 `agents`（第一个为主、其余为 handoff 目标）、`user_prompt`、`run`（shell 任务，与 prompt 互斥）、`model/models`（多模型扇出）、`max_steps`（默认 50 轮，`runner.py:64`）、`must_complete`、`if`（GitHub-Actions 风格条件表达式）、`repeat_prompt`+`over`（迭代展开）、`toolboxes`/`blocked_tools`、`env`、`headless`、`async`、`id`+`outputs`（带 JSON Schema 的类型化输出）、`capture: response|tool_result`、`completion: all|any`、`model_concurrency`、`uses`（复用子 taskflow）。

**主循环**（`runner.py:1018-1419`）：`for task_index, task_wrapper in enumerate(taskflow)`，resume 时跳过已完成任务；每任务渲染提示 → 构建分支 → 执行 → 捕获输出 → `session.record_task` checkpoint。

**Agent 内循环**：委托给所选 SDK 的原生 agent loop（OpenAI Agents SDK 的 Runner），每任务 `max_turns` 上限；系统提示统一注入四条行为约束（`runner.py:750-755`）：

> "Do not prompt the user with questions. / Run tasks until a final result is available. / Ensure responses are based on the latest information from available tools. / Run tools sequentially, wait until one tool has completed before calling the next."

**反思**：无显式自我反思循环；反思性质体现在 ① 任务级 `if` 条件与 `outputs` schema 校验失败即硬失败；② 审计 taskflow 的 prompt 要求"Reflect on your analysis for accuracy before returning it"（`examples/taskflows/CVE-2023-2283.yaml:70`）。

**终止条件**：taskflow 序列结束；单任务 `max_steps`（BackendMaxTurnsError）；`must_complete` 失败即中止整个 run 并保存 session（`runner.py:1336-1348`）；任务重试 `TASK_RETRY_LIMIT=3` 次退避（10s×attempt，仅对 Timeout/Connection 类瞬态错误重试，`runner.py:1297-1325`）。

**Handoff**：agents 列表 >1 时构建 OpenAI Agents SDK handoff 图（`runner.py:757-817`），用于分诊模式（主 agent 决定移交给后续专家）。

## 2.5 LLM 集成

**三后端抽象**（`sdk/base.py:104-131`）：`AgentBackend` Protocol 四方法 `validate/build/run_streamed/aclose`；中立事件流 `StreamEvent = TextDelta | ToolEnd | TokenUsage`（`sdk/base.py:67`）。工厂与解析（`sdk/__init__.py:42-86`）懒加载单例。backend 选择优先级：任务级 > 模型级 > model_config 顶层 > `SECLAB_TASKFLOW_BACKEND` 环境变量 > `openai_agents`（README:106-115）。`validate()` 在构建前拒绝该后端不支持的字段（如 copilot_sdk 不支持 handoffs/temperature），抛 `BackendCapabilityError` 明确点名字段——**能力协商前置**。

**模型解析链**（`runner.py:134-206`）：逻辑名 → `model_config.models` 映射 → `model_settings` 合并（api_type/endpoint/token/backend 引擎键先弹出）→ 任务级覆盖。支持 `chat_completions` / `responses` / `messages` 三种 api_type。

**上下文管理**：
- 系统提示由 `mcp_system_prompt()`（`mcp_prompt.py:15-105`）分段拼装：personality + Available Tools/Resources 段 + Important Guidelines 段 + 各 MCP server 的 `server_prompt` 段 + Primary Task 段；
- `exclude_from_context: true` 时工具结果不回传 LLM（`agent.py:198-212` 用 `ToolsToFinalOutputFunction` 直接截断），用于纯副作用任务省 token；
- 流式输出经 `OutputRouter` 缓冲（多模型/异步任务带标签分块 flush，`runner.py:852-854`）。

**Token 计量**：`TokenUsage` 中立事件含 `cache_read/write_tokens`，并文档化各家口径差异（`sdk/base.py:39-58`：OpenAI 系 input 含 cache read，Anthropic 系三者不相交）；按任务快照差值归属（`runner.py:1295,1413`）。

**健壮性**：httpx 分相超时（connect 10s / read 300s）防 CLOSE_WAIT 死锁（`agent.py:182-191`）；`_stream.py` 处理限速指数退避（5s 起、120s 封顶、MAX_API_RETRY=5）；`_watchdog.py` 守护线程在事件循环停滞时强杀进程。

## 2.6 工具层：定义/注册/执行/解析

**定义（toolbox YAML）**：每个工具箱 = 一个 MCP server 配置。`ToolboxDocument`（`models.py:314-323`）：`server_params`（kind: stdio/sse/streamable + command/args/env/url/headers/optional_headers/timeout/reconnecting）、`server_prompt`（注入系统提示的使用说明）、`confirm`（需人工确认的工具名单）、`client_session_timeout`。env 支持 `{{ env('VAR') }}` 模板从父进程取值（`mcp_utils.py:228-249` `swap_env`），可选头缺失时静默丢弃（`mcp_utils.py:318-325`）。

**注册/加载**：`AvailableTools`（`available_tools.py:58-167`）按 dotted path（如 `seclab_taskflow_agent.toolboxes.codeql`）用 `importlib.resources.files()` 定位 YAML，校验 header filetype 后解析为 Pydantic 模型并缓存——**YAML 工作流可打包成 PyPI 包分发**。

**连接与生命周期**（`mcp_lifecycle.py:44-123`）：按 kind 建 `MCPServerStdio/Sse/StreamableHttp`（含自研 `ReconnectingMCPServerStdio` 和 streamable 子进程线程 `StreamableMCPThread`），`mcp_session_task` 后台任务统一 connect（`connected` 事件）→ 等待 `cleanup` 事件 → 逆序清理（超时 5s，兜底 cancel）。

**执行与命名空间**（`mcp_utils.py:73-180`）：`MCPNamespaceWrap` 包装每个 server：
- `list_tools` 给工具名加 `ns<sha256 前 12 位>` 前缀，解决 OpenAI 64 字符工具名限制与多 server 重名（`compress_name`，`mcp_utils.py:43-70`；前缀 `ns` 保证首字符为字母，规避 Gemini 400 拒绝——注释详细记录了这个跨模型坑）；
- `call_tool`（`mcp_utils.py:165-180`）剥前缀后调用，若工具名在 `confirm` 列表则 `confirm_tool()` 交互式 yes/no，拒绝则返回 "Tool call not allowed." 结果；
- `headless: true` 时 confirms 清空（自动放行，README 明确警告）。

**解析**：工具结果统一规范化为 `ToolResult`（`results.py`），`decode_tool_result` 尝试 JSON 解码、失败回落原文；带 `outputs` schema 时用 jsonschema 校验（`runner.py:456-466`）。

**内置 CodeQL MCP server**（`mcp_servers/codeql/mcp_server.py`）：FastMCP 实现，9 个工具——`get_file_contents`（file URI + 行列区域）、`list_source_files`、`search_in_source_code`、`definition_location_for_function`、`declaration_location_for_variable`、`statement_location`、`call_graph_to/from/from_to`、`list_functions`。核心设计：**不让 LLM 写 CodeQL 查询，而是用模板化 .ql 查询包（`TEMPLATED_QUERY_PATHS`，mcp_server.py:30-50）提供确定性代码导航原语**，结果 CSV→JSON。

## 2.7 渗透方法论建模

本项目的"方法论"是**代码审计方法论**而非攻击执行：
- **漏洞模式即提示词**：CVE-2023-2283 审计 taskflow（`examples/taskflows/CVE-2023-2283.yaml:24-85`）把漏洞模式写成结构化 prompt——"We are looking for a specific kind of authentication bypass. Specifically we want to know if ... `pki_verify_data_signature` can return `SSH_OK` via a code path that SHOULD actually return `SSH_ERROR`"，随后给出可操作的判定步骤（枚举对 `rc` 赋值的函数、检查 `goto out` 未重置 `rc` 的路径），并附通用审计守则（不臆测、结论必须由代码示例支撑、不确定要声明、笔记写入 memcache `notes` 键）；
- **审计工作流三段式**：清缓存 → 模式驱动的自主审计（CodeQL 导航 + memcache 记笔记）→ 只读汇报笔记；
- **分诊模式**：主 agent + 多专家 handoff（`examples/taskflows/example_triage_taskflow.yaml`）；
- **多模型评审模式**：同一问题扇出多模型、`capture: response` 收集各模型散文答案、judge 任务读 `outputs.answers` 选优（README:240-260）——可用于漏洞判定交叉验证。

## 2.8 状态与记忆

- **TaskflowSession**（`session.py:75-254`）：任务级 checkpoint（JSON，platformdirs 数据目录），记录 completed_tasks（index/name/success/skipped/models/duration/token usage）+ `result_snapshot`（ResultStore 快照：有序工具结果 + 命名 outputs）+ CLI model config（resume 时确定性复用）；`--resume` 跳过已完成任务、恢复 store 快照；
- **ResultStore**：运行期工具结果流 + `outputs.<id>` 命名输出（跨任务数据通道），fan-out 任务产出 `[{model, item, result}]` 扇入记录（`runner.py:302-327`）；
- **memcache MCP server**：跨任务 KV 记忆（字典文件/SQLite 两种后端，`MEMCACHE_STATE_DIR` 隔离每 run 状态目录），agent 审计笔记的载体；
- **manifest.json**：run 结束/失败写入 artifacts 目录的机器可读审计摘要（无 endpoint/token），可按 session ID 重打。

## 2.9 隔离与安全

- **MCP 环境变量 denylist**：`TASKFLOW_ENV_DENYLIST` 阻止指定变量泄漏给 MCP 子进程，而 toolbox 显式 `env:` 注入不受影响（README:215-227）；
- **敏感值不落日志**：环境变量只记名字不记值（`mcp_utils.py:186-193` `_env_names`）；manifest 不含 endpoint/token；
- **工具级人工确认**：`confirm` 名单 + 交互确认，`headless` 旁路有显著文档警告（README:607-610）；
- **Docker 明示非安全边界**："the Agent Docker image is _NOT_ intended as a security boundary but strictly a deployment convenience"（README:335）；
- **离线 lint**：`--lint` 无模型调用解析全部引用并报告未知字段（疑似拼写错误），`--strict` 升级为错误，可做 CI 门禁；
- **弱点**：stdio MCP 子进程默认继承父环境（靠 denylist 反向剔除）；`confirm` 的 input() 在无人值守环境会阻塞；Jinja2 模板求值 `over`/`if` 表达式的输入若来自不可信 YAML 有注入面。

## 2.10 关键代码走读

**链路 1：任务执行矩阵与结果捕获**（`runner.py:1148-1285`）

```python
# runner.py:1188-1204 —— prompts × models 分支矩阵与并发策略
cross = multi_model and len(resolved_prompts) > 1
branches: list[_Branch] = []
for item_index, (ra, pp) in enumerate(resolved_prompts):
    for rm in resolved_models:
        label = rm.label
        if cross:
            label = f"{rm.label} [item {item_index}]"
        branches.append(_Branch(agents=ra, prompt=pp, rm=rm, item_index=item_index, label=label))

concurrent = async_task or multi_model
concurrency = model_concurrency if multi_model else max_concurrent_tasks
```

每个 branch 的工具结果进**私有 sink**（`_branch.tool_end` 闭包，`runner.py:1213-1217`），并发分支互不串扰；执行后单模型任务按序投影回共享 store（`runner.py:1358-1361`），多模型任务只按名消费（无"最后一个结果"歧义）。命名输出捕获区分扇出/非扇出（`runner.py:1370-1402`），空产出且声明 schema 时硬失败而非泄漏前一任务结果（`runner.py:449-455` 注释明确此设计）。这是该项目最精巧的并发正确性设计。

**链路 2：MCP 服务器生命周期**（`runner.py:740-746` → `mcp_lifecycle.py:126-167`）

```python
# runner.py:741-746 —— 连接事件同步
servers_connected = asyncio.Event()
start_cleanup = asyncio.Event()
mcp_sessions = asyncio.create_task(mcp_session_task(entries, servers_connected, start_cleanup))
await servers_connected.wait()
```

`mcp_session_task` 串行 connect 所有 server（streamable 且带 command 时先起子进程并轮询等待 HTTP 就绪）→ set connected → 等 cleanup → **逆序**清理（LIFO，与依赖建立顺序相反）并在 finally 清空 entries。`deploy_task_agents` 的 finally 块（`runner.py:857-893`）负责关 agent 句柄、触发清理、超时兜底 cancel，防止悬挂 task 卡住事件循环。

**链路 3：CVE-2023-2283 审计任务的提示工程**（`examples/taskflows/CVE-2023-2283.yaml:32-82`）

漏洞模式 prompt 的三段结构：`IMPORTANT: Vulnerability Pattern Details`（判定规则：rc 初值 SSH_ERROR / 成功路径置 SSH_OK / `goto out` 前未重置 rc 即潜在漏洞）→ `Vulnerability Pattern Analysis`（要求枚举所有可达 `goto out` 且 rc 未重置的错误条件，附代码示例）→ `General Guidance that ALWAYS applies`（禁用 list_functions 大结果工具、自主推进不请示、先反思再交付、不臆测、笔记写 memcache）。配合 c_auditer personality 与 CodeQL 工具箱，构成可复现的"模式→证据→结论"审计闭环。

## 2.11 亮点与弱点

**亮点**
1. **声明式 YAML 工作流 + Pydantic 语法模型 + lint/schema**：安全工作流可版本化、可评审、可 CI 门禁，"prompt 即配置"；
2. **中立后端协议**（三 SDK 一个 Protocol + 能力协商前置 + 统一流事件/Token 计量），跨 provider 迁移成本极低；
3. **MCP 全栈工程化**：命名空间哈希防重名/超长、confirm 人工门禁、环境变量模板与 denylist、重连 transport、LIFO 生命周期；
4. **分支矩阵并发模型**（prompt×model 私有 sink + 投影 + 扇入），多模型评测/交叉验证开箱即用；
5. **任务级 checkpoint + ResultStore 快照 + manifest 审计**，长任务可恢复、可审计；
6. CodeQL 工具"模板化查询而非自由生成"的确定性工具设计。

**弱点**
1. 无自主规划能力，工作流完全预定义（对探索性强的渗透任务表达力不足）；
2. 单文件 runner.py 过大（1,419 行），闭包嵌套深（run_prompts 内 _deploy 内再嵌钩子）；
3. anthropic/copilot 后端功能裁剪（不支持 handoff 等），混用后端时行为不一致；
4. 交互式 confirm 与 headless/CI 场景存在张力；Docker 不提供隔离。

---

# 三、nyuctf_agents（NYU CTF Automation Framework）

## 3.1 一句话定位 + 项目背景

**一句话定位**：NYU CTF Bench 官方 agent 实现，包含 **D-CIPHER 多智能体系统**（auto-prompter + planner + executor）与 **NYU CTF baseline 单智能体**，在 Docker 环境内解 CTF 题目（web/pwn/crypto/rev/forensics/misc 六类）的学术 benchmark 框架。

**背景**：配套 NYU CTF Bench（nyu-llm-ctf.github.io）论文代码（NYU 联合 FAU 等）。学术属性强：核心关注可复现实验——按题目类别给不同 toolset/prompt 配置、精确成本预算控制（max_cost 美元计）、完整 JSON 逐轮日志、exit_reason 归因、结果汇总/绘图脚本（scripts/log_summary.py、plot_results.py）。baseline 分支冻结于 v20250206 供论文对齐。D-CIPHER 是其提出的 planner-executor 架构 + 可选 auto-prompter（自动生成初始提示）。

## 3.2 技术栈与代码规模

| 维度 | 内容 |
|---|---|
| 语言 | Python ≥3.10 |
| 规模 | 54 个 .py（~6,000 行）+ 38 个 YAML（configs: baseline/dcipher/single_executor 三组）+ Java（Ghidra headless 反编译脚本 DecompileToJson/DisassembleToJson）+ shell 脚本 + 2 个 Dockerfile |
| LLM provider | 多后端：openai / anthropic / gemini / together / openrouter / vllm（multiagent 5 个 + baseline 3 个），每个后端内置 MODELS 价格表 |
| 工具调用 | 原生 function calling（OpenAI 风格 schema） |
| 环境 | Docker（agent 容器 + 挑战容器，独立 network ctfnet），依赖 nyuctf 数据集库 |
| 日志 | rich 终端 + JSON 文件（含成本、轮次、exit_reason、完整对话） |

## 3.3 整体架构：模块划分、数据流

```
run_dcipher.py / run_single_executor.py / run_baseline.py (CLI 入口)
   │  CTFDataset → CTFChallenge → Config(YAML) → APIKeys
   ▼
nyuctf_multiagent/
   ├─ agent.py      BaseAgent / SingleAgent / AutoPromptAgent /
   │                PlannerAgent / ExecutorAgent / PlannerExecutorSystem
   ├─ backends/     backend.py(抽象) + 5 provider
   ├─ prompting.py  PromptManager (YAML prompt 模板, str.format)
   ├─ tools/        tool.py(基类) + run_command/misc/editing/reversing
   ├─ environment.py CTFEnvironment (docker 容器生命周期 + 工具注册)
   ├─ conversation.py Conversation (消息 + 观察窗口截断)
   └─ config.py    Config (planner/executor/autoprompter 三段 AgentConfig)
nyuctf_baseline/    单 agent 版 (formatters: xml/yaml/vbpy 多种提示格式实验)
```

数据流（D-CIPHER）：`challenge 启动容器 → (可选) autoprompter 先探索并生成初始 prompt → planner 主循环（LLM → tool_call → 执行/观察；遇 delegate 则派生新 executor 会话执行子任务 → finish_summary 回填 planner 观察）→ submit_flag 成功或 giveup/超限退出 → JSON 日志`。

## 3.4 Agent/编排设计

**四类角色**（`nyuctf_multiagent/agent.py`）：

- **BaseAgent**（13-72 行）：聚合 environment/challenge/prompter/backend + Conversation；`check_flag_in_response` 在**每条**消息（用户/助手/观察/工具参数）里检测 flag 子串，命中即置 `environment.solved`（兜底检测，不依赖模型主动提交）；
- **AutoPromptAgent**（216-288 行）：先用 `run_command/generate_prompt` 两个工具自主探索题目，最后调 `GenAutoPromptTool` 产出优化后的 planner 初始提示；超时未生成则 `run_for_autoprompt` 追问一次，仍失败回落硬编码提示（`agent.py:202-203` 明确 WARNING）；
- **PlannerAgent**（290-327 行）：工具集 `run_command/submit_flag/giveup/delegate`；`run_one_round` 中 `delegate` 调用不执行而是暂存 `delegated_task`（`agent.py:321-323`），由系统层接管；
- **ExecutorAgent**（329-407 行）：工具集 `run_command/finish_task/disassemble/decompile/create_file`；`new()`（339-343 行）克隆配置但**全新会话**——每个子任务干净上下文；`finish_task` 工具携带 summary 结束（371-374 行）；异常/超限未总结时 `run_for_finish_summary` 追问（380-407 行）。

**PlannerExecutorSystem**（409-563 行）：组合根 + 生命周期（context manager 进出启停挑战容器与环境）。主循环（`agent.py:502-530`）：

```python
while not self.environment.solved and not self.environment.giveup and \
        self.planner.conversation.round <= self.planner.max_rounds and \
        self.total_cost() <= self.max_cost:
    self.planner.conversation.next_round()
    self.planner.run_one_round()
    if self.planner.delegated_task is not None:
        result = self.run_executor(self.planner.delegated_task)
        tool_result = ToolResult(name=DelegateTool.NAME, id=..., result=result)
        self.planner.add_observation_message(tool_result)
```

`run_executor`（532-562 行）：新 executor → 注入任务描述 → 内层 while（solved/finished/rounds/cost 四条件）→ 未总结则追问 → 返回 finish_summary / finish_error / finish_empty 三态之一给 planner。

**终止条件**（四维，所有循环共用）：`solved`（flag 提交正确或响应中检出）/ `giveup`（agent 主动放弃工具）/ `max_rounds`（planner 30、executor 100、autoprompter 5）/ `max_cost`（美元预算，默认 1.0，跨所有 agent 累计）。`get_exit_reason`（`agent.py:102-112,439-449`）输出六种归因（solved/giveup/cost/max_rounds/planner_rounds/unknown）供 benchmark 统计。

**反思**：无显式反思；层次化分工（planner 策略 / executor 执行 + 摘要压缩）承担同等职能；auto-prompter 是"元反思"（反思提示词本身）。

## 3.5 LLM 集成

**Backend 抽象**（`backends/backend.py:26-99`）：基类持有 `role/model/tools/config` + 每模型价格（`in_price/out_price`）；统一 `send(messages) -> BackendResponse(content, error, tool_call, cost)`；**参数校验放在 backend 层** `parse_tool_arguments`（58-99 行）：JSON 解码 → 工具存在性 → 必填参数缺失检查 → 删除多余参数 → "number" 类型 float 转换，任何失败返回 `(False, error_ToolResult)` 直接作为观察回给模型（自纠错回路）。

**MODELS 表**（如 `backends/openai_backend.py:14-156`）：每模型登记 `max_context / cost_per_input_token / cost_per_output_token / reasoning / min_reasoning`，覆盖 GPT-4o 至 GPT-5.6 全系及 o3 等；成本实时累计（`calculate_cost`，198-199 行）。

**Reasoning 模型适配**（`openai_backend.py:178-196`）：reasoning 模型用 `max_completion_tokens` 而非 `max_tokens`+`temperature`，且 `DISABLE_REASONING=True` 时压到 `reasoning_effort=min_reasoning`（多为 "none"）以省钱提速——**学术评测的成本控制细节**。`parallel_tool_calls=False` 强制串行工具调用。

**消息格式转换**（`openai_backend.py:201-221`）：内部 `MessageRole.OBSERVATION` → OpenAI `role:"tool"` + `tool_call_id`；ASSISTANT 带 tool_calls 数组——中立内部表示 + 后端侧适配。

**Prompt 管理**：multiagent 用 YAML + `str.format`（`prompting.py:20-25`，注入 challenge/environment 对象；按 server_type web/nc 换服务描述）；baseline 用 Jinja2 模板目录 + FallbackLoader（prompt set 回退默认集，`nyuctf_baseline/prompts/prompts.py:26-41`）支持提示变体实验。

**上下文管理**（`conversation.py:49-70`，本项目最精炼的机制）：

```python
@property
def messages(self):
    trunc_before = -1
    if self.len_observations is not None:
        trunc_before = self.round - self.len_observations
    for m in self.all_messages:
        if m.role == MessageRole.OBSERVATION and m.index <= trunc_before:
            continue                      # 旧观察：直接丢弃
        elif m.role == MessageRole.ASSISTANT and m.index <= trunc_before:
            if m.content is not None:
                yield replace(m, tool_data=None)   # 旧助手消息：去工具调用只留思考
            else:
                continue
        else:
            yield m
```

即**滑动窗口**：只保留最近 `len_observations`（executor 默认 5）轮的观察，更早轮次的 assistant 消息剥掉 tool_call 只留 thought——上下文随轮次自动"瘦身"且保留推理轨迹。另有单条观察 25,000 字符硬截断（`conversation.py:90-98`）。

## 3.6 工具层：定义/注册/执行/解析

**定义**（`tools/tool.py:5-24`）：类属性 `NAME / DESCRIPTION / PARAMETERS: dict[name, (type, desc)] / REQUIRED_PARAMETERS`，子类实现 `call(**kwargs)` 与可选 `setup/teardown`。

**注册**：`CTFEnvironment.__init__`（`environment.py:15-18`）实例化 `ALLTOOLS` 全部 9 个工具入字典；`get_toolset(toolset)`（25-27 行）按配置返回**每角色子集**——planner 与 executor 看到不同工具面。

**schema 生成**（`openai_backend.py:163-176`）：`get_tool_schema` 把 PARAMETERS 元组转成 OpenAI function calling JSON schema（properties/required），无手写 schema 重复。

**执行**（`environment.py:71-75`）：`run_tool` 直接 `tool.call(**parsed_arguments)` 返回 `ToolResult`。

**九个工具**：
- `run_command`（`tools/run_command.py:29-54`）：`docker exec <container> bash -c <cmd>` + 超时 kill，返回 `{stdout, stderr, returncode, timed_out}` 四元组（结构化错误通道）；
- `submit_flag`（`tools/misc.py:5-33`）：与 `challenge.flag` 比对，正确置 solved；
- `giveup`（35-55 行）；
- `delegate`（57-76 行）：planner 专用，参数 `task` 描述；
- `finish_task`（99-118 行）：executor 专用，参数 `summary`；
- `generate_prompt`（78-97 行）：autoprompter 专用；
- `create_file`（`tools/editing.py`）；
- `disassemble / decompile`（`tools/reversing.py:58-124`）：容器内 Ghidra headless 脚本（`/opt/ghidra/customScripts/decompile.sh`）输出 JSON，**按 binary 粒度缓存**（`rev_cache`），`find_function` 兼容 `_start/invoke_main/entry/fcn.<addr>` 等函数名变体。

**baseline 侧工具**（`nyuctf_baseline/tools/tools.py`）：`run_command/check_flag/createfile/give_up/decompile_function/disassemble_function`，用 `Annotated[type, "描述"]` 类型注解自描述；Ghidra 在宿主机跑且结果按 challenge 目录落盘缓存（`decomp/<category>/<chal>/<bin>.decomp.json`，183-189 行）。

## 3.7 渗透方法论建模

- **按题目类别的配置化方法论**：`configs/dcipher/{web,pwn,crypto,rev,forensics,misc}_planner_executor.yaml` 每类一套（不同 prompt YAML + toolset），如 pwn 版 executor 含 disassemble/decompile/create_file 而 web 版不含（对比 `configs/dcipher/pwn_planner_executor.yaml:25-30` 与 web 版）；executor max_rounds=100 远高于 planner 30——高层少步决策、低层多步实操；
- **Planner-Executor 分层**：策略（做什么）与执行（怎么做）解耦，中间以自然语言任务描述 + 摘要传递——与人渗透团队"队长/执行者"结构同构；
- **Auto-prompter 元层**：承认"初始提示质量决定成败"，用一轮自主探索生成题目特定提示（类似自动 prompt 优化）；
- **flag 语义内置**：submit_flag 工具 + 全消息 flag 检测双保险，把"成功"从模糊判断变成可机判事件；
- baseline 的 formatters（xml/yaml/vbpy 三种提示编码格式）是"工具调用表达方式"的对照实验设施。

## 3.8 状态与记忆

- **Conversation 每 agent 独立**；executor 每任务全新会话（防跨任务污染）；
- **finish_summary 压缩交接**：executor 全程对话不进 planner 上下文，只有 `finish_task(summary)` 的一段自然语言回填——**分层记忆压缩**；
- **观察窗口 + 内容截断**（3.5 节）双级上下文控制；
- **JSON 日志**（`agent.py:114-139,451-479`）：start/end/time、三 agent 模型名、总成本、success、exit_reason、error、autoprompter/planner/executors 全量对话 dump、debug_log——离线分析（scripts/print_transcript.py、log_summary.py、database_summary.py、plot_results.py）齐备；
- 无跨 run 记忆/知识库（benchmark 场景刻意隔离）。

## 3.9 隔离与安全

- **Docker 双容器**：agent 容器（`docker/multiagent/Dockerfile`：ubuntu:22.04 + CTF 全家桶——gdb/sagemath/sqlmap/nikto/apktool/radare2/Ghidra/jadx/pwntools/angr；**非 root ctfplayer 用户**（56-62 行）但配 NOPASSWD sudo）与挑战容器通过独立 docker network `ctfnet` 互通（`environment.py:44-50` `--network` 指定），挑战文件 `docker cp` 进容器；
- 生命周期用 context manager 保证容器启停（`agent.py:86-100`）；
- **弱点**：sudo NOPASSWD 使非 root 形同虚设；无网络出口限制（容器可访问外网，存在数据外泄/提权面）；flag 明文在配置与日志中（benchmark 可接受，产品化不可）；API key 明文 keys.cfg。

## 3.10 关键代码走读

**链路 1：delegate 派发与摘要回填**（`agent.py:321-327` → `agent.py:532-562`）

```python
# agent.py:532-540 —— 每个子任务全新 executor 会话
def run_executor(self, task):
    executor = self.executor.new() # Create new executor with empty conversation
    self.all_executors.append(executor)
    task_description = task.parsed_arguments["task"]
    executor.add_system_message(executor.prompter.get("system"))
    executor.add_user_message(executor.prompter.get("initial", task_description=task_description))
```

```python
# agent.py:553-562 —— 三态结果回传 planner
if executor.finished and executor.finish_summary is not None:
    return executor.finish_summary
elif executor.error is not None:
    return self.executor.prompter.get("finish_error", error=executor.error)
else:
    return self.executor.prompter.get("finish_empty")
```

回传值包成 `DelegateTool` 的 ToolResult 作为 planner 的观察（`agent.py:528-529`）——planner 视角里子任务就是一个"慢工具"。

**链路 2：run_one_round 通用执行步**（`agent.py:345-378`，四个角色类共享同构实现）

```python
response = self.backend.send(self.conversation.messages)
if response.error is not None: ...
self.current_cost += response.cost
self.add_assistant_message(response.content, response.tool_call)
if not response.tool_call:
    self.add_user_message(self.prompter.get("continue"))   # 无动作 → 催促继续
    return
parsed, parsed_call = self.backend.parse_tool_arguments(response.tool_call)
if not parsed:
    self.add_observation_message(parsed_call)              # 解析错误 → 作为观察自纠错
    return
if parsed_call.name == FinishTaskTool.NAME:                # 角色特有控制流工具拦截
    self.finish_summary = parsed_call.parsed_arguments.get("summary", None)
    self.finished = True
else:
    tool_result = self.environment.run_tool(parsed_call)
    self.add_observation_message(tool_result)
```

要点：错误即观察（模型能看到自己的参数错误并修正）；控制流（delegate/finish_task/generate_prompt）实现为**工具**而非特殊消息类型，架构极简。

**链路 3：成本与终止的全局统筹**（`agent.py:481-486`）

```python
def total_cost(self):
    cost = self.planner.current_cost + sum(e.current_cost for e in self.all_executors)
    if self.autoprompter != None:
        cost += self.autoprompter.current_cost
    logger.progress_message(f"${cost:.3f} / ${self.max_cost:.3f}")
    return cost
```

所有循环条件都同时检查 `total_cost() <= self.max_cost`，预算是跨 agent 全局的；进度条实时显示消耗。配合 dump_log 的 exit_reason 归因，构成完整的"经济约束"实验框架。

## 3.11 亮点与弱点

**亮点**
1. **控制流工具化**（delegate/finish_task/generate_prompt 都是普通工具），多 agent 编排零特殊语法，极易增删角色；
2. **观察滑动窗口截断**（旧观察丢弃、旧 assistant 去 tool_call 留 thought）——简单有效的长任务上下文治理；
3. **成本作为一等公民**（全局美元预算 + 每模型价格表 + exit_reason 归因），对生产系统的"预算熔断"直接可借鉴；
4. **分层摘要交接**（executor 新会话 + finish_summary 压缩回填），子任务上下文污染免疫；
5. 双保险成功判定（submit_flag + 全消息 flag 检测）；
6. 按任务类别的 toolset/prompt 配置化。

**弱点**
1. 无并行（planner 串行等待 executor）；无跨 run 学习；
2. prompt YAML 不在仓库内（`prompts/*.yaml` 引用但未随库发布，复现需自备）；
3. 安全隔离弱（NOPASSWD sudo、无网络出口控制、key/flag 明文）；
4. 工具少而粗（无浏览器、无交互式 shell 工具，nc 服务只能靠 echo 管道拼）；
5. baseline 与 multiagent 两套代码重复度高（backend/tools 各自一份）。

---

# 四、横向对比与借鉴要点

| 维度 | VulnBot | seclab-taskflow-agent | nyuctf_agents |
|---|---|---|---|
| 目标 | 自主渗透执行（黑盒网络渗透） | 代码审计/漏洞分诊（白盒静态分析） | CTF 解题 benchmark（混合） |
| 编排 | 三角色链 + 每角色 plan-react 循环 | YAML 声明式任务流 + handoff | planner-executor 层次 + 工具化控制流 |
| 工具 | 提示词清单 + `<execute>` 标签 + SSH shell | MCP 全协议（toolbox 即 server） | 原生 function calling（9 个类工具） |
| 记忆 | MySQL + 阶段摘要 + RAG | session checkpoint + 命名 outputs + memcache | 观察窗口 + 分层摘要 |
| 终止 | max_interactions / 计划为空 | 任务序列完 / must_complete / max_steps | solved/giveup/cost/rounds 四维 |
| 隔离 | 远程 Kali SSH | 无（声明式，Docker 非边界） | Docker 双容器 + 独立网络 |
| 成熟度 | 研究原型 | 工程化产品级 | 学术 benchmark 级 |

**对自研 AI 渗透系统最值得吸收的组合**：VulnBot 的三阶段角色分工与交互式 shell 适配层；seclab 的声明式可审计工作流（YAML+lint+manifest）、MCP 工具治理（confirm 门禁/env denylist/命名空间）与多模型交叉验证扇出；nyuctf 的控制流工具化、观察滑动窗口、全局成本熔断与按任务类别配置 toolset。
