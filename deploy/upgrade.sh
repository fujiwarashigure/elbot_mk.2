#!/usr/bin/env bash
# ElBot 单机升级：先校验当前配置、做一致性数据快照和上一版镜像快照，
# 再用新镜像做配置兼容性检查，通过后重建服务。
#
# 用法：
#   bash upgrade.sh            # 使用 deploy/VERSION 作为新版本
#   ELBOT_VERSION=0.6.2 bash upgrade.sh
set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE="${ELBOT_SERVICE:-elbot}"
ROLLBACK_DIR="${DEPLOY_DIR}/rollback"
NEW_VERSION="${ELBOT_VERSION:-$(tr -d '[:space:]' <"${DEPLOY_DIR}/VERSION")}"
IMAGE_TAG="${ELBOT_IMAGE:-elbot:${NEW_VERSION}}"
ROLLBACK_TAG="elbot:rollback"
COMPOSE=()

log()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[!]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

detect_compose() {
    if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
        COMPOSE=(docker compose)
        return 0
    fi
    if command -v docker-compose >/dev/null 2>&1; then
        COMPOSE=(docker-compose)
        return 0
    fi
    return 1
}

detect_compose || die "未找到 Docker Compose"
mkdir -p "${ROLLBACK_DIR}"

container_id="$("${COMPOSE[@]}" -f "${DEPLOY_DIR}/docker-compose.yml" ps -q "${SERVICE}" 2>/dev/null || true)"
if [ -z "${container_id}" ]; then
    die "未发现运行中的 ${SERVICE} 容器；请先启动一次，或使用 docker compose up -d 后重试"
fi

current_image="$(docker inspect --format '{{.Config.Image}}' "${container_id}" 2>/dev/null || true)"
if [ -z "${current_image}" ]; then
    die "无法读取当前 ${SERVICE} 镜像标签"
fi

log "当前镜像：${current_image}"
log "新镜像：${IMAGE_TAG}"

log "升级前配置检查"
"${COMPOSE[@]}" -f "${DEPLOY_DIR}/docker-compose.yml" exec -T "${SERVICE}" elbot config check

log "升级前数据快照"
bash "${DEPLOY_DIR}/backup.sh"
backup_file="$(ls -1t "${DEPLOY_DIR}"/backups/elbot-data-*.tar.gz 2>/dev/null | head -n 1 || true)"
[ -n "${backup_file}" ] || die "未找到刚生成的备份文件"
bash "${DEPLOY_DIR}/restore-verify.sh" "${backup_file}"

log "保存上一版镜像快照"
docker tag "${current_image}" "${ROLLBACK_TAG}"
docker save "${ROLLBACK_TAG}" -o "${ROLLBACK_DIR}/previous-image.tar"

cat >"${ROLLBACK_DIR}/rollback.env" <<EOF
ROLLBACK_IMAGE=${ROLLBACK_TAG}
ROLLBACK_IMAGE_TAR=${ROLLBACK_DIR}/previous-image.tar
ROLLBACK_DATA_BACKUP=${backup_file}
ROLLBACK_FROM_VERSION=${current_image}
ROLLBACK_TO_VERSION=${NEW_VERSION}
EOF
log "回滚信息已写入 ${ROLLBACK_DIR}/rollback.env"

log "构建新镜像 ${IMAGE_TAG}"
ELBOT_VERSION="${NEW_VERSION}" ELBOT_IMAGE="${IMAGE_TAG}" \
    "${COMPOSE[@]}" -f "${DEPLOY_DIR}/docker-compose.yml" build "${SERVICE}"

log "用新镜像做配置兼容性检查"
docker run --rm \
    -v "${DEPLOY_DIR}/data:/data" \
    -e XDG_CONFIG_HOME=/data/config \
    -e XDG_DATA_HOME=/data \
    -e XDG_RUNTIME_DIR=/data/run \
    "${IMAGE_TAG}" elbot config check || die "新镜像配置检查失败，已取消升级"

log "重建 ${SERVICE}"
ELBOT_VERSION="${NEW_VERSION}" ELBOT_IMAGE="${IMAGE_TAG}" \
    "${COMPOSE[@]}" -f "${DEPLOY_DIR}/docker-compose.yml" up -d --force-recreate "${SERVICE}"

log "升级完成。回滚命令：bash ${DEPLOY_DIR}/rollback.sh"
