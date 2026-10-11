#!/usr/bin/env bash
# MTxCC 一键安装（spec §Task 25）：环境检测 → 密钥生成 → compose 拉起 → 健康等待。
set -euo pipefail

MTX_DIR="${MTX_DIR:-$PWD}"
COMPOSE_FILE="$(cd "$(dirname "$0")" && pwd)/docker-compose.yml"
DRYRUN="${MTX_INSTALL_DRYRUN:-0}"
cd "$MTX_DIR"

log() { printf '\033[36m[mtx]\033[0m %s\n' "$*"; }
die() { printf '\033[31m[mtx] 错误：%s\033[0m\n' "$*" >&2; exit 1; }

# 1) 环境检测。
command -v docker >/dev/null || die "未找到 docker，请先安装 Docker"
if ! docker compose version >/dev/null 2>&1; then
  die "docker compose 不可用（需要 compose v2）"
fi

# 2) nftables 能力探测（失败降级为仅解析层，写入 .env 提示）。
EGRESS_MODE="enforce"
if ! docker run --rm --cap-add NET_ADMIN alpine:3 sh -c 'apk add -q iptables-ng nftables 2>/dev/null || apk add -q nftables; nft list ruleset' >/dev/null 2>&1; then
  EGRESS_MODE="parse-only"
  log "⚠ 内核 nftables 不可用：出口围栏降级为仅解析层（RoE 解析层仍强制）"
fi

# 3) .env 生成（幂等：已有值不覆盖）。
if [ ! -f .env ]; then
  log "生成 .env（主密钥与数据库密码）"
  MASTER_KEY=$(head -c 32 /dev/urandom | base64 -w0)
  PG_PASSWORD=$(head -c 24 /dev/urandom | base64 -w0 | tr -d '/+=')
  cat > .env <<EOF
MTX_MASTER_KEY=$MASTER_KEY
MTX_PG_PASSWORD=$PG_PASSWORD
MTX_PORT=8080
MTX_EGRESS=$EGRESS_MODE
EOF
  chmod 600 .env
else
  log ".env 已存在，跳过生成（幂等）"
fi

# 4) DRYRUN 止步（测试模式）。
if [ "$DRYRUN" = "1" ]; then
  log "DRYRUN：已生成 .env，不拉起服务"
  exit 0
fi

# 5) 拉起。
log "拉起服务（首次会构建镜像，需要几分钟）…"
docker compose -f "$COMPOSE_FILE" --env-file .env up -d

# 6) 等待健康。
log "等待就绪…"
for i in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:$(grep MTX_PORT .env | cut -d= -f2)/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 2
done

PORT=$(grep MTX_PORT .env | cut -d= -f2)
if curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1; then
  printf '\033[32m✅ MTxCC 已就绪：%s\033[0m\n' "http://$(hostname -I 2>/dev/null | awk '{print $1}' || echo 127.0.0.1):$PORT"
  log "首次使用：打开页面 → /setup 设置 admin 密码 → 「LLM 配置」添加模型 → 创建任务"
else
  die "服务未在 120s 内就绪，请查看：docker compose -f $COMPOSE_FILE logs mtx"
fi
