# 13. ArtiPhishell 与 Atlantis-Multilang 深度技术分析

> 分析对象（均为只读代码走读，未执行任何代码）：
> - `refer/04-competition/artiphishell/` — Shellphish 的 AIxCC Finals CRS
> - `refer/04-competition/atlantis-multilang/` — Team Atlanta 的多语言 CRS（CRS-multilang）
>
> **重要定位纠正**：经代码核实，本目录下的 "artiphishell" **不是** INRIA 的 ArtiPhishell 钓鱼演练平台。README（`README.md:3-5` "Official Deployment ... deploy ARTIPHISHELL for the AFC"）与全部组件代码表明，这是 **Shellphish 战队（shellphish-support-syndicate）参加 DARPA AIxCC（AI Cyber Challenge） Finals 的 CRS（Cyber Reasoning System）**，名字是 "Artificial + Shellphish" 的谐音梗。同样，atlantis-multilang 也是 AIxCC 参赛系统（Team Atlanta，Gatech SSLab 等，见 `README.md:113-129` 作者表）。两者都是"LLM 驱动的自动漏洞发现/验证/修补"系统——恰好是与"AI 渗透测试系统"最同构的两类参考架构，以下按原定结构分析。

---

# 第一部分：ArtiPhishell（Shellphish AIxCC CRS）

## 1.1 一句话定位与项目背景

一句话：**一个部署在 Azure Kubernetes 上的、以数据流水线为骨架、以十几个可独立替换的"角色组件"（LLM Agent 与确定性工具）为肌肉的自动化漏洞挖掘-验证-修补-提交系统**。

背景：DARPA AIxCC 要求参赛 CRS 在限定时间内对 OSS-Fuzz 开源项目自动发现漏洞（PoV）并生成修补补丁，按 mitigated PoV 计分。Shellphish 是老牌战队（CGC 时代的 Mechaphish 后裔），本仓库是其 Finals 开源版，含完整 Azure Terraform/Helm 部署（`README.md:66-83`，`infra/Makefile`）。比赛通过 CRS API（Tailscale 私网）下发任务（`README.md:7-23`）。

## 1.2 技术栈与代码规模

- 语言分布（全仓库）：Python ~1188 文件、C/C++ ~796、Shell 513、Java 506、Yaml 464、Rust 85。总源文件约 2300+。
- **核心（自研）**：
  - `libs/agentlib/`：自研 Agent 框架（LangChain 之上），约 30 个 py 文件 / 11k+ 行，含 planning/critic/curriculum agents、LLM provider 层、预算记账。
  - `libs/aijon-lib/`：LLM 驱动的 IJON 插桩，13 文件 / 1568 行。
  - `components/aijon/`：插桩入口（`main.py` 473 行）。
  - `components/patcherq/`：主 LLM 补丁 Agent（约 7135 行 py）。
  - `components/patchery/`：一次性 LLM 补丁器（约 5775 行 py）。
  - `components/patcherg/`：补丁编排"emperor"（1261 行，非 LLM）。
  - `components/poiguy`(294 行)、`pov-patrol`(393 行)、`povguy`(654 行)、`griller`(181 行)、`code-swipe`(31 py)、`components/`下另有 50 余个构建/分析组件（dyva、quickseed、scanguy、harness-den 等）。
  - `libs/pydatatask/`：自研数据流水线框架（60 py）；`libs/analysis-graph/`：Neo4j 分析图；`libs/crs-utils`、`libs/organizers`、`libs/kumu-shi` 等。
- **外围（vendored/生成/前端）**：`components/clusterfuzz`(467 文件)、`components/snapchange`(152)、`components/aflplusplus`(45)、`components/libfuzzer`(21)、`libs/nautilus`(58)、`libs/crs-utils/.../jazzer-aixcc`（~500 个 Java 文件集中于此）、CodeQL/Semgrep 封装。真正需要读的是上面"核心"清单。

## 1.3 整体架构：模块划分与数据流

骨架是 **pydatatask（PDT）声明式数据流**：`pipelines/main.yaml`（775 行）定义了 ~100 个 repository（三类存储原语：`FilesystemRepository`/`BlobRepository`/`MetadataRepository`，可挂 Azure blob 与 nginx cache 注解，`pipelines/main.yaml:162-354`）与 task（`pipeline_input`、`sanity_check` 等，带 `max_concurrent_jobs`、node 池标签、spot 节点亲和，`pipelines/main.yaml:718-775`），再 import 五条子流水线：preprocessing / oss_fuzz_tasks / targets_c / targets_java / targets_common / postprocessing（`pipelines/main.yaml:355-653`）。

主数据流（C/C++ 侧）：

