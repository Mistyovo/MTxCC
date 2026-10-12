---
name: ssti-templates
description: 服务端模板注入识别与确认
triggers: ["SSTI", "模板注入", "Freemarker", "Jinja", "Twig"]
mitre: ["T1190"]
---

# SSTI 探测

1. 识别：输入回显在渲染后页面（URL/邮件/昵称）。
2. **GATE A**：数学探针对照（`{{7*7}}`/`${7*7}`/`<%= 7*7 %>` 三族都要试，引擎未知）→ 命中计算结果才算 candidate；进一步读环境（`{{config}}`）才算 runtime_observed。
3. 引擎指纹后按引擎 payload 表（Jinja2/Freemarker/Velocity/Smarty/ERB）。
4. 利用只读证明（读环境变量/文件），禁执行破坏。
