#!/usr/bin/env bash
# 重新生成 ElBot 离线 / 预编译部署产物。
#
# 前置：Go 1.26 工具链（GOROOT 已设置或用 PATH 找到 go）；
#       模块依赖需已在本地缓存，或在能访问 proxy.golang.org 的网络下执行。
#
# 用法：
#   bash prepare-offline.sh
#   GOROOT=/usr/local/go PATH=/usr/local/go/bin:$PATH bash prepare-offline.sh
set -euo pipefail

# 选择可用的 Python 3 解释器；可用 PYTHON=... 覆盖
if [ -z "${PYTHON:-}" ]; then
    if command -v python3 >/dev/null 2>&1 && python3 -c 'import sys; sys.exit(0 if sys.version_info[0] == 3 else 1)' >/dev/null 2>&1; then
        PYTHON=python3
    elif command -v python >/dev/null 2>&1; then
        PYTHON=python
    else
        echo "错误：未找到 python3 / python" >&2
        exit 1
    fi
fi

PACK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "${PACK_DIR}/.." && pwd)"
REPO_ROOT="$(cd "${DEPLOY_DIR}/.." && pwd)"
DIST_DIR="${DEPLOY_DIR}/dist"

# 版本号单点来源：deploy/VERSION（可用 ELBOT_VERSION 覆盖）
if [ -n "${ELBOT_VERSION:-}" ]; then
    VERSION="${ELBOT_VERSION}"
elif [ -f "${DEPLOY_DIR}/VERSION" ]; then
    VERSION="$(tr -d '[:space:]' <"${DEPLOY_DIR}/VERSION")"
else
    VERSION="dev"
fi

# 把离线包内可能被 Windows 检出成 CRLF 的文本文件统一成 LF。
# 否则 Linux 上 ./deploy.sh 会报 "bad interpreter: /usr/bin/env bash^M"，
# systemd unit 也可能解析失败。
normalize_line_endings() {
    local dir="$1"
    "${PYTHON}" - "$dir" <<'PYEOF'
import pathlib
import sys

CRLF = (chr(13) + chr(10)).encode()
LF = chr(10).encode()
SUFFIXES = {".sh", ".service", ".timer", ".conf", ".yml", ".yaml", ".example"}
NAMES = {"Dockerfile"}

root = pathlib.Path(sys.argv[1])
changed = 0
for path in sorted(root.rglob("*")):
    if not path.is_file():
        continue
    if path.suffix not in SUFFIXES and path.name not in NAMES:
        continue
    data = path.read_bytes()
    if CRLF in data:
        path.write_bytes(data.replace(CRLF, LF))
        changed += 1
print("    CRLF -> LF:", changed)
PYEOF
}

if ! command -v go >/dev/null 2>&1; then
    echo "错误：未找到 go。请安装 Go 1.26，或设置 GOROOT/PATH。" >&2
    exit 1
fi

mkdir -p "${DIST_DIR}"
cd "${REPO_ROOT}"

# 1. 交叉编译静态二进制
for arch in amd64 arm64; do
    echo "==> 编译 linux/${arch}"
    GOFLAGS="${GOFLAGS:--mod=mod}" CGO_ENABLED=0 GOOS=linux GOARCH="${arch}" \
        go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
        -o "${DIST_DIR}/elbot-linux-${arch}" ./cmd/elbot
done

# 2. 生成 docker load 用的镜像 tar
for arch in amd64 arm64; do
    echo "==> 生成镜像 tar linux/${arch}"
    "${PYTHON}" "${PACK_DIR}/build-image-tar.py" \
        --binary "${DIST_DIR}/elbot-linux-${arch}" \
        --arch "${arch}" \
        --version "${VERSION}" \
        --output "${DIST_DIR}/elbot-${VERSION}-linux-${arch}.tar.gz"
done

