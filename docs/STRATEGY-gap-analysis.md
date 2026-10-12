# MTxCC 战略分析：差距 · 价值 · 开发计划

> 基准：54 个参考系统的 22 份深度调研报告 + MTxCC v0.6.0-hardened 实测数据（DVWA 双漏洞打穿 15 findings、61 自主意图、41 次 RoE 拦截、319K tokens 记账）
> 日期：2026-10-12

---

## 第一部分：差距全维度解剖

### 1.1 八维度差距矩阵

| 维度 | 行业最强 | MTxCC 现状 | 差距评级 | 关键缺口 |
|------|---------|-----------|---------|---------|
| LLM 集成 | ARTEX（任务级模型链+熔断持久化+llmrec 归因） | 三协议+Pool 熔断（内存态）+记账 | 🟡 中 | 熔断不持久、无任务级模型链（侦察便宜/利用强）、无调用回放 |
| 上下文工程 | ARTEX（图谱级 digest 压缩）/RedAmon（缓存边界）/PentAGI（chainAST 契约） | 三层 compaction+Settlement+空轮恢复 | 🟡 中 | 无图谱冷压缩、无静态/动态缓存切分、无摘要感知契约 |
| 编排 | RedAmon（LATS+Deep Think+fireteam）/PentAGI（15 角色+refiner） | planner/worker CAS+steer/kill+Jaccard+三模态简化 | 🟡 中 | 无 refiner 增量重规划、无 fireteam 并行、无元认知三层栈、失败三模态未细分 |
| 工具层 | Decepticon（tmux 深交互+msf）/ARTEX（MCP 三传输+工具热更） | bash+tmux+MCP stdio+专家委托+文本协议 | 🟢 较小 | msfconsole 未实战驱动、MCP 缺 HTTP/SSE、工具 schema UI 热更 |
| **安全** | Decepticon（RoE 双层+HMAC 账本+证据门+安全件闸门） | **RoE 双层+端口级+沙箱默认+四层审批+vault+untrusted 围栏** | 🟢 **领先** | HMAC 链式账本、证据撤销、Abort/Cleanup 文档、防篡改闸门 |
| **验证质量** | shannon（无利用不报告）/Decepticon（双方法） | **oracle 机器验证+去重+复测闭环+证据门** | 🟢 **领先** | epistemics 置信度演进、proof capsule 可重放证明 |
| 产品面 | ARTEX（243 API+模板/归档/通知/覆盖度/自更新） | RBAC+审计+报告+对话+任务管理 | 🔴 大 | 通知推送、任务模板、归档、覆盖度管理、自更新、OTel 可观测 |
| **评测** | Decepticon 102/104、RedAmon 101/104、cai HTB 第一 | DVWA 自建靶场 15 findings | 🔴 **最大** | XBOW/HTB/黑客松公开成绩为零 |

### 1.2 各家独有强项详表（可借鉴清单）

**ARTEX（对标基准）**
- 任务级 LLM 链（侦察便宜模型→利用强模型）
- 探索图冷节点 digest 压缩（图谱级记忆管理）
- 243 条 API 的产品完整度（模板/归档/IM 通知/复测 UI/资产同步/自更新）
- mainagent @提及操舵（我们已补 chat API，缺 UI 集成）

**Decepticon（安全+技能标杆）**
- 314 个 SKILL.md + Skillogy 技能图（BUILDS_ON/CHAINS_TO 关系检索）
- 安全关键件防篡改闸门（插件不能削弱基线）
- HMAC 链式审计账本 + 9 份军事化规划文档（含 Abort/Cleanup）
- OPPLAN 版本化 DAG + PlanFact 证据门（事实撤销→传递性 blocked）
- finding epistemics（置信度演进追踪）

**RedAmon（反僵化+全链路标杆）**
- 三层元认知栈（productivity 审计 / Deep Think 竞争假设 / LATS 树搜索）
- 24 类 response_class 分类法（受约束分类供价值函数）
- CypherFix 修复闭环（发现→分诊→自动开 PR）
- Neo4j EvoGraph 攻击链图谱（跨会话复用）

