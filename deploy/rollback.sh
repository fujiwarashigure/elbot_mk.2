#!/usr/bin/env bash
# ElBot 单机回滚：读取 upgrade.sh 写入的 rollback.env，校验数据快照，
# 载入上一版镜像快照，恢复数据并重建服务，等待 /ready 后运行 doctor 验收。
#
# 用法：
#   bash rollback.sh
#   ROLLBACK_CONFIRM=1 bash rollback.sh   # 跳过交互确认
set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE="${ELBOT_SERVICE:-elbot}"
ROLLBACK_ENV="${DEPLOY_DIR}/rollback/rollback.env"
COMPOSE_FILE="${ELBOT_COMPOSE_FILE:-${DEPLOY_DIR}/docker-compose.yml}"
COMPOSE=()
STOPPED=0
DATA_BEFORE=""
DATA_FAILED=""
ROLLBACK_IMAGE=""
ROLLBACK_IMAGE_TAR=""
ROLLBACK_DATA_BACKUP=""
ROLLBACK_FROM_IMAGE=""

log()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[!]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

read_env_value() {
    local file="$1" key="$2" line value
    while IFS= read -r line || [ -n "${line}" ]; do
        line="${line%$'\r'}"
        case "${line}" in
            ''|'#'*) continue ;;
        esac
        case "${line}" in
            "${key}="*) value="${line#*=}" ;;
            *) continue ;;
        esac
        case "${value}" in
            \"*\") value="${value#\"}"; value="${value%\"}" ;;
            \'*\') value="${value#\'}"; value="${value%\'}" ;;
        esac
        printf '%s' "${value}"
        return 0
    done <"${file}"
    return 1
}

compose_cmd() {
    ( cd "${DEPLOY_DIR}" && "${COMPOSE[@]}" -f "${COMPOSE_FILE}" "$@" )
}

compose_up() {
    local image="$1"
    (
        cd "${DEPLOY_DIR}"
        ELBOT_IMAGE="${image}" "${COMPOSE[@]}" -f "${COMPOSE_FILE}" up -d --force-recreate "${SERVICE}" >/dev/null
    )
}

wait_ready() {
    local timeout="${ROLLBACK_READY_TIMEOUT:-90}"
    local deadline=$(( $(date +%s) + timeout ))
    local cid status
    while :; do
        cid="$(compose_cmd ps -q "${SERVICE}" 2>/dev/null || true)"
        status=""
        if [ -n "${cid}" ]; then
            status="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "${cid}" 2>/dev/null || true)"
            case "${status}" in
                exited|dead)
                    warn "容器已退出：${status}"
                    return 1
                    ;;
            esac
        fi
        if compose_cmd exec -T "${SERVICE}" curl -fsS --max-time 3 http://127.0.0.1:32171/ready >/dev/null 2>&1; then
            return 0
        fi
        if [ "$(date +%s)" -ge "${deadline}" ]; then
            warn "等待 /ready 超时（最后状态：${status:-unknown}）"
            return 1
        fi
        sleep 2
    done
}

run_doctor() {
    local -a args=(--no-model)
    if [ -n "${ROLLBACK_DOCTOR_ARGS:-}" ]; then
        # 允许运维显式传入 --require-platform 等严格验收参数。
        read -r -a args <<<"${ROLLBACK_DOCTOR_ARGS}"
    fi
    compose_cmd exec -T "${SERVICE}" elbot doctor "${args[@]}"
}

restore_pre_rollback_state() {
    local reason="$1" image
    warn "${reason}"
    if [ -n "${DATA_BEFORE}" ] && [ -d "${DATA_BEFORE}" ]; then
        warn "尝试恢复回滚前 data：${DATA_BEFORE}"
        compose_cmd stop "${SERVICE}" >/dev/null 2>&1 || true
        if [ -d "${DEPLOY_DIR}/data" ]; then
            DATA_FAILED="${DEPLOY_DIR}/data.rollback-failed-${STAMP}"
            mv "${DEPLOY_DIR}/data" "${DATA_FAILED}" 2>/dev/null || true
        fi
        if mv "${DATA_BEFORE}" "${DEPLOY_DIR}/data" 2>/dev/null; then
            chown -R 10001:10001 "${DEPLOY_DIR}/data" 2>/dev/null || true
            chmod 750 "${DEPLOY_DIR}/data" 2>/dev/null || true
            image="${ROLLBACK_FROM_IMAGE:-${ROLLBACK_IMAGE}}"
            warn "用回滚前镜像重建服务：${image}"
            if compose_up "${image}"; then
                if wait_ready; then
                    STOPPED=0
                    warn "已恢复回滚前 data 和镜像 ${image}；失败的恢复数据保留在 ${DATA_FAILED}"
                else
                    warn "已恢复回滚前 data，但服务未通过 /ready；请手动检查"
                fi
            else
                warn "回滚前镜像重建失败；请手动检查 ${DEPLOY_DIR}/data 与 ${DATA_FAILED}"
            fi
        else
            warn "回滚前 data 恢复失败；请手动检查 ${DATA_BEFORE} 和 ${DATA_FAILED}"
        fi
    fi
    die "${reason}"
}