```
crs_task(挑战元数据) ─▶ 构建产物(canonical/debug/coverage/aflpp)
   ─▶ 函数索引(function-index-generator) + CodeQL/Semgrep SARIF
   ─▶ code-swipe 加权过滤排名(FilterFramework, components/code-swipe/src/framework/filter_framework.py:14)
   ─▶ aijon: 对 POI 做 LLM IJON 插桩 → 定向 fuzz 种子(components/aijon/main.py)
   ─▶ crashing_harness_inputs ─▶ povguy 验证 PoV(5 次重跑、sanitizer 交集去抖)
   ─▶ poiguy: 崩溃栈→POI 报告(components/poiguy/poiguy.py:34-123)
   ─▶ patcherg 聚类成桶并发 PatchRequest ─▶ patcherq/patchery 生成补丁
   ─▶ pov-patrol: 补丁×PoV 缓解矩阵(Neo4j 边)
   ─▶ patcherg 按 Bayesian 分数与截止期策略 ─▶ submitter 提交
```

Java 侧走 jazzer/dyva 变体；所有跨组件状态经 Neo4j analysis-graph 与 PDT repo 传递，组件间零直接 RPC（除 PDClient 拉取产物）。

## 1.4 Agent/编排设计

三层编排，职责分明：

1. **流水线层（PDT）**：非智能，只做数据依赖调度与并发/预算控制（`scale_replicas`、`job_quota`、fuzzing 专用污点节点 `support.shellphish.net/only-fuzzing`，`pipelines/main.yaml:750-759`）。
2. **角色组件层**：每个"X-guy"组件是一个独立容器化进程，内部要么是确定性工具（poiguy/povguy/patcherg），要么是一个用 agentlib 写的单角色 Agent（aijon、patcherq、griller）。
3. **Agent 内部**（agentlib）：
   - `Planner`：goal → JSON 步骤列表（`libs/agentlib/agentlib/lib/agents/planning.py:171-206`，模板 `planner/generic_task_decomposition.*.j2`）。
   - `PlanExecutor`：主循环 `execute_plan()` 逐 step 执行（`planning.py:352-414`）；每 step 最多 `__MAX_STEP_ATTEMPTS__=10` 次尝试（`planning.py:496-507`），尝试失败即 `add_annotation('give_up_on_task')` 后抛错——**终止条件 = 计划走完 或 单步尝试耗尽**。
   - `CriticalPlanExecutor`：在 `validate_step_result` 挂 `PlanStepResultCritic` 评审，critic 不通过则 step 不算完成、进入下一次 attempt（`planning.py:687-723`）——即"反思=批评者门控"。
   - 步间上下文压缩：每步完成后用 `gpt-4-turbo` 把对话提炼成 `final_context` 传给下一步（`planning.py:602-620`），这是其跨步记忆的核心手法。
   - patcherq 的实战循环（业务层最完整的 plan-act-verify-reflect 例子）：TriageGuy(工具型根因分析) → ProgrammerGuy(带 peek_src/日志/CodeQL/LSP 工具的编辑 agent) → PatchVerifier(编译/复现/单测/fuzz 九道验证) → 失败码驱动的模板化 feedback 回灌同一位 ProgrammerGuy 继续修（`components/patcherq/src/patcherq/main.py:90-194`，feedback 见 `utils/programmer.py:318-327,461-784`）；各类失败有独立重试预算（compile=4/crash=4/tests=2/duplicates=3，`src/patcherq/config.py:43-50`）。

## 1.5 LLM 集成

