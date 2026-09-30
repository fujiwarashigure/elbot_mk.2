#!/usr/bin/env bash
# 阿里云轻量应用服务器（Lighthouse）/ 腾讯云轻量 一键部署 ElBot。
#
# 用法：
#   在解压后的 offline-<arch>/ 目录内执行：
#     bash deploy.sh                 # 推荐：用预编译二进制 + Debian 运行时构建（功能完整）
#     bash deploy.sh --load          # 直接 docker load 现成镜像 tar（完全离线，但无 shell）
#     bash deploy.sh --load /path/to/elbot-0.5.0-linux-amd64.tar.gz
#
# 前置：
#   - 已安装 Docker 和 docker compose
#   - 已准备好 .env（脚本会自动从 .env.example 生成并提示填写）
set -euo pipefail

log()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[!]\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

MODE="build"
IMAGE_TAR=""
while [ $# -gt 0 ]; do
    case "$1" in
        --load)
            MODE="load"
            if [ $# -ge 2 ] && [ -f "$2" ]; then IMAGE_TAR="$2"; shift; fi
            ;;
        -h|--help)
            sed -n '2,16p' "$0"; exit 0
            ;;
        *)
            die "未知参数：$1（可用 --load [image.tar.gz]）"
            ;;
    esac
    shift
done

BUNDLE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${BUNDLE_DIR}"

# 1. 架构检测
case "$(uname -m)" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) die "不支持的架构：$(uname -m)" ;;
esac
log "服务器架构：${ARCH}"

# 2. Docker 检查
command -v docker >/dev/null 2>&1 || die "未找到 docker，请先安装（宝塔软件商店 -> Docker管理器）"
if docker compose version >/dev/null 2>&1; then
    COMPOSE=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
    COMPOSE=(docker-compose)
else
    die "未找到 docker compose / docker-compose"
fi
docker info >/dev/null 2>&1 || die "Docker 守护进程未运行：systemctl start docker"
log "Docker 可用：${COMPOSE[*]}"

# 3. 环境变量
if [ ! -f .env ]; then
    cp .env.example .env
    chmod 600 .env
    warn "已生成 ${BUNDLE_DIR}/.env"
    warn "请填写 DEEPSEEK_API_KEY / ELBOT_CLI_LOCAL_TOKEN / ELNIS_HOME_TOKEN 后重新执行本脚本。"
    exit 1
fi

# 4. 数据目录属主（bind mount 会使用宿主机属主，容器内是 10001）
mkdir -p data
if command -v chown >/dev/null 2>&1; then
    chown -R 10001:10001 data 2>/dev/null || warn "chown data 失败，如容器反复重启请手动执行 chown -R 10001:10001 data"
fi
chmod 750 data 2>/dev/null || true

# 5. 获得镜像
if [ "${MODE}" = "load" ]; then
    if [ -z "${IMAGE_TAR}" ]; then
        # 自动在上级目录找对应架构的镜像 tar
        for cand in "../elbot-0.5.0-linux-${ARCH}.tar.gz" "./elbot-0.5.0-linux-${ARCH}.tar.gz"; do
            if [ -f "${cand}" ]; then IMAGE_TAR="${cand}"; break; fi
        done
    fi
    [ -n "${IMAGE_TAR}" ] && [ -f "${IMAGE_TAR}" ] || die "未找到镜像 tar，请用 --load /path/to/elbot-0.5.0-linux-${ARCH}.tar.gz"
    log "docker load -i ${IMAGE_TAR}（scratch 精简镜像，无 shell 工具）"
    docker load -i "${IMAGE_TAR}"
else
    log "docker compose build（预编译二进制 + Debian 运行时，功能完整）"
    "${COMPOSE[@]}" build
fi

# 6. 启动
log "docker compose up -d"
"${COMPOSE[@]}" up -d --remove-orphans

# 7. 结果
sleep 2
"${COMPOSE[@]}" ps || true
echo
log "首次启动会在 ./data/config/elbot/ 生成默认配置；改 TOML 后 restart，改 .env 后必须 up -d --force-recreate："
log "  cd ${BUNDLE_DIR} && ${COMPOSE[*]} restart      # 只改 TOML"
log "  cd ${BUNDLE_DIR} && ${COMPOSE[*]} up -d --force-recreate  # 改 .env"
log "查看日志：cd ${BUNDLE_DIR} && ${COMPOSE[*]} logs -f --tail=200"
log "健康检查：curl -sS http://127.0.0.1:32171/live  # 另有 /ready /healthz"
log "上线前请实际发消息、重启后确认会话仍在，并做一次备份恢复演练。"
