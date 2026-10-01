#!/usr/bin/env bash
# 在隔离临时目录恢复一份 ElBot 备份，并验证数据库、角色素材与本地媒体
# 是否能一起被读取。该脚本不接触生产 data 目录，可安全地由 backup.sh 调用，
# 也可以手动对已有备份做恢复演练。
#
# 用法：
#   bash restore-verify.sh /path/to/elbot-data-YYYYmmdd-HHMMSS.tar.gz
set -euo pipefail

ARCHIVE="${1:-}"
TMP_DIR=""

log()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[!]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

cleanup() {
    local status=$?
    if [ -n "${TMP_DIR}" ] && [ -d "${TMP_DIR}" ]; then
        rm -rf "${TMP_DIR}"
    fi
    return "${status}"
}
trap cleanup EXIT

[ -n "${ARCHIVE}" ] || die "用法：bash restore-verify.sh /path/to/elbot-data-*.tar.gz"
[ -f "${ARCHIVE}" ] || die "备份文件不存在：${ARCHIVE}"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/elbot-restore-verify.XXXXXX")"
log "解压备份到隔离目录 ${TMP_DIR}"
tar -xzf "${ARCHIVE}" -C "${TMP_DIR}"

MANIFEST="${ARCHIVE}.manifest"
if [ -f "${MANIFEST}" ] && [ -s "${MANIFEST}" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
        log "校验备份清单 ${MANIFEST}"
        if ! (cd "${TMP_DIR}" && sha256sum -c "${MANIFEST}"); then
            die "备份清单校验失败：${MANIFEST}"
        fi
    else
        warn "备份带有 manifest，但未安装 sha256sum，跳过文件级校验"
    fi
fi

DATA_DIR="${TMP_DIR}/data"
[ -d "${DATA_DIR}" ] || die "备份中缺少 data/ 目录，无法恢复"

CONFIG_DIR=""
for candidate in "${DATA_DIR}/config/elbot" "${DATA_DIR}/config"; do
    if [ -f "${candidate}/app.toml" ]; then
        CONFIG_DIR="${candidate}"
        break
    fi
done
[ -n "${CONFIG_DIR}" ] || die "备份中找不到 config/elbot/app.toml，配置无法恢复"
for required in app.toml providers.toml state.toml; do
    [ -f "${CONFIG_DIR}/${required}" ] || warn "缺少配置文件 ${required}，恢复后需要补齐"
done

DBS=()
while IFS= read -r -d '' db; do
    DBS+=("${db}")
done < <(find "${DATA_DIR}" -type f \( -name '*.db' -o -name '*.sqlite' -o -name '*.sqlite3' \) -print0)
[ "${#DBS[@]}" -gt 0 ] || die "备份中没有任何 SQLite 数据库文件，不能视为可恢复数据"

SQLITE_AVAILABLE=0
DB_VERIFY_RESULT="files_present"
if command -v sqlite3 >/dev/null 2>&1; then
    SQLITE_AVAILABLE=1
    DB_VERIFY_RESULT="passed"
else
    warn "未安装 sqlite3，跳过 SQLite integrity_check 与表结构检查"
fi

DB_STRICT_OK=0
if [ "${SQLITE_AVAILABLE}" -eq 1 ]; then
    for db in "${DBS[@]}"; do
        integrity="$(sqlite3 "${db}" 'PRAGMA integrity_check;' 2>&1 || true)"
        [ "${integrity}" = "ok" ] || die "SQLite 完整性校验失败：${db}: ${integrity}"
    done
    log "SQLite integrity_check = ok（${#DBS[@]} 个数据库）"
    DB_STRICT_OK=1
else
    log "SQLite 文件存在（${#DBS[@]} 个）；未做严格校验"
fi

CHARACTER_DIR=""
for candidate in "${CONFIG_DIR}/characters" "${DATA_DIR}/config/elbot/characters"; do
    if [ -d "${candidate}" ]; then
        CHARACTER_DIR="${candidate}"
        break
    fi
done
if [ -n "${CHARACTER_DIR}" ]; then
    character_count="$(find "${CHARACTER_DIR}" -type f | wc -l | tr -d ' ')"
    log "角色素材目录存在：${character_count} 个文件"
else
    warn "备份中没有 characters/ 角色素材目录（可能未启用角色库）"
fi

MEDIA_DIR=""
while IFS= read -r -d '' candidate; do
    MEDIA_DIR="${candidate}"
    break
done < <(find "${DATA_DIR}" -type d -name media -print0)
if [ -n "${MEDIA_DIR}" ]; then
    media_count="$(find "${MEDIA_DIR}" -type f | wc -l | tr -d ' ')"
    log "媒体目录存在：${media_count} 个文件"
else
    warn "备份中没有 media/ 目录（可能尚未导入媒体）"
fi

# 如果 SQLite 中记录了本地媒体，恢复路径可能从容器 /data 变成临时目录；
# 这里按文件名检查媒体文件是否仍然存在，确保数据库和媒体文件一起可用。
if [ "${DB_STRICT_OK}" -eq 1 ]; then
    for db in "${DBS[@]}"; do
        has_media_table="$(sqlite3 "${db}" "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='media';" 2>/dev/null || echo 0)"
        [ "${has_media_table}" = "1" ] || continue
        local_count="$(sqlite3 "${db}" "SELECT COUNT(*) FROM media WHERE backend='local';" 2>/dev/null || echo 0)"
        [ "${local_count}" = "0" ] && continue
        [ -n "${MEDIA_DIR}" ] || die "数据库 ${db} 记录了 ${local_count} 个本地媒体，但备份中没有 media/ 目录"
        missing=0
        while IFS= read -r local_path; do
            [ -n "${local_path}" ] || continue
            base_name="$(basename "${local_path}")"
            if ! find "${MEDIA_DIR}" -type f -name "${base_name}" -print -quit | grep -q .; then
                missing=$((missing + 1))
            fi
        done < <(sqlite3 "${db}" "SELECT local_path FROM media WHERE backend='local' AND local_path IS NOT NULL AND local_path<>'';" 2>/dev/null)
        if [ "${missing}" -gt 0 ]; then
            die "数据库 ${db} 有 ${missing} 个本地媒体文件在备份 media/ 中找不到"
        fi
        log "本地媒体引用检查通过：$(basename "${db}")（${local_count} 条）"
    done
fi

log "恢复验证通过：config=passed database=${DB_VERIFY_RESULT} characters=${CHARACTER_DIR:+present} media=${MEDIA_DIR:+present}"
printf 'restore_verify: passed\n'
