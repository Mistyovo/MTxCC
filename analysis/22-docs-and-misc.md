# 22. 文档类与杂项资料分析

> 本组无源码或极少源码：crs-claude-code（少量代码）、Tsec-Hackathon（比赛资料）、Awesome-Offensive-AI-Landscape（清单）、AiScan-N / mitan / xbow-competition（纯 README）。

---

## 22.1 crs-claude-code — 用 Claude Code 做自主漏洞修复的 CRS

**定位**：一个 Cyber Reasoning System（网络推理系统），把 Claude Code CLI 当作自主修复 agent，用于在开源项目中自动寻找并修补漏洞（DARPA AIxCC 风格的防守端/Fuzzing 修复链路）。提交于竞赛目录，属于 oss-crs 框架的一个 patcher 实现。

**架构**（README.md:9-46）：
```
patcher.py (编排器)
  1. crs.fetch(POV/BUG_CANDIDATE/DIFF/SEED) + crs.download(src)  # 一次性拉取启动证据
  2. claude -p --dangerously-skip-permissions --append-system-prompt <rules>
     └─ Claude Code 自主循环: 分析证据 → 编辑源码 → apply-patch-build → run-pov
        → apply-patch-test → 重试 → 把最终 .diff 写入 /patches/
  3. patcher 轮询 /patches/ 稳定后 crs.submit(DataType.PATCH, path) 提交第一个 patch
```

**关键实现**：
- `patcher.py:1-13` 模块说明：thin launcher，agent 通过 `CRS_AGENT` 环境变量可插拔（agents/ 目录下实现 `setup()`/`run()` 接口即可换 agent）。
- `patcher.py:52-70` 从 oss-crs 环境变量读取目标、harness、语言、sanitizer、LLM API 配置；`PATCH_POLL_INTERVAL_SECS=0.5`、`PATCH_STABLE_POLLS=3`。
- `patcher.py:73-79` `_reset_source()`：每轮开始 `git reset --hard HEAD` + `git clean -fd`，并清理 `.git/**/*.lock` 陈旧锁文件。
- `patcher.py:90-130` `_snapshot_patch_state()` / `_read_patch_signature()`：用 (mtime_ns, size) 双时间点采样判断 patch 文件"已写完不再变动"（防读到半截文件），这是与自主 agent 通过**文件系统握手**的典型模式。
- `agents/claude_code.py:21-27` `AGENT_TIMEOUT` 默认 0 = 无超时，跑到 token 预算耗尽为止。
- `agents/claude_code.py:37-59` prompt 组装：`claude_code.md` 主模板 + `sections/` 下 8 个按证据类型（pov_present / diff_present / seed_present / bug_candidates_present / workflow_pov / workflow_static / pre_submit）条件拼装的片段。
- `agents/claude_code.py:314-316` 核心调用：`claude -p --dangerously-skip-permissions --append-system-prompt <system_prompt>`，stdin 喂入启动证据路径。
- `oss-crs/`：base.Dockerfile / builder.Dockerfile（编译 sidecar）/ patcher.Dockerfile 三容器结构 + docker-bake.hcl + sample-litellm-config.yaml（用 LiteLLM 做模型网关）。

**可借鉴**：
1. **"CLI agent 即引擎"模式**：不自研 agent 循环，直接用 Claude Code headless（`-p`）+ CLAUDE.md 注入工作流，自己只做编排/提交——开发成本最低的 agent 化路径。
2. 文件系统握手协议（patch 稳定性采样）解决"自主进程如何通知编排器已完成"。
3. sections/ 式 prompt 条件拼装。
4. builder sidecar 隔离编译/测试执行。

---

## 22.2 Tsec-Hackathon — 腾讯云黑客松智能渗透挑战赛资料

**内容**：两届比赛资料。首届为 10 份决赛 PPT（BinX、D@WNEDG3、Pachinko、ai小分队、sickhack、xjtuHunter、yhy、长亭外、Antix、NeuroSploit 战队，见 `首届智能渗透黑客松/`）；第二届有 API 文档、MCP 接入文档、决赛答辩 PPT、零界论坛工具包。

**第二届 API 文档**（`第二届…API文档.md`）要点：
- Base URL `http://<SERVER_HOST>/api`，认证头 `Agent-Token`；统一响应 `{code, message, data}`；全局限频 **每队 3 QPS**（超限 429，文档建议 sleep(0.5)）。
- 接口：`GET /challenges`（关卡制，current_level / total / solved / flag_count / flag_got_count / 难度 / 分数）、启动/停止实例、提交 flag、看提示（提示扣该题总分 10%）。
- 每队最多同时运行 **3 个目标容器实例**。

