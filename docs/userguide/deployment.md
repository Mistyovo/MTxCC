# 部署指南

## Docker Compose（推荐）

```bash
./deploy/install.sh   # 一键：环境检测→密钥生成→compose 拉起→健康等待
```

## 环境变量

| 变量 | 说明 | 默认 |
|------|------|------|
| MTX_LISTEN_ADDR | 监听地址 | :8080 |
| MTX_DATABASE_URL | PG DSN | 必填 |
| MTX_MASTER_KEY | 32B base64 主密钥 | 必填 |
| MTX_DATA_DIR | 数据目录 | /data |
| MTX_SANDBOX_IMAGE | 沙箱镜像 | 空=本地执行 |

## 升级

1. `docker compose pull` 拉新镜像
2. `docker compose up -d` 重启（迁移自动执行、幂等）
3. `mtx version` 确认 schema 版本

## 备份

```bash
docker compose exec postgres pg_dump -U mtx mtx > backup.sql
```

## 无 Docker 环境

```bash
go build -o mtx ./cmd/mtx
export MTX_DATABASE_URL=postgres://...
export MTX_MASTER_KEY=$(head -c 32 /dev/urandom | base64)
./mtx
```
