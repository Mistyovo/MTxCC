---
name: websocket-security
description: WebSocket 安全（握手鉴权/注入）
triggers: ["WebSocket", "ws://", "wss"]
mitre: ["T1190"]
---

# WebSocket

GATE：握手无鉴权对照（有 token vs 无）→ 消息注入（跨站 WS 劫持）→ 跨域 Origin 校验。消息内容注入点走 xss-contexts 纪律。