**第二届 MCP 接入文档**（`第二届…MCP接入文档.md:16-24`）要点：
- MCP Server `http://<SERVER_HOST>/mcp`（streamable-http），5 个工具：`list_challenges` / `start_challenge` / `stop_challenge` / `submit_flag` / `view_hint`，Token 走 Authorization Header。
- 文档给出了 Claude Code、Cursor/Cline/Windsurf、LangChain、openai-agents、Python 原生 5 种接入示例（:53-513）——**比赛平台把"渗透目标管理"本身 MCP 化**，agent 只需自带渗透能力。

**对自研系统的启示**：这类比赛是标准化评测场。系统应把"平台对接层"（赛题获取/实例管理/flag 提交/限频退避）抽象成可插拔 adapter，核心 agent 引擎专注渗透本身；限频与实例并发管理要内建（0.5s 退避、3 实例轮换）。

---

## 22.3 Awesome-Offensive-AI-Landscape — 进攻性 AI 全景清单

502 行 README（中文），分类体系（`README.md` 的节标题）：
- 📋 开源 Agent 列表（:22）
- 🧬 进攻型/安全专用开源模型：A. 无对齐/弱审查进攻模型（:92）、B. 安全领域专用模型（:107）
- 🧩 进攻型 AI Skill 资源（:123）、🔌 进攻型 MCP Server 精选（:140）
- 🏆 赛事：DARPA AIxCC 2025（:167）、腾讯云智能渗透黑客松（:183）
- 📑 论文：A. 渗透/红队 Agent（:208）、B. 漏洞挖掘/利用/修复（:276）、C. 基准/训练/综述（:302）
- 🧪 Benchmark（:347）+ 选型小贴士（:372）
- 💼 商业方案：国外 Top20（:391）、国内厂商（:417）、场景选型对照（:434）

**用途**：作为我们调研的索引地图——它把我们 refer 里没收录的商业玩家（XBow、CAISI 等）和学术基线（HackTheBox/CTF 基准）都列了名，可作为后续补充调研的 checklist。

---

## 22.4 三个纯 README 项目

### AiScan-N（`02-chinese/AiScan-N/README.md`）
SecNN 出品的闭源 AI 安全 CLI（v3.0.3，仅发布二进制）。宣称能力：多会话工作台（消息编辑/重生成/分支历史）、文件上传分析（PCAP/APK/IPA 等）、漏洞扫描、代码审计、应急响应、AI 大模型工具集。面向零基础用户。**无源码可析**，仅作商业/产品形态参考：证明"AI 安全 CLI 工作台"有真实需求市场。

### 密探 mitan（`02-chinese/mitan/Readme.md`）
闭源 Rust 工具（v2.0.6，v1.0 为 JavaFx，19 次迭代）。定位"项目级一站式网络安全检测与运维工具"，50 余项功能：主体查询/空间测绘（FOFA、Hunter、Quake、ZoomEye、Shodan…）/Fuzz 爆破（指纹、目录、子域、弱口令、POC）/云安全/代理池/**AI 渗透（MCP、技能、AI 助手、自动化渗透、代码审计）**（`Readme.md:24-46`）。README 有完整免责声明（仅限授权测试）。**无源码**。参考点：传统国产安全工具正在把 MCP+AI 能力作为新模块叠加进既有工具矩阵，而非从零造 agent 平台。

### xbow-competition（`02-chinese/xbow-competition/README.md`）
m-sec-org 的腾讯黑客松参赛系统（初赛 7/238，决赛优秀奖）。README 描述了两个组件：`ez-xbow-platform-mcp`（MCP 服务器：挑战管理、**内建 9 类漏洞 CTF 知识库**（XSS/SQL/SSTI/SSRF/IDOR/XXE/LFI/CodeI/AFR&PT）、持久化 Kali 容器、自动笔记）+ `kimi-cli-for-xbow`（基于 Kimi CLI 定制的自动解题客户端，支持 DeepSeek/通义等，Daemon 无人值守）。架构：Kimi CLI Agent ←MCP→ XBow MCP Server →（平台/Kali 容器/知识库）。**本地两个子目录为空（克隆失败）**，仅 README 可析。参考点：知识库内置到 MCP 层 + daemon 模式无人值守解题。