- **Provider 抽象**：`libs/agentlib/agentlib/lib/common/llm_api.py` 以 `ChatApi` 为基类派生 `ChatApiOpenAi`(:1479)、`ChatApiAnthropic`(:1532)、`ChatApiGoogle`(:1057)、`ChatApiTogether`(:1169)、`ChatVllmModel`(:1175，自托管 vLLM，`services/vllm/` 有对应服务)，外加 `LiteLLMBudgetManager`(:56) 统一预算。消息类带 conversation_id/message_index trait 以支持 provider 级 prompt cache 续接（`llm_api.py:92-135`）。
- **成本/预算**：`TokenUsage.calculate_costs()` 硬编码 30+ 个模型的每百万 token 价格（含 cache read/write 差价，`libs/agentlib/agentlib/lib/agents/agent.py:91-283`）；全局 `EventDumper` 把每次 LLM 调用落盘为 JSON 事件并在超预算时可 `os._exit(0)` 熔断（`agent.py:314-401`）。
- **上下文管理**：`Agent.__CONTEXT_WINDOW_EXCEEDED_STRATEGY__` 支持按轮删除/摘要/抛异常三种策略，`remove_turns` 支持百分比删除并可对下一轮禁用 cache（`agent.py:1462-1520`）。
- **Prompt 原文摘录**（AIJON 插桩 system prompt，`libs/aijon-lib/aijon_lib/llm_interface/prompts/aijon.system.j2:1-2,30-33`）：

  > "You are a senior fuzz-engineer tasked with assisting a human analyst in instrumenting C/C++ code with IJON macros… You are a scalpel, strategically instrumenting the code to help the fuzzer reach deeper states and potentially trigger vulnerabilities with only the exact instrumentation that is necessary."
  > "ANY ANNOTATIONS CONTAINING FUNCTION CALLS MORE THAN JUST THE IJON_XXX CALL WILL BE DELETED AND WASTED. DO NOT DO NOT DO NOT DO NOT DO IT."

  重试时的 user prompt 直接咆哮（`aijon.user.j2:1-14`）：

  > "<YELL>\nYOUR OUTPUT DID NOT FOLLOW THE FORMAT OF: + LINE_NUMBER_TO_INSERT_AFTER IJON_XXX(YYY, ZZZ, …)\nTRY AGAIN.\n</YELL>" / "DONT ADD NEW CURLY BRACES. IT CANNOT BE ALLOWED."

  patchery 的"三专家"单次完稿 prompt（`components/patchery/patchery/generator/prompts/one_shot_prompts.py:282-302`）：三个专家角色互评打分后给出补丁，并带奖励/惩罚话术 "If your patch is accepted, you will be rewarded with a flag. / Please think carefully or a human will be physically harmed."——典型的社会心智（society-of-mind）压缩。

## 1.6 工具层/内容生成层

- **工具协议**：LangChain tool calls；`ExceptionHandlingAgentExecutor` 把工具异常包装成 `<function_call_error>` 观察值回给模型而非中断（`libs/agentlib/agentlib/lib/agents/agent.py:840-937`）；连续 N 次工具校验失败按策略 `end_turn` 提前收束（`agent.py:900-911`）。
- **工具集**（patcherq toolbox）：`peek_src`（带 md5 去重守卫，相同调用直接回 "[ERROR] You performed the same tool call … Just reuse the old results"，`components/patcherq/src/patcherq/toolbox/peek_src.py:195-221`）、构建/崩溃日志查看、CodeQL 查询、LSP；aijon 的"工具"则是纯结构化输出（`+ 行号 插桩语句`），由确定性 `apply_llm_response` 落盘。
- **内容生成层**：LLM 不直接产出 exploit 二进制，而是产出（a）插桩宏（aijon）、（b）补丁 diff（patcherq/patchery）、（c）链接器 flag（griller 的 `game_over(final_linker_flags)` 结构化终止工具，`components/griller/src/get_compile_flags.py:103-127`）——全部走"LLM 写代码、确定性框架执行验证"的模式。

## 1.7 业务方法论建模（漏洞链路）

AIxCC 链路被拆为可分别计分的阶段，每个阶段一个组件 + 一类中间产物（这是最值得借鉴的建模）：

| 阶段 | 组件 | 中间产物 |
|---|---|---|
| 攻击面排序 | code-swipe | `codeswipe_rankings`（函数权重） |
| 引导加深 | aijon | 插桩后源码 + allowlist + 种子 zip |
| 漏洞证明 | povguy | `dedup_pov_reports`（PoVReport YAML） |
| 根因定位 | poiguy / dyva / triage | POIReport |
| 修补 | patcherq/patchery | patch diff + PatchMetaData |
| 验证 | pov-patrol | Neo4j `mitigated_povs` 边 |
| 提交决策 | patcherg | submission edict |

补丁选择用 **Beta(1,1) 贝叶斯后验** `(k+1)/(n+2)` 对"缓解概率"打分（`components/patcherg/patcherg/__main__.py:113-133`），并有明确截止期博弈：完美补丁囤到超时才交、不完美补丁等到 `NON_PERFECT_PATCH_SUBMISSION_TIMEOUT_MINUTES` 才 "NO TIME LEFT! NOW OR NEVER!"（`__main__.py:506-628`）。

## 1.8 状态与记忆

- **无长程对话记忆**：Agent 基本无状态，`AgentWithHistory` 可选挂 `chat_history`；跨步记忆靠"step 完成后 LLM 提炼 final_context"（1.4 节）。
- **持久状态全在外部**：Neo4j 分析图（patch/PoV/桶/边）、PDT repo（文件/对象/元数据三级）、`AgentPlanRepository`/`AgentRepository` 本地 volumes（`planning.py:27-28`、`agent.py:601-602`）使 plan/step/attempts 可序列化并 **resume**（`reload_id_from_file_or_new` + `sync_steps`，`planning.py:229-236`；`execute_plan` 里跳过已完成步、从半途 attempt 续跑，`planning.py:359-383`）。
- **跨运行记忆**：PatchCache 以失败补丁哈希 memoization，重复补丁直接短路验证（`components/patcherq/src/patcherq/utils/programmer.py:266-283`）。

