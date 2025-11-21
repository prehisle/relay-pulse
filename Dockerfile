# ============================================
# Stage 1: Backend Builder (Go)
# ============================================
FROM golang:1.24-alpine AS backend-builder
ARG TARGETOS=linux
ARG TARGETARCH

WORKDIR /build

# 安装必要的构建工具
RUN apk add --no-cache git ca-certificates

# 设置 Go 模块缓存以加速构建
ENV GOMODCACHE=/go/pkg/mod

# 复制 go.mod 和 go.sum,利用 Docker 层缓存
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码
COPY cmd/ ./cmd/
COPY internal/ ./internal/

# 编译静态二进制文件 (无 CGO 依赖,支持多架构)
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH:-amd64} \
    go build -ldflags="-s -w" -o /build/monitor ./cmd/server

# ============================================
# Stage 2: Frontend Builder (Node.js)
# ============================================
FROM node:20-alpine AS frontend-builder

WORKDIR /build

# 复制 package.json 和 lock 文件,利用缓存
COPY frontend/package*.json ./
RUN npm ci

# 复制前端源代码
COPY frontend/ ./

# 构建生产版本
RUN npm run build

# ============================================
# Stage 3: Runtime (Minimal Image)
# ============================================
FROM alpine:3.19

WORKDIR /app

# 安装必要的运行时依赖
RUN apk add --no-cache ca-certificates tzdata bash wget

# 从后端 builder 复制二进制文件
COPY --from=backend-builder /build/monitor /app/monitor

# 从前端 builder 复制构建产物
COPY --from=frontend-builder /build/dist /app/frontend/dist

# 复制默认配置文件作为模板
COPY config.yaml.example /app/config.yaml.default

# 复制 data 目录 (用于 !include 引用的 JSON 文件)
COPY data/ /app/data/

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
