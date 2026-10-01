#!/usr/bin/env bash
# ElBot 外部 watchdog：只依据独立 /live 接口判断进程是否还能响应，
# 连续失败达到阈值后收集诊断信息，并按冷却时间和次数上限受控重启容器。
#
# 推荐由 systemd timer 每分钟执行一次；不要和宝塔 Docker 管理器同时托管同一套 Compose。
# 手动暂停：touch <state-dir>/paused
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE_DIR="${WATCHDOG_COMPOSE_DIR:-${SCRIPT_DIR}}"
STATE_DIR="${WATCHDOG_STATE_DIR:-${COMPOSE_DIR}/watchdog-state}"
DIAG_DIR="${WATCHDOG_DIAG_DIR:-${COMPOSE_DIR}/diagnostics}"
HEALTH_URL="${WATCHDOG_HEALTH_URL:-http://127.0.0.1:32171/live}"
READY_URL="${WATCHDOG_READY_URL:-http://127.0.0.1:32171/ready}"
HEALTHZ_URL="${WATCHDOG_HEALTHZ_URL:-http://127.0.0.1:32171/healthz}"
SERVICE="${WATCHDOG_SERVICE:-elbot}"
FAILURE_THRESHOLD="${WATCHDOG_FAILURE_THRESHOLD:-3}"
COOLDOWN_SECONDS="${WATCHDOG_COOLDOWN_SECONDS:-300}"
MAX_RESTARTS="${WATCHDOG_MAX_RESTARTS:-3}"
WINDOW_SECONDS="${WATCHDOG_WINDOW_SECONDS:-21600}"
DRY_RUN="${WATCHDOG_DRY_RUN:-0}"
WEBHOOK_URL="${WATCHDOG_WEBHOOK_URL:-}"
OPS_TOKEN="${WATCHDOG_OPS_TOKEN:-}"
RESTART_REASON_FILE="${WATCHDOG_RESTART_REASON_FILE:-${COMPOSE_DIR}/data/run/elbot/last_restart_reason}"

mkdir -p "${STATE_DIR}" "${DIAG_DIR}"

log()  { printf '[%s] %s\n' "$(date '+%F %T')" "$*"; }
warn() { printf '[%s] WARN: %s\n' "$(date '+%F %T')" "$*" >&2; }

state_file() { printf '%s/%s' "${STATE_DIR}" "$1"; }
read_int() {
    local file
    file="$(state_file "$1")"
    if [ -f "${file}" ]; then
        tr -dc '0-9' <"${file}" 2>/dev/null || true
    else
        printf '%s' "${2:-0}"
    fi
}
write_int() {
    printf '%s\n' "$2" >"$(state_file "$1")"
}