## 1.9 隔离与安全

- K8s 层：专用 fuzzing 节点池 + 污点容忍 + spot 优先（`pipelines/main.yaml:746-759`）；每组件独立 Docker 镜像；目标代码在临时 `mkdtemp` 副本上修改（`components/aijon/main.py:264-272`）。
- LLM 层：全局美元预算熔断（可 `os._exit`）、LiteLLM 预算名、provider 轮换 + 等待 7 分钟"budget tick"（`components/patcherq/src/patcherq/utils/programmer.py:212-241`）。
- 输出层：LLM 的代码修改全部过确定性守卫——aijon 拒绝在预处理行/无花括号 if-else/多行 for 后插桩（`libs/aijon-lib/aijon_lib/llm_interface/instrumentor.py:39-65`），每条插桩打 `PATCHID` 便于编译失败时让 FixerAgent 按 ID 整行删除（`instrumentor.py:67-68,275-297`）；programmer 禁改 `LLVMFuzzerTestOneInput` 入口（`components/patcherq/src/patcherq/prompts/programmerGuy/programmerGuy.CoT.system.j2:44`）。
- 遥测：OTEL 全链路（`components/aijon/main.py:26-28`）。

## 1.10 关键代码走读

**链路 A：AIJON LLM 插桩（LLM→定向 fuzz）**
POI 进入 `worker_function`（`components/aijon/main.py:103-243`）→ 查 Neo4j 分析图找"最近被覆盖的调用者"与调用路径（`main.py:134-160`）→ `instrument_code_with_ijon()`（`libs/aijon-lib/aijon_lib/llm_interface/instrumentor.py:84-196`）调 `AIJONInstrumentorAgent`，重试 3 次，每次先过 `is_valid_response`（必须含 `RESULT` 且每行 `+ 行号 IJON_XXX`，`instrumentor.py:243-256`）→ `apply_llm_response()` 把行号偏移回真实文件并执行安全守卫插桩（`instrumentor.py:22-73`）→ `AIJONCritiqueAgent` 审查 diff 后二次应用（`instrumentor.py:148-171`）→ 外层 `Pool(20)` 并行处理所有 POI，按行号降序回写避免偏移失效（`components/aijon/main.py:284-328`）→ 全失败则 10 分钟后整体重试，最多 10 轮（`main.py:399-421`）。

**链路 B：PoV 验证与去抖（povguy）**
候选 PoV 跑 5 次，取各次触发 sanitizer 集合的**交集**判一致性，同时并集回填栈帧（`components/povguy/povguy.py:120-184`）；delta 模式下把 base 源码 rsync 到临时目录重跑，若 base 也崩则 exit(0) 静默丢弃（"See what to do here as this could possibly be a zero day"，`povguy.py:283-290`）；产出 dedup(MD5 键)+代表输入+LOSAN 变体并上传图（`povguy.py:232-359`）。

**链路 C：patcherq 生成-验证-反馈闭环**
`PatcherQ.start()` 三层循环：预算外循环 → 根因报告流 → `while programmer.patch_state != 'stop': generate(); verify()`（`components/patcherq/src/patcherq/main.py:90-194`）。验证失败产生结构化 failure_code，`set_feedback()` 把模板化反馈塞回同一 ProgrammerGuy 的对话继续修（`utils/programmer.py:318-327`）；结构化输出解析失败时用便宜的 `gpt-4.1-mini` 做"格式修复"再解析，最多 3 次（`agents/programmerGuy.py:100-169`）。

**链路 D：patcherg 提交决策（非 LLM 的博弈层）**
Neo4j 聚 PoV 桶（桶键 = 去重标识排序后的 md5，`__main__.py:972-973`）→ 对每补丁算 Beta(1,1) 缓解分（`:95-111`）→ 完美补丁超时才交、非完美补丁压哨交（`:506-628`）→ 自适应提交超时 `1.5 + 0.2*log2(1+extra)` 随桶数增长（`:81-93`）。

## 1.11 亮点与弱点

亮点：
1. **数据流骨架 + 无状态角色组件**：PDT 让"换掉 patcherq"不影响上下游，LLM 幻觉的爆炸半径被限制在单组件内。
2. **agentlib 的工程完备度**：预算熔断、按 provider 的 cache 记账、上下文超限三种策略、工具异常转观察值、计划可序列化断点续跑——都是生产级 Agent 系统的硬需求。
3. **"LLM 写、框架验"的纪律**：所有 LLM 产出都经确定性守卫与多道验证门（povguy 5 次交集、pov-patrol 矩阵、PatchVerifier 九关）。
4. patchery 的温度"退火"与三专家 prompt、patcherg 的贝叶斯+截止期博弈，都是低成本高收益的启发式。

