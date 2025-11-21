# 开发者指南

本文档帮助新人快速上手项目开发和维护。

## 目录

- [环境准备](#环境准备)
- [项目结构](#项目结构)
- [开发流程](#开发流程)
- [代码规范](#代码规范)
- [测试](#测试)
- [提交规范](#提交规范)

---

## 环境准备

### 必需工具

```bash
# Go 1.21+
go version

# pre-commit (代码提交检查)
pip install pre-commit
# 或 brew install pre-commit

# 初始化 pre-commit hooks
pre-commit install
```

### 首次运行

```bash
# 1. 克隆项目
git clone <repo-url>
cd relay-pulse

# 2. 安装 Go 依赖
go mod download

# 3. 构建前端（首次或 dist 目录缺失时执行）
cd frontend
npm install
npm run build
cd ..

# 4. 复制配置
cp config.yaml.example config.yaml

# 5. 编译运行
go build -o monitor ./cmd/server
./monitor
```

> 💡 `./scripts/setup-dev.sh` 会自动执行前端构建与复制、检查 `config.yaml` 是否存在，并支持 `--rebuild-frontend` 强制重新打包。更新前端或拉取最新 main 后运行该脚本，可确保 `internal/api/frontend` 与 UI 保持一致。

### 前端构建与调试

```bash
cd frontend
npm install           # 安装依赖
npm run dev           # 启动 Vite 开发服务器 (http://localhost:5173)
npm run build         # 生成 dist，用于后端 embed
npm run preview       # 预览生产构建
```

- `npm run dev` 访问后端 API（跨域需求可在 `.env.development` 中设置 `VITE_API_BASE_URL`）。
- 本地修改前端后，需要 `npm run build` 并执行 `./scripts/setup-dev.sh --rebuild-frontend`，以同步嵌入的静态文件。

---

## 项目结构

```
.
├── cmd/server/main.go      # 程序入口
├── internal/               # 内部包（不对外暴露）
│   ├── config/            # 配置管理
│   │   ├── config.go      # 数据结构和验证
│   │   ├── loader.go      # 配置加载
│   │   └── watcher.go     # 热更新监听
│   ├── storage/           # 数据存储
│   │   ├── storage.go     # 接口定义
│   │   └── sqlite.go      # SQLite 实现
│   ├── monitor/           # 监控探测
│   │   ├── client.go      # HTTP 客户端池
│   │   └── probe.go       # 探测逻辑
│   ├── scheduler/         # 调度器
│   │   └── scheduler.go   # 定时任务调度
│   └── api/               # HTTP API
│       ├── handler.go     # 请求处理
│       └── server.go      # 服务器
├── scripts/               # 工具脚本
├── docs/                  # 项目文档
├── config.yaml            # 运行配置
└── config.yaml.example    # 配置示例
```

### 关键组件说明

| 组件 | 职责 | 关键文件 |
|-----|------|----------|
| Config | 配置加载、验证、热更新 | `internal/config/*.go` |
| Storage | 数据持久化（SQLite） | `internal/storage/*.go` |
| Monitor | HTTP 探测、客户端池 | `internal/monitor/*.go` |
| Scheduler | 定时调度、并发控制 | `internal/scheduler/*.go` |
| API | RESTful 接口 | `internal/api/*.go` |

---

## 开发流程

### 添加新功能

1. **理解需求** - 阅读相关 PRD 或 Issue
2. **设计方案** - 确定影响的组件和接口
3. **编写代码** - 遵循代码规范
4. **编写测试** - 单元测试覆盖关键路径
5. **更新文档** - README、注释、CHANGELOG
6. **提交代码** - pre-commit 会自动检查

### 修复 Bug

1. **复现问题** - 确认环境和步骤
2. **定位原因** - 查看日志、调试代码
3. **编写修复** - 最小化改动范围
4. **添加测试** - 防止回归
5. **提交修复** - 在 commit message 中引用 Issue

### 常用命令

```bash
# 编译
go build -o monitor ./cmd/server

# 运行
./monitor
./monitor config.yaml  # 指定配置文件

# 格式化代码
go fmt ./...

# 代码检查
go vet ./...

# 运行测试
go test ./...

# 测试覆盖率
go test -cover ./...

# 手动运行所有 pre-commit 检查
pre-commit run --all-files
```

---

## 代码规范

### Go 规范

- **格式化**: 使用 `go fmt`
- **命名**:
  - 包名小写单词：`config`, `storage`
  - 导出函数大驼峰：`NewScheduler`, `GetStatus`
  - 私有函数小驼峰：`runChecks`, `parsePeriod`
- **注释**: 导出函数必须有注释
- **错误处理**: 使用 `fmt.Errorf("描述: %w", err)` wrap 错误

### 并发安全

项目大量使用并发，修改代码时注意：

```go
// 配置访问需要加锁
s.cfgMu.RLock()
cfg := s.cfg
s.cfgMu.RUnlock()

// 状态修改需要加锁
s.mu.Lock()
s.running = true
s.mu.Unlock()
```

### 日志规范

```go
// 模块前缀
log.Printf("[Scheduler] 调度器已启动")
log.Printf("[Config] 配置已重载")
log.Printf("[Probe] ERROR %s: %v", name, err)

// 用户提示使用 emoji
log.Println("✅ 服务已启动")
log.Println("❌ 启动失败")
log.Println("⚠️  警告信息")
```

---

## 测试

### 单元测试

```bash
# 运行所有测试
go test ./...

# 运行特定包的测试
go test ./internal/config/

# 显示详细输出
go test -v ./internal/storage/

# 生成覆盖率报告
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### 手动测试

```bash
# 启动服务
./monitor

# 健康检查
curl http://localhost:8080/health

# 获取状态
curl http://localhost:8080/api/status

# 测试热更新
vim config.yaml  # 修改后保存，观察日志
```

---

## 提交规范

### Commit Message 格式

```
<type>: <subject>

<body>

<footer>
```

**Type 类型**:
- `feat`: 新功能
- `fix`: Bug 修复
- `docs`: 文档更新
- `refactor`: 重构
- `test`: 测试
- `chore`: 构建/工具

**示例**:

```
feat: 添加热更新后立即触发巡检

- 在 Scheduler 中添加 TriggerNow 方法
- 热更新回调中调用 TriggerNow
- 复用调度器的 context 控制生命周期

Closes #123
```

### Pre-commit 检查

提交前会自动运行以下检查：

- `go-fmt`: 代码格式
- `go-vet`: 代码问题
- `go-build`: 编译检查
- `go-mod-tidy`: 依赖整理
- `check-docs-sync`: 文档同步

如果检查失败，请修复后重新提交。

---

## 常见问题

### Q: 编译报错 "database is locked"

SQLite 并发写入问题。确保使用 WAL 模式：
```go
dsn := "file:monitor.db?_journal_mode=WAL"
```

### Q: 热更新不生效

检查配置文件是否有语法错误：
```bash
# 验证 YAML 格式
python -c "import yaml; yaml.safe_load(open('config.yaml'))"
```

### Q: API 返回空数据

检查是否有探测记录：
```bash
sqlite3 monitor.db "SELECT COUNT(*) FROM probe_history"
```

### Q: pre-commit 安装失败

```bash
# 使用 pip
pip install pre-commit

# 或使用 brew (macOS)
brew install pre-commit

# 然后初始化
pre-commit install
```

---

## 联系方式

- Issue: 通过 GitHub Issue 报告问题
- 文档: 查看 `docs/` 目录

---

*最后更新: 2025-11-20*
