#!/usr/bin/env bash
# 构建并推送 ElBot 镜像到阿里云 ACR（容器镜像服务）或腾讯云 TCR。
#
# 用法：
#   docker login registry.cn-hangzhou.aliyuncs.com
#   bash build-push.sh registry.cn-hangzhou.aliyuncs.com/<命名空间>/elbot:0.6.6
#
# 腾讯云示例：
#   docker login ccr.ccs.tencentyun.com
#   bash build-push.sh ccr.ccs.tencentyun.com/<命名空间>/elbot:0.6.6
#
# 架构（可多选，逗号分隔）：
#   PLATFORM=linux/amd64,linux/arm64 bash build-push.sh <image>
#   默认 linux/amd64。多平台必须使用 buildx，且会直接 --push（buildx 不支持把
#   多平台镜像同时 --load 到本地 docker images）。
#
# 其他可用环境变量：
#   ELBOT_VERSION   覆盖版本号（默认读 deploy/VERSION）
#   GO_VERSION      覆盖 Go 版本（默认 1.26）
#   GO_BASE_IMAGE / RUNTIME_BASE_IMAGE   覆盖基础镜像（国内加速 / pin digest）
#   BUILDX_EXTRA_ARGS  追加给 buildx 的额外参数，例如
#                      BUILDX_EXTRA_ARGS="--provenance=true --sbom=true"
set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${DEPLOY_DIR}/.." && pwd)"
IMAGE="${1:-}"
PLATFORM="${PLATFORM:-linux/amd64}"

# 版本号单点来源：deploy/VERSION（可用 ELBOT_VERSION 覆盖）
default_version() {
    if [ -f "${DEPLOY_DIR}/VERSION" ]; then
        tr -d '[:space:]' <"${DEPLOY_DIR}/VERSION"
    else
        printf 'dev'
    fi
}
VERSION="${ELBOT_VERSION:-$(default_version)}"

if [ -z "${IMAGE}" ]; then
    echo "用法: $0 <registry>/<namespace>/elbot:<tag>" >&2
    exit 1
fi

if ! command -v docker >/dev/null 2>&1; then
    echo "错误：未找到 docker" >&2
    exit 1
fi

cd "${REPO_ROOT}"

build_args=(
    --build-arg "VERSION=${VERSION}"
    --build-arg "GO_VERSION=${GO_VERSION:-1.26}"
)
if [ -n "${GO_BASE_IMAGE:-}" ]; then
    build_args+=(--build-arg "GO_BASE_IMAGE=${GO_BASE_IMAGE}")
fi
if [ -n "${RUNTIME_BASE_IMAGE:-}" ]; then
    build_args+=(--build-arg "RUNTIME_BASE_IMAGE=${RUNTIME_BASE_IMAGE}")
fi
if [ -n "${EXTRA_TOOLS:-}" ]; then
    build_args+=(--build-arg "EXTRA_TOOLS=${EXTRA_TOOLS}")
fi
if [ -n "${GOPROXY:-}" ]; then
    build_args+=(--build-arg "GOPROXY=${GOPROXY}")
fi
if [ -n "${APT_MIRROR:-}" ]; then
    build_args+=(--build-arg "APT_MIRROR=${APT_MIRROR}")
fi

# shellcheck disable=SC2206  # 需要按空白拆分成多个参数
extra_args=(${BUILDX_EXTRA_ARGS:-})

echo "==> 构建 ${IMAGE} (platform=${PLATFORM}, version=${VERSION})"

if docker buildx version >/dev/null 2>&1; then
    # 多平台只能 --push；单平台同样直接推送，行为统一。
    docker buildx build \
        --platform "${PLATFORM}" \
        "${build_args[@]}" \
        ${extra_args[@]+"${extra_args[@]}"} \
        -f deploy/Dockerfile \
        -t "${IMAGE}" \
        --push \
        .
else
    # 无 buildx：只能用经典构建。deploy/Dockerfile 依赖 BuildKit（RUN --mount、
    # # syntax 指令），因此这里必须显式开启 BuildKit，否则会报
    # "the --mount option requires BuildKit"。
    if [ "${PLATFORM}" != "linux/amd64" ] && [ "${PLATFORM}" != "linux/arm64" ]; then
        echo "错误：无 buildx 时 PLATFORM 只能是单一平台，当前为 ${PLATFORM}" >&2
        echo "      请安装 docker buildx 插件，或改用 PLATFORM=linux/amd64" >&2
        exit 1
    fi
    echo "!! 未检测到 buildx，退回经典构建（仅当前机器架构）"
    DOCKER_BUILDKIT=1 docker build \
        "${build_args[@]}" \
        -f deploy/Dockerfile \
        -t "${IMAGE}" \
        .
    docker push "${IMAGE}"
fi

echo "==> 已推送：${IMAGE}"
echo "服务器拉取：docker pull ${IMAGE}"
echo "服务器使用：在 deploy/.env 里设置 ELBOT_IMAGE=${IMAGE}"