cleanup() {
    local status=$?
    if [ "${STOPPED}" -eq 1 ]; then
        warn "回滚未完成，请检查现场后手动执行：cd ${DEPLOY_DIR} && ${COMPOSE[*]} -f ${COMPOSE_FILE} up -d --force-recreate ${SERVICE}"
    fi
    return "${status}"
}
trap cleanup EXIT

[ -f "${ROLLBACK_ENV}" ] || die "未找到 ${ROLLBACK_ENV}；请先执行 upgrade.sh"
ROLLBACK_IMAGE="$(read_env_value "${ROLLBACK_ENV}" ROLLBACK_IMAGE || true)"
ROLLBACK_IMAGE_TAR="$(read_env_value "${ROLLBACK_ENV}" ROLLBACK_IMAGE_TAR || true)"
ROLLBACK_DATA_BACKUP="$(read_env_value "${ROLLBACK_ENV}" ROLLBACK_DATA_BACKUP || true)"
ROLLBACK_FROM_IMAGE="$(read_env_value "${ROLLBACK_ENV}" ROLLBACK_FROM_IMAGE || true)"
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

[ -f "${COMPOSE_FILE}" ] || die "Compose 文件不存在：${COMPOSE_FILE}"
[ -f "${ROLLBACK_IMAGE_TAR}" ] || die "镜像快照不存在：${ROLLBACK_IMAGE_TAR}"
[ -f "${ROLLBACK_DATA_BACKUP}" ] || die "数据快照不存在：${ROLLBACK_DATA_BACKUP}"
[ -d "${DEPLOY_DIR}/data" ] || die "数据目录不存在：${DEPLOY_DIR}/data"

if [ "${ROLLBACK_CONFIRM:-0}" != "1" ]; then
    printf '将回滚到 %s，并用 %s 覆盖当前 data。输入 yes 继续：' "${ROLLBACK_IMAGE}" "${ROLLBACK_DATA_BACKUP}"
    read -r answer
    [ "${answer}" = "yes" ] || die "已取消回滚"
fi

log "载入上一版镜像快照 ${ROLLBACK_IMAGE_TAR}"
if ! docker load -i "${ROLLBACK_IMAGE_TAR}" >/dev/null; then
    die "载入上一版镜像失败：${ROLLBACK_IMAGE_TAR}"
fi
if ! docker image inspect "${ROLLBACK_IMAGE}" >/dev/null 2>&1; then
    die "镜像快照中没有找到 ${ROLLBACK_IMAGE}"
fi

log "用旧镜像验证数据快照：image=${ROLLBACK_IMAGE} start=${ROLLBACK_VERIFY_START:-required}"
if ! RESTORE_VERIFY_IMAGE="${ROLLBACK_IMAGE}" RESTORE_VERIFY_START="${ROLLBACK_VERIFY_START:-required}" \
    bash "${DEPLOY_DIR}/restore-verify.sh" "${ROLLBACK_DATA_BACKUP}"; then
    warn "数据快照验证失败；不会停止或覆盖当前服务。如果这是 0.6.1 之前生成、没有 .manifest 的旧备份，可用 RESTORE_VERIFY_STRICT=0 重试"
    exit 1
fi

log "停止 ${SERVICE}"
( cd "${DEPLOY_DIR}" && "${COMPOSE[@]}" -f "${COMPOSE_FILE}" stop "${SERVICE}" >/dev/null )
STOPPED=1

STAMP="$(date +%Y%m%d-%H%M%S)"
DATA_BEFORE="${DEPLOY_DIR}/data.before-rollback-${STAMP}"
DATA_FAILED="${DEPLOY_DIR}/data.rollback-failed-${STAMP}"
log "恢复数据快照，回滚前 data 保留在 ${DATA_BEFORE}"
mv "${DEPLOY_DIR}/data" "${DATA_BEFORE}"
mkdir -p "${DEPLOY_DIR}/data"
tar -xzf "${ROLLBACK_DATA_BACKUP}" -C "${DEPLOY_DIR}"
# 备份中可能保留旧运行的 PID 标记；回滚到尚未使用 flock 的旧版本时，这个
# 标记会阻止旧镜像启动。恢复出的环境不应携带旧进程标记。
rm -f "${DEPLOY_DIR}/data/run/elbot/elbot.pid"
chown -R 10001:10001 "${DEPLOY_DIR}/data" 2>/dev/null || true
chmod 750 "${DEPLOY_DIR}/data" 2>/dev/null || true

log "使用上一版镜像重建 ${SERVICE}：${ROLLBACK_IMAGE}"
if ! compose_up "${ROLLBACK_IMAGE}"; then
    restore_pre_rollback_state "使用上一版镜像重建失败"
fi
STOPPED=0

log "等待 ${SERVICE} 通过 /ready"
if ! wait_ready; then
    STOPPED=1
    restore_pre_rollback_state "回滚后服务未通过 /ready"
fi

log "运行回滚后 doctor 验收"
if ! run_doctor; then
    STOPPED=1
    restore_pre_rollback_state "回滚后 doctor 验收失败"
fi

log "回滚完成：${ROLLBACK_IMAGE}，数据备份已恢复；回滚前 data 保留在 ${DATA_BEFORE}"
