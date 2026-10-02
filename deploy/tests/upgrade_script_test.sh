#!/usr/bin/env bash
# 升级脚本自测：用假 git / docker 验证
#   1. ELBOT_GIT_REF 通过 repo 根的 rev-parse 识别 git 工作区，而不是假设 deploy/.git；
#   2. 真实镜像 ENTRYPOINT 已包含 elbot，因此 docker run 只能传 "config check"，
#      不能再传一次 elbot。
#
# 不依赖真实 Docker 引擎或真实 ElBot 镜像，可在 CI/开发机上快速执行。
# 用法：bash deploy/tests/upgrade_script_test.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_SRC="$(cd "${SCRIPT_DIR}/.." && pwd)"

TMP="$(mktemp -d)"
export TMP
trap 'rm -rf "${TMP}"' EXIT

ROOT="${TMP}/repo"
DEPLOY="${ROOT}/deploy"
BIN="${TMP}/bin"
mkdir -p "${ROOT}/.git" "${DEPLOY}" "${BIN}" "${DEPLOY}/backups" "${DEPLOY}/data"

cp "${DEPLOY_SRC}/upgrade.sh" "${DEPLOY}/upgrade.sh"
printf '0.6.3\n' >"${DEPLOY}/VERSION"

# 升级脚本只要求这两个脚本存在并成功；具体备份内容由测试夹具模拟。
cat >"${DEPLOY}/backup.sh" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
printf '%s
' "${BACKUP_MODE:-}" >>"${TMP}/backup-mode.log"
mkdir -p "$(dirname "$0")/backups"
: >"$(dirname "$0")/backups/elbot-data-20260101-000000.tar.gz"
STUB
cat >"${DEPLOY}/restore-verify.sh" <<'STUB'
#!/usr/bin/env bash
exit 0
STUB

GIT_LOG="${TMP}/git.log"
DOCKER_LOG="${TMP}/docker.log"
export GIT_LOG DOCKER_LOG FAKE_REPO_ROOT="${ROOT}" FAKE_DEPLOY="${DEPLOY}"

cat >"${BIN}/git" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
printf 'git %s\n' "$*" >>"${GIT_LOG}"
if [ "${1:-}" = "-C" ]; then
    shift 2
fi
case "${1:-}" in
    rev-parse)
        if [ "${2:-}" = "--show-toplevel" ]; then
            printf '%s\n' "${FAKE_REPO_ROOT}"
            exit 0
        fi
        ;;
    fetch)
        exit 0
        ;;
    checkout)
        case "$*" in
            *" v0.6.4"*) printf '0.6.4
' >"${FAKE_DEPLOY}/VERSION" ;;
        esac
        exit 0
        ;;
esac
echo "unexpected git invocation: $*" >&2
exit 1
STUB

cat >"${BIN}/docker" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
printf 'docker %s\n' "$*" >>"${DOCKER_LOG}"
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
        case "$*" in
            *State.Health*) printf 'healthy\n' ;;
            *Config.Image*) printf 'elbot:old-tag\n' ;;
            *.Image*) printf 'sha256:old-image-id\n' ;;
            *) printf 'elbot:old-tag\n' ;;
        esac
        exit 0
        ;;
    run)
        # 镜像 ENTRYPOINT 是 `tini -- elbot`。如果 docker run 的参数里又出现
        # 一个独立的 elbot，就会变成 `elbot elbot config check`。
        for arg in "$@"; do
            if [ "${arg}" = "elbot" ]; then
                echo "fake docker: unexpected extra 'elbot' argument in: $*" >&2
                exit 97
            fi
        done
        exit 0
        ;;
    tag|save|rm)
        exit 0
        ;;
esac
echo "unexpected docker invocation: $*" >&2
exit 1
STUB
chmod +x "${BIN}/git" "${BIN}/docker"

export PATH="${BIN}:${PATH}"
export ELBOT_UPGRADE_SKIP_DOCTOR=1
export ELBOT_VERSION=0.6.3
export ELBOT_IMAGE=elbot:0.6.3

run_upgrade() {
    local label="$1"
    shift
    if ! ( cd "${DEPLOY}" && env "$@" bash upgrade.sh ) >"${TMP}/${label}.log" 2>&1; then
        cat "${TMP}/${label}.log"
        echo "FAIL: ${label} 升级脚本执行失败"
        return 1
    fi
    return 0
}

fail=0
echo "== case 1: ELBOT_GIT_REF uses repository root =="
if ! run_upgrade "with-git-ref" ELBOT_GIT_REF=v0.6.3; then
    fail=1
fi
if ! grep -q -- "-C ${DEPLOY} rev-parse --show-toplevel" "${GIT_LOG}"; then
    echo "FAIL: 没有在 deploy/ 下执行 rev-parse 来发现仓库根目录"
    fail=1
fi
if ! grep -q -- "-C ${ROOT} fetch --tags --force" "${GIT_LOG}"; then
    echo "FAIL: 没有在仓库根目录执行 git fetch"
    fail=1
fi
if ! grep -q -- "-C ${ROOT} checkout --detach v0.6.3" "${GIT_LOG}"; then
    echo "FAIL: 没有在仓库根目录执行 git checkout"
    fail=1
fi
if grep -qE -- "-C ${DEPLOY} (fetch|checkout)" "${GIT_LOG}"; then
    echo "FAIL: 仍然把 deploy/ 当成 git 根目录执行 fetch/checkout"
    fail=1
fi

echo "== case 2: docker run does not pass extra elbot =="
if ! run_upgrade "without-git-ref"; then
    fail=1
fi
if ! grep -q 'run .*config check' "${DOCKER_LOG}"; then
    echo "FAIL: 新镜像配置检查没有执行"
    fail=1
fi

echo "== case 3: GIT_REF alone resolves target version after checkout =="
if ! run_upgrade "git-ref-only" ELBOT_GIT_REF=v0.6.4 ELBOT_VERSION=; then
    fail=1
fi
if ! grep -q '源码版本 0.6.4' "${TMP}/git-ref-only.log"; then
    echo "FAIL: 未在切换源码后解析出 0.6.4 目标版本"
    fail=1
fi

echo "== case 4: upgrade snapshot asks backup.sh for stop mode =="
if ! grep -q '^stop$' "${TMP}/backup-mode.log"; then
    echo "FAIL: 升级前数据快照没有使用 stop 模式"
    fail=1
fi

echo "== case 5: old image snapshot uses actual image ID =="
if ! grep -q -- "docker tag sha256:old-image-id elbot:rollback" "${DOCKER_LOG}"; then
    echo "FAIL: 没有按实际 image ID 保存旧镜像"
    fail=1
fi
if grep -q -- "docker tag elbot:old-tag elbot:rollback" "${DOCKER_LOG}"; then
    echo "FAIL: 仍然按 .Config.Image 标签保存旧镜像"
    fail=1
fi

if [ "${fail}" -eq 0 ]; then
    echo "upgrade_script_test: passed"
fi
exit "${fail}"
