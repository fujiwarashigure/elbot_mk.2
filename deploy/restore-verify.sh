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
STRICT="${RESTORE_VERIFY_STRICT:-1}"
START_MODE="${RESTORE_VERIFY_START:-auto}"
START_IMAGE="${RESTORE_VERIFY_IMAGE:-}"
VERIFY_CONTAINER=""

log()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[!]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[x]\033[0m %s\n' "$*" >&2; printf 'restore_verify: failed\n' >&2; exit 1; }

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
    local status=$?
    if [ -n "${VERIFY_CONTAINER}" ] && command -v docker >/dev/null 2>&1; then
        docker rm -f "${VERIFY_CONTAINER}" >/dev/null 2>&1 || true
    fi
    if [ -n "${TMP_DIR}" ] && [ -d "${TMP_DIR}" ]; then
        rm -rf "${TMP_DIR}"
    fi
    return "${status}"
}
trap cleanup EXIT

[ -n "${ARCHIVE}" ] || die "用法：bash restore-verify.sh /path/to/elbot-data-*.tar.gz"
[ -f "${ARCHIVE}" ] || die "备份文件不存在：${ARCHIVE}"
# 转成绝对路径。调用方可能传相对目录（例如 backup.sh backups），而后续
# sha256sum -c 会在临时目录里执行，相对归档/清单路径会失效。
ARCHIVE_DIR="$(cd "$(dirname "${ARCHIVE}")" && pwd)"
ARCHIVE="${ARCHIVE_DIR}/$(basename "${ARCHIVE}")"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/elbot-restore-verify.XXXXXX")"
log "解压备份到隔离目录 ${TMP_DIR}"
tar -xzf "${ARCHIVE}" -C "${TMP_DIR}"

CHECK_MANIFEST="skipped"
CHECK_TOML="skipped"
CHECK_DATABASE="skipped"
CHECK_MEDIA_PATHS="skipped"
CHECK_START="skipped"
MANIFEST="${ARCHIVE}.manifest"
if [ ! -f "${MANIFEST}" ] || [ ! -s "${MANIFEST}" ]; then
    if [ "${STRICT}" = "1" ]; then
        die "缺少备份清单 ${MANIFEST}，严格模式拒绝跳过文件完整性校验；确认不需要时可用 RESTORE_VERIFY_STRICT=0 降级"
    fi
    warn "备份没有清单，跳过文件级校验"
elif ! command -v sha256sum >/dev/null 2>&1; then
    if [ "${STRICT}" = "1" ]; then
        die "备份带有 manifest，但宿主机没有 sha256sum，严格模式拒绝跳过文件完整性校验"
    fi
    warn "备份带有 manifest，但未安装 sha256sum，跳过文件级校验"
else
    log "校验备份清单 ${MANIFEST}"
    if ! (cd "${TMP_DIR}" && sha256sum -c "${MANIFEST}"); then
        die "备份清单校验失败：${MANIFEST}"
    fi
    CHECK_MANIFEST="passed"
fi

DATA_DIR="${TMP_DIR}/data"
[ -d "${DATA_DIR}" ] || die "备份中缺少 data/ 目录，无法恢复"

# 备份可能来自仍在运行的实例，归档里会保留旧 PID 标记。恢复环境没有对应
# 进程；旧版本 ElBot 会把这个标记当成“已有服务运行”，导致隔离启动失败。
RUNTIME_MARKER="${DATA_DIR}/run/elbot/elbot.pid"
if [ -f "${RUNTIME_MARKER}" ]; then
    log "移除恢复数据中的旧服务 PID 标记：${RUNTIME_MARKER}"
    rm -f "${RUNTIME_MARKER}"
fi

CONFIG_DIR=""
for candidate in "${DATA_DIR}/config/elbot" "${DATA_DIR}/config"; do
    if [ -f "${candidate}/app.toml" ]; then
        CONFIG_DIR="${candidate}"
        break
    fi
done
[ -n "${CONFIG_DIR}" ] || die "备份中找不到 config/elbot/app.toml，配置无法恢复"
# app.toml 必需；服务配置既可能是新的 services.toml，也可能是旧 providers.toml。
[ -s "${CONFIG_DIR}/app.toml" ] || die "缺少必需的主配置 app.toml（或文件为空），恢复后无法启动"
if [ ! -s "${CONFIG_DIR}/services.toml" ] && [ ! -s "${CONFIG_DIR}/providers.toml" ]; then
    die "缺少必需的服务配置 services.toml 或 providers.toml（或文件为空），恢复后无法启动"
fi
for optional in state.toml; do
    [ -s "${CONFIG_DIR}/${optional}" ] || warn "缺少配置文件 ${optional}，将使用内置默认值"
