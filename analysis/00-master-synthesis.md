# AI 渗透测试系统参考调研 · 总览与综合分析

> 调研范围：`refer/` 下 4 大分类约 59 个项目（去重后 54 个有实际代码），共产出 22 份分组深度报告（`analysis/01` ~ `analysis/22`），本报告为横向综合。
> 定位：为自研"世界最强 AI 渗透测试系统"（对标 ARTEX/ACTEX）提供设计输入。
> 全部结论来自源码静态走读，引用见各分组报告的 file:line。

---

## 0. 阅读指南

| 想了解 | 看哪份 |
|---|---|
| 对标基准 ARTEX 的完整实现 | 01（核心）+ 09（三个 fork 的增强） |
| 最强的多 agent 平台工程 | 03（PentAGI）、04（Decepticon）、05（RedAmon） |
| 轻量级"agent 循环嫁接 shell"路线 | 02（cai）、14（BoxPwnr 等）、20（ctf-agent） |
| 学术方法（验证、终止、DRL） | 10、17、18、20 |
| 竞赛/自动解题方向 | 13（AIxCC CRS）、20（HackSynth/ctf-agent）、22（黑客松资料） |
| 中文社区生态 | 07、08、21、22 |

---

## 1. 生态全景：五种产品形态

54 个系统按形态可分为五类，它们其实是同一技术在不同约束下的收敛：

1. **交互式副驾（Copilot 形态）**：PentestGPT、EVA、BugTrace-AI。人不离环，LLM 出建议/解析输出。价值在 prompt 工程与状态建模（PTT 任务树、"LLM 提议、代码裁决"）。
2. **单/少 agent 全自动循环**：cai、BoxPwnr、ctf-agent、shannon、AI-OPS、Xalgorix。核心资产是"一个足够好的 agent harness + shell 执行封装 + 记忆文件"。XBOW/HTB 高分大多出这一类（Decepticon 本质也是收束的编排器+执行子 agent）。
3. **多 agent 编排平台**：ARTEX、PentAGI、RedAmon、CyberStrikeAI、Pentest-Swarm-AI、VulnBot。核心问题是"角色如何分工、通信、防打转、收尾"。四种拓扑见 §2。
4. **流水线/数据流型 CRS**（竞赛产物）：ArtiPhishell、atlantis、crs-claude-code、AI-Infra-Guard（半流水线）。特点是无状态角色组件 + 持久化数据骨架（pydatatask/Neo4j），LLM 幻觉的爆炸半径被限制在单组件内。
5. **MCP 工具层**：hexstrike-ai、pentest-ai、seclab-taskflow、Tsec 比赛平台。把能力封装成 MCP 供任意 agent 消费——趋势明显：**工具层正在 MCP 化，agent 层与工具层解耦**。

一个清晰的行业趋势：**"自建 Claude Code 式 harness + Bash 万能工具 + 文件系统记忆"正在胜过"为每个安全工具做精细封装"**。ARTEX 的 norma、Decepticon 的 bash/tmux、cai 的 generic_linux_command、BoxPwnr 的 executor 都是这个路线。领域工具（nmap/sqlmap）不再逐个封装，而是靠：环境声明（shell hints）、技能文档（SKILL.md 方法论）、prompt 使用规范三件套供给。

---

## 2. 编排拓扑对比（多 agent 的四种答案）

| 拓扑 | 代表 | 机制 | 优点 | 代价 |
|---|---|---|---|---|
| **Planner/Worker + 共享图** | ARTEX、Nebula | planner 派意图入 frontier，worker CAS 领取，经 DB 图解耦 | 重启即恢复、天然并行、事件驱动唤醒 | 双图 schema 复杂；planner 提示词复杂度失控风险 |
| **Orchestrator + 专家即工具** | PentAGI、cai、mapta、VulnBot | 专家是注册表里的一个工具，委托=嵌套调用同一引擎 | 统一重试/摘要/落库/追踪；新增专家三件套 | 同步嵌套、上层链被慢专家占住；上下文层层放大 |
| **图状态机 + 中断恢复** | RedAmon、Strix | LangGraph N 节点 + await_* → END 挂起 + checkpoint resume | 人在环路一等公民；状态显式可审计 | JSON 决策协议自研脆弱；每轮重组 prompt 成本 |
| **群智能黑板** | Pentest-Swarm-AI | 信息素衰减 + Trigger 谓词订阅 + 信任经济 | 自组织广度；廉价模型污染有折扣机制 | 可控性/可解释性弱，工程成熟度低 |

