# Stage 1: Modules caching
FROM golang:1.26-bookworm AS modules
COPY go.mod go.sum /modules/
WORKDIR /modules
RUN go mod download

# Stage 2: Build
FROM golang:1.26-bookworm AS builder
COPY --from=modules /go/pkg /go/pkg
COPY . /workdir
WORKDIR /workdir

# Install playwright CLI (version from go.mod)
RUN PWGO_VER=$(grep -oE "playwright-go v\S+" /workdir/go.mod | sed 's/playwright-go //g') \
    && go install github.com/playwright-community/playwright-go/cmd/playwright@${PWGO_VER}

# Build binary
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /bin/notifier ./cmd/notifier

# Stage 3: Runtime
FROM ubuntu:24.04

# Prevent interactive prompts
ENV DEBIAN_FRONTEND=noninteractive
ENV TZ=Asia/Shanghai

WORKDIR /app

# Copy binaries from builder
COPY --from=builder /go/bin/playwright /bin/playwright
COPY --from=builder /bin/notifier .

# Install dependencies and Chromium browser
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    tzdata \
    wget \
    # Chinese font support
    fonts-noto-cjk \
    && /bin/playwright install --with-deps chromium \
    && rm -rf /var/lib/apt/lists/*

# Copy default config template
COPY config.yaml.example config.yaml

# Create data directory
RUN mkdir -p /app/data

# Expose API port
EXPOSE 8081

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget -q --spider http://localhost:8081/health || exit 1

ENTRYPOINT ["./notifier"]
