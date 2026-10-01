#!/usr/bin/env bash
# ElBot 单机回滚：读取 upgrade.sh 写入的 rollback.env，校验数据快照，
# 载入上一版镜像快照，恢复数据并重建服务。
#
# 用法：
#   bash rollback.sh
#   ROLLBACK_CONFIRM=1 bash rollback.sh   # 跳过交互确认
set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE="${ELBOT_SERVICE:-elbot}"
ROLLBACK_ENV="${DEPLOY_DIR}/rollback/rollback.env"
COMPOSE=()
STOPPED=0

log()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[!]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

cleanup() {
    local status=$?
    if [ "${STOPPED}" -eq 1 ]; then
        warn "回滚未完成，请检查现场后手动执行：cd ${DEPLOY_DIR} && ${COMPOSE[*]} up -d --force-recreate ${SERVICE}"
    fi
    return "${status}"
}
trap cleanup EXIT

[ -f "${ROLLBACK_ENV}" ] || die "未找到 ${ROLLBACK_ENV}；请先执行 upgrade.sh"
# shellcheck disable=SC1090
source "${ROLLBACK_ENV}"
: "${ROLLBACK_IMAGE:?missing ROLLBACK_IMAGE}"
: "${ROLLBACK_IMAGE_TAR:?missing ROLLBACK_IMAGE_TAR}"
: "${ROLLBACK_DATA_BACKUP:?missing ROLLBACK_DATA_BACKUP}"

if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    COMPOSE=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
    COMPOSE=(docker-compose)
else
    die "未找到 Docker Compose"
fi

[ -f "${ROLLBACK_IMAGE_TAR}" ] || die "镜像快照不存在：${ROLLBACK_IMAGE_TAR}"
[ -f "${ROLLBACK_DATA_BACKUP}" ] || die "数据快照不存在：${ROLLBACK_DATA_BACKUP}"
[ -d "${DEPLOY_DIR}/data" ] || die "数据目录不存在：${DEPLOY_DIR}/data"

if [ "${ROLLBACK_CONFIRM:-0}" != "1" ]; then
    printf '将回滚到 %s，并用 %s 覆盖当前 data。输入 yes 继续：' "${ROLLBACK_IMAGE}" "${ROLLBACK_DATA_BACKUP}"
    read -r answer
    [ "${answer}" = "yes" ] || die "已取消回滚"
fi

log "验证数据快照"
bash "${DEPLOY_DIR}/restore-verify.sh" "${ROLLBACK_DATA_BACKUP}"

log "停止 ${SERVICE}"
"${COMPOSE[@]}" -f "${DEPLOY_DIR}/docker-compose.yml" stop "${SERVICE}" >/dev/null
STOPPED=1

log "载入上一版镜像 ${ROLLBACK_IMAGE}"
docker load -i "${ROLLBACK_IMAGE_TAR}" >/dev/null

log "恢复数据快照"
stamp="$(date +%Y%m%d-%H%M%S)"
mv "${DEPLOY_DIR}/data" "${DEPLOY_DIR}/data.before-rollback-${stamp}"
mkdir -p "${DEPLOY_DIR}/data"
tar -xzf "${ROLLBACK_DATA_BACKUP}" -C "${DEPLOY_DIR}"
chown -R 10001:10001 "${DEPLOY_DIR}/data" 2>/dev/null || true
chmod 750 "${DEPLOY_DIR}/data" 2>/dev/null || true

log "使用上一版镜像重建 ${SERVICE}"
ELBOT_IMAGE="${ROLLBACK_IMAGE}" \
    "${COMPOSE[@]}" -f "${DEPLOY_DIR}/docker-compose.yml" up -d --force-recreate "${SERVICE}"
STOPPED=0
log "回滚完成：${ROLLBACK_IMAGE}，数据备份已恢复；旧 data 保留在 ${DEPLOY_DIR}/data.before-rollback-${stamp}"
