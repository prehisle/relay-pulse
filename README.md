# LLM Service Monitor - 企业级监控服务

生产级 LLM 服务可用性监控系统，支持热更新、真实历史数据持久化。

## 核心特性

✅ **配置驱动** - YAML 配置，支持环境变量覆盖
✅ **热更新** - 修改配置无需重启服务
✅ **真实历史** - SQLite 持久化历史数据
✅ **并发安全** - HTTP 客户端池复用，防重复触发
✅ **生产级质量** - 完整错误处理，优雅关闭

## 项目结构

```
monitor/
├── cmd/server/main.go          # 入口
├── internal/
│   ├── config/                 # 配置管理（验证、热更新、环境变量）
│   ├── storage/                # 存储层（SQLite 持久化）
│   ├── monitor/                # 监控引擎（HTTP 客户端池、探测）
│   ├── scheduler/              # 调度器（防重复、并发控制）
│   └── api/                    # API 层（gin、历史查询）
├── config.yaml                 # 配置文件
└── monitor.db                  # SQLite 数据库
```

## 快速开始

### 1. 安装依赖

```bash
go mod tidy
```

### 2. 配置服务

复制示例配置：

```bash
cp config.yaml.example config.yaml
```

编辑 `config.yaml`，填入真实的 API Key 和必填字段：

```yaml
monitors:
  - provider: "88code"
    service: "cc"
    category: "commercial"       # 必填：commercial（推广站）或 public（公益站）
    sponsor: "团队自有"          # 必填：提供 API Key 的赞助者
    url: "https://api.88code.com/v1/chat/completions"
    method: "POST"
    api_key: "sk-your-real-key"  # 修改这里
    headers:
      Authorization: "Bearer {{API_KEY}}"
      Content-Type: "application/json"
    body: |
      {
        "model": "claude-3-opus",
        "messages": [{"role": "user", "content": "hi"}],
        "max_tokens": 1
      }
```

**⚠️ 配置迁移提示**：
- `category` 和 `sponsor` 为**必填字段**，缺失将导致启动失败
- 如果升级旧配置，请为每个 monitor 添加这两个字段
- 参考 `config.yaml.example` 查看完整示例

如果请求体较大，可将 JSON 放在 `data/` 目录并在 `body` 中引用：

```yaml
body: "!include data/cx_base.json"  # 路径必须位于 data/ 下
```

### 3. 配置巡检间隔

可以在根级配置巡检频率（默认 1 分钟一次）：

```yaml
interval: "1m"  # 支持 Go duration 格式，例如 "30s"、"1m"、"5m"
```

修改保存后，调度器会在下一轮自动使用新的间隔。

### 4. 运行服务

```bash
go run cmd/server/main.go
```

### 5. 测试 API

```bash
# 获取所有监控状态（24小时）
curl "http://localhost:8080/api/status"

# 获取 7 天历史
curl "http://localhost:8080/api/status?period=7d"

# 过滤特定 provider
curl "http://localhost:8080/api/status?provider=88code"

# 健康检查
curl "http://localhost:8080/health"
```

## 环境变量支持

可通过环境变量覆盖 API Key（更安全）：

```bash
export MONITOR_88CODE_CC_API_KEY="sk-real-key"
export MONITOR_DUCKCODING_CC_API_KEY="sk-duck-key"

go run cmd/server/main.go
```

命名规则：`MONITOR_<PROVIDER>_<SERVICE>_API_KEY`（大写，`-` 替换为 `_`）

## 热更新

修改 `config.yaml` 后保存，服务会自动重载：

```bash
# 修改配置
vim config.yaml

# 观察日志
# [Config] 检测到配置文件变更，正在重载...
# [Config] 热更新成功！已加载 3 个监控任务
# [Scheduler] 配置已更新，下次巡检将使用新配置
```

如果配置错误，服务会保持旧配置并输出错误日志。

## API 响应格式

```json
{
  "meta": {
    "period": "24h",
    "count": 3
  },
  "data": [
    {
      "provider": "88code",
      "service": "cc",
      "category": "commercial",
      "sponsor": "团队自有",
      "channel": "vip-channel",
      "current_status": {
        "status": 1,
        "latency": 234,
        "timestamp": 1735559123
      },
      "timeline": [
        {
          "time": "14:30",
          "status": 1,
          "latency": 234
        }
      ]
    }
  ]
}
```

**字段说明**：
- `category`: 分类，`commercial`（推广站）或 `public`（公益站）
- `sponsor`: 赞助者名称
- `channel`: 业务通道标识（可选）

**Status 说明**：
- `0` = 🔴 红色（服务不可用）
- `1` = 🟢 绿色（正常）
- `2` = 🟡 黄色（延迟高或临时错误）

## 高级特性

### 占位符替换

`{{API_KEY}}` 在 **headers 和 body** 中都会被替换：

