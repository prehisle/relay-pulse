# Makefile for LLM Monitor Service

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GORUN=$(GOCMD) run
GOTEST=$(GOCMD) test
GOMOD=$(GOCMD) mod
GOFMT=$(GOCMD) fmt

# Binary name
BINARY_NAME=monitor
BINARY_PATH=./$(BINARY_NAME)

# Main package
MAIN_PACKAGE=./cmd/server

# Air hot reload tool
# 优先使用 PATH 中的 air，如果没有则尝试 Go bin 目录
AIR_CMD=$(shell command -v air 2>/dev/null || echo "$(shell go env GOPATH)/bin/air")

# 配置文件（用于 run 命令）
# 注意：make dev 使用 air，配置文件固定为 config.yaml
# 如需自定义配置文件，请使用 make run 或直接运行 air
CONFIG ?= config.yaml

# 开发环境 CORS 配置（允许前端开发服务器访问）
MONITOR_CORS_ORIGINS ?= http://localhost:5173,http://127.0.0.1:5173,http://localhost:5174,http://127.0.0.1:5174,http://localhost:5175,http://127.0.0.1:5175,http://localhost:3000

.PHONY: help build run dev test fmt clean install-air

# 默认目标：显示帮助
help:
	@echo "可用命令:"
	@echo "  make build       - 编译生产版本"
	@echo "  make run         - 直接运行（无热重载）"
	@echo "  make dev         - 开发模式（热重载，需要air）"
	@echo "  make test        - 运行测试"
	@echo "  make fmt         - 格式化代码"
	@echo "  make clean       - 清理编译产物"
	@echo "  make install-air - 安装air热重载工具"
	@echo ""
	@echo "开发环境已自动配置 CORS，允许前端开发服务器访问（端口 5173-5175, 3000）"

# 编译二进制
build:
	@echo "正在编译 $(BINARY_NAME)..."
	$(GOBUILD) -o $(BINARY_PATH) $(MAIN_PACKAGE)
	@echo "编译完成: $(BINARY_PATH)"

# 直接运行（不编译）
run:
	@echo "正在启动监控服务..."
	MONITOR_CORS_ORIGINS="$(MONITOR_CORS_ORIGINS)" $(GORUN) $(MAIN_PACKAGE)

# 开发模式（热重载）
dev:
	@if [ ! -f "$(AIR_CMD)" ] && [ -z "$$(command -v air 2>/dev/null)" ]; then \
		echo "错误: air 未安装"; \
		echo ""; \
		echo "请运行以下命令安装:"; \
		echo "  make install-air"; \
		echo ""; \
		echo "或手动安装:"; \
		echo "  go install github.com/air-verse/air@latest"; \
		exit 1; \
	fi
	@echo "正在启动开发服务（热重载）..."
	@echo "修改 .go 文件将自动重新编译"
	MONITOR_CORS_ORIGINS="$(MONITOR_CORS_ORIGINS)" $(AIR_CMD) -c .air.toml

# 运行测试
test:
	@echo "正在运行测试..."
	$(GOTEST) -v ./...

# 运行测试（带覆盖率）
test-coverage:
	@echo "正在运行测试并生成覆盖率..."
	$(GOTEST) -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "覆盖率报告已生成: coverage.html"

# 格式化代码
fmt:
	@echo "正在格式化代码..."
	$(GOFMT) ./...
	@echo "格式化完成"

# 清理编译产物
clean:
	@echo "正在清理..."
	@rm -f $(BINARY_PATH)
	@rm -rf tmp/
	@rm -f coverage.out coverage.html
	@echo "清理完成"

# 安装 air 热重载工具
install-air:
	@echo "正在安装 air..."
	$(GOCMD) install github.com/air-verse/air@latest
	@echo "安装完成！现在可以运行 'make dev'"

# 整理依赖
tidy:
	@echo "正在整理依赖..."
	$(GOMOD) tidy
	@echo "依赖整理完成"
