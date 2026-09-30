#!/usr/bin/env bash
# ElBot 数据备份脚本，可在宝塔【计划任务】中每天执行。
#
# 默认按下列顺序选择一致性备份方式：
#   1. 宿主机有 sqlite3：对所有 SQLite 数据库执行 .backup，其余文件直接归档，不中断服务；
#   2. 没有 sqlite3 但有 Docker Compose：短暂停止 ElBot 容器后打包，再自动启动；
#   3. 两者都没有：回退到热打包，并明确警告可能不一致。
#
# 用法：
#   bash backup.sh [备份目录]
#   BACKUP_MODE=stop bash backup.sh [备份目录]   # 强制短暂停机备份
#   BACKUP_MODE=hot  bash backup.sh [备份目录]   # 明确接受热打包风险
#
# 默认备份到 ./backups，保留最近 14 份（KEEP 可覆盖）。
set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DATA_DIR="${DEPLOY_DIR}/data"
BACKUP_DIR="${1:-${DEPLOY_DIR}/backups}"
KEEP="${KEEP:-14}"
MODE="${BACKUP_MODE:-auto}"
STAMP="$(date +%Y%m%d-%H%M%S)"
TARGET=""
STAGE=""
STOPPED=0
COMPOSE=()

log()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[!]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

cleanup() {
    local status=$?
    if [ "${STOPPED}" -eq 1 ]; then
        log "恢复 ElBot 容器"
        if ! "${COMPOSE[@]}" up -d --remove-orphans >/dev/null 2>&1; then
            warn "容器恢复失败，请手动执行：cd ${DEPLOY_DIR} && ${COMPOSE[*]} up -d --remove-orphans"
        fi
    fi
    if [ -n "${STAGE}" ] && [ -d "${STAGE}" ]; then
        rm -rf "${STAGE}"
    fi
    return "${status}"
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

prune_old_backups() {
    ls -1t "${BACKUP_DIR}"/elbot-data-*.tar.gz 2>/dev/null \
        | tail -n +$((KEEP + 1)) \
        | xargs -r rm -f
}

if [ ! -d "${DATA_DIR}" ]; then
    die "错误：${DATA_DIR} 不存在"
fi

mkdir -p "${BACKUP_DIR}"

case "${MODE}" in
    auto)
        if command -v sqlite3 >/dev/null 2>&1; then
            MODE=sqlite
        else
            MODE=stop
        fi
        ;;
    sqlite)
        if ! command -v sqlite3 >/dev/null 2>&1; then
            warn "未找到 sqlite3，退回到 stop 模式"
            MODE=stop
        fi
        ;;
    stop|hot)
        ;;
    *)
        die "未知 BACKUP_MODE=${MODE}（可用 auto / sqlite / stop / hot）"
        ;;
esac

if [ "${MODE}" = "sqlite" ]; then
    case "${DATA_DIR}:${BACKUP_DIR}" in
        *"'"*)
            die "数据或备份路径包含单引号，无法安全执行 sqlite3 .backup；请改用 BACKUP_MODE=stop"
            ;;
    esac

    STAGE="$(mktemp -d "${BACKUP_DIR}/.elbot-backup-XXXXXX")"
    mkdir -p "${STAGE}/data"
    log "复制 data 到临时目录 ${STAGE}/data"
    cp -a "${DATA_DIR}/." "${STAGE}/data/"

    while IFS= read -r -d '' db; do
        rel="${db#${DATA_DIR}/}"
        out="${STAGE}/data/${rel}"
        rm -f "${out}" "${out}-wal" "${out}-shm" "${out}-journal"
        log "SQLite 一致性备份：${rel}"
        sqlite3 "${db}" ".backup '${out}'"
    done < <(find "${DATA_DIR}" -type f \( -name '*.db' -o -name '*.sqlite' -o -name '*.sqlite3' \) -print0)

    find "${STAGE}/data" -type f \( -name '*-wal' -o -name '*-shm' -o -name '*-journal' \) -delete

    TARGET="${BACKUP_DIR}/elbot-data-${STAMP}.tar.gz"
    log "打包 ${TARGET}"
    tar -czf "${TARGET}" -C "${STAGE}" data
elif [ "${MODE}" = "stop" ]; then
    if detect_compose && [ -f "${DEPLOY_DIR}/docker-compose.yml" ]; then
        container_id="$("${COMPOSE[@]}" ps -q elbot 2>/dev/null || true)"
        if [ -n "${container_id}" ]; then
            log "短暂停止 ElBot 容器，确保 SQLite WAL 一致"
            if "${COMPOSE[@]}" stop elbot; then
                STOPPED=1
            else
                warn "停止容器失败，改用热打包"
                MODE=hot
            fi
        else
            log "未发现运行中的 ElBot 容器，直接按冷数据打包"
        fi
    else
        warn "未检测到可用的 Docker Compose，且 sqlite3 不可用；回退到热打包"
        warn "SQLite WAL 模式下热打包可能不一致，生产环境不建议"
        MODE=hot
    fi
fi

if [ "${MODE}" = "hot" ]; then
    TARGET="${BACKUP_DIR}/elbot-data-${STAMP}.tar.gz"
    warn "热打包 ${DATA_DIR} -> ${TARGET}（SQLite WAL 下可能不一致）"
    tar -czf "${TARGET}" -C "${DEPLOY_DIR}" data
elif [ "${MODE}" = "stop" ]; then
    TARGET="${BACKUP_DIR}/elbot-data-${STAMP}.tar.gz"
    log "打包 ${TARGET}"
    tar -czf "${TARGET}" -C "${DEPLOY_DIR}" data
fi

if [ -z "${TARGET}" ] || [ ! -f "${TARGET}" ]; then
    die "未生成备份文件"
fi

log "清理超过 ${KEEP} 份的旧备份"
prune_old_backups

log "完成：${TARGET}"