弱点：
1. 复杂度极高：50+ 组件、775 行 yaml、Terraform/Helm/Tailscale 多重依赖，复现与调试成本大；README 自认"private Wiki"才有内部文档。
2. prompt 里大量咒骂式/全大写约束（`aijon.user.j2`），说明结构化输出纪律是靠补丁式对抗维持的；格式修复还要再花一次便宜模型调用。
3. 成本表硬编码在 `agent.py:91-283`，模型迭代即过期。
4. `execute_plan` 的 plan 是一次性生成、不可重规划（`sync_steps` 只同步描述）；critic 也只做单步门控，没有全局反思。

---

# 第二部分：Atlantis-Multilang（Team Atlanta CRS-multilang）

## 2.1 一句话定位与项目背景

一句话：**以 UniAFL（LibAFL 改造版）为执行引擎、以 FuzzDB 为共享状态、以三条 LLM 生成通道（字典/Testlang 语法/Python 生成器脚本）向 fuzzer 供给结构化输入的多语言漏洞挖掘系统**。

背景：Team Atlanta（Gatech SSLab 等，`README.md:113-129`）的 AIxCC 作品。本仓库是去掉 concolic 执行器的开源快照（`README.md:1` "CRS-multilang (w/o concolic)"）。运行形态是 `oss-crs` 仓库的 `crs-compose prepare/build-target/run` 三段式 Docker Compose（`README.md:28-57`），目标项目必须基于 `ghcr.io/aixcc-finals/base-builder:v1.3.0` 镜像（`README.md:63-78`）。

## 2.2 技术栈与代码规模

- 全仓库：Python ~605 文件、Rust 90、Java 462（Joern 相关）、`.g4` 语法 346（testlang 语法族）、`.stmt` 1914（语法测试数据）、SQL 306。
- **核心**：
  - `blob-gen/multilang-llm-agent/`（MLLA）：约 6600+ 行 py 的多 Agent 系统（`mlla/main.py` 882 行、`mlla/agents/` 下 CPUA 1015 / MCGA 1817 / BCDA 实验版 1368 / CGPA 1107 行等）。
  - `dictgen/`：LLM 字典推断，~1100 行（`src/dictgen.py` 325 行 + 5 个 agent）。
  - `reverser/harness-reverser/`：Testlang 输入格式反推，12 个 py + SYSTEM/IMPROVE/MAJORITY 等 prompt 文件。
  - `uniafl/`：Rust fuzzing 基础设施（`src/msa/` 调度、`src/input_gen/{dict,testlang}/`、`src/executor/`）。
  - `fuzzdb/`：Rust 共享数据库（`src/db.rs` 649 行）。
- **外围**：`oss-crs/` 编排（外部仓库引用）、`joern/`、`lsp/`、`tests/`、`static/` 图、语法测试语料。

## 2.3 整体架构：模块划分与数据流

组件表（`README.md:88-96`）：UniAFL（fuzzing 核心）/ FuzzDB（结果库）/ dictgen+字典 mutator / Testlang reverser+生成器 / MLLA（LLM 输入生成）/（concolic 略）。

数据流：

```
目标源码 ──┬─▶ MLLA full 模式: CPUA(理解 harness)→MCGA(带污点调用图)→BCDA(BITs 候选)
           │        └─▶ Orchestrator 并发 {BlobGen, Generator, Mutator} ─▶ Python 生成脚本 ─▶ blobs + coverage
           ├─▶ dictgen: 每函数 Token/Trigger/可解析串 ─▶ fuzz 字典(供 AFL dict mutator)
           ├─▶ reverser: harness ─▶ Testlang 语法 ─▶ 结构化生成/变异
           └─▶ UniAFL: 加权调度跑 harness ─▶ FuzzDB(覆盖/diff/interesting/BCDA/CPV)
                    ▲__________ FuzzDB 权重反过来驱动调度(scheduler.rs) __________|
```

关键闭环：MLLA 的 BCDA 结果与 diff 信息写入 FuzzDB，`UniScheduler.load_bcda_result()` 触发 corpus 重新加权（`uniafl/src/msa/scheduler.rs:160-166`）——**LLM 的静态判断直接改变 fuzzer 的采样分布**。

## 2.4 Agent/编排设计（以 MLLA 为核心）