**结论**：自研系统建议 **Planner/Worker 图解耦（ARTEX 骨架）+ 专家即工具（PentAGI 委托原语）混合**——跨意图的粗粒度并行用图解耦，意图内按需嵌套专家（pentester→coder），避免 full swarm 的复杂度。

---

## 3. 八大设计维度对比

### 3.1 LLM 集成与上下文工程

- **Provider 抽象**：ARTEX 裸 HTTP 三协议（零 SDK 依赖）+ 多 profile 故障转移池（"未交付任何事件才允许换 member"防重复输出）；Decepticon 三轴（Tier×AuthMethod×Profile）按角色失败代价定档 + resolve_chain 主备链；PentAGI 每角色独立 AgentConfig + 全局热备。**按角色路由模型（侦察用便宜模型、利用用强模型）是标配**。
- **上下文压缩三家派**：① Claude Code 式三层（Micro/Auto/Reactive，ARTEX norma、BoxPwnr 双历史）；② 结构化摘要（PentAGI chainAST 分节字节预算 + "摘要感知协议"教模型识别压缩标记且禁止模仿——把压缩做成系统与模型的双向契约）；③ 每轮重组状态化 prompt（RedAmon：无 messages 回放，只有 execution_trace + 图查询，天然免疫历史腐烂）。
- **缓存边界**：静态前缀/动态段切分打 cache_control（Decepticon CACHE_BOUNDARY、RedAmon 不透明哨兵、cai 手动 4 块）。省钱大招，必做。
- **知识图谱冷压缩**：ARTEX 独有——把对话压缩思想搬到探索图（冷节点折叠成 digest，expand_digest 还原）。

### 3.2 工具层

- **Bash 万能工具 + tmux/PTY 持久会话**是可用性分水岭：Decepticon tmux PS1 标记完成检测 + `is_input` 续话让 msfconsole/evil-winrm 可被 agent 驱动；cai PTY 会话 + LLM 友好子语法（S1/#1/last + 参数错位兜底）；VulnBot paramiko 密码提示自动配对。**没有交互式会话支持的系统打不了真实内网**。
- **输出治理三级制**（Decepticon 15K 内联/.scratch 卸载/5M watchdog；RedAmon 20KB offload + head/tail stub；ARTEX 30K 截断+落盘）：长扫描不撑爆上下文的标配。配套"2KB 必重定向"prompt 硬规则 + STALE 检测。
- **工具可见性即权限**：PentAGI 每角色一个 executor 视图；RedAmon TOOL_PHASE_MAP 阶段可见性 + executor 硬校验双层；ARTEX worker 刻意裁剪检索类工具。
- **MCP**：协议已成事实标准（stdio/HTTP/SSE 三传输、deferred tools 省上下文）。hexstrike 证明工具 schema 的 additional_args 直通就是注入面——**MCP 工具参数必须结构化校验，禁止拼 shell**。另一极是 tinyctfer 的 **Meta-Tooling**：对模型只暴露 1 个 execute_code（有状态 Jupyter kernel，变量跨调用持久），Python 内 toolset 库驱动一切——上下文污染归零、token 成本极低，小团队可优先考虑。
- **C2 原语化**：Viper 文档展示成熟 C2 平台的正确 agent 接法——以 MCP 暴露 15 个受控原语（session_shell_command_run/msf_module_execute 等）而非裸 shell，131 篇模块文档以 ATT&CK 战术为命名空间。
- **留痕**：ARTEX 的 MITM 代理 + 六生态 CA 环境变量注入（任意 CLI 工具零配置全流量留痕 + SQLite FTS 检索）是最优雅的取证设计。

### 3.3 渗透方法论建模

四种流派，效果依次增强：

