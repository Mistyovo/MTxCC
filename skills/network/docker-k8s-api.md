---
name: docker-k8s-api
description: Docker/K8s API 暴露测试
triggers: ["Docker", "Kubernetes", "K8s", "2375", "6443"]
mitre: ["T1610"]
---

# Docker / K8s API

1. 发现：2375/2376（docker）、6443/10250（k8s）、/var/run/docker.sock 访问。
2. **GATE A**：API 无认证确认（version/info 只读）→ 容器列表 → 创建能力探测（dry-run 级别，不实建）。
3. 提权含义（docker 组=root、挂载宿主）写进 finding summary——**本次实测已验证此类发现的真实价值**。
4. 默认只读枚举；创建/挂载动作需 RoE 显式允许。