```yaml
headers:
  Authorization: "Bearer {{API_KEY}}"
body: |
  {"api_key": "{{API_KEY}}", "model": "gpt-4"}
```

### 配置验证

服务启动时会验证：
- 必填字段（provider, service, url, method）
- Method 枚举（GET/POST/PUT/DELETE/PATCH）
- Provider+Service 唯一性

### 数据清理

自动清理 30 天前的历史数据（每天执行一次）。

### 优雅关闭

`Ctrl+C` 时会：
1. 停止调度器
2. 完成进行中的探测
3. 关闭 HTTP 服务器
4. 关闭数据库连接

## 生产部署建议

### Docker 部署（推荐）

#### 方式一：使用 GitHub Container Registry 镜像

```bash
# 拉取最新镜像
docker pull ghcr.io/yourusername/ysh-monitor:latest

# 使用 Docker Compose 启动（推荐）
docker-compose up -d

# 或手动启动
docker run -d \
  --name llm-monitor \
  -p 8080:8080 \
  -v $(pwd)/config.local.yaml:/config/config.yaml:ro \
  -e MONITOR_88CODE_CC_API_KEY="sk-xxx" \
  -e MONITOR_DUCKCODING_CC_API_KEY="sk-yyy" \
  ghcr.io/yourusername/ysh-monitor:latest
```

#### 方式二：本地构建镜像

```bash
# 构建镜像（多架构支持）
docker build -t llm-monitor:latest .

# 启动容器
docker run -d \
  --name llm-monitor \
  -p 8080:8080 \
  -v $(pwd)/config.local.yaml:/config/config.yaml:ro \
  llm-monitor:latest
```

#### Docker Compose 部署

项目根目录已包含 `docker-compose.yaml`，支持以下特性：

```yaml
services:
  monitor:
    image: ghcr.io/yourusername/ysh-monitor:latest
    ports:
      - "8080:8080"
    volumes:
      - ./config.local.yaml:/config/config.yaml:ro
    environment:
      - MONITOR_88CODE_CC_API_KEY=sk-xxx
    restart: unless-stopped
```

**常用操作**：
```bash
# 启动服务
docker-compose up -d

# 查看日志
docker-compose logs -f monitor

# 重启服务（配置更新后）
docker-compose restart monitor

# 停止服务
docker-compose down
```

#### 环境变量配置（推荐）

创建 `.env` 文件存储敏感信息：

```bash
# .env
MONITOR_88CODE_CC_API_KEY=sk-your-real-key
MONITOR_88CODE_CX_API_KEY=sk-another-key
MONITOR_DUCKCODING_CC_API_KEY=sk-duck-key
```

然后在 `docker-compose.yaml` 中引用：
```yaml
services:
  monitor:
    env_file:
      - .env
```

⚠️ **安全提示**：记得将 `.env` 添加到 `.gitignore`，避免泄露密钥。

### Systemd 服务

```ini
[Unit]
Description=LLM Monitor Service
After=network.target

[Service]
Type=simple
User=monitor
WorkingDirectory=/opt/monitor
ExecStart=/opt/monitor/monitor
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

## 技术栈

- **Web 框架**：gin
- **数据库**：SQLite (modernc.org/sqlite - 纯 Go)
- **配置**：yaml.v3
- **热更新**：fsnotify
- **CORS**：gin-contrib/cors

## 开发

### 开发模式（热重载）

推荐使用 [cosmtrek/air](https://github.com/cosmtrek/air) 进行本地开发，代码修改后自动重新编译和重启：

```bash
# 首次使用：安装 air
make install-air

# 启动开发服务（监听 .go 文件变化）
make dev
```

**工作原理**：
- 监听 `cmd/` 和 `internal/` 目录下的 `.go` 文件
- 文件变更后延迟 1 秒触发增量编译
- 自动重启后端服务
- 配置文件 `config.yaml` 仍由 `fsnotify` 热更新（互不干扰）

**可用命令**：
```bash
make help         # 查看所有可用命令
make build        # 编译生产版本
make run          # 直接运行（无热重载）
make dev          # 开发模式（需要air）
make test         # 运行测试
make fmt          # 格式化代码
make clean        # 清理临时文件
```

### 快速开始（无热重载）

```bash
# 安装 pre-commit
pip install pre-commit
pre-commit install

# 编译运行
go build -o monitor ./cmd/server
./monitor

# 或直接运行
make run
```

### 代码检查

```bash
# 手动运行所有检查
pre-commit run --all-files

# 单独检查
go fmt ./...
go vet ./...
go test ./...
```

### 详细指南

查看 [CONTRIBUTING.md](CONTRIBUTING.md) 获取完整的开发者指南，包括：

- 项目结构说明
- 代码规范
- 提交规范
- 常见问题

## 许可

MIT
