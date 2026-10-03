#!/usr/bin/env bash
# ElBot 单机升级：先校验当前配置、做一致性数据快照和上一版镜像快照，
# 再用新镜像做配置兼容性检查，通过后重建服务。
#
# 用法：
#   bash upgrade.sh            # 使用 deploy/VERSION 作为新版本
#   ELBOT_VERSION=0.6.6 bash upgrade.sh
set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE="${ELBOT_SERVICE:-elbot}"
ROLLBACK_DIR="${DEPLOY_DIR}/rollback"
REQUESTED_VERSION="${ELBOT_VERSION:-}"
NEW_VERSION=""
IMAGE_TAG=""
ROLLBACK_TAG="elbot:rollback"
COMPOSE=()
PRECHECK_DIR=""

log()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[!]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

# 在 Windows Git Bash 中把宿主路径转成 Docker 能识别的 Windows 路径；Linux
# 下没有 cygpath，原样返回。容器内的 /data 目标不受影响。
host_path() {
    if command -v cygpath >/dev/null 2>&1; then
        cygpath -w "$1"
    else
        printf '%s' "$1"
    fi
}

cleanup() {
    if [ -n "${PRECHECK_DIR}" ] && [ -d "${PRECHECK_DIR}" ]; then
        rm -rf "${PRECHECK_DIR}" 2>/dev/null || true
    fi
}
trap cleanup EXIT

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
# 先切换源码，再解析未显式指定的目标版本；否则 ELBOT_GIT_REF 指向新标签但
# 用户没有设置 ELBOT_VERSION 时，NEW_VERSION 仍停留在旧源码版本而被守卫拒绝。
NEW_VERSION="${REQUESTED_VERSION:-${SOURCE_VERSION}}"
IMAGE_TAG="${ELBOT_IMAGE:-elbot:${NEW_VERSION}}"
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
current_image_id="$(docker inspect --format '{{.Image}}' "${container_id}" 2>/dev/null || true)"
if [ -z "${current_image_id}" ]; then
    die "无法读取当前 ${SERVICE} 镜像 ID"
fi
current_image_digest="$(docker image inspect --format '{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}' "${current_image_id}" 2>/dev/null || true)"

log "当前镜像：${current_image}（id=${current_image_id}）"
log "新镜像：${IMAGE_TAG}（源码版本 ${SOURCE_VERSION}）"

log "升级前配置检查"
"${COMPOSE[@]}" -f "${DEPLOY_DIR}/docker-compose.yml" exec -T "${SERVICE}" elbot config check

UPGRADE_BACKUP_MODE="${UPGRADE_BACKUP_MODE:-stop}"
log "升级前数据快照（BACKUP_MODE=${UPGRADE_BACKUP_MODE}）"
if ! BACKUP_MODE="${UPGRADE_BACKUP_MODE}" bash "${DEPLOY_DIR}/backup.sh"; then
    die "升级前数据快照失败"
fi
backup_file="$(ls -1t "${DEPLOY_DIR}"/backups/elbot-data-*.tar.gz 2>/dev/null | head -n 1 || true)"
[ -n "${backup_file}" ] || die "未找到刚生成的备份文件"
if ! RESTORE_VERIFY_IMAGE="${current_image_id}" RESTORE_VERIFY_START=required \
    bash "${DEPLOY_DIR}/restore-verify.sh" "${backup_file}"; then
    die "升级前数据快照无法被当前实际镜像 ${current_image_id} 恢复"
fi

log "保存上一版实际镜像快照（id=${current_image_id}）"
docker tag "${current_image_id}" "${ROLLBACK_TAG}"
docker save "${ROLLBACK_TAG}" -o "${ROLLBACK_DIR}/previous-image.tar"

cat >"${ROLLBACK_DIR}/rollback.env" <<EOF
ROLLBACK_IMAGE=${ROLLBACK_TAG}
ROLLBACK_IMAGE_TAR=${ROLLBACK_DIR}/previous-image.tar
ROLLBACK_IMAGE_ID=${current_image_id}
ROLLBACK_IMAGE_DIGEST=${current_image_digest}
ROLLBACK_DATA_BACKUP=${backup_file}
ROLLBACK_FROM_IMAGE=${current_image_id}
ROLLBACK_FROM_VERSION=${current_image}
ROLLBACK_TO_VERSION=${NEW_VERSION}
EOF
log "回滚信息已写入 ${ROLLBACK_DIR}/rollback.env"

log "构建新镜像 ${IMAGE_TAG}"
ELBOT_VERSION="${NEW_VERSION}" ELBOT_IMAGE="${IMAGE_TAG}" \
    "${COMPOSE[@]}" -f "${DEPLOY_DIR}/docker-compose.yml" build "${SERVICE}"

log "用新镜像做配置兼容性检查（隔离数据副本）"
# 镜像 ENTRYPOINT 已经是 `tini -- /usr/local/bin/elbot`，这里只能传 "config check"；
# 再传一次 "elbot" 会变成 `.../elbot elbot config check` 并阻断升级验收。
if [ "${UPGRADE_PRECHECK_ISOLATED:-1}" = "1" ]; then
    PRECHECK_DIR="$(mktemp -d "${DEPLOY_DIR}/.upgrade-check-XXXXXX")"
    mkdir -p "${PRECHECK_DIR}/data"
    cp -a "${DEPLOY_DIR}/data/." "${PRECHECK_DIR}/data/"
    PRECHECK_DATA="${PRECHECK_DIR}/data"
else
    warn "UPGRADE_PRECHECK_ISOLATED=0：新镜像配置检查直接挂载生产 data，可能修改生产数据"
    PRECHECK_DATA="${DEPLOY_DIR}/data"
fi
PRECHECK_UID="$(id -u 2>/dev/null || printf '0')"
PRECHECK_GID="$(id -g 2>/dev/null || printf '0')"
if ! MSYS_NO_PATHCONV=1 docker run --rm --user "${PRECHECK_UID}:${PRECHECK_GID}" \
    -v "$(host_path "${PRECHECK_DATA}"):/data" \
    -e XDG_CONFIG_HOME=/data/config \
    -e XDG_DATA_HOME=/data \
    -e XDG_RUNTIME_DIR=/data/run \
    "${IMAGE_TAG}" config check; then
    die "新镜像配置检查失败，已取消升级"
fi

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
