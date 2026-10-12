---
name: k8s-dashboard
description: K8s Dashboard/API 安全
triggers: ["K8s", "Dashboard", "kubelet"]
mitre: ["T1610"]
---

# K8s 面

GATE：10250 kubelet 未授权（/runningpods）→ 6443 匿名 → Dashboard 未授权 → etcd 暴露。与 docker-k8s-api 配合，创建动作需 RoE。
