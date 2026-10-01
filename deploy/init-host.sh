#!/usr/bin/env bash
# 在宝塔 / 阿里云 / 腾讯云轻量服务器上初始化 ElBot 部署。
# 用法：bash init-host.sh
set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DATA_DIR="${DEPLOY_DIR}/data"
ELBOT_UID="${ELBOT_UID:-10001}"
ELBOT_GID="${ELBOT_GID:-10001}"

echo "==> 检查 Docker"
if ! command -v docker >/dev/null 2>&1; then
    echo "错误：未找到 docker。请先在宝塔面板【软件商店 -> Docker管理器】安装 Docker。"
    exit 1
fi
if docker compose version >/dev/null 2>&1; then
    COMPOSE="docker compose"
elif command -v docker-compose >/dev/null 2>&1; then
    COMPOSE="docker-compose"
else
    echo "错误：未找到 docker compose / docker-compose。请确认已安装 Compose 插件。"
    exit 1
fi
echo "    使用：${COMPOSE}"

echo "==> 准备数据目录 ${DATA_DIR}"
mkdir -p "${DATA_DIR}/config" "${DATA_DIR}/run" "${DATA_DIR}/logs" "${DATA_DIR}/cache"
chown -R "${ELBOT_UID}:${ELBOT_GID}" "${DATA_DIR}"
chmod 750 "${DATA_DIR}" "${DATA_DIR}/run"

if [ ! -f "${DEPLOY_DIR}/.env" ]; then
    cp "${DEPLOY_DIR}/.env.example" "${DEPLOY_DIR}/.env"
    chmod 600 "${DEPLOY_DIR}/.env"
    echo
    echo "已生成 ${DEPLOY_DIR}/.env"
    echo "请填写其中的 API Key / token 后，重新运行：bash init-host.sh"
    exit 0
fi

echo "==> 构建镜像"
cd "${DEPLOY_DIR}"
${COMPOSE} build

echo "==> 启动容器"
${COMPOSE} up -d --remove-orphans

echo
${COMPOSE} ps
echo
echo "首次启动会自动生成默认配置，位置："
echo "  ${DATA_DIR}/config/elbot/app.toml"
echo "  ${DATA_DIR}/config/elbot/services.toml"
echo "  ${DATA_DIR}/config/elbot/state.toml"
echo "  ${DATA_DIR}/config/elbot/elnis.toml"
echo
echo "首次启动后请核对："
echo "  1. services.toml 的 api_key_env 与 .env 的变量名逐个对应；"
echo "  2. state.toml 的 provider/model 与 services.toml 一致，且模型名真实可用；"
echo "  3. app.toml 的 CLI server / OneBot / Elnis / 生图 / security 配置符合实际接入方式；"
echo "  4. data 目录属主为 ${ELBOT_UID}:${ELBOT_GID}，且不需要 root 才能写入。"
echo
echo "只改 TOML 配置：cd ${DEPLOY_DIR} && ${COMPOSE} restart"
echo "改 .env 后必须：cd ${DEPLOY_DIR} && ${COMPOSE} up -d --remove-orphans --force-recreate"
echo "查看日志：cd ${DEPLOY_DIR} && ${COMPOSE} logs -f --tail=200"
echo "健康接口：curl -sS http://127.0.0.1:32171/live  # 另有 /ready /healthz"
echo "完整验收清单见 README 第 12 节。"
