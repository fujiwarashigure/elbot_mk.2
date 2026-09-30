#!/usr/bin/env bash
# 构建并推送 ElBot 镜像到阿里云 ACR（容器镜像服务）或腾讯云 TCR。
#
# 用法：
#   docker login registry.cn-hangzhou.aliyuncs.com
#   bash build-push.sh registry.cn-hangzhou.aliyuncs.com/<命名空间>/elbot:0.5.0
#
# 腾讯云示例：
#   docker login ccr.ccs.tencentyun.com
#   bash build-push.sh ccr.ccs.tencentyun.com/<命名空间>/elbot:0.5.0
#
# 架构：轻量服务器通常是 amd64。若是 ARM 实例，改成 linux/arm64。
set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${DEPLOY_DIR}/.." && pwd)"
IMAGE="${1:-}"
PLATFORM="${PLATFORM:-linux/amd64}"
VERSION="${ELBOT_VERSION:-0.5.0}"

if [ -z "${IMAGE}" ]; then
    echo "用法: $0 <registry>/<namespace>/elbot:<tag>"
    exit 1
fi

if ! command -v docker >/dev/null 2>&1; then
    echo "错误：未找到 docker"
    exit 1
fi

cd "${REPO_ROOT}"

echo "==> 构建 ${IMAGE} (${PLATFORM})"
if docker buildx version >/dev/null 2>&1; then
    docker buildx build \
        --platform "${PLATFORM}" \
        --build-arg "VERSION=${VERSION}" \
        -f deploy/Dockerfile \
        -t "${IMAGE}" \
        --push \
        .
else
    # 老版本 docker 不支持 buildx，只能构建本机架构
    docker build \
        --build-arg "VERSION=${VERSION}" \
        -f deploy/Dockerfile \
        -t "${IMAGE}" \
        .
    docker push "${IMAGE}"
fi

echo "==> 已推送：${IMAGE}"
echo "服务器拉取：docker pull ${IMAGE}"