1. **写进 prompt**（多数系统）：PentAGI pentester.tmpl 的 msfconsole 规则/detach 协议；cai TRACE 七步。
2. **阶段状态机 + 硬门**：RedAmon 三阶段（informational/exploitation/post_exploitation）+ 升级需审批 + recon 预算门（15 轮后禁扫描工具强制转利用——对"永远侦察不敢下手"通病的硬约束）；shannon 五阶段"无利用不报告"。
3. **分类路由 + 技能库**：RedAmon attack_path 15 类路由 + 18 个带 GATE 硬门的漏洞类 workflow（SQLi 登录页认证绕过矩阵）+ 46 个技能文档；Decepticon 314 个 SKILL.md 渐进披露 + frontmatter 契约；CyberStrikeAI 16 杀伤链子代理 + 24 技能。
4. **图/DAG 驱动**：ARTEX 探索图血缘涌现式推进；Decepticon OPPLAN 版本化目标 DAG + PlanFact 证据门（事实撤销→依赖目标传递性 blocked）+ ATT&CK 目录校验。

**结论**：方法论应该"分层编码"——骨架用 DAG/图（可校验可恢复），战术纪律用带硬门的 workflow 文档（可积累可评审），细节用 prompt。

### 3.4 状态与记忆

| 系统 | 方案 | 独特贡献 |
|---|---|---|
| ARTEX | 双图（探索图×资产图+anchors） | 任务内血缘 vs 跨任务真值分离，抗压缩抗重置 |
| RedAmon | Neo4j 攻击面图 + EvoGraph 攻击链 | "graph-first 不重测"；会话记忆升格为图谱资产 |
| Decepticon | 文件系统工作区 | tried.txt/ACTIVE.md/PIVOT.md 外置状态机跨摘要存活 |
| BoxPwnr | progress.md 八节 | Dead Ends DO NOT RETRY 节跨尝试交接 |
| cai | JSONL 轨迹 | 数据飞轮（debug+训练语料一体） |
| PentesterFlow | 四层记忆 | scenarios.jsonl 跨会话自动学习带质量门控 |
| Zen | PostgreSQL + 哈希链 | 证据区块链式保全 |
| CyberStrikeAI | 黑板 Fact 图谱 | fact_key 唯一 + 索引/正文两级取回 |

**共识**：LLM 上下文记忆（会腐烂）必须与事实记忆（DB/图/文件，永不丢失）分离；发现（finding）必须独立留存并带证据/置信度/复现引用。

### 3.5 隔离与安全（最大方差维度）

- **正面教材 Decepticon**：RoE"一份数据、两个执行点"——命令解析层快速失败（承认可绕过）+ 同一规则编译成 nftables/DNS 出口围栏权威封堵；安全关键件闸门（插件想动安全件必须显式 env 开关）；沙箱不可达 LLM 网关（双网络拓扑）；HMAC 审计账本。
- **正面教材 RedAmon**：不可伪造 untrusted 围栏（输出后生成 nonce 包裹 + 伪标记中和——对"目标即敌手"提示注入的优雅解）；agent 容器不在目标网（工具层即策略执行点）；三网隔离 + cap_drop ALL；密钥不进扫描容器。
- **正面教材 PentAGI**：CapDrop ALL + 白名单能力 + PidsLimit 防 fork 炸弹；DinD 启动期实测（sandbox_probe 拒绝 host-escape 才放行）。
- **正面教材 LuaN1aoAgent**：per-task Docker（cap-drop ALL/只读 rootfs）+ **gvisor 用户态网关做任务网唯一出口**，容器配置启动后逐项断言；scope CIDR 若由 AI 从 goal 推断必须逐字出现否则拒绝（防注入扩权）。
- **反面教材**（引以为戒）：cai 的 Docker 是特权工具箱（host 网络 + seccomp unconfined + 失败回退宿主）；ARTEX 全部命令在宿主机执行无沙箱；hexstrike 无鉴权 HTTP + f-string 拼 CLI + shell=True；AI-Infra-Guard 无鉴权 + SYS_ADMIN 容器。**几乎所有系统的 scope 约束都只停在 prompt 层**（PentAGI 报告明确指出）——这是全行业通病，也是自研系统最大的超越机会。

### 3.6 防打转与反僵化（决定任务成败的暗功夫）

四层递进，全部值得实现：

