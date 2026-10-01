#!/usr/bin/env bash
# 备份 / 恢复验证自测：构造一个最小 data 目录，断言
#   1. manifest 与归档内容完全一致，且严格模式恢复验证通过；
#   2. 缺少 manifest、manifest 被篡改、缺少必需配置、TOML 语法错误、
#      本地媒体文件丢失时都不再输出 passed。
#
# 重依赖 sqlite3（严格模式必需）；没有 sqlite3 时整体跳过。
# 用法：bash deploy/tests/backup_restore_test.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_SRC="$(cd "${SCRIPT_DIR}/.." && pwd)"

if ! command -v sqlite3 >/dev/null 2>&1; then
    echo "skip: 未安装 sqlite3，跳过 backup_restore_test"
    exit 0
fi

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

DEPLOY="${TMP}/deploy"
DATA="${DEPLOY}/data"
mkdir -p "${DATA}/config/elbot/characters/hero" "${DATA}/elbot/media/ab" "${DATA}/run"
cp "${DEPLOY_SRC}/backup.sh" "${DEPLOY_SRC}/restore-verify.sh" "${DEPLOY}/"

printf 'mode = "default"\n' >"${DATA}/config/elbot/app.toml"
printf '[providers]\n' >"${DATA}/config/elbot/providers.toml"
printf '[session]\n' >"${DATA}/config/elbot/state.toml"
printf 'hero\n' >"${DATA}/config/elbot/characters/hero/character.toml"
printf 'media-body\n' >"${DATA}/elbot/media/ab/cdef0123"
sqlite3 "${DATA}/sessions.db" "CREATE TABLE media (id TEXT, backend TEXT, local_path TEXT); CREATE TABLE messages (id TEXT); INSERT INTO media VALUES ('media:aa','local','/data/elbot/media/ab/cdef0123');"

fail=0
note() { echo "FAIL: $*"; fail=1; }

echo "== case 1: strict happy path =="
if ! BACKUP_MODE=hot bash "${DEPLOY}/backup.sh" "${TMP}/backups" >"${TMP}/backup.log" 2>&1; then
    cat "${TMP}/backup.log"
    note "backup.sh 失败"
else
    ARCHIVE="$(ls -1 "${TMP}"/backups/elbot-data-*.tar.gz | head -1)"
    cut -d' ' -f3- "${ARCHIVE}.manifest" | sort >"${TMP}/from-manifest.txt"
    tar -tzf "${ARCHIVE}" | grep -v '/$' | sort >"${TMP}/from-archive.txt"
    diff -u "${TMP}/from-archive.txt" "${TMP}/from-manifest.txt" || note "manifest 与归档内容不一致"
    grep -q 'restore_verify: passed' "${TMP}/backup.log" || note "严格模式没有输出 passed"
fi

echo "== case 2: missing manifest =="
cp "${ARCHIVE}" "${TMP}/no-manifest.tar.gz"
if bash "${DEPLOY_SRC}/restore-verify.sh" "${TMP}/no-manifest.tar.gz" >"${TMP}/c2.log" 2>&1; then
    note "缺少 manifest 时严格模式仍然通过"
fi
if ! RESTORE_VERIFY_STRICT=0 bash "${DEPLOY_SRC}/restore-verify.sh" "${TMP}/no-manifest.tar.gz" >"${TMP}/c2b.log" 2>&1; then
    cat "${TMP}/c2b.log"
    note "RESTORE_VERIFY_STRICT=0 时仍无法通过"
fi

echo "== case 3: tampered manifest =="
cp "${ARCHIVE}" "${TMP}/tampered.tar.gz"
echo "$(printf 'x' | sha256sum | cut -d' ' -f1)  data/elbot/media/ab/cdef0123" >>"${TMP}/tampered.tar.gz.manifest"
if bash "${DEPLOY_SRC}/restore-verify.sh" "${TMP}/tampered.tar.gz" >"${TMP}/c3.log" 2>&1; then
    note "manifest 被篡改时仍然通过"
fi

echo "== case 4: missing local media file =="
mkdir -p "${TMP}/stage-media"
tar -xzf "${ARCHIVE}" -C "${TMP}/stage-media"
rm -f "${TMP}/stage-media/data/elbot/media/ab/cdef0123"
tar -czf "${TMP}/broken-media.tar.gz" -C "${TMP}/stage-media" data
if RESTORE_VERIFY_STRICT=0 bash "${DEPLOY_SRC}/restore-verify.sh" "${TMP}/broken-media.tar.gz" >"${TMP}/c4.log" 2>&1; then
    note "本地媒体缺失时仍然通过"
fi

echo "== case 5: missing providers.toml =="
mkdir -p "${TMP}/stage-prov"
tar -xzf "${ARCHIVE}" -C "${TMP}/stage-prov"
rm -f "${TMP}/stage-prov/data/config/elbot/providers.toml"
tar -czf "${TMP}/no-providers.tar.gz" -C "${TMP}/stage-prov" data
if RESTORE_VERIFY_STRICT=0 bash "${DEPLOY_SRC}/restore-verify.sh" "${TMP}/no-providers.tar.gz" >"${TMP}/c5.log" 2>&1; then
    note "缺少 providers.toml 时仍然通过"
fi

echo "== case 6: invalid TOML =="
if command -v python3 >/dev/null 2>&1 && python3 -c 'import tomllib' >/dev/null 2>&1; then
    mkdir -p "${TMP}/stage-toml"
    tar -xzf "${ARCHIVE}" -C "${TMP}/stage-toml"
    printf 'this is [not valid toml\n' >"${TMP}/stage-toml/data/config/elbot/app.toml"
    tar -czf "${TMP}/bad-toml.tar.gz" -C "${TMP}/stage-toml" data
    if RESTORE_VERIFY_STRICT=0 bash "${DEPLOY_SRC}/restore-verify.sh" "${TMP}/bad-toml.tar.gz" >"${TMP}/c6.log" 2>&1; then
        note "TOML 语法错误时仍然通过"
    fi
else
    echo "skip: 没有带 tomllib 的 python3，跳过 TOML 解析用例"
fi

if [ "${fail}" -eq 0 ]; then
    echo "backup_restore_test: passed"
fi
exit "${fail}"
