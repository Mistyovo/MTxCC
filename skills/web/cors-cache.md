---
name: cors-cache
description: CORS 误配与缓存投毒探测
triggers: ["CORS", "缓存投毒", "web cache"]
mitre: ["T1190"]
---

# CORS / 缓存投毒

CORS：`Origin: https://evil.com` 反射对照 + `null` Origin + 子域信任链；任意 Origin 带凭据反射 = high。
缓存投毒：unkeyed header（X-Forwarded-Host / X-Original-URL）注入 → 看响应是否缓存到其他用户（需两次请求对照：注入响应 vs 干净响应的缓存头）。
