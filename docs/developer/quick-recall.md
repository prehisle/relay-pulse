# 快速回忆清单

> **Audience**: 维护者本人 | **用途**: 3个月后快速重新上手

## 🚀 3分钟重新上手

### 开发环境启动

```bash
# 1. 首次或前端更新后必须运行
./scripts/setup-dev.sh
# 或强制重建前端: ./scripts/setup-dev.sh --rebuild-frontend

# 2. 启动后端（热重载）
./dev.sh

# 3. 启动前端开发服务器（可选）
cd frontend && npm run dev
```

### 生产环境部署

```bash
# Docker Compose 部署
docker compose pull
docker compose up -d

# 查看日志
docker compose logs -f monitor

# 健康检查
curl http://localhost:8080/health
```

---

## 📁 关键文件位置

### 核心业务逻辑

| 功能 | 文件路径 | 说明 |
|------|----------|------|
| 配置热更新 | `internal/config/watcher.go` | fsnotify 监听配置文件变更 |
| 探测逻辑 | `internal/monitor/probe.go` | HTTP 健康检查、状态码判断 |
| 调度器 | `internal/scheduler/scheduler.go` | 定时触发探测、并发控制 |
| API 处理 | `internal/api/handler.go` | 查询参数解析、时间线聚合 |
| 数据存储 | `internal/storage/sqlite.go` | SQLite WAL 模式、30天清理 |

### 配置与部署

| 文件 | 用途 |
|------|------|
| `config.yaml` | 运行时配置（监控项、巡检间隔） |
| `docker-compose.yaml` | 容器编排 |
| `deploy/relaypulse.env` | 环境变量模板（API Keys） |
| `scripts/setup-dev.sh` | 开发环境初始化脚本 |

---

## ⚠️ 我容易忘记的坑

### 1. embed 不支持符号链接

```bash
# ❌ 错误做法：软链接不会被 embed 识别
ln -s ../frontend/dist internal/api/frontend/dist

# ✅ 正确做法：必须复制文件
./scripts/setup-dev.sh --rebuild-frontend
```

**原因**: Go 的 `//go:embed` 指令在编译时只处理实际文件，忽略符号链接。

---

### 2. SQLite 必须用 WAL 模式

```go
// 连接字符串必须带参数
dsn := "monitor.db?_journal_mode=WAL"
```

**原因**: WAL 模式允许写入时并发读取，避免 "database locked" 错误。

---

### 3. 数据自动保留 30 天

位置: `cmd/server/main.go:99-110`

```go
// 每 24 小时清理一次
ticker := time.NewTicker(24 * time.Hour)
go func() {
    for range ticker.C {
        storage.CleanOldRecords(30) // 删除 30 天前的数据
    }
}()
```

**修改方法**: 调整参数或改为配置项（需要 PR）。

---

### 4. Docker 卷挂载只能挂 /data

```yaml
# ❌ 错误：会导致二进制文件不更新
volumes:
  - relay-pulse-data:/app

# ✅ 正确：只挂载数据目录
volumes:
  - relay-pulse-data:/data
```

**原因**: 挂载整个 `/app` 会覆盖镜像中的最新二进制和静态文件。

---

## 🔍 快速调试技巧

### 查看配置热更新日志

```bash
docker compose logs -f | grep "Config"
# 预期输出:
# [Config] 检测到配置文件变更，正在重载...
# [Config] 热更新成功！已加载 3 个监控任务
```

### 查看探测失败原因

```bash
docker compose logs --tail=50 | grep "探测失败"
```

### 手动触发配置重载

```bash
touch config.yaml
```

### 进入容器调试

```bash
docker exec -it relaypulse-monitor sh
# 查看进程: ps aux
# 查看数据库: sqlite3 /data/monitor.db
```

---

## 🏗️ 架构速记

```
用户 → API (Gin) → Handler (查询逻辑) → Storage (SQLite/PG)
                                        ↑
                                    Scheduler (定时触发)
                                        ↓
                                    Monitor (HTTP 探测)
                                        ↓
                                    ClientPool (连接复用)
```

**数据流**:
1. Scheduler 每 N 分钟触发一次探测
2. Monitor.Probe 并发检测所有服务
3. 结果写入 Storage (probe_history 表)
4. API 查询最近数据并聚合成时间线
5. Frontend 每 30 秒轮询 `/api/status`

---

## 📝 上次离开时的状态

**最后更新**: 2025-11-21

**进行中的工作**:
- ✅ 完成文档重组和改进
- 🔮 计划中：Prometheus metrics 端点

**已知问题**:
- 无

**待优化**:
- 可选：添加数据保留天数配置项
- 可选：支持 Webhook 告警

---

## 📚 相关文档

- [架构概览](overview.md) - 详细的系统设计文档
- [配置手册](../user/config.md) - 所有配置项说明
- [运维手册](../user/operations.md) - 故障排查和日常维护
- [CLAUDE.md](../../CLAUDE.md) - AI 辅助开发指南

---

## 💬 自我提醒

> 3个月后的你可能已经忘记很多细节，但只要：
> 1. 跑一次 `./scripts/setup-dev.sh`
> 2. 看看这份清单
> 3. 翻翻 Git log (`git log --oneline -20`)
>
> 就能快速回到状态！加油！ 💪