1. **重复检测**：PentAGI 回贴真实工具响应"它不会变"；ctf-agent 假工具结果打断循环；nyuctf 观察滑窗。
2. **预算与收尾**：ARTEX Settlement（步数耗尽注入收尾词+禁 Bash+独立轮数，强制写回发现再结束——长任务不烂尾的关键）+ 意图终态 done/exhausted/blocked 区分；Strix 生命周期强制终止工具（finish_scan 是唯一出口）；nyuctf/ArtiPhishell 美元预算熔断。
3. **元认知审计**：RedAmon productivity 评分用观测计数器交叉审计 LLM 自评并自动降级不诚实自评；Deep Think 强制竞争假设 + 区分性探针 + Jaccard 拒换皮计划。
4. **搜索与死亡账本**：RedAmon LATS 树搜索 + `_lats_dead` 跨树死亡账本；Decepticon 失败三模态（INFRA/CRASH/WANDERING）差异化重试 + 反置信度膨胀护栏。

### 3.7 验证与反幻觉（发现质量）

- **机器 oracle**：pentest-ai verification_recipe→oracle（N/N 重放/差分/OAST）→Receipt，verdict 只有 verified/candidate 永不 refuted，prove-or-kill 抑制未证实输出；Xalgorix 独立 Verifier 闭环（confirmed 必须有真实复测动作）+ 7 个确定性 verify_* 工具。
- **真值校验声明**：hackingBuddyGPT task_solved 每条命令真实输出过 check_command_success（uid=0 正则），幻觉"got root"被拒。
- **证据门**：Decepticon 严重度≤证据等级天花板；Cyber-AutoAgent proof_pack（发现必须附真实存在工件路径，os.path.exists 校验）；Zen 多 LLM 投票 + 贝叶斯 + EPSS 加权；shannon 只有 status:exploited 才进报告。
- **反证验证**：artex-ymh 的 verifier agent 专门做反证。

### 3.8 人在环路

- **干预原语**：ARTEX steer_work（在跑 worker 下次工具调用前注入纠偏，不 kill 不丢进展）+ kill_work 止损——最细粒度；RedAmon 确认门支持 modify（改参数放行）；PentAGI ask 屏障三级 back-propagation + 回答回填工具响应断点续跑。
- **审批层次**：pentest-copilot 三档 + 独立小模型审批官；Strix 双策略美元预算 + 梯度预警；Decepticon HITL 按 ATT&CK 标签匹配 + 超时默认 deny + "操作员批准也不能越过 RoE"。
- **挂起恢复**：LangGraph await→END+checkpoint（RedAmon）；DB 状态机 waiting（PentAGI）；transcript Resume（ARTEX）。

---

## 4. 各系统"一句话精华"速查表

