#!/usr/bin/env bash
# 停机备份自测：强制 BACKUP_MODE=stop，用假 docker 模拟
#   1. compose up 成功但容器一直 unhealthy -> backup.sh 必须以非零退出；
#   2. 容器 healthy -> backup.sh 正常成功。
#
# 用法：bash deploy/tests/backup_restart_test.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_SRC="$(cd "${SCRIPT_DIR}/.." && pwd)"

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

DEPLOY="${TMP}/deploy"
DATA="${DEPLOY}/data"
BIN="${TMP}/bin"
mkdir -p "${DATA}" "${DEPLOY}/backups" "${BIN}"
printf 'some data\n' >"${DATA}/payload.txt"
: >"${DEPLOY}/docker-compose.yml"
cp "${DEPLOY_SRC}/backup.sh" "${DEPLOY}/backup.sh"

cat >"${BIN}/docker" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
    compose)
        if [ "${2:-}" = "version" ]; then
            exit 0
        fi
        case "$*" in
            *" ps -q "*) printf 'fake-container-id\n' ;;
        esac
        exit 0
        ;;
    inspect)
        printf '%s\n' "${FAKE_INSPECT_STATUS:-unhealthy}"
        exit 0
        ;;
esac
exit 0
STUB
chmod +x "${BIN}/docker"

export PATH="${BIN}:${PATH}"
export BACKUP_MODE=stop
export BACKUP_VERIFY=0
export KEEP=7

fail=0
echo "== case 1: restart succeeds but container is unhealthy =="
if env FAKE_INSPECT_STATUS=unhealthy bash "${DEPLOY}/backup.sh" "${TMP}/backups" >"${TMP}/unhealthy.log" 2>&1; then
    cat "${TMP}/unhealthy.log"
    echo "FAIL: 容器未就绪时 backup.sh 仍返回成功"
    fail=1
fi

echo "== case 2: restart succeeds and container is healthy =="
if ! env FAKE_INSPECT_STATUS=healthy bash "${DEPLOY}/backup.sh" "${TMP}/backups" >"${TMP}/healthy.log" 2>&1; then
    cat "${TMP}/healthy.log"
    echo "FAIL: 容器 healthy 时 backup.sh 反而失败"
    fail=1
fi

if [ "${fail}" -eq 0 ]; then
    echo "backup_restart_test: passed"
fi
exit "${fail}"
