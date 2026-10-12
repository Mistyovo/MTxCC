---
name: lxd-container
description: 容器逃逸与 LXD/LXC 提权
triggers: ["容器逃逸", "LXD", "docker.sock"]
mitre: ["T1611"]
---

# 容器逃逸

GATE：docker.sock 挂载检测 → privileged 判定 → capability 清单（cap_sys_admin 等）→ 卷挂载宿主路径。逃逸路径写 finding（**实测中已验证 docker 组发现的真实性**）。
