# CLAUDE.md

本文档为 Claude Code (claude.ai/code) 在此代码库中工作时提供指导。

## 项目概览

这是一个企业级 LLM 服务可用性监控系统，支持配置热更新、SQLite/PostgreSQL 持久化和实时状态追踪。

### 项目文档

- **README.md** - 项目简介和快速开始
- **docs/user/** - 用户文档（安装、配置、运维）
- **docs/developer/** - 开发者文档（架构、工作流）
- **CONTRIBUTING.md** - 贡献指南

**注意**: 历史开发笔记已归档到 `archive/` 目录，不再维护。

### 技术栈

- **后端**: Go 1.24+ (Gin, fsnotify, SQLite/PostgreSQL)
- **前端**: React 19, TypeScript, Tailwind CSS v4, Vite

## 开发命令

### 首次开发环境设置

```bash
# ⚠️ 首次开发或前端代码更新后必须运行此脚本
./scripts/setup-dev.sh

# 如果前端代码有更新，需要重新构建并复制
./scripts/setup-dev.sh --rebuild-frontend
```

**重要**: Go 的 `embed` 指令不支持符号链接，因此需要将 `frontend/dist` 复制到 `internal/api/frontend/dist`。setup-dev.sh 脚本会自动处理这个问题。

### 后端 (Go)

```bash
# 开发环境 - 使用 Air 热重载（推荐）
./dev.sh
# 或直接使用: air

# 生产环境 - 手动构建运行
go build -o monitor ./cmd/server
./monitor

# 使用自定义配置运行
./monitor path/to/config.yaml

# 运行测试
go test ./...

# 运行测试并生成覆盖率
go test -cover ./...
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 运行特定包的测试
go test ./internal/config/
go test -v ./internal/storage/

# 代码格式化和检查
go fmt ./...
go vet ./...

# 整理依赖
go mod tidy
```

### 前端 (React)

```bash
cd frontend

# 开发服务器
npm run dev

# 生产构建
npm run build

# 代码检查
npm run lint

# 预览生产构建
npm run preview
```

### Pre-commit Hooks

```bash
# 安装 pre-commit (一次性设置)
pip install pre-commit
pre-commit install

# 手动运行所有检查
pre-commit run --all-files
```

## 架构与设计模式

### 后端架构

Go 后端遵循**分层架构**，职责清晰分离：

```
cmd/server/main.go          → 应用程序入口，依赖注入
internal/
├── config/                 → 配置管理（使用 fsnotify 实现热更新）
│   ├── config.go          → 数据结构、验证、规范化
│   ├── loader.go          → YAML 解析、环境变量覆盖
│   └── watcher.go         → 文件监听实现热更新
├── storage/               → 存储抽象层
│   ├── storage.go         → 接口定义
│   └── sqlite.go          → SQLite 实现 (modernc.org/sqlite)
├── monitor/               → 监控逻辑
│   ├── client.go          → HTTP 客户端池管理
│   └── probe.go           → 健康检查探测逻辑
├── scheduler/             → 任务调度
│   └── scheduler.go       → 周期性健康检查、并发执行
└── api/                   → HTTP API 层
    ├── handler.go         → 请求处理器、查询参数处理
    └── server.go          → Gin 服务器设置、中间件、CORS
```

**核心设计原则：**
1. **基于接口的设计**: `storage.Storage` 接口允许切换不同实现
2. **并发安全**: 所有共享状态使用 `sync.RWMutex` 或 `sync.Mutex`
3. **热更新**: 配置变更触发回调，无需重启即可更新运行时状态
4. **优雅关闭**: Context 传播确保资源清理
5. **HTTP 客户端池**: 通过 `monitor.ClientPool` 复用连接

### 配置热更新模式

系统采用**基于回调的热更新**机制：
1. `config.Watcher` 使用 `fsnotify` 监听 `config.yaml`
2. 文件变更时，先验证新配置再应用
3. 调用注册的回调函数（调度器、API 服务器）传入新配置
4. 各组件使用锁原子性地更新状态
5. 调度器立即使用新配置触发探测周期

**环境变量覆盖**: API 密钥可通过 `MONITOR_<PROVIDER>_<SERVICE>_API_KEY` 设置（大写，`-` → `_`）

### 前端架构

React SPA，基于组件的结构：

```
frontend/src/
├── components/            → UI 组件（StatusCard、StatusTable、Tooltip 等）
├── hooks/                 → 自定义 Hooks（useMonitorData 用于 API 数据获取）
├── types/                 → TypeScript 类型定义
├── constants/             → 应用常量（API URLs、时间周期）
├── utils/                 → 工具函数
└── App.tsx               → 主应用组件
```

**关键模式：**
- **自定义 Hooks**: `useMonitorData` 封装 API 轮询逻辑
- **TypeScript**: 使用 `types/` 中的接口实现完整类型安全
- **Tailwind CSS**: Tailwind v4 实用优先的样式
- **组件组合**: 小型、可复用组件

### 数据流

1. **Scheduler** (`scheduler.Scheduler`) 运行周期性健康检查
2. **Monitor** (`monitor.Probe`) 向配置的端点执行 HTTP 请求
3. 结果保存到 **Storage** (`storage.SQLiteStorage`)
4. **API** (`api.Handler`) 通过 `/api/status` 提供历史数据
5. **Frontend** 轮询 `/api/status` 并渲染可视化

### 状态码系统

- `0` = 红色（服务不可用、连接错误、其他4xx错误）
- `1` = 绿色（成功、HTTP 2xx、延迟 < slow_latency 阈值）
- `2` = 黄色（响应慢或临时问题，如5xx、429、缺少 `success_contains` 关键字）
- `3` = 灰色（未配置/认证失败，HTTP 400/401/403）

**可用率计算**：
- 采用**平均值法**：`总可用率 = 平均(所有时间块的可用率)`
- 灰色状态（无数据/认证失败）算作100%可用，避免初期可用率虚低
- 只有红色状态（真正的服务故障）才会降低可用率
- 所有可用率显示（列表、Tooltip、热力图）统一使用渐变色：
  - < 60% → 红色
  - 60-80% → 红到黄渐变
  - 80-100% → 黄到绿渐变

## 配置管理

### 配置文件结构

```yaml
interval: "1m"         # 探测频率（Go duration 格式）
slow_latency: "5s"     # 慢请求黄灯阈值

monitors:
  - provider: "88code"
    service: "cc"
    url: "https://api.88code.com/v1/chat/completions"
    method: "POST"
    api_key: "sk-xxx"  # 可通过 MONITOR_88CODE_CC_API_KEY 覆盖
    headers:
      Authorization: "Bearer {{API_KEY}}"
    body: |
      {"model": "claude-3-opus", "messages": [...]}
    success_contains: "optional_keyword"  # 语义验证（可选）
```

**模板占位符**: `{{API_KEY}}` 在 headers 和 body 中会被替换。

**引用文件**: 对于大型请求体，使用 `body: "!include data/filename.json"`（必须在 `data/` 目录下）。

### 热更新测试

```bash
# 启动监控服务
./monitor

# 在另一个终端编辑配置
vim config.yaml

# 观察日志：
# [Config] 检测到配置文件变更，正在重载...
# [Config] 热更新成功！已加载 3 个监控任务
# [Scheduler] 配置已更新，下次巡检将使用新配置
```

## API 端点

```bash
# 健康检查
curl http://localhost:8080/health

# 获取状态（默认 24h）
curl http://localhost:8080/api/status

# 查询参数：
# - period: "24h", "7d", "30d" (默认: "24h")
# - provider: 按 provider 名称过滤
# - service: 按 service 名称过滤
curl "http://localhost:8080/api/status?period=7d&provider=88code"
```

**响应格式**:
```json
{
  "meta": {"period": "24h", "count": 3},
  "data": [
    {
      "provider": "88code",
      "service": "cc",
      "current_status": {"status": 1, "latency": 234, "timestamp": 1735559123},
      "timeline": [{"time": "14:30", "status": 1, "latency": 234}, ...]
    }
  ]
}
```

## 测试

### 后端测试

- 测试文件与源文件放在一起（`*_test.go`）
- 关键测试文件：`internal/config/config_test.go`、`internal/monitor/probe_test.go`
- 使用 `go test -v` 查看详细输出

### 手动集成测试

```bash
# 终端 1：启动后端
./monitor

# 终端 2：启动前端
cd frontend && npm run dev

# 终端 3：测试 API
curl http://localhost:8080/api/status

# 测试热更新
vim config.yaml  # 修改 interval 为 "30s"
# 观察调度器日志中的配置重载信息
```

## 提交信息规范

遵循 conventional commits：

```
<type>: <subject>

<body>

<footer>
```

**类型**: `feat`、`fix`、`docs`、`refactor`、`test`、`chore`

**示例**:
```
feat: add response content validation with success_contains

- Add success_contains field to ServiceConfig
- Implement keyword matching in probe.go
- Update config.yaml.example with usage

Closes #42
```

## 常见模式与陷阱

### Scheduler 中的并发

调度器使用两个锁：
- `cfgMu` (RWMutex): 保护配置访问
- `mu` (Mutex): 保护调度器状态（运行标志、定时器）

对于只读配置访问，始终使用 `RLock()/RUnlock()`。

### SQLite 并发

使用 WAL 模式（`_journal_mode=WAL`）允许写入时并发读取。连接 DSN：`file:monitor.db?_journal_mode=WAL`

### Probe 中的错误处理

- 网络错误 → 状态 0（红色）
- HTTP 4xx/5xx → 状态 0（红色）
- HTTP 2xx + 慢延迟 → 状态 2（黄色）
- HTTP 2xx + 快速 + 内容匹配 → 状态 1（绿色）

### 前端数据获取

`useMonitorData` Hook 每 30 秒轮询 `/api/status`。组件卸载时需禁用轮询以防止内存泄漏。

## 生产部署

### 环境变量（推荐）

```bash
export MONITOR_88CODE_CC_API_KEY="sk-real-key"
export MONITOR_DUCKCODING_CC_API_KEY="sk-duck-key"
./monitor
```

### Systemd 服务

参见 README.md 中的 systemd unit 文件模板。

### Docker

参见 README.md 中的多阶段 Dockerfile。

## 相关文档

- 完整开发指南：`CONTRIBUTING.md`
- API 设计细节：`archive/prds.md`（历史参考）
- 实现笔记：`docs/IMPLEMENTATION.md`
