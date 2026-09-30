# syntax=docker/dockerfile:1.7
#
# ElBot 容器镜像
# 默认构建精简运行镜像（runtime target），如需容器内编译 Go Skill，
# 使用：docker build --target go-runtime -f deploy/Dockerfile .
#
# 构建：
#   docker build -f deploy/Dockerfile -t elbot:0.5.0 .
# 指定版本号：
#   docker build -f deploy/Dockerfile --build-arg VERSION=0.5.0 -t elbot:0.5.0 .

ARG GO_VERSION=1.26

# ============================================================
# 构建阶段
# ============================================================
FROM golang:${GO_VERSION}-bookworm AS builder

WORKDIR /src

# 先复制依赖清单，利用 Docker 层缓存
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# 复制源码并静态编译（CGO_ENABLED=0，产物不依赖 glibc）
COPY . .
ARG VERSION=0.5.0
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/elbot ./cmd/elbot

# ============================================================
# 运行阶段（默认）：精简镜像，约 150-250MB
# ============================================================
FROM debian:bookworm-slim AS runtime

ARG DEBIAN_FRONTEND=noninteractive

# XDG_CONFIG_HOME / XDG_DATA_HOME 指向 /data，首次运行自动生成默认配置到
# /data/config/elbot/ ，运行数据（SQLite、日志、sandbox）到 /data/elbot/ 。
ENV TZ=Asia/Shanghai \
    XDG_CONFIG_HOME=/data/config \
    XDG_DATA_HOME=/data \
    XDG_RUNTIME_DIR=/data/run \
    HOME=/home/elbot \
    LANG=C.UTF-8 \
    ELBOT_HEALTH_ADDR=0.0.0.0:32171

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        ca-certificates \
        tzdata \
        tini \
        curl \
        wget \
        git \
        jq \
        unzip \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd -g 10001 elbot \
 && useradd -u 10001 -g 10001 -d /home/elbot -m -s /usr/sbin/nologin elbot \
 && mkdir -p /data/config /data/run /home/elbot \
 && chown -R elbot:elbot /data /home/elbot \
 && chmod 750 /data/run

COPY --from=builder /out/elbot /usr/local/bin/elbot

USER elbot:elbot
WORKDIR /home/elbot

VOLUME ["/data"]

# 32170 = Elnis HTTP 入口；32171 = 独立健康接口；32172 = CLI 远程 WebSocket 服务端
# docker-compose 中只映射到宿主机 127.0.0.1，公网访问请走宝塔 Nginx 反向代理。
EXPOSE 32170 32171 32172

# 使用独立 /live 心跳判断关键调度循环是否推进；不再只看 PID。
# /live 不判断模型 API、QQ OneBot / Telegram 或 CLI / Elnis 的完整连通性，
# 部署验收仍需实际发一条消息并按 deploy/README.md 第 12 节检查。
HEALTHCHECK --interval=30s --timeout=5s --start-period=45s --retries=3 \
    CMD ["curl", "-fsS", "--max-time", "3", "http://127.0.0.1:32171/live"]

# 用 tini 作为 PID 1，保证 docker stop / systemd 的 SIGTERM 能转发给 ElBot，
# ElBot 自身会在 30s 内优雅关闭。
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/elbot"]
CMD ["service", "run"]

# ============================================================
# 运行阶段（可选）：带 Go 工具链，用于容器内编译 Go Skill
# 镜像较大（约 1GB+），仅在确实使用 Go Skill 时选择。
# ============================================================
FROM golang:${GO_VERSION}-bookworm AS go-runtime

ARG DEBIAN_FRONTEND=noninteractive

ENV TZ=Asia/Shanghai \
    XDG_CONFIG_HOME=/data/config \
    XDG_DATA_HOME=/data \
    XDG_RUNTIME_DIR=/data/run \
    HOME=/home/elbot \
    LANG=C.UTF-8 \
    ELBOT_GO_BINARY=/usr/local/go/bin/go \
    ELBOT_HEALTH_ADDR=0.0.0.0:32171

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        ca-certificates \
        tzdata \
        tini \
        curl \
        git \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd -g 10001 elbot \
 && useradd -u 10001 -g 10001 -d /home/elbot -m -s /usr/sbin/nologin elbot \
 && mkdir -p /data/config /data/run /home/elbot \
 && chown -R elbot:elbot /data /home/elbot \
 && chmod 750 /data/run

COPY --from=builder /out/elbot /usr/local/bin/elbot

USER elbot:elbot
WORKDIR /home/elbot

VOLUME ["/data"]
EXPOSE 32170 32171 32172

HEALTHCHECK --interval=30s --timeout=5s --start-period=45s --retries=3 \
    CMD ["curl", "-fsS", "--max-time", "3", "http://127.0.0.1:32171/live"]

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/elbot"]
CMD ["service", "run"]