**PentAGI（角色编排标杆）**
- generator/refiner 动态重规划（增量 patch）
- 四层防打转监督栈（重复检测/导师注入/反射/优雅收尾）
- chainAST 字节预算摘要 + 摘要感知双向契约
- pgvector 长期记忆 + 匿名化入库

**cai（成本+数据飞轮标杆）**
- 三重成本闸门（预估/记账/锁死）+ 成本下钻
- JSONL 全量轨迹（18TB 训练语料）
- 30+ 角色 agent（blue_teamer/dfir/retester 等）

**shannon（验证纪律标杆）**
- "无利用不报告"门禁
- Temporal durable 流水线（崩溃恢复）
- 文件交付物通信（agent 间解耦）

---

## 第二部分：价值分析

### 2.1 差距的价值权重（补上后回报）

| 差距 | 补上成本 | 补上价值 | 价值/成本 | 优先级 |
|------|---------|---------|----------|--------|
| XBOW 公开成绩 | 低（token+跑分） | **极高**（称号唯一实证、市场入场券） | ⭐⭐⭐⭐⭐ | P0 |
| 产品面（通知/模板/归档/覆盖度） | 中 | 高（购买决策、留存） | ⭐⭐⭐⭐ | P1 |
| 技能库 40→150 | 中（内容沉淀） | 高（护城河、能力天花板） | ⭐⭐⭐⭐ | P1 |
| 元认知三层栈 | 高（复杂） | 中高（长任务成功率） | ⭐⭐⭐ | P2 |
| 修复闭环（CodeFix） | 高 | 中高（产品天花板） | ⭐⭐⭐ | P2 |
| 图谱压缩/缓存边界 | 中 | 中（成本效率） | ⭐⭐⭐ | P2 |
| HMAC 账本/证据撤销 | 低 | 中（合规深度） | ⭐⭐⭐ | P2 |
| refiner/fireteam | 高 | 中（编排深度） | ⭐⭐ | P3 |

### 2.2 三大价值支柱（我们的差异化）

**支柱一：安全可信度（行业唯一）**
- 全行业 scope 约束停在 prompt 层（调研 54 系统确认）——MTxCC 是唯一实现"解析层+网络围栏+端口级"双层强制的系统
- 实测证据：41 次 RoE 拦截、RCE EVAL 被拦后模型自动换向量
- **价值**：审计师认可 = 政企采购的决定性因素（等保/合规场景唯一可交付）
- 变现路径：私有化部署（中国政企安全团队）+ 授权测试服务商

**支柱二：验证闭环（行业首创自动化）**
- oracle 机器验证（replay/differential）比 Decepticon"双方法"更自动、比 shannon 门禁更可操作
- 实测证据：candidate→verified 自动升级、复测闭环、去重合并
- **价值**：误报率是渗透工具的生命线——"零误报"是市场最强卖点
- 变现路径：报告可信度溢价、MSSP 规模化交付

**支柱三：实战验证能力**
- 实测 DVWA 双漏洞打穿 + 意外面发现（docker 提权/proxy file://）——不是玩具
- 61 意图自主规划、智能换向量——真实智能而非脚本
- **价值**：能力实证 = 品牌信任

### 2.3 竞争定位建议

| 对标 | 定位 | MTxCC 差异化 |
|------|------|-------------|
| Decepticon | 开源安全标杆 | 商业产品化 + 私有化交付 + 中文生态 |
| RedAmon | 全链路平台 | 安全可信度 + 验证闭环（他们 scope 仍 prompt 层） |
| ARTEX | 产品基准 | 超越安全面（沙箱/RoE/验证）+ 活着的项目（ARTEX 已停更） |
| 商业厂商（XBow 等） | SaaS | 私有化 + 合规 + 成本可控 |

**一句话定位**：「唯一通过审计的 AI 渗透系统」——安全可信度做尖、验证闭环做深、产品体验做齐。

