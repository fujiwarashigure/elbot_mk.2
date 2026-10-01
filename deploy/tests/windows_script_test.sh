#!/usr/bin/env bash
# 静态自测：Windows 本地部署入口的编码、包装和文档版本号。
#
# 真实运行需要 Windows + Docker Desktop；本测试只保证：
#   - PowerShell 5.1 能正确读取 UTF-8 BOM（否则中文会被按 ANSI 解析）；
#   - elbot.cmd 是 CRLF 且调用 elbot.ps1；
#   - README / CHANGELOG 中引用的版本号与 deploy/VERSION 一致。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

PYTHON="${PYTHON:-}"
if [ -z "${PYTHON}" ]; then
    # Windows 的 python3 可能是 Microsoft Store 执行别名（退出码 49），
    # 因此优先选择真正能 import 的解释器。
    for candidate in python3 python; do
        if command -v "${candidate}" >/dev/null 2>&1 &&            "${candidate}" -c 'import sys; sys.exit(0)' >/dev/null 2>&1; then
            PYTHON="${candidate}"
            break
        fi
    done
fi
if [ -z "${PYTHON}" ]; then
    echo "windows_script_test: 需要可用的 python3 / python 来检查字节级约束" >&2
    exit 1
fi

"${PYTHON}" - "${ROOT}" <<'PYEOF'
import sys
from pathlib import Path

root = Path(sys.argv[1])
errors = []

ps1 = root / "deploy" / "windows" / "elbot.ps1"
cmd = root / "deploy" / "windows" / "elbot.cmd"
version_file = root / "deploy" / "VERSION"
win_readme = root / "deploy" / "windows" / "README.md"

if not ps1.is_file():
    errors.append(f"missing {ps1}")
else:
    data = ps1.read_bytes()
    if not data.startswith(b"\xef\xbb\xbf"):
        errors.append("elbot.ps1 must start with a UTF-8 BOM for Windows PowerShell 5.1")
    if b"\r\n" in data:
        errors.append("elbot.ps1 should use LF line endings")

if not cmd.is_file():
    errors.append(f"missing {cmd}")
else:
    data = cmd.read_bytes()
    if b"\r\n" not in data:
        errors.append("elbot.cmd must use CRLF line endings")
    if b"elbot.ps1" not in data or b"powershell.exe" not in data:
        errors.append("elbot.cmd must invoke powershell.exe with elbot.ps1")

if not version_file.is_file():
    errors.append(f"missing {version_file}")
    version = ""
else:
    version = version_file.read_text(encoding="utf-8").strip()
    if not version or version == "dev":
        errors.append(f"unexpected deploy/VERSION: {version!r}")

for rel in [
    "README.md",
    "README.zh-CN.md",
    "CHANGELOG.md",
    "CHANGELOG.en.md",
    "deploy/README.md",
    "deploy/windows/README.md",
]:
    path = root / rel
    if not path.is_file():
        errors.append(f"missing {rel}")
        continue
    text = path.read_text(encoding="utf-8")
    if version and version not in text:
        errors.append(f"{rel} does not mention deploy/VERSION {version}")
    if rel == "deploy/windows/README.md" and "elbot.ps1" not in text:
        errors.append("deploy/windows/README.md does not document elbot.ps1")

if errors:
    for err in errors:
        print("windows_script_test: " + err, file=sys.stderr)
    sys.exit(1)
print(f"windows_script_test: ok (version={version})")
PYEOF
