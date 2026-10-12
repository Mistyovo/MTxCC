---
name: cache-deception
description: Web Cache Deception（缓存欺骗）
triggers: ["缓存欺骗", "deception"]
mitre: ["T1190"]
---

# 缓存欺骗

请求 `/user/settings.css`（敏感路径+静态扩展名）→ 对照响应头（X-Cache/Age）与实际内容 → 若含个人信息被 CDN 缓存 = high。需要两次请求对照（冷/热）。