---

## 第三部分：下一步开发计划

### v0.7 评测期（称号之战）—— 2 周

| 任务 | 对标 | 验收标准 |
|------|------|---------|
| XBOW adapter 实跑（需 token） | Decepticon 102/104 | 成绩单 + 对比表 + trace 回放 |
| 腾讯黑客松 adapter 联调 | 比赛实战 | API/MCP 双通道打通 |
| 成本-成绩曲线报告 | cai 成本闸门 | 同分最低成本论证 |
| 真实网关 contract 测试（防回归） | 工程健康 | max_tokens/stream_options 类 bug 不再逃逸 |

**价值**：称号实证、市场入场券、媒体报道点。

### v0.8 知识期（护城河）—— 4 周

| 任务 | 对标 | 验收标准 |
|------|------|---------|
| 技能库 40→150（含 GATE 硬门） | Decepticon 314 | catalog 150 + 每技能 GATE 清单 |
| 技能关系图（BUILDS_ON/CHAINS_TO） | Skillogy | 关系检索 API |
| response_class 分类法（24 类） | RedAmon | worker 输出分类 + 统计 |
| 知识图谱升级（EvoGraph 攻击链） | RedAmon Neo4j | 跨任务攻击链复用 |

**价值**：知识资产沉淀（不可复制）、长任务成功率提升。

### v0.9 元认知期（成功率）—— 4 周

| 任务 | 对标 | 验收标准 |
|------|------|---------|
| 失败三模态精细化（INFRA/CRASH/WANDERING） | Decepticon | 差异化重试策略 |
| refiner 增量重规划 | PentAGI | subtask patch 重规划 |
| Deep Think 竞争假设 + Jaccard 拒换皮（已有一层） | RedAmon | 分歧探针强制 |
| 图谱冷压缩（digest） | ARTEX | 长任务上下文恒定 |

**价值**：长任务成功率（当前长任务靠 Settlement 收尾，成功率有天花板）。

### v1.0 产品期（商业交付）—— 6 周

| 任务 | 对标 | 验收标准 |
|------|------|---------|
| 通知推送（钉钉/飞书/企微/TG） | ARTEX | 事件订阅+模板 |
| 任务模板（预置 RoE+技能组合） | ARTEX | 一键建任务 |
| 覆盖度管理（scope 分母 vs 已测） | ARTEX list_untested | 未测资产清单 |
| 任务归档/导出（PDF 报告） | PentAGI | 合规报告 |
| 自动更新 + 备份方案 | ARTEX | 生产运维 |
| OTel 可观测 | PentAGI | Grafana 面板 |

**价值**：购买决策闭环、MSSP 规模化交付能力。

### v1.1 生态期（天花板）—— 持续

| 任务 | 对标 | 验收标准 |
|------|------|---------|
| CodeFix 修复建议生成（发现→PR 雏形） | RedAmon CypherFix | 修复 patch 建议 |
| MCP HTTP/SSE + 社区 MCP 生态 | Decepticon | 30+ 社区工具 |
| 插件安全件闸门（防篡改） | Decepticon | 插件不能削弱基线 |
| HMAC 链式账本 + 证据撤销 | Decepticon | 可信审计链 |
| 训练数据飞轮（JSONL 轨迹→微调） | cai 18TB | 轨迹导出+微调管线 |

---

## 附录：MTxCC 相对参考系统的领先面（不可忽视）

1. **端口级 RoE**（host:port scope）——54 系统无一实现
2. **oracle 机器验证自动化**——比"双方法验证"更可规模化
3. **凭据 vault 占位符双向网关**——ARTEX/Decepticon 均无
4. **文本工具协议 fallback**——弱模型可用性（T3MP3ST 有思想，我们有完整实现）
5. **空轮恢复机制**——思考型模型适配（GLM/mimo 实测修复）
6. **私有化一键部署**（curl|bash ≤15 分钟）——商业交付门槛
7. **实测验证的自主智能**——换向量/操舵/换打法是真实行为非脚本
