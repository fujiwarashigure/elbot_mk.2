# syntax=docker/dockerfile:1.7
#
# ElBot 容器镜像
#
# 默认构建运行镜像（runtime target）；如需在容器内编译 Go Skill：
#   docker build --target go-runtime -f deploy/Dockerfile .
#
# 本机构建（版本号取自 deploy/VERSION，也可显式传入）：
#   docker build -f deploy/Dockerfile \
#     --build-arg VERSION="$(cat deploy/VERSION)" -t "elbot:$(cat deploy/VERSION)" .
#
# 多架构（需 buildx；一次产出 amd64 + arm64 的 manifest list）：
#   docker buildx build --platform linux/amd64,linux/arm64 \
#     -f deploy/Dockerfile --build-arg VERSION=0.6.6 \
#     --push -t <registry>/<namespace>/elbot:0.6.6 .
#
# 基础镜像可以覆盖（国内加速 / pin digest）：
#   --build-arg GO_BASE_IMAGE=registry.cn-hangzhou.aliyuncs.com/library/golang
#   --build-arg RUNTIME_BASE_IMAGE=debian:bookworm-slim@sha256:<digest>
# 运行镜像里的系统命令默认只增不减（Agent 的 shell 工具能力优先）：
# 需要更多命令用 --build-arg "EXTRA_TOOLS=xxx" 追加；确实要精简时改用
# deploy/pack 的 scratch 预编译镜像（无任何命令，shell 工具不可用）。

ARG GO_VERSION=1.26
ARG GO_BASE_IMAGE=golang
ARG RUNTIME_BASE_IMAGE=debian:bookworm-slim

# ============================================================
# 构建阶段
# ============================================================
# 固定在构建机架构上跑（原生速度），产出的 GOARCH 由 TARGETARCH 决定：
# 这样 buildx --platform linux/arm64 时，不会让整个 Go 构建阶段都走 QEMU 模拟。
FROM --platform=$BUILDPLATFORM ${GO_BASE_IMAGE}:${GO_VERSION}-bookworm AS builder

ARG TARGETOS
ARG TARGETARCH

