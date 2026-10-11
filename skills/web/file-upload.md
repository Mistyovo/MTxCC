---
name: file-upload
description: 文件上传利用路径
triggers: ["上传", "upload", "头像"]
mitre: ["T1105"]
---

# 文件上传

1. 先测**任意文件读/路径穿越**（`../../`）与覆盖（同名冲突响应）。
2. 扩展名黑名单绕过对照矩阵：`.php5/.phtml/.PhP/空格/点尾/双写/::DATA`。
3. Content-Type 与魔数错配；解析漏洞（`x.php.jpg`）按服务器指纹选。
4. 拿到 webshell 位置后**只做只读证明**（id），路径与命令入 evidence。
