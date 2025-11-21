# Relay Pulse - LLM 服务可用性监控

> **Audience**: 用户（部署和使用）| **Last reviewed**: 2025-11-21

企业级 LLM 服务可用性监控系统，实时追踪服务状态并提供可视化仪表板。

![Status Dashboard](https://img.shields.io/badge/status-production-green) ![License](https://img.shields.io/badge/license-MIT-blue)

## ✨ 核心特性

- **📊 实时监控** - 多服务并发健康检查，实时状态追踪
- **🔄 配置热更新** - 修改配置无需重启，立即生效
- **💾 多存储后端** - 支持 SQLite（单机）和 PostgreSQL（K8s）
- **📈 历史数据** - 24小时/7天/30天可用率统计
- **🎨 可视化仪表板** - React + Tailwind CSS，响应式设计
- **🐳 云原生** - Docker/K8s 就绪，支持水平扩展

## 🚀 快速开始

### Docker 部署（推荐）

```bash
# 1. 下载配置文件
curl -O https://raw.githubusercontent.com/prehisle/relay-pulse/main/docker-compose.yaml
curl -O https://raw.githubusercontent.com/prehisle/relay-pulse/main/config.yaml.example

# 2. 准备配置
cp config.yaml.example config.yaml
vi config.yaml  # 填入你的 API Key

# 3. 启动服务
docker compose up -d

# 4. 访问 Web 界面
open http://localhost:8080
```

**🎬 完整安装教程**：[docs/user/install.md](docs/user/install.md)

### 本地开发

```bash
# 安装依赖
go mod tidy
cd frontend && npm install && cd ..

# 准备配置
cp config.yaml.example config.yaml

# 启动开发服务（带热重载）
make dev

# 或直接运行
go run cmd/server/main.go
```

**👨‍💻 开发者指南**：[docs/developer/overview.md](docs/developer/overview.md)

## 📖 文档导航

### 快速索引

| 我要... | 看这个 |
|---------|--------|
| 🚀 快速部署 | [Docker 安装](docs/user/install.md#docker-部署推荐) |
| 💻 本地开发 | [快速回忆清单](docs/developer/quick-recall.md#-3分钟重新上手) |
| ⚙️ 配置监控项 | [配置手册](docs/user/config.md#监控项配置) |
| 🔧 排查问题 | [运维手册 - 故障排查](docs/user/operations.md#故障排查) |
| 🏗️ 了解架构 | [架构概览](docs/developer/overview.md) |
| 🔄 3个月后回来 | [快速回忆清单](docs/developer/quick-recall.md) |

---

### 用户文档
- [安装指南](docs/user/install.md) - Docker/K8s/二进制部署
- [配置手册](docs/user/config.md) - YAML 配置、环境变量、安全实践
- [运维手册](docs/user/operations.md) - 健康检查、备份恢复、故障排查

### 开发者文档
- [快速回忆清单](docs/developer/quick-recall.md) - ⭐ 3个月后快速重新上手
- [架构概览](docs/developer/overview.md) - 系统设计、模块说明
- [贡献指南](CONTRIBUTING.md) - 代码规范、提交规范
- [部署手册](docs/deployment.md) - 多环境部署、CI/CD 建议

### 参考文档
- API 规范与发布流程文档正在整理，欢迎在 Issue 中提出需求或直接贡献 PR。

## 🔧 配置示例

```yaml
# config.yaml
interval: "1m"         # 检查频率
slow_latency: "5s"     # 慢请求阈值

monitors:
  - provider: "88code"
    service: "cc"
    category: "commercial"
    sponsor: "团队自有"
    url: "https://api.88code.com/v1/chat/completions"
    method: "POST"
    api_key: "sk-xxx"  # 或通过环境变量 MONITOR_88CODE_CC_API_KEY
    headers:
      Authorization: "Bearer {{API_KEY}}"
    body: |
      {
        "model": "claude-3-opus",
        "messages": [{"role": "user", "content": "hi"}],
        "max_tokens": 1
      }
```

**详细配置说明**：[docs/user/config.md](docs/user/config.md)

## 🗄️ 存储后端

| 后端       | 适用场景            | 优点                   |
|------------|---------------------|------------------------|
| **SQLite** | 单机部署、开发环境  | 零配置，开箱即用       |
| **PostgreSQL** | K8s、多副本部署 | 高可用、水平扩展       |

```bash
# SQLite（默认）
docker compose up -d monitor

# PostgreSQL
docker compose up -d postgres monitor-pg
```

## 📊 API 端点

```bash
# 获取监控状态（24小时）
curl http://localhost:8080/api/status

# 获取 7 天历史
curl http://localhost:8080/api/status?period=7d

# 健康检查
curl http://localhost:8080/health

# 版本信息
curl http://localhost:8080/api/version
```

> 🔧 API 参考章节正在整理，以上端点示例即当前权威来源。

## 🛠️ 技术栈

**后端**
- Go 1.24+
- Gin (HTTP framework)
- SQLite / PostgreSQL
- fsnotify (配置热更新)

**前端**
- React 19
- TypeScript
- Tailwind CSS v4
- Vite

## 📝 变更日志

查看 [CHANGELOG.md](CHANGELOG.md) 了解版本历史和最新变更。

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！请先阅读 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 📄 许可证

[MIT License](LICENSE) © 2025

---

**🌐 在线演示**: https://relaypulse.top
**📦 镜像仓库**: `ghcr.io/prehisle/relay-pulse:latest`
**💬 问题反馈**: https://github.com/prehisle/relay-pulse/issues