send_webhook() {
    local event="$1" text="$2"
    [ -n "${WEBHOOK_URL}" ] || return 0
    local payload
    payload="$(printf '{"source":"elbot-watchdog","event":"%s","service":"%s","time":"%s","message":"%s"}' \
        "${event}" "${SERVICE}" "$(date -Is)" "${text//\"/\\\"}")"
    curl -fsS --max-time 5 -X POST -H 'Content-Type: application/json' -d "${payload}" "${WEBHOOK_URL}" >/dev/null 2>&1 || \
        warn "webhook delivery failed for event ${event}"
}

redact_diagnostics() {
    local dir="$1"
    [ -d "${dir}" ] || return 0
    if ! command -v sed >/dev/null 2>&1; then
        return 0
    fi
    # 诊断包可能包含上游错误或日志片段；再做一层通用凭据脱敏。
    find "${dir}" -type f -print0 2>/dev/null | xargs -0 -r sed -i -E \n        -e 's/(sk-[A-Za-z0-9_-]{8,})/[REDACTED]/g' \n        -e 's/(Bearer[[:space:]]+)[A-Za-z0-9._-]+/[REDACTED]/Ig' \n        -e 's/((api[_-]?key|token|secret|password)[[:space:]]*[=:][[:space:]]*)([^[:space:]]+)/[REDACTED]/Ig' \n        2>/dev/null || true
}

capture_diagnostics() {
    local stamp dir
    stamp="$(date '+%Y%m%d-%H%M%S')"
    dir="${DIAG_DIR}/${stamp}"
    mkdir -p "${dir}"
    echo "$(date '+%F %T') capture diagnostics -> ${dir}" >&2
    {
        echo "time=$(date -Is)"
        echo "service=${SERVICE}"
        echo "health_url=${HEALTH_URL}"
        echo "ready_url=${READY_URL}"
        echo "healthz_url=${HEALTHZ_URL}"
        echo "consecutive_failures=$(read_int consecutive_failures 0)"
        echo "last_restart_at=$(read_int last_restart_epoch 0)"
        echo "last_restart_reason=$(cat "${RESTART_REASON_FILE}" 2>/dev/null || tail -n 1 "$(state_file restart_reasons.log)" 2>/dev/null || true)"
    } >"${dir}/summary.txt" 2>&1 || true
    curl -sS --max-time 5 "${HEALTH_URL}" >"${dir}/live.json" 2>&1 || true
    curl -sS --max-time 5 "${READY_URL}" >"${dir}/ready.json" 2>&1 || true
    curl -sS --max-time 5 "${HEALTHZ_URL}" >"${dir}/healthz.json" 2>&1 || true
    local curl_auth=()
    if [ -n "${OPS_TOKEN}" ]; then
        curl_auth=(-H "Authorization: Bearer ${OPS_TOKEN}")
    fi
    curl -sS --max-time 5 "${HEALTHZ_URL%/healthz}/tasks" "${curl_auth[@]}" >"${dir}/tasks.json" 2>&1 || true
    curl -sS --max-time 5 "${HEALTHZ_URL%/healthz}/metrics" "${curl_auth[@]}" >"${dir}/metrics.json" 2>&1 || true
    if command -v docker >/dev/null 2>&1; then
        docker ps -a --filter "name=^/${SERVICE}$" >"${dir}/docker-ps.txt" 2>&1 || true
        # 只抓 State/Ports，避免 docker inspect 中的 Config.Env 把 API Key / token 写进诊断包。
        docker inspect --format '{{json .State}}' "${SERVICE}" >"${dir}/docker-state.json" 2>&1 || true
        docker inspect --format '{{json .NetworkSettings.Ports}}' "${SERVICE}" >"${dir}/docker-ports.json" 2>&1 || true
        docker logs --tail 500 "${SERVICE}" >"${dir}/docker-logs.txt" 2>&1 || true
        docker stats --no-stream "${SERVICE}" >"${dir}/docker-stats.txt" 2>&1 || true
    fi
    redact_diagnostics "${dir}"
    df -h >"${dir}/disk.txt" 2>&1 || true
    if [ -d "${COMPOSE_DIR}/data" ]; then
        du -sh "${COMPOSE_DIR}/data" >"${dir}/data-size.txt" 2>&1 || true
    fi
    # Keep only the newest 30 diagnostic directories.
    find "${DIAG_DIR}" -mindepth 1 -maxdepth 1 -type d -printf '%T@ %p\n' 2>/dev/null \
        | sort -nr | tail -n +31 | awk '{print $2}' | xargs -r rm -rf
    printf '%s\n' "${dir}"
}

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

if [ -f "$(state_file paused)" ]; then
    log "watchdog paused by operator; remove $(state_file paused) to resume"
    exit 0
fi

fails="$(read_int consecutive_failures 0)"
if curl -fsS --max-time 5 "${HEALTH_URL}" >/dev/null 2>&1; then
    if [ "${fails}" -gt 0 ]; then
        log "health recovered after ${fails} consecutive failure(s)"
    fi
    write_int consecutive_failures 0
    exit 0
fi

fails=$((fails + 1))
write_int consecutive_failures "${fails}"
warn "live check failed (${fails}/${FAILURE_THRESHOLD})"
if [ "${fails}" -lt "${FAILURE_THRESHOLD}" ]; then
    exit 0
fi

now="$(date +%s)"
last_restart="$(read_int last_restart_epoch 0)"
if [ "${last_restart}" -gt 0 ] && [ $((now - last_restart)) -lt "${COOLDOWN_SECONDS}" ]; then
    warn "restart cooldown active ($((now - last_restart))s < ${COOLDOWN_SECONDS}s); not restarting"
    exit 0
fi

history_file="$(state_file restart_history)"
if [ -f "${history_file}" ]; then
    cutoff=$((now - WINDOW_SECONDS))
    awk -v cutoff="${cutoff}" '$1 >= cutoff' "${history_file}" >"${history_file}.tmp" 2>/dev/null || true
    mv "${history_file}.tmp" "${history_file}" 2>/dev/null || true
fi
restarts_in_window=0
if [ -f "${history_file}" ]; then
    restarts_in_window="$(wc -l <"${history_file}")"
fi
if [ "${restarts_in_window}" -ge "${MAX_RESTARTS}" ]; then
    printf '%s\n' "max restarts reached at $(date -Is)" >"$(state_file paused)"
    paused_line="$(date -Is) paused: max restarts (${MAX_RESTARTS}) reached in window"
    printf '%s\n' "${paused_line}" >>"$(state_file restart_reasons.log)"
    mkdir -p "$(dirname "${RESTART_REASON_FILE}")" 2>/dev/null || true
    printf '%s\n' "${paused_line}" >"${RESTART_REASON_FILE}" 2>/dev/null || true
    warn "max restarts (${MAX_RESTARTS}) reached in window; pausing auto-restart"
    capture_diagnostics >/dev/null
    send_webhook "paused" "max restarts reached; manual intervention required"
    exit 1
fi

diag_dir="$(capture_diagnostics)"
if [ "${DRY_RUN}" = "1" ]; then
    log "DRY_RUN=1: would restart ${SERVICE}; diagnostics=${diag_dir}"
    exit 0
fi

if ! detect_compose || [ ! -f "${COMPOSE_DIR}/docker-compose.yml" ]; then
    warn "docker compose not available; cannot restart ${SERVICE}"
    send_webhook "restart_failed" "docker compose not available"
    exit 1
fi

log "restarting ${SERVICE} after ${fails} failed live checks"
reason_line="$(date -Is) restart: ${fails} consecutive failures of ${HEALTH_URL}"
printf '%s\n' "${reason_line}" >>"$(state_file restart_reasons.log)"
mkdir -p "$(dirname "${RESTART_REASON_FILE}")" 2>/dev/null || true
printf '%s\n' "${reason_line}" >"${RESTART_REASON_FILE}" 2>/dev/null || true
if "${COMPOSE[@]}" -f "${COMPOSE_DIR}/docker-compose.yml" up -d --force-recreate "${SERVICE}"; then
    write_int last_restart_epoch "${now}"
    printf '%s\n' "${now}" >>"${history_file}"
    write_int consecutive_failures 0
    send_webhook "restarted" "restarted ${SERVICE}; diagnostics=${diag_dir}"
    log "restart command completed"
else
    warn "restart command failed for ${SERVICE}"
    send_webhook "restart_failed" "docker compose up failed"
    exit 1
fi