done
PYTHON_TOML=""
for candidate in python3 python; do
    if command -v "${candidate}" >/dev/null 2>&1 && "${candidate}" -c 'import tomllib' >/dev/null 2>&1; then
        PYTHON_TOML="${candidate}"
        break
    fi
done
if [ -n "${PYTHON_TOML}" ]; then
    if ! "${PYTHON_TOML}" - "${CONFIG_DIR}" "${DATA_DIR}" <<'PYTOML'
import os
import pathlib
import sys
import tomllib

root = pathlib.Path(sys.argv[1])
data_root = pathlib.Path(sys.argv[2])
bad = []


def parse(path):
    with path.open("rb") as fh:
        return tomllib.load(fh)


def normalize(value):
    value = str(value)
    # 容器内绝对路径 /data/... 映射回恢复出来的 data 目录。放在 isabs 之前，
    # 这样在 Windows 上做脚本自测时也能正确识别。
    if value == "/data":
        return os.path.normpath(str(data_root))
    if value.startswith("/data/"):
        return os.path.normpath(os.path.join(str(data_root), value[len("/data/") :]))
    if os.path.isabs(value):
        return os.path.normpath(value)
    return os.path.normpath(os.path.join(str(root), value))


for path in sorted(root.glob("*.toml")):
    try:
        parse(path)
    except Exception as exc:  # noqa: BLE001
        bad.append(f"{path.name}: {exc}")

# app.toml 引用的服务配置必须存在；state.toml 必须和只读配置分离。
try:
    app = parse(root / "app.toml")
except Exception as exc:  # noqa: BLE001
    bad.append(f"app.toml: {exc}")
    app = {}
if not isinstance(app, dict):
    app = {}
config_files = app.get("config_files") or {}
if not isinstance(config_files, dict):
    config_files = {}
services = str(config_files.get("services") or "")
providers = str(config_files.get("providers") or "")
state = str(config_files.get("state") or "")

service_path = normalize(services) if services else ""
provider_path = normalize(providers) if providers else ""
if services:
    if not os.path.isfile(service_path) or os.path.getsize(service_path) == 0:
        bad.append(f"config_files.services 指向的文件不存在或为空: {service_path}")
elif providers:
    if not os.path.isfile(provider_path) or os.path.getsize(provider_path) == 0:
        bad.append(f"config_files.providers 指向的文件不存在或为空: {provider_path}")
else:
    default_provider = normalize("providers.toml")
    if not os.path.isfile(default_provider) or os.path.getsize(default_provider) == 0:
        bad.append("app.toml 未配置 config_files.services/providers，且默认 providers.toml 不存在或为空")

if state:
    shared = {"app": normalize("app.toml")}
    if services:
        shared["services"] = service_path
    if providers:
        shared["providers"] = provider_path
    for key in ("elnis", "tool_tags"):
        value = str(config_files.get(key) or "")
        if value:
            shared[key] = normalize(value)
    state_path = normalize(state)
    for name, shared_path in shared.items():
        if state_path == shared_path:
            bad.append(f"config_files.state 不能与 {name} 共用同一个文件: {state_path}")

if bad:
    print("配置检查失败: " + "; ".join(bad), file=sys.stderr)
    sys.exit(1)
PYTOML
    then
        die "配置检查失败：${CONFIG_DIR}"
    fi
    CHECK_TOML="passed"
    log "配置解析与引用检查通过"
else
    if [ "${STRICT}" = "1" ]; then
        die "未找到带 tomllib 的 python3/python，严格模式拒绝跳过 TOML 解析检查；确认不需要时可用 RESTORE_VERIFY_STRICT=0 降级"
    fi
    warn "未找到带 tomllib 的 python3/python，跳过 TOML 解析检查"
fi

DBS=()
while IFS= read -r -d '' db; do
    DBS+=("${db}")
done < <(find "${DATA_DIR}" -type f \( -name '*.db' -o -name '*.sqlite' -o -name '*.sqlite3' \) -print0)
[ "${#DBS[@]}" -gt 0 ] || die "备份中没有任何 SQLite 数据库文件，不能视为可恢复数据"

SQLITE_AVAILABLE=0
if command -v sqlite3 >/dev/null 2>&1; then
    SQLITE_AVAILABLE=1
elif [ "${STRICT}" = "1" ]; then
    die "未安装 sqlite3，严格模式拒绝跳过 SQLite 完整性检查；确认不需要时可用 RESTORE_VERIFY_STRICT=0 降级"
else
    warn "未安装 sqlite3，跳过 SQLite integrity_check 与表结构检查"
fi