# Go 模块代理。国内直连 proxy.golang.org 会超时/失败，构建时传：
#   --build-arg GOPROXY=https://goproxy.cn,direct
ARG GOPROXY
ENV GOPROXY=${GOPROXY:-https://proxy.golang.org,direct}

WORKDIR /src

# 先复制依赖清单，利用 Docker 层缓存
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# 复制源码并静态编译（CGO_ENABLED=0，产物不依赖 glibc）
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 \
        GOOS="${TARGETOS:-linux}" \
        GOARCH="${TARGETARCH:-$(go env GOARCH)}" \
        go build -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/elbot ./cmd/elbot

# ============================================================
# 运行阶段（默认）
#
# 这里的“工具”指 ElBot 的 shell 工具能调用的系统命令：保留得越全，Agent 能做的
# 事越多。用 EXTRA_TOOLS 追加更多命令，不要在 apt 列表里删除已有命令。
# ============================================================
FROM ${RUNTIME_BASE_IMAGE} AS runtime

ARG DEBIAN_FRONTEND=noninteractive
ARG VERSION=dev

# EXTRA_TOOLS 只用于“追加”更多命令；下面的默认命令列表是内联的，不依赖它，
# 因此不会受 build-arg 解析差异影响。ping 依赖容器的 NET_RAW capability
# （Docker 默认就有，勿轻易 cap_drop: ALL）。
ARG EXTRA_TOOLS=

# XDG_CONFIG_HOME / XDG_DATA_HOME 指向 /data，首次运行自动生成默认配置到
# /data/config/elbot/ ，运行数据（SQLite、日志、sandbox）到 /data/elbot/ 。
# XDG_CACHE_HOME 也放在 /data：Go Skill 编译缓存、媒体处理临时文件等落在卷上，
# 重建容器后依然可用。
ENV TZ=Asia/Shanghai \
    XDG_CONFIG_HOME=/data/config \
    XDG_DATA_HOME=/data \
    XDG_CACHE_HOME=/data/cache \
    XDG_RUNTIME_DIR=/data/run \
    HOME=/home/elbot \
    LANG=C.UTF-8 \
    ELBOT_HEALTH_ADDR=0.0.0.0:32171

# debian:bookworm-slim 里没有 CA 证书，直接切 https 源会报
# "No system certificates available"；先从 builder（buildpack-deps 基础，自带 CA）
# 拷一份 bundle，后面安装 ca-certificates 时会正规化。
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# APT_MIRROR：把镜像内的 deb.debian.org 换成国内镜像（如 mirrors.aliyun.com、
# mirrors.cloud.tencent.com）。实测本网络直连 deb.debian.org 只有几十 KB/s，
# 而阿里云/腾讯云 https 有 1~6 MB/s。
# 另外：这台网络的明文 HTTP(80) 会被黑洞（实测 http 卡死 / https 6MB/s），
# 所以无论是否换镜像，都把源强制改成 https。
# 留空 = 保持 deb.debian.org（仍强制 https），适合境外 CI。
ARG APT_MIRROR=

RUN for f in /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources /etc/apt/sources.list.d/*.list; do \
        [ -f "${f}" ] || continue; \
        sed -i "s|http://deb[.]debian[.]org|https://deb.debian.org|g" "${f}" 2>/dev/null || true; \
        if [ -n "${APT_MIRROR}" ]; then \
            sed -i "s|deb[.]debian[.]org|${APT_MIRROR}|g" "${f}" 2>/dev/null || true; \
        fi; \
    done \
 && apt-get update \
 && apt-get install -y --no-install-recommends \
        ca-certificates \
        tzdata \
        tini \
        curl \
        wget \
        git \
        jq \
        unzip \
        bash \
        procps \
        iputils-ping \
        dnsutils \
        netcat-openbsd \
        iproute2 \
        less \
        file \
        tree \
        tar \
        gzip \
        xz-utils \
        rsync \
        zip \
        python3 \
        ${EXTRA_TOOLS} \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd -g 10001 elbot \
 && useradd -u 10001 -g 10001 -d /home/elbot -m -s /usr/sbin/nologin elbot \
 && mkdir -p /data/config /data/cache /data/run /home/elbot \
 && chown -R elbot:elbot /data /home/elbot \
 && chmod 750 /data/run

COPY --from=builder /out/elbot /usr/local/bin/elbot

LABEL org.opencontainers.image.title="elbot" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.source="https://github.com/fujiwarashigure/elbot_mk.2" \
      org.opencontainers.image.licenses="Apache-2.0"

USER elbot:elbot
WORKDIR /home/elbot

VOLUME ["/data"]

# 32170 = Elnis HTTP 入口；32171 = 独立健康接口；32172 = CLI 远程 WebSocket 服务端
# docker-compose 中只映射到宿主机 127.0.0.1，公网访问请走宝塔 Nginx 反向代理。
EXPOSE 32170 32171 32172

# 使用独立 /live 判断进程是否还能响应；不再只看 PID。
# /live 不判断调度心跳、模型 API、QQ OneBot / Telegram 或 CLI / Elnis 的完整连通性，
# 调度/数据目录就绪看 /ready，部署验收仍需实际发消息并按 deploy/README.md 第 12 节检查。
HEALTHCHECK --interval=30s --timeout=5s --start-period=45s --retries=3 \
    CMD ["curl", "-fsS", "--max-time", "3", "http://127.0.0.1:32171/live"]

# 用 tini 作为 PID 1，保证 docker stop / systemd 的 SIGTERM 能转发给 ElBot，
# ElBot 自身会在 30s 内优雅关闭。
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/elbot"]
CMD ["service", "run"]

# ============================================================
# 运行阶段（可选）：带 Go 工具链，用于容器内编译 Go Skill
# 镜像较大（约 1GB+），仅在确实使用 Go Skill 时选择。
#
# 关键点：官方 golang 镜像的 GOPATH=/go 属于 root，而容器以 10001 运行，
# 直接 go build 带第三方依赖的技能会 permission denied。这里把 GOPATH /
# GOMODCACHE / GOCACHE 全部改到 10001 可写的位置（并落在 /data 卷上），
# 同时保留 GOTOOLCHAIN=auto，让技能需要的工具链能正常自动下载。
# ============================================================
FROM ${GO_BASE_IMAGE}:${GO_VERSION}-bookworm AS go-runtime

ARG DEBIAN_FRONTEND=noninteractive
ARG VERSION=dev
ARG EXTRA_TOOLS=
# Go 模块代理（运行时编译 Go Skill 也要用）：国内传
#   --build-arg GOPROXY=https://goproxy.cn,direct
ARG GOPROXY

ENV TZ=Asia/Shanghai \
    XDG_CONFIG_HOME=/data/config \
    XDG_DATA_HOME=/data \
    XDG_CACHE_HOME=/data/cache \
    XDG_RUNTIME_DIR=/data/run \
    HOME=/home/elbot \
    LANG=C.UTF-8 \
    ELBOT_GO_BINARY=/usr/local/go/bin/go \
    GOTOOLCHAIN=auto \
    GOPROXY=${GOPROXY:-https://proxy.golang.org,direct} \
    GOPATH=/home/elbot/go \
    GOMODCACHE=/data/cache/gomod \
    GOCACHE=/data/cache/go-build \
    PATH=/usr/local/go/bin:/home/elbot/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    ELBOT_HEALTH_ADDR=0.0.0.0:32171

ARG APT_MIRROR=

RUN for f in /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources /etc/apt/sources.list.d/*.list; do \
        [ -f "${f}" ] || continue; \
        sed -i "s|http://deb[.]debian[.]org|https://deb.debian.org|g" "${f}" 2>/dev/null || true; \
        if [ -n "${APT_MIRROR}" ]; then \
            sed -i "s|deb[.]debian[.]org|${APT_MIRROR}|g" "${f}" 2>/dev/null || true; \
        fi; \
    done \
 && apt-get update \
 && apt-get install -y --no-install-recommends \
        ca-certificates \
        tzdata \
        tini \
        curl \
        wget \
        git \
        jq \
        unzip \
        bash \
        procps \
        iputils-ping \
        dnsutils \
        netcat-openbsd \
        iproute2 \
        less \
        file \
        tree \
        tar \
        gzip \
        xz-utils \
        rsync \
        zip \
        python3 \
        ${EXTRA_TOOLS} \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd -g 10001 elbot \
 && useradd -u 10001 -g 10001 -d /home/elbot -m -s /usr/sbin/nologin elbot \
 && mkdir -p /data/config /data/cache /data/cache/gomod /data/run /home/elbot /home/elbot/go \
 && chown -R elbot:elbot /data /home/elbot \
 && chmod 750 /data/run \
 && echo 'export PATH=/usr/local/go/bin:/home/elbot/go/bin:$PATH' > /etc/profile.d/golang-path.sh

COPY --from=builder /out/elbot /usr/local/bin/elbot

LABEL org.opencontainers.image.title="elbot" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.source="https://github.com/fujiwarashigure/elbot_mk.2" \
      org.opencontainers.image.licenses="Apache-2.0"

USER elbot:elbot
WORKDIR /home/elbot

VOLUME ["/data"]
EXPOSE 32170 32171 32172

HEALTHCHECK --interval=30s --timeout=5s --start-period=45s --retries=3 \
    CMD ["curl", "-fsS", "--max-time", "3", "http://127.0.0.1:32171/live"]

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/elbot"]
CMD ["service", "run"]