角色（`blob-gen/multilang-llm-agent/README.md:67-115`）：
- **CPUA**：理解 harness，产出目标函数 + `entry_to_function` 调用链 + 污点参数索引 + 优先级（结构化 JSON）。
- **MCGA**：以 CPUA 结果为根构建调用图，工具层有 Joern/LSP/CodeIndexer/AST-grep；**CGPA** 是被其它 agent 复用的"函数信息工具 agent"（带优先级的工具选择，`mlla/utils/llm.py:174` `PrioritizedTool`，提示词 `mlla/prompts/llm.py:3-9` 明示"priority 高者优先"）。
- **BCDA**：在调用图上找 Bug Inducing Things（BITs），输出极简 JSON（`mlla/prompts/bcda.py:6-15`：`{"jazzer_triggering_line": ..., "required_info": [filename,...]}`，按需索取更多代码）。
- **Orchestrator**：把 BITs 变成三路上下文，asyncio 并发调 BlobGen/Generator/Mutator，做去重过滤与聚合（`mlla/agents/orchestrator_agent/modules/run_all_agents.py`）。

主循环（Generator Agent，LangGraph 状态图，`mlla/agents/generator_agent/graph.py:25-75`）：

```
preprocess → [select_sanitizer] → plan_generator → create_generator → collect_coverage
   → (条件) update_interesting_functions → analyze_coverage → (条件) create_generator（改进回环）→ finalize
```

- **规划**：`plan_generator` 先让 LLM 出两阶段计划（先到达目标函数、再触发漏洞）。
- **反思/改进**：`analyze_coverage` 对比前后覆盖 diff 生成改进建议，条件边 `should_improve_generator` 决定是否回到 `create_generator`。
- **终止条件**（`graph.py:78-167`）：发现 crash 即停（"Crash found, stopping generator loop"）、无 payload、无合并覆盖、或 `BGA_GENERATOR_MAX_ITERATION`（env 控制）耗尽；standalone 模式外层还有 `SIGALRM` 硬超时（`mlla/main.py:184-200`，`BGA_GENERATOR_STANDALONE_TIMEOUT` 默认 1000s）。
- 注意 `main_graph()`（`mlla/main.py:149-175`）当前只激活 `preprocess→cp_understand`，BCDA/orchestrator 边被注释——主线走 standalone 生成器，全流水线按需启用，这是**渐进降级**的部署策略。

## 2.5 LLM 集成

- **Provider 抽象**（`mlla/utils/llm.py:327-560` 的 `LLM` 类）：内部 "atlanta-chat/tool/reasoning" 模型走 **LiteLLM router**（自建网关，`LITELLM_KEY/LITELLM_URL`）；`claude*` 走 ChatAnthropic 且启用 `betas=["extended-cache-ttl-2025-04-11"]` 与 prompt caching；`gemini*`/其余走 ChatOpenAI 兼容端点。全局双速率限制器（general + claude 专用）。
- **大上下文降级链**：每个 LLM 实例自动准备 `large_context_model` 及其 fallback（`get_large_context_model_name`，`llm.py:298-310`），上下文溢出时切换。
- **上下文预算**：`get_context_limit()` = 模型 max_input − 输出预留 − 1000 保守缓冲（`llm.py:515-547`）。
- **输出修复协议**（`mlla/prompts/llm.py:11-24`）：
  > CONTINUE_MSG = "Your previous answer was truncated. Please continue from the right after the exact character where you left off. Do not repeat…"
  > ASK_AND_REPEAT_UNTIL_MSG = "Your previous answer was INVALID.\nThe error was:\n{e}\n\nCarefully think step by step and try again. This is the last change."
  截断续写用 `accumulate_content()` 拼接（`llm.py:155`）。
- **Prompt 原文摘录**（Generator system prompt，`mlla/agents/generator_agent/prompts/system_prompts.py:4-42`）：

  > "You are an expert security researcher specializing in vulnerability analysis and exploit development in an oss-fuzz project… you will implement a 'generate(rnd: random.Random) -> bytes' function that: Uses the provided Random instance for all randomness / Returns ONLY a single bytes object / … Uses ONLY built-in Python libraries (e.g., struct, json, base64)… The core challenge is that reaching and exploiting the vulnerability often requires: Navigating through complex validation checks / Satisfying format requirements / …"

  CPUA 的 harness 理解 prompt 用 `<term>/<task>/<step>/<input>/<output>/<requirements>` 七段式 XML 结构并规定 JSON 输出模式（`mlla/prompts/cpua.py:17-113`）。

## 2.6 工具层/内容生成层