| 系统 | 精华 | 最值得偷的 1 件东西 |
|---|---|---|
| ARTEX | 双图 + planner/worker CAS 解耦 + Settlement | 探索图/资产图数据模型 |
| artex-ymh | 攻击纵深扩展（webshell 会话/隧道/凭据库） | pending_scope 人工审批扩段 + 任务级流量路由 |
| artex-ko | 防御侧产出 | Sigma/Suricata 检测规则包（系统行为指纹） |
| cai | 角色即插件 + 成本闸门 | 三重价格闸门 + JSONL 轨迹飞轮 |
| PentAGI | 虚拟安全公司 15 角色 | agent 即工具的委托原语 + 屏障协议 |
| Decepticon | 军事化规划 + RoE 双层执行 | nftables 出口围栏 + OPPLAN PlanFact 证据门 |
| RedAmon | 全链路（侦察管线→攻击→修 PR） | untrusted nonce 围栏 + 三层反僵化栈 |
| Nebula | 崩溃一致性 | uuid5 确定性调用身份 + 幂等账本 |
| pentest-ai | 机器 oracle 验证 | proof capsule 可重放证明 |
| Xalgorix | 防幻觉验证 | Verifier 独立闭环 + XML 畸形修复族 |
| Pentest-Swarm-AI | 信息素黑板 | Finding 半衰期衰减 + 模型信任经济 |
| Strix | 预算与证据闭环 | 生命周期强制终止工具 |
| PentesterFlow | 权限与记忆 | 三档权限 + scope deny 硬拒 + 四层记忆 |
| ArtiPhishell | CRS 数据流骨架 | pydatatask 三级存储 + Beta(1,1) 缓解评分 |
| atlantis | LLM↔fuzzer 闭环 | 生成物沙箱模板（RLIMIT+白名单 import） |
| pentest-copilot | 审批谱系 | 独立小模型审批官 + fail-closed |
| shannon | 无利用不报告 | Temporal durable 流水线 + 文件交付物通信 |
| BoxPwnr | 靶机自动化标杆 | progress.md 交接协议 + 双历史压缩 |
| Cyber-AutoAgent | 置信度驱动循环 | proof_pack 工件存在性校验 |
| AI-OPS | 干净内核 | Tool[In,Out] 泛型 + bashlex AST 白名单准入 |
| hackingBuddyGPT | 学术严谨 | task_solved 真值验证 |
| pentestagent | 图谱攻击路径 | ShadowGraph 最短路发现多跳链 |
| PentestGPT | 交互式鼻祖 | reasoning/generation/parsing 三模块 + PTT |
| VulnBot | shell 深度适配 | paramiko 密码提示自动配对 |
| seclab-taskflow | 声明式可审计 | YAML 五类文档 + --lint + MCP confirm 门 |
| nyuctf_agents | 成本一等公民 | 控制流工具化 + 观察滑窗 |
| Dark-Moon | 预检与隐私 | execution_guard 预检 + 可逆脱敏网关 |
| cochise | 最简分层 | 凭据嵌计划树（跨压缩存活） |
| AI-Infra-Guard | 异构子进程协议 | stdout-JSON 事件协议 + 70% 压缩保护安全发现 |
| CyberStrikeAI | 杀伤链子代理 | execution_id 异步工具 + 黑板 Fact 图 |
| Zen | 风险分级 | 四级风险闸门（误借鉴其拼装方式） |
| ctf-agent | 冠军方案 | TracingToolset 中间件 + 教练式协调者 |
| hexstrike-ai | 151 MCP 工具 | 三级自愈信封（安全上反面教材） |
| AutoPentest-DRL | DRL 路径决策 | CVSS×可利用性边奖励 |
| cai/crs-claude-code | CLI agent 即引擎 | claude -p headless + 文件系统握手 |
| BreachWeave | TCH 冠军 Manager/Solver/Observer | Observer 认识论规程（idea 判死前三连问 + 双重节流纠偏） |
| LuaN1aoAgent | P-E-O 三图 | 证据强制 provenance + scope CIDR 逐字校验防注入扩权 |
| Cairn | Fact/Intent 图协议（ARTEX 上游） | Dispatcher 唯一协议写入者 + Fact 只增不改（图即审计日志） |
| AboutSecurity | SKILL 知识库 | description 触发式三元组 + Vuln 卡双请求验证对 |
| tinyctfer | 极简冠军方案 | 单 execute_code 工具 + 有状态 Jupyter kernel（上下文污染归零） |
| T3MP3ST | TS 多 agent，XBEN 90.1% | Arsenal.execute 单点四道门（scope 数学含 CIDR）+ 复用本地 coding CLI 当骨干 |
| deadend-cli | ADaPT 置信度策略机 | 置信度分档递归分解 + 凭据占位符匿名化（LLM 永不见真值） |
| guardian-cli | 编排型 + 合规层 | execution_id 证据链贯通到 SARIF + 红蓝辩分诊按置信度触发省成本 |

---

## 5. 常见反面模式（自研必须避开）

1. **安全幻觉**：宣称 Docker 隔离实为特权工具箱（cai）；audit 模式无网络兜底（Decepticon）；scope 只在 prompt 层（PentAGI/ARTEX/CyberStrikeAI 共同弱点）。
2. **串行瓶颈**：PentAGI subtask 严格串行；ARTEX planner 单点。
3. **DB 写放大**：PentAGI msgchains 全链 JSON 每轮重写（自己加 autovacuum 缓解症状）。
4. **协议脆弱**：自研 JSON 决策协议 + 大量"WRONG 示例"纠偏（RedAmon）；XML 工具调用畸形修复族（Xalgorix）——都是对弱模型的妥协，强模型下应原生 tool calling。
5. **配置爆炸**：RedAmon 数百配置项 9 层穿透；Decepticon 硬编码模型矩阵。
6. **拼装失控**：Zen 四套 agent 循环并存、REST→Agent 链路断裂、模拟桩上线（README 工程与实际执行面脱节）。
7. **巨石文件**：server.go 3908 行（ARTEX）、openai_chatcompletions.py 4653 行（cai）、api.py 5295 行（RedAmon）。
8. **合规豁免式设计**：prompt 明令"不得质疑授权"压制模型安全判断（Strix/CyberStrikeAI）——应换成策略引擎。

