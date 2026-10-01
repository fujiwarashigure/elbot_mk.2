#!/usr/bin/env bash
# 恢复验证的隔离启动自测：用假 docker 模拟
#   1. 恢复出的 data 可以启动一次性实例，等待 /ready 成功后输出 start=passed；
#   2. /ready 一直失败时严格失败，并输出 restore_verify: failed。
#
# 重依赖 sqlite3 和带 tomllib 的 python3；缺失时整体跳过。
# 用法：bash deploy/tests/restore_verify_start_test.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_SRC="$(cd "${SCRIPT_DIR}/.." && pwd)"

if ! command -v sqlite3 >/dev/null 2>&1; then
    echo "skip: 未安装 sqlite3，跳过 restore_verify_start_test"
    exit 0
fi
if ! command -v python3 >/dev/null 2>&1 || ! python3 -c 'import tomllib' >/dev/null 2>&1; then
    echo "skip: 没有带 tomllib 的 python3，跳过 restore_verify_start_test"
    exit 0
fi

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

STAGE="${TMP}/stage"
DATA="${STAGE}/data"
ARCHIVE="${TMP}/elbot-data-test.tar.gz"
BIN="${TMP}/bin"
mkdir -p "${DATA}/config/elbot" "${BIN}"
printf 'mode = "default"\n' >"${DATA}/config/elbot/app.toml"
printf '[providers]\n' >"${DATA}/config/elbot/providers.toml"
sqlite3 "${DATA}/sessions.db" "CREATE TABLE sessions (id TEXT);"
( cd "${STAGE}" && find data -type f -print0 | xargs -0 -r sha256sum ) >"${ARCHIVE}.manifest"
tar -czf "${ARCHIVE}" -C "${STAGE}" data

cat >"${BIN}/docker" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
printf 'docker %s\n' "$*" >>"${FAKE_DOCKER_LOG}"
case "${1:-}" in
    image)
        exit 0
        ;;
    inspect)
        case "$*" in
            *State.Health*) printf '%s\n' "${FAKE_CONTAINER_STATE:-running}" ;;
            *) printf '%s\n' "elbot:test" ;;
        esac
        exit 0
        ;;
    run)
        case "$*" in
            *" --network none "*) ;;
            *)
                echo "fake docker: missing --network none: $*" >&2
                exit 97
                ;;
        esac
        exit 0
        ;;
    exec)
        exit "${FAKE_EXEC_RC:-0}"
        ;;
    rm)
        exit 0
        ;;
esac
exit 0
STUB
chmod +x "${BIN}/docker"

export PATH="${BIN}:${PATH}"
export FAKE_DOCKER_LOG="${TMP}/docker.log"
export RESTORE_VERIFY_START=1
export RESTORE_VERIFY_IMAGE=elbot:test
export RESTORE_VERIFY_START_TIMEOUT=1

fail=0
note() { echo "FAIL: $*"; fail=1; }

echo "== case 1: isolated instance reaches /ready =="
if ! bash "${DEPLOY_SRC}/restore-verify.sh" "${ARCHIVE}" >"${TMP}/ok.log" 2>&1; then
    cat "${TMP}/ok.log"
    note "隔离实例 /ready 成功时恢复验证失败"
fi
grep -q 'start=passed' "${TMP}/ok.log" || note "没有报告 start=passed"
grep -q -- '--network none' "${FAKE_DOCKER_LOG}" || note "启动隔离实例时没有使用 --network none"
grep -q 'elbot:test service run' "${FAKE_DOCKER_LOG}" || note "没有用真实镜像执行 service run"

echo "== case 2: /ready never succeeds =="
: >"${FAKE_DOCKER_LOG}"
if FAKE_EXEC_RC=1 bash "${DEPLOY_SRC}/restore-verify.sh" "${ARCHIVE}" >"${TMP}/fail.log" 2>&1; then
    note "隔离实例从未 /ready 时严格模式仍然通过"
fi
grep -q 'restore_verify: failed' "${TMP}/fail.log" || note "启动验收失败时没有输出 restore_verify: failed"

if [ "${fail}" -eq 0 ]; then
    echo "restore_verify_start_test: passed"
fi
exit "${fail}"
