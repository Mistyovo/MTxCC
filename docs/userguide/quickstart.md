# 快速开始

## 一键安装

```bash
curl -fsSL <安装脚本 URL> | bash
```

或从源码：

```bash
git clone <repo> && cd mtxcc && ./deploy/install.sh
```

## 三步开始

1. **初始化**：浏览器打开 `http://<IP>:8080` → `/setup` 设置 admin 密码
2. **配置 LLM**：左侧「LLM 配置」→ 添加模型（Anthropic/OpenAI/兼容网关/Ollama）→ 点「测试连接」
3. **创建任务**：「任务」页输入目标（如 `http://10.0.0.5`）→ 创建并开始

## 想先试试？

```bash
docker compose --profile demo up -d   # 附带 DVWA 靶机（127.0.0.1:4280）
```

## 角色权限

| 角色 | 任务 | 审批 | 配置 | 用户管理 |
|------|------|------|------|----------|
| admin | ✅ 增删控 | ✅ | ✅ | ✅ |
| operator | ✅ 增删控 | ❌ | ❌ | ❌ |
| auditor | 👁 只读 | ✅ | 👁 只读 | 👁 只读 |
| viewer | 👁 只读 | ❌ | ❌ | ❌ |
