---
name: jwt-alg-confusion
description: JWT 算法混淆与密钥注入（GATE 全矩阵）
triggers: ["JWT", "alg", "jku", "token 注入"]
mitre: ["T1550"]
---

# JWT 算法混淆（GATE：8 形态全走）

alg=none / RS256→HS256（公钥当密钥）/ kid 注入（SQL、路径、空）/ jku+x5u 指向自控 / 密钥爆破（弱密钥 top100）/ claim 膨胀（sub 越权）/ CRLF 注入 / 未校验过期。每形态独立 record_fact，命中 report_finding。