- **代码导航工具**：CodeIndexer（自建索引）、LSP（go-to-definition/find-references）、Joern CPG、ripgrep/AST-grep——由 CGPA 封装成带优先级的工具集供各 agent 调用。
- **生成物执行沙箱**（最值得抄的部分，`mlla/agents/generator_agent/nodes/common.py:9-52`）：LLM 产出的 `generate()` 被包进模板脚本——

  ```python
  def limit_memory():
      resource.setrlimit(resource.RLIMIT_AS, (1024 * 1024 * 1024, -1))   # 1GB 内存
  ...
      payload = generate(rnd)          # 固定种子 rnd，可复现
      if len(payload) > 1024 * 1024:   # 1MB payload 上限
          payload = payload[:1024 * 1024]
  ```

  白名单库仅 `base64/struct/sys/hashlib/json/zlib/random/binascii/re/libfdp`（`mlla/utils/execute_llm_code.py:12-23`），以子进程 + 60s 超时执行（`execute_python_script`，`execute_llm_code.py:54-92`）；错误回灌给 LLM 修复（`common.py:79-99` 对 MemoryError 给出专门提示"Please generate more efficient code"）。PoV 执行则进 Docker（`process_pov_in_docker_async`，`nodes/collect_coverage.py` 引用）。
- **三条内容通道**：① 字典 token（dictgen，AFL dict）；② Testlang 语法（reverser，编译成 AST 生成器/变异器，Rust 侧 `uniafl/src/input_gen/testlang/`）；③ Python 生成脚本（MLLA，"generator/encoder/mutator"三签名）。

## 2.7 业务方法论建模（挖掘链路）

把"找到 bug"分解成与 fuzzer 天然对齐的四个正交目标（README `MLLA Details`，`README.md:97-110`）：
1. **理解**（CPUA）：入口→目标函数→污点参数；
2. **构图**（MCGA）：带覆盖标注的调用图（AttributeCG）；
3. **定位**（BCDA）：BITs = 有优先级的疑似漏洞点（`mlla/utils/bit.py`）；
4. **供给**（Orchestrator 三 agent）：分别针对"单点精确打击（BlobGen）/概率发散（Generator）/函数转移（Mutator）"三种失败模式设计——Mutator 明确为"上下文放不下整条调用链时只学一条边"的降级策略（README :107-112）。

FuzzDB 把 LLM 判断转成调度权重（见 2.9），dictgen/reverser 则覆盖"无 LLM 也可用"的基线通道——**多通道冗余 + 按目标特征选通道**。

## 2.8 状态与记忆

- **Redis**：跨 agent 的缓存/断点/恢复（`--redis` 参数、`mlla/utils/redis_saver.py`、`redis_utils.py`；dictgen 也用它按 (type, file, func) 缓存 token，`dictgen/src/agents/DictAnalyzer.py:100-107`）。
- **LangGraph checkpointer**：`builder.compile(checkpointer=gc.checkpointer)`（`mlla/main.py:173`），状态图可持久化续跑。
- **FuzzDB（进程间真相源）**：`fuzzdb/src/db.rs` 存覆盖（按函数/源码行）、diff 信息、interesting 函数、BCDA 结果、CPV（crash/PoV）通知；`match_interesting_cov()`（db.rs:229）、`extract_callstack_from_log()`（db.rs:360）等查询供调度器加权。
- **历史压缩**：`summarize_chat_model`（gpt 系小模型，`llm.py:540-549`）用于对话摘要；`historytracker.py` 跟踪长任务。

## 2.9 隔离与安全

- 目标与 CRS 全部容器化：`crs-compose` 强制 base-builder 镜像版本（供应链收口，`README.md:63-78`）；每 harness 独立 compose 运行。
- LLM 生成代码三重限制：白名单 import、RLIMIT_AS 1GB、子进程超时（2.6 节）；payload 尺寸截断防止 fuzzer 压力测试被滥用。
- LLM 访问经 LiteLLM 网关（密钥不落组件）；Claude 专属速率限制器防 429。
- 遥测：OpenTelemetry（Phoenix/Traceloop 后端，MLLA README :270-283）。

## 2.10 关键代码走读

**链路 A：Generator Agent 的生成-执行-覆盖反馈闭环**
`graph.py:49-72` 建图 → `create_generator` 节点用 `GENERATOR_CREATION_PROMPT` 让 LLM 按 plan 产出 `<generator_code>` 标签包裹的 `generate()`（`mlla/agents/generator_agent/prompts/create_prompts.py:10-74`）→ `execute_generator()` 包模板、限资源、跑出 N 个 blob（`nodes/common.py:54-99`）→ `collect_coverage` 在 Docker 中跑 PoV、合并覆盖、计算新增/移除函数与行数（`nodes/collect_coverage.py:26-80`）→ `analyze_coverage` 用 `COVERAGE_SUMMARY_TEMPLATE` 生成改进建议 → 条件边回 `create_generator`（`graph.py:141-167`）。