DB_STRICT_OK=0
if [ "${SQLITE_AVAILABLE}" -eq 1 ]; then
    for db in "${DBS[@]}"; do
        integrity="$(sqlite3 "${db}" 'PRAGMA integrity_check;' 2>&1 || true)"
        [ "${integrity}" = "ok" ] || die "SQLite 完整性校验失败：${db}: ${integrity}"
        table_count="$(sqlite3 "${db}" "SELECT COUNT(*) FROM sqlite_master WHERE type='table';" 2>&1)" || die "无法读取数据库表结构：${db}: ${table_count}"
        [ "${table_count}" -gt 0 ] || die "数据库 ${db} 中没有任何表，可能不是有效的 ElBot 数据库"
    done
    log "SQLite integrity_check = ok（${#DBS[@]} 个数据库）"
    DB_STRICT_OK=1
    CHECK_DATABASE="passed"
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
# 这里按 exact path 检查媒体文件是否仍然存在，确保数据库和媒体文件一起可用。
MEDIA_PATHS_APPLICABLE=0
MEDIA_PATHS_FALLBACK=0
if [ "${DB_STRICT_OK}" -eq 1 ]; then
    for db in "${DBS[@]}"; do
        has_media_table="$(sqlite3 "${db}" "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='media';" 2>&1)" || die "查询 media 表是否存在失败：${db}: ${has_media_table}"
        [ "${has_media_table}" = "1" ] || continue
        local_count="$(sqlite3 "${db}" "SELECT COUNT(*) FROM media WHERE backend='local';" 2>&1)" || die "查询本地媒体数量失败：${db}: ${local_count}"
        [ "${local_count}" = "0" ] && continue
        MEDIA_PATHS_APPLICABLE=1
        [ -n "${MEDIA_DIR}" ] || die "数据库 ${db} 记录了 ${local_count} 个本地媒体，但备份中没有 media/ 目录"
        missing=0
        media_rows="$(sqlite3 "${db}" "SELECT local_path FROM media WHERE backend='local' AND local_path IS NOT NULL AND local_path<>'';" 2>&1)" || die "读取本地媒体引用失败：${db}: ${media_rows}"
        while IFS= read -r local_path; do
            [ -n "${local_path}" ] || continue
            # 容器内路径形如 /data/elbot/media/<xx>/<rest>，备份中的对应路径是
            # ${DATA_DIR}/elbot/media/...，因此严格模式只接受这个映射。
            case "${local_path}" in
                /data/*)
                    if [ ! -f "${DATA_DIR}/${local_path#/data/}" ]; then
                        missing=$((missing + 1))
                    fi
                    ;;
                *)
                    if [ "${STRICT}" = "1" ]; then
                        die "数据库 $(basename "${db}") 中有 media.local_path 不是 /data/... 形式（${local_path}），严格模式拒绝按文件名兜底；确认不需要时可用 RESTORE_VERIFY_STRICT=0 降级"
                    fi
                    MEDIA_PATHS_FALLBACK=1
                    if ! find "${MEDIA_DIR}" -type f -name "$(basename "${local_path}")" -print -quit | grep -q .; then
                        missing=$((missing + 1))
                    fi
                    ;;
            esac
        done <<<"${media_rows}"
        if [ "${missing}" -gt 0 ]; then
            die "数据库 ${db} 有 ${missing} 个本地媒体文件在备份 media/ 中找不到"
        fi
        log "本地媒体引用检查通过：$(basename "${db}")（${local_count} 条）"
    done
    if [ "${MEDIA_PATHS_APPLICABLE}" -eq 0 ] || [ "${MEDIA_PATHS_FALLBACK}" -eq 0 ]; then
        CHECK_MEDIA_PATHS="passed"
    else
        warn "部分 media.local_path 不是 /data/... 形式，媒体路径检查记为 skipped"
        CHECK_MEDIA_PATHS="skipped"
    fi
else
    warn "SQLite 严格校验已跳过，媒体路径检查同步跳过"
fi

# 可选的隔离启动验收：用真实镜像 + 恢复出的 data 启动一个
# --network none 的一次性实例，并等待 /ready。默认 auto：只有 Docker 和镜像
# 都可用时才执行；设为 1/required 时缺少前置条件会直接失败。
START_MODE="$(printf '%s' "${START_MODE}" | tr '[:upper:]' '[:lower:]')"
case "${START_MODE}" in
    auto) ;;
    1|true|yes|require|required) START_MODE="required" ;;
    0|false|no|off) START_MODE="off" ;;
    *) die "未知 RESTORE_VERIFY_START=${START_MODE}（可用 auto / 1 / 0 / required）" ;;
esac
if [ "${START_MODE}" != "off" ]; then
    if [ -z "${START_IMAGE}" ]; then
        START_IMAGE="$(docker inspect --format '{{.Config.Image}}' "${ELBOT_SERVICE:-elbot}" 2>/dev/null || true)"
    fi
    if ! command -v docker >/dev/null 2>&1; then
        if [ "${START_MODE}" = "required" ]; then
            die "RESTORE_VERIFY_START=${START_MODE} 但未找到 docker"
        fi
        warn "未找到 docker，跳过隔离启动验收"
    elif [ -z "${START_IMAGE}" ]; then
        if [ "${START_MODE}" = "required" ]; then
            die "RESTORE_VERIFY_START=${START_MODE} 但未找到可用镜像；请设置 RESTORE_VERIFY_IMAGE"
        fi
        warn "未找到可用的 ElBot 镜像，跳过隔离启动验收"
    elif ! docker image inspect "${START_IMAGE}" >/dev/null 2>&1; then
        if [ "${START_MODE}" = "required" ]; then
            die "RESTORE_VERIFY_START=${START_MODE} 但镜像不存在：${START_IMAGE}"
        fi
        warn "镜像不存在，跳过隔离启动验收：${START_IMAGE}"
    else
        VERIFY_CONTAINER="elbot-restore-verify-$$-$(date +%s)"
        START_TIMEOUT="${RESTORE_VERIFY_START_TIMEOUT:-45}"
        START_DEADLINE=$(( $(date +%s) + START_TIMEOUT ))
        START_UID="$(id -u 2>/dev/null || printf '0')"
        START_GID="$(id -g 2>/dev/null || printf '0')"
        # 用调用者 UID/GID 运行，避免 root 写出的临时 data 让宿主用户无法清理。
        log "启动隔离实例做恢复验收（network=none，镜像 ${START_IMAGE}）"
        # MSYS_NO_PATHCONV 防止 Windows Git Bash 把 -e/-v 里的 /data 路径改写成 C: 路径。
        if ! MSYS_NO_PATHCONV=1 docker run -d --name "${VERIFY_CONTAINER}" --network none --user "${START_UID}:${START_GID}" \
            -v "$(host_path "${DATA_DIR}"):/data" \
            -e XDG_CONFIG_HOME=/data/config \
            -e XDG_DATA_HOME=/data \
            -e XDG_RUNTIME_DIR=/data/run \
            "${START_IMAGE}" service run >/dev/null; then
            die "隔离实例启动失败：${START_IMAGE}"
        fi
        START_STATE=""
        while :; do
            START_STATE="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "${VERIFY_CONTAINER}" 2>/dev/null || true)"
            case "${START_STATE}" in
                exited|dead)
                    die "隔离实例提前退出（${START_STATE}）"
                    ;;
            esac
            if docker exec "${VERIFY_CONTAINER}" curl -fsS --max-time 3 http://127.0.0.1:32171/ready >/dev/null 2>&1; then
                CHECK_START="passed"
                log "隔离实例 /ready 通过"
                break
            fi
            if [ "$(date +%s)" -ge "${START_DEADLINE}" ]; then
                die "等待隔离实例 /ready 超时（最后状态：${START_STATE:-unknown}）"
            fi
            sleep 1
        done
    fi
fi

CHARACTER_STATE="missing"
[ -n "${CHARACTER_DIR}" ] && CHARACTER_STATE="present"
MEDIA_STATE="missing"
[ -n "${MEDIA_DIR}" ] && MEDIA_STATE="present"
log "恢复验证结果：manifest=${CHECK_MANIFEST} toml=${CHECK_TOML} database=${CHECK_DATABASE} media_paths=${CHECK_MEDIA_PATHS} start=${CHECK_START} characters=${CHARACTER_STATE} media=${MEDIA_STATE}"

# 严格模式不会带着静态跳过项走到这里；此处主要区分：
#   passed                 静态完整性和真实隔离启动都通过
#   static_passed          静态完整性通过，但启动验收被关闭/跳过
#   static_passed_with_skips 非严格模式静态完整性通过，但有跳过项
# 调用方读取最后一行的状态时可明确知道“通过”是否包含真实启动。
STATIC_PASSED=1
for state in "${CHECK_MANIFEST}" "${CHECK_TOML}" "${CHECK_DATABASE}" "${CHECK_MEDIA_PATHS}"; do
    [ "${state}" = "passed" ] || STATIC_PASSED=0
done
if [ "${STATIC_PASSED}" -eq 1 ]; then
    if [ "${CHECK_START}" = "passed" ]; then
        printf 'restore_verify: passed\n'
    else
        printf 'restore_verify: static_passed\n'
    fi
else
    printf 'restore_verify: static_passed_with_skips\n'
fi
