#!/usr/bin/env bash
# ElBot 数据备份脚本，可在宝塔【计划任务】中每天执行。
# 备份内容：配置、SQLite、记忆、Skill、插件、日志目录结构（不含大文件旧日志可自行排除）。
#
# 用法：bash backup.sh [备份目录]
# 默认备份到 ./backups，保留最近 14 份。
set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DATA_DIR="${DEPLOY_DIR}/data"
BACKUP_DIR="${1:-${DEPLOY_DIR}/backups}"
KEEP="${KEEP:-14}"
STAMP="$(date +%Y%m%d-%H%M%S)"

mkdir -p "${BACKUP_DIR}"

if [ ! -d "${DATA_DIR}" ]; then
    echo "错误：${DATA_DIR} 不存在"
    exit 1
fi

# SQLite 建议先让容器安静下来再复制，或使用 sqlite3 .backup。
# 这里用 tar 直接归档；ElBot 使用 WAL 模式时热备仍可能不一致，
# 更稳妥的做法是短暂 stop 容器或在低峰期执行。
TARGET="${BACKUP_DIR}/elbot-data-${STAMP}.tar.gz"
echo "==> 打包 ${DATA_DIR} -> ${TARGET}"
tar -czf "${TARGET}" -C "${DEPLOY_DIR}" data

echo "==> 清理超过 ${KEEP} 份的旧备份"
ls -1t "${BACKUP_DIR}"/elbot-data-*.tar.gz 2>/dev/null | tail -n +$((KEEP + 1)) | xargs -r rm -f

echo "==> 完成：${TARGET}"