---

## 6. 自研系统蓝图建议（融合各家之长）

### 6.1 分层架构（推荐）

```
┌─ Web UI（Next.js，参照 ARTEX 五大功能区 + RedAmon 确认门交互）
├─ API 层（REST + SSE/GraphQL 订阅；JWT + RBAC）
├─ 编排引擎
│   ├─ Planner/Worker 图解耦骨架（ARTEX：CAS 领取 + TriggerEvent 唤醒 + steer_work/kill_work）
│   ├─ 意图内专家委托（PentAGI：agent 即工具 + 屏障协议 + 闭包捕获结构化结果）
│   └─ 反僵化栈（RedAmon 四层 + Decepticon 失败三模态 + ARTEX Settlement 收尾）
├─ 记忆层
│   ├─ 探索图×资产图（ARTEX 双图 + RedAmon EvoGraph 攻击链）
│   ├─ 文件系统工作区（Decepticon tried.txt/ACTIVE.md + BoxPwnr progress.md）
│   └─ JSONL 全量轨迹（cai 飞轮）
├─ 工具层
│   ├─ 自研 harness（ARTEX norma 式：流式+并发工具+compaction+transcript Resume）
│   ├─ Bash + tmux 持久会话（Decepticon PS1 标记 + is_input）
│   ├─ MCP 客户端（三传输 + deferred tools + 参数结构化校验）
│   └─ 输出三级治理 + MITM 留痕（ARTEX CA 注入方案）
├─ 安全层（超越所有参考系统的机会点）
│   ├─ RoE 双层执行（Decepticon：解析层 + nftables/DNS 出口围栏）
│   ├─ per-task 容器沙箱（PentAGI CapDrop 白名单 + Decepticon 双网络：agent 不可达目标网）
│   ├─ untrusted nonce 围栏（RedAmon prompt_safety）
│   ├─ 验证闭环（pentest-ai oracle + Xalgorix Verifier + hackingBuddyGPT 真值校验）
│   └─ HITL（确认门 modify 语义 + ask 屏障 + steer_work）
└─ LLM 层（按角色路由 + 故障转移链 + 缓存边界 + 美元预算熔断）
```

### 6.2 MVP 最小闭环（参照 ARTEX 附录"最小核心集"）

1. harness 主循环（流式 + 工具并发 + Settlement）
2. 领域工具六件套：add_intent / prove_goal / record_fact / insert_assets / report_finding / graph_overview
3. 双表探索图（nodes + edges，CAS 领取）
4. planner/worker 双循环 + TriggerEvent
5. Bash + 输出截断落盘
6. 审批门（规则 + ask 超时默认拒绝）

之后再叠加：MITM 留痕 → 容器沙箱 → MCP → 技能库 → 验证 agent → 报告 agent → 复测闭环 → LLM 池 → 多任务编排。

### 6.3 差异化机会（当前生态空白）

1. **代码级 scope 强制**：把授权范围做成执行器层白名单 + 网络围栏（没有一家完整做到）。
2. **验证即一等公民**：所有 finding 必须过机器 oracle 才入报告（pentest-ai 思路 + Xalgorix 双人口）。
3. **可审计合规工件**：Decepticon 的 9 份规划文档 + HMAC 账本方向，补上中文等保/授权书模板。
4. **评测协议**：内建 XBOW/HTB/腾讯黑客松基准 + 10k+ trace 回放（BoxPwnr/ctf-agent/ArtiPhishell 经验）。
5. **成本经济学**：按角色模型路由 + 三重闸门 + token 四列记账（cai）+ LATS 死亡账本防重复消耗。

---

## 7. 附：调研覆盖清单与产出索引

- 01-international（37 项）：报告 02-08、10-12、14-21
- 02-chinese（11 项）：报告 07、08、21、22
- 03-artex-mirrors（6 项）：报告 01（核心）、09（forks）
- 04-competition（5 项）：报告 13、22
- 去重说明：xalgorix-official 与 xalgorix 完全相同（仅 .git 差异）；Hinln-ARTEX 与 mhtsec-ARTEX 仅 10 处差异；AiScan-N/mitan/xbow/Awesome-Landscape/Tsec 为纯文档或空仓库。
