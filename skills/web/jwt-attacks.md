---
name: jwt-attacks
description: JWT 攻击矩阵与 token 解剖
triggers: ["JWT", "token", "Bearer"]
---

# JWT 攻击矩阵

先解剖（header.payload.signature base64url 解码）再按矩阵逐行试：

| 向量 | 快速探测 |
|---|---|
| alg=none | 去签名段，alg 置 none，重放 |
| RS256→HS256 | 用公钥当 HMAC 密钥签名 |
| kid 注入 | kid=../../dev/null、SQL、RCE（部分实现） |
| jku/x5u 头 | 指向自控 URL 的 JWKS |
| claim inflation | 混淆 aud/iss，跨服务重放 |
| 弱密钥 | jwt_tool 撞 rockyou-top1000 |
| 过期不校验 | 过期 token 重放 |

每个探测前 traffic_search 是否已测；命中即 report_finding（BAC 类）。
