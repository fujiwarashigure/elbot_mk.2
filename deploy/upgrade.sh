#!/usr/bin/env bash
# ElBot 单机升级：先校验当前配置、做一致性数据快照和上一版镜像快照，
# 再用新镜像做配置兼容性检查，通过后重建服务。
#
# 用法：
#   bash upgrade.sh            # 使用 deploy/VERSION 作为新版本
#   ELBOT_VERSION=0.6.3 bash upgrade.sh
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

# 需要时先切换到目标源码标签，再校验源码版本，避免“只改版本号、不换代码”。
if [ -n "${ELBOT_GIT_REF:-}" ]; then
    command -v git >/dev/null 2>&1 || die "设置了 ELBOT_GIT_REF，但未找到 git"
    # 正常仓库结构是 仓库/.git + 仓库/deploy/upgrade.sh，因此不能用 deploy/.git 判断；
    # rev-parse 同时兼容 .git 目录、worktree 和子模块等布局。
    REPO_ROOT="$(git -C "${DEPLOY_DIR}" rev-parse --show-toplevel 2>/dev/null || true)"
    [ -n "${REPO_ROOT}" ] || die "设置了 ELBOT_GIT_REF，但 ${DEPLOY_DIR} 不在 git 工作区内"
    log "切换源码到 ${ELBOT_GIT_REF}"
    git -C "${REPO_ROOT}" fetch --tags --force || die "git fetch 失败"
    git -C "${REPO_ROOT}" checkout --detach "${ELBOT_GIT_REF}" || die "git checkout ${ELBOT_GIT_REF} 失败"
fi

SOURCE_VERSION="$(tr -d '[:space:]' <"${DEPLOY_DIR}/VERSION")"
if [ "${NEW_VERSION}" != "${SOURCE_VERSION}" ] && [ "${ELBOT_ALLOW_VERSION_MISMATCH:-0}" != "1" ]; then
    die "目标版本 ${NEW_VERSION} 与当前源码 deploy/VERSION=${SOURCE_VERSION} 不一致：本脚本只构建当前工作区的代码，不会自动切换。请先 git fetch --tags && git checkout v${NEW_VERSION}（或设置 ELBOT_GIT_REF=v${NEW_VERSION}）；确实只想改版本号时设置 ELBOT_ALLOW_VERSION_MISMATCH=1。"
fi

container_id="$("${COMPOSE[@]}" -f "${DEPLOY_DIR}/docker-compose.yml" ps -q "${SERVICE}" 2>/dev/null || true)"
if [ -z "${container_id}" ]; then
    die "未发现运行中的 ${SERVICE} 容器；请先启动一次，或使用 docker compose up -d 后重试"
fi

current_image="$(docker inspect --format '{{.Config.Image}}' "${container_id}" 2>/dev/null || true)"
if [ -z "${current_image}" ]; then
    die "无法读取当前 ${SERVICE} 镜像标签"
fi

log "当前镜像：${current_image}"
log "新镜像：${IMAGE_TAG}（源码版本 ${SOURCE_VERSION}）"

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
# 镜像 ENTRYPOINT 已经是 `tini -- /usr/local/bin/elbot`，这里只能传 "config check"；
# 再传一次 "elbot" 会变成 `.../elbot elbot config check` 并阻断升级验收。
docker run --rm \
    -v "${DEPLOY_DIR}/data:/data" \
    -e XDG_CONFIG_HOME=/data/config \
    -e XDG_DATA_HOME=/data \
    -e XDG_RUNTIME_DIR=/data/run \
    "${IMAGE_TAG}" config check || die "新镜像配置检查失败，已取消升级"

log "重建 ${SERVICE}"
ELBOT_VERSION="${NEW_VERSION}" ELBOT_IMAGE="${IMAGE_TAG}" \
    "${COMPOSE[@]}" -f "${DEPLOY_DIR}/docker-compose.yml" up -d --force-recreate "${SERVICE}"

rollback_hint() {
    warn "如需回滚：bash ${DEPLOY_DIR}/rollback.sh"
}

service_health() {
    local cid
    cid="$("${COMPOSE[@]}" -f "${DEPLOY_DIR}/docker-compose.yml" ps -q "${SERVICE}" 2>/dev/null || true)"
    [ -n "${cid}" ] || return 1
    docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "${cid}" 2>/dev/null
}

log "等待 ${SERVICE} 通过健康检查"
status=""
healthy=0
for _ in $(seq 1 40); do
    status="$(service_health || true)"
    if [ "${status}" = "healthy" ]; then
        healthy=1
        break
    fi
    if [ "${status}" = "unhealthy" ] || [ "${status}" = "exited" ]; then
        break
    fi
    sleep 3
done
if [ "${healthy}" -ne 1 ]; then
    warn "升级后 ${SERVICE} 未通过健康检查（最后状态：${status:-unknown}）"
    rollback_hint
    exit 1
fi
log "${SERVICE} 健康检查通过"

if [ "${ELBOT_UPGRADE_SKIP_DOCTOR:-0}" != "1" ]; then
    doctor_args=(--no-model)
    if [ "${ELBOT_UPGRADE_E2E:-0}" = "1" ]; then
        doctor_args=(--e2e)
    fi
    log "运行部署验收：elbot doctor ${doctor_args[*]}"
    if ! "${COMPOSE[@]}" -f "${DEPLOY_DIR}/docker-compose.yml" exec -T "${SERVICE}" elbot doctor "${doctor_args[@]}"; then
        warn "升级后验收失败"
        rollback_hint
        exit 1
    fi
else
    warn "ELBOT_UPGRADE_SKIP_DOCTOR=1，跳过升级后 doctor 验收"
fi

log "升级完成并通过验收。回滚命令：bash ${DEPLOY_DIR}/rollback.sh"
