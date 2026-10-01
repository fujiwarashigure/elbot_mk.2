#!/usr/bin/env bash
# 诊断脱敏自测：构造包含假 API Key / Bearer token / JSON 凭据 / Telegram
# bot token 的 fixture，断言脱敏后原始秘密不再出现、非敏感内容保留、
# 不引入控制字符，重复脱敏是幂等的；并验证独立凭据检测与已知凭据值检测
# 会把问题显式报出来。
#
# 用法：bash deploy/tests/watchdog_redaction_test.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WATCHDOG="${SCRIPT_DIR}/../elbot-watchdog.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

DIR="${TMP}/diag"
mkdir -p "${DIR}"
cat >"${DIR}/docker-logs.txt" <<'FIXTURE'
2026-10-01T00:00:00Z starting elbot
provider api_key=sk-abcdefghijklmnopqrstuvwxyz ok
authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payloadpayload
{"refresh_token":"json-secret-value","password": "pw-123456"}
https://api.telegram.org/bot123456789:AAFakeTelegramToken/getMe failed
https://user:secret-pass@example.com/hook
plain=keepme
FIXTURE

if ! bash "${WATCHDOG}" --redact-dir "${DIR}" >"${TMP}/redact.log" 2>&1; then
    cat "${TMP}/redact.log"
    echo "FAIL: --redact-dir exited non-zero"
    exit 1
fi

fail=0
for secret in \
    'sk-abcdefghijklmnopqrstuvwxyz' \
    'eyJhbGciOiJIUzI1NiJ9.payloadpayload' \
    'json-secret-value' \
    'pw-123456' \
    'AAFakeTelegramToken' \
    'secret-pass'; do
    if grep -qF "${secret}" "${DIR}/docker-logs.txt"; then
        echo "FAIL: 脱敏后仍能找到原始秘密：${secret}"
        fail=1
    fi
done

grep -qF 'plain=keepme' "${DIR}/docker-logs.txt" || {
    echo "FAIL: 非敏感内容被误删"
    fail=1
}

if od -An -v -tx1 "${DIR}/docker-logs.txt" | grep -qE '(^|[[:space:]])01([[:space:]]|$)'; then
    echo "FAIL: 脱敏输出中出现了控制字符 0x01"
    fail=1
fi

cp -f "${DIR}/docker-logs.txt" "${TMP}/once.txt"
rm -f "${DIR}/REDACTION-FAILED"
if ! bash "${WATCHDOG}" --redact-dir "${DIR}" >"${TMP}/redact2.log" 2>&1; then
    cat "${TMP}/redact2.log"
    echo "FAIL: 第二次脱敏执行失败"
    fail=1
fi
cmp -s "${TMP}/once.txt" "${DIR}/docker-logs.txt" || {
    echo "FAIL: 脱敏不是幂等的（第二次仍在改动内容）"
    fail=1
}
[ -e "${DIR}/REDACTION-FAILED" ] && {
    echo "FAIL: 出现了 REDACTION-FAILED 标记"
    fail=1
}

# 独立模式检测：选择 REDACT_SED 不会覆盖的原始 JWT（且不带 token= 前缀），
# 如果 verify_redaction 只是重复 sed，这个用例会错误通过。
PATTERN_DIR="${TMP}/pattern"
mkdir -p "${PATTERN_DIR}"
printf 'raw=%s\n' 'eyJhbGciOiJIUzI1NiJ9.header1234567890.signature1234567890' >"${PATTERN_DIR}/notes.txt"
if bash "${WATCHDOG}" --redact-dir "${PATTERN_DIR}" >"${TMP}/pattern.log" 2>&1; then
    echo "FAIL: 独立凭据检测没有发现未脱敏 JWT"
    fail=1
fi
[ -e "${PATTERN_DIR}/REDACTION-FAILED" ] || {
    echo "FAIL: 独立凭据检测失败后没有留下 REDACTION-FAILED 标记"
    fail=1
}

# 已知凭据值检测：WATCHDOG_TEST_SECRET 的值不在 REDACT_SED 的通用模式里，
# 必须靠“当前环境敏感变量值”这条独立路径发现。
KNOWN_DIR="${TMP}/known"
mkdir -p "${KNOWN_DIR}"
KNOWN_SECRET='known-secret-value-abcdef123456'
printf 'plain note: %s\n' "${KNOWN_SECRET}" >"${KNOWN_DIR}/notes.txt"
if env WATCHDOG_TEST_SECRET="${KNOWN_SECRET}" bash "${WATCHDOG}" --redact-dir "${KNOWN_DIR}" >"${TMP}/known.log" 2>&1; then
    echo "FAIL: 已知凭据值检测没有发现环境变量里的秘密"
    fail=1
fi
[ -e "${KNOWN_DIR}/REDACTION-FAILED" ] || {
    echo "FAIL: 已知凭据值检测失败后没有留下 REDACTION-FAILED 标记"
    fail=1
}

# 脱敏失败必须显式报错，不能被静默吞掉。
READONLY="${TMP}/readonly"
mkdir -p "${READONLY}"
echo 'api_key=sk-readonly-secret-value' > "${READONLY}/log.txt"
chmod 500 "${READONLY}"
if touch "${READONLY}/probe" 2>/dev/null; then
    echo "skip: 当前环境无法用目录权限制造写失败（root 或非 POSIX 挂载）"
    rm -f "${READONLY}/probe"
else
    if bash "${WATCHDOG}" --redact-dir "${READONLY}" >"${TMP}/readonly.log" 2>&1; then
        echo "FAIL: 脱敏写入失败时脚本仍然返回成功"
        fail=1
    fi
fi
chmod 700 "${READONLY}"

if [ "${fail}" -eq 0 ]; then
    echo "watchdog_redaction_test: passed"
fi
exit "${fail}"