# 3. 组装完整功能离线包
for arch in amd64 arm64; do
    out="${DIST_DIR}/offline-${arch}"
    echo "==> 组装 ${out}"
    rm -rf "${out}"
    mkdir -p "${out}"
    cp "${DIST_DIR}/elbot-linux-${arch}" "${out}/elbot"
    cp "${PACK_DIR}/Dockerfile.prebuilt" "${out}/Dockerfile"
    cp "${PACK_DIR}/docker-compose.prebuilt.yml" "${out}/docker-compose.yml"
    cp "${DEPLOY_DIR}/.env.example" "${out}/.env.example"
    cp "${DEPLOY_DIR}/nginx-elbot.conf" "${out}/nginx-elbot.conf"
    cp "${DEPLOY_DIR}/elbot-compose.service" "${out}/elbot-compose.service"
    cp "${DEPLOY_DIR}/elbot-watchdog.sh" "${out}/elbot-watchdog.sh"
    cp "${DEPLOY_DIR}/elbot-watchdog.service" "${out}/elbot-watchdog.service"
    cp "${DEPLOY_DIR}/elbot-watchdog.timer" "${out}/elbot-watchdog.timer"
    cp "${DEPLOY_DIR}/watchdog.env.example" "${out}/watchdog.env.example"
    cp "${DEPLOY_DIR}/backup.sh" "${out}/backup.sh"
    cp "${DEPLOY_DIR}/init-host.sh" "${out}/init.sh"
    cp "${PACK_DIR}/README-OFFLINE.md" "${out}/README.md"
    cp "${PACK_DIR}/deploy-lighthouse.sh" "${out}/deploy.sh"
    cp "${PACK_DIR}/README-ALIYUN.md" "${out}/README-ALIYUN.md"
    cp "${PACK_DIR}/README-BAOTA-CONFIG.md" "${out}/README-BAOTA-CONFIG.md"
    # 把版本号拷进离线包：deploy.sh / README 里都用它，避免多处硬编码
    cp "${DEPLOY_DIR}/VERSION" "${out}/VERSION"
    mkdir -p "${out}/data"
    chmod +x "${out}/backup.sh" "${out}/init.sh" "${out}/deploy.sh" "${out}/elbot-watchdog.sh" 2>/dev/null || true
    normalize_line_endings "${out}"
done

# 3.5 打包成单文件离线包
for arch in amd64 arm64; do
    echo "==> 打包 offline-${arch}"
    tar -czf "${DIST_DIR}/elbot-${VERSION}-offline-${arch}.tar.gz" \
        -C "${DIST_DIR}" "offline-${arch}"
done

# 4. 校验 + 校验和
"${PYTHON}" "${PACK_DIR}/verify-image-tar.py" \
    --tag "elbot:${VERSION}" \
    "${DIST_DIR}/elbot-${VERSION}-linux-amd64.tar.gz" \
    "${DIST_DIR}/elbot-${VERSION}-linux-arm64.tar.gz"

cp "${PACK_DIR}/README-OFFLINE.md" "${DIST_DIR}/README-OFFLINE.md"
cp "${PACK_DIR}/README-BAOTA-CONFIG.md" "${DIST_DIR}/README-BAOTA-CONFIG.md"
cp "${PACK_DIR}/build-image-tar.py" "${DIST_DIR}/build-image-tar.py"
cp "${PACK_DIR}/verify-image-tar.py" "${DIST_DIR}/verify-image-tar.py"

cd "${DIST_DIR}"
if command -v sha256sum >/dev/null 2>&1; then
    SHA_TOOL="sha256sum"
else
    SHA_TOOL="shasum -a 256"
fi
$SHA_TOOL elbot-linux-amd64 elbot-linux-arm64 \
    "elbot-${VERSION}-linux-amd64.tar.gz" \
    "elbot-${VERSION}-linux-arm64.tar.gz" \
    "elbot-${VERSION}-offline-amd64.tar.gz" \
    "elbot-${VERSION}-offline-arm64.tar.gz" > SHA256SUMS

echo "==> 完成，产物在 ${DIST_DIR}"
ls -la "${DIST_DIR}"
