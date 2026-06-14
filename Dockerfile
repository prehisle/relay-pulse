# syntax=docker/dockerfile:1.6

# ============================================
# Stage 1: Frontend Builder (Node.js)
# ============================================
ARG FRONTEND_SOURCE=frontend-builder
FROM node:20-alpine AS frontend-builder

# Notifier API URL for subscription feature
ARG VITE_NOTIFIER_API_URL=https://notifier.relaypulse.top
# 是否隐藏公开列表/卡片中的"价格 ¥/$"列（值为 true 时隐藏，默认显示）
ARG VITE_HIDE_PRICE_COLUMN=

WORKDIR /build

# 复制 package.json 和 lock 文件,利用缓存
COPY frontend/package*.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --legacy-peer-deps

# 复制前端源代码
COPY frontend/ ./

# 构建生产版本（传入环境变量）
ENV VITE_NOTIFIER_API_URL=${VITE_NOTIFIER_API_URL}
ENV VITE_HIDE_PRICE_COLUMN=${VITE_HIDE_PRICE_COLUMN}
RUN npm run build

# ============================================
# Stage 1.5: 预构建前端（CI artifact 复用）
# ============================================
FROM scratch AS frontend-prebuilt
COPY frontend/dist/ /build/dist/

# 选择前端来源（默认构建，CI 传 FRONTEND_SOURCE=frontend-prebuilt）
FROM ${FRONTEND_SOURCE} AS frontend

# ============================================
# Stage 2: Backend Builder (Go)
# ============================================
FROM golang:1.26-alpine AS backend-builder
ARG TARGETOS=linux
ARG TARGETARCH

WORKDIR /build

# 安装必要的构建工具
RUN apk add --no-cache git ca-certificates

# 设置 Go 模块缓存以加速构建
ENV GOMODCACHE=/go/pkg/mod

# 复制 go.mod 和 go.sum,利用 Docker 层缓存
COPY go.mod go.sum ./

# 使用多个 Go 代理以提高可靠性
ENV GOPROXY=https://goproxy.cn,https://proxy.golang.org,direct

RUN go mod download

# 复制源代码
COPY cmd/ ./cmd/
COPY internal/ ./internal/

# 从前端构建阶段复制构建产物到 Go embed 目录
# 先清理目标目录，避免嵌套 dist/dist/ 问题
RUN rm -rf internal/api/frontend/dist && mkdir -p internal/api/frontend/dist
COPY --from=frontend /build/dist/. ./internal/api/frontend/dist/

# 验证关键前端产物存在
RUN test -f ./internal/api/frontend/dist/index.html && \
    test -f ./internal/api/frontend/dist/favicon.svg && \
    echo "Frontend assets verified"

# 获取构建时间和版本信息
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown

# 编译静态二进制文件 (前端文件已嵌入，注入版本信息)
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH:-amd64} \
    go build \
    -ldflags="-s -w \
    -X monitor/internal/buildinfo.Version=${VERSION} \
    -X monitor/internal/buildinfo.GitCommit=${GIT_COMMIT} \
    -X 'monitor/internal/buildinfo.BuildTime=${BUILD_TIME}'" \
    -o /build/monitor ./cmd/server

# ============================================
# Stage 3: Runtime (Minimal Image)
# ============================================
FROM alpine:3.24

# OCI 镜像标签
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown

LABEL org.opencontainers.image.title="Relay Pulse Monitor" \
      org.opencontainers.image.description="Enterprise LLM service availability monitoring system" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${GIT_COMMIT}" \
      org.opencontainers.image.created="${BUILD_TIME}" \
      org.opencontainers.image.source="https://github.com/prehisle/relay-pulse" \
      org.opencontainers.image.licenses="MIT"

WORKDIR /app

# 安装必要的运行时依赖
RUN apk add --no-cache ca-certificates tzdata bash wget

# 从后端 builder 复制二进制文件（前端已嵌入）
COPY --from=backend-builder /build/monitor /app/monitor

# 复制默认配置文件作为模板
COPY config.yaml.example /app/config.yaml.default

# 复制 templates 目录 (用于 !include 引用的 JSON 文件)
COPY templates/ /app/templates/

# 复制入口脚本
COPY docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod +x /app/docker-entrypoint.sh

# 创建配置挂载目录
RUN mkdir -p /config

# 暴露端口
EXPOSE 8080

# 设置环境变量
ENV TZ=Asia/Shanghai

# 健康检查
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# 入口点
ENTRYPOINT ["/app/docker-entrypoint.sh"]