**链路 B：UniScheduler 加权采样**
`calculate_score_one()`（`uniafl/src/msa/scheduler.rs:37-68`）：BIT 命中漏洞函数名或行 = +8（VULN_WEIGHT），key 命中 ×权重 ×4，`should_be_taken` ×1，deprioritized 直接 1 分；`eval_seed()` 再叠加 diff 文件 ×2、diff 行区间 ×4（`:70-82`）。采样分布（`:124-137`）：25% interesting 桶、50% 加权桶、25% 均匀——兼顾聚焦与探索。BCDA/CPV 事件触发全量 `rebalance()`（`:98-112,149-158`）。testlang 语料与普通语料分池，testlang 阶段 90% 时间用 testlang 池（`:182-204`）。

**链路 C：dictgen 的多 agent 投票**
`DictAnalyzer.__init_agents()`（`dictgen/src/agents/DictAnalyzer.py:33-74`）实例化 7 个角色：token_extractor / diff_token_extractor / trigger_extractor / trigger_verifier / **两个 parsing_function_detector（多数投票）** / parsable_string_extractor；`analyze()` 用 `asyncio.gather` 并发三路再合并（`:76-90`）。prompt 以 JSON 文件外置（`src/configs.py:86-89`），支持 delta 模式（diff_token_extractor 只看 diff）。

**链路 D：reverser 的 Testlang 反推**
SYSTEM prompt（`reverser/harness-reverser/prompts/SYSTEM`）定义领域语言契约："fuzzer 先依 testlang 构建 AST 再序列化为字节；字段可带 encoder/generator；遇 crash_logs 应转向新漏洞；有 diff 时聚焦 diff"；复杂结构允许逃逸到 `custom`（已知格式库）或自写 Python `encoder(bytes->bytes)/generator(()->bytes)`，且要求 generator 内置解析自校验、不捕获异常以便把畸形反馈回来。输出用 pydantic 强类型（`agents/reverser.py:77-347`：`CodeReference/TestLangRef/FunctionLinesRef/PythonCode/ReverserLLMOutput`）+ LangGraph 状态（`ReverseOneState(MessagesState)`），配 MAJORITY/IMPROVE 两个 prompt 文件做投票与迭代。

## 2.11 亮点与弱点

亮点：
1. **LLM↔fuzzer 的双向闭环**：不只让 LLM 生成输入，还把 LLM 的漏洞假设（BITs）量化进语料采样权重（scheduler.rs 权重表），静态判断获得执行反馈后可 rebalance。
2. **生成物沙箱模板**：固定种子、内存/payload 双上限、白名单 import、错误分类回灌——把"运行 LLM 写的代码"这件危险事做成了可复现实验。
3. **三通道互补 + 三 agent 分工**：对"LLM 概率性失败"的应对不是重试而是概率化（Generator 的多变异假设）与降级（Mutator 只学一条边）。
4. 大上下文模型 fallback 链、Claude 长缓存 TTL、截断续写协议等细节都很"打过仗"。

弱点：
1. `main_graph` 大量注释掉的边、`bcda_experimental.py` 与正式 BCDA 并存、测试语言文件混杂（`.stmt` 1914 个）——研究代码痕迹重，主线与实验线边界模糊。
2. reverser 的 SYSTEM prompt 承载了整个 DSL 契约，长且无版本化拆分；Testlang 生态（.g4 346 个）自维护成本高。
3. prompt 硬编码模型名（如 `tiktoken_model_name="gpt-5.4"` 等散落 `llm.py`），与 artiphishell 同样的模型迭代脆弱性。
4. concolic 通道在本快照缺席，文档与代码存在漂移（README 明示另仓）。

---

# 第三部分：对构建授权 AI 渗透测试系统的可借鉴设计（横向总结）

1. **骨架与肌肉分离**：用声明式数据流（PDT）或 compose 管理非智能编排，Agent 只做角色组件——渗透系统的 recon/exploit/report 阶段同理可拆成可独立替换的角色。
2. **"LLM 写、框架验"纪律 + 结构化最小输出**：AIJON 的 `+ 行号 语句` 格式、BCDA 的两字段 JSON、reverser 的 pydantic 类型，都把 LLM 输出压到最小可验证面；守卫失败→定向反馈→有限重试→放弃，全程留 annotation。
3. **执行反馈必须回流**：覆盖 diff（MLLA）、编译错误（aijon Fixer）、缓解矩阵（pov-patrol）三类反馈分别驱动改进回环；渗透系统应把"动作结果"结构化后回灌规划层。
4. **预算与降级是一等公民**：美元熔断、模型轮换、大上下文 fallback、温度退火、多通道冗余（dict/testlang/LLM）。
5. **状态外置可续跑**：Neo4j/Redis/FuzzDB + 可序列化 plan，长任务崩溃后能从半途 attempt 恢复。
