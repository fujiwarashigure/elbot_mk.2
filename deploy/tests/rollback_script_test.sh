#!/usr/bin/env bash
# 回滚脚本自测：用假 Docker 验证
#   1. rollback.env 不走 source，含空格的路径不会被拆开，未知键不会被 eval；
#   2. 先载入旧镜像，再用旧镜像做恢复验证；
#   3. 回滚后等待 /ready 并运行 doctor 验收。
#
# 用法：bash deploy/tests/rollback_script_test.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_SRC="$(cd "${SCRIPT_DIR}/.." && pwd)"

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

DEPLOY="${TMP}/deploy with space"
BIN="${TMP}/bin"
FAKE_LOG="${TMP}/fake.log"
EVIL_MARKER="${TMP}/evil-marker"
IMAGE_TAR="${TMP}/previous image.tar"
DATA_BACKUP="${TMP}/data backup.tar.gz"
mkdir -p "${DEPLOY}/data" "${DEPLOY}/rollback" "${BIN}"
cp "${DEPLOY_SRC}/rollback.sh" "${DEPLOY}/rollback.sh"
: >"${DEPLOY}/docker-compose.yml"
: >"${IMAGE_TAR}"
mkdir -p "${TMP}/backup-src/data"
: >"${TMP}/backup-src/data/keep"
tar -czf "${DATA_BACKUP}" -C "${TMP}/backup-src" data

cat >"${DEPLOY}/rollback/rollback.env" <<EOF
ROLLBACK_IMAGE=elbot:old
ROLLBACK_IMAGE_TAR=${IMAGE_TAR}
ROLLBACK_DATA_BACKUP=${DATA_BACKUP}
ROLLBACK_FROM_IMAGE=sha256:old-image-id
IGNORED=\$(touch "${EVIL_MARKER}")
EOF

cat >"${DEPLOY}/restore-verify.sh" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
[ -f "$1" ] || { echo "missing archive $1" >&2; exit 1; }
printf 'verify image=%s start=%s archive=%s\n' "${RESTORE_VERIFY_IMAGE:-}" "${RESTORE_VERIFY_START:-}" "$1" >>"${FAKE_LOG}"
STUB
chmod +x "${DEPLOY}/restore-verify.sh"

cat >"${BIN}/docker" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
printf 'docker %s\n' "$*" >>"${FAKE_LOG}"
case "${1:-}" in
    load|image|tag|save|rm)
        exit 0
        ;;
    inspect)
        case "$*" in
            *State.Health*) printf 'healthy\n' ;;
            *) printf 'fake-container-id\n' ;;
        esac
        exit 0
        ;;
    compose)
        exit 0
        ;;
esac
exit 0
STUB
chmod +x "${BIN}/docker"

export PATH="${BIN}:${PATH}"
export FAKE_LOG

fail=0
note() { echo "FAIL: $*"; fail=1; }

echo "== rollback happy path =="
if ! ROLLBACK_CONFIRM=1 bash "${DEPLOY}/rollback.sh" >"${TMP}/rollback.log" 2>&1; then
    cat "${TMP}/rollback.log"
    note "rollback.sh 执行失败"
fi

grep -qF "docker load -i ${IMAGE_TAR}" "${FAKE_LOG}" || note "没有先载入旧镜像快照"
grep -qF "verify image=elbot:old start=required" "${FAKE_LOG}" || note "没有用旧镜像和 required 模式验证备份"
grep -qF "curl -fsS --max-time 3 http://127.0.0.1:32171/ready" "${FAKE_LOG}" || note "回滚后没有等待 /ready"
grep -qF "elbot doctor --no-model" "${FAKE_LOG}" || note "回滚后没有运行 doctor 验收"
[ -e "${EVIL_MARKER}" ] && note "rollback.env 中的未知键被 eval 执行了"
[ -d "${DEPLOY}/data" ] || note "回滚后 data 目录不存在"

if [ "${fail}" -eq 0 ]; then
    echo "rollback_script_test: passed"
fi
exit "${fail}"
