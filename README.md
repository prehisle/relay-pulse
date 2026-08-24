<div align="center">

# Relay Pulse

### 拒绝 API 假活，基于真实调用的 LLM 服务质量观测台

**中文** | [English](README.en.md)

[![在线演示](https://img.shields.io/badge/🌐_在线演示-relaypulse.top-00d8ff?style=for-the-badge)](https://relaypulse.top)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?style=for-the-badge&logo=go)](https://go.dev/)
[![React](https://img.shields.io/badge/React-19-61DAFB?style=for-the-badge&logo=react)](https://react.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue?style=for-the-badge)](LICENSE)

<img src="docs/screenshots/dashboard-preview.png" alt="RelayPulse Dashboard" width="100%">

</div>

---

## 简介

传统监测工具（如 Uptime Kuma）监测的是 HTTP 连通性——但在 LLM 中转场景下，**"HTTP 200 却返回空内容或错误码"** 的"假活"现象屡见不鲜。

**RelayPulse** 通过消耗真实 Token 定时发起 API 请求，并校验响应内容。只有 LLM 真的"吐字"了，才算可用。

## ✨ 核心特性

- **💸 真实 API 探测** - 消耗真实 Token，拒绝虚假繁荣
- **📊 可视化矩阵** - 24h/7d/30d 可用率热力图，一眼看穿服务质量
- **🔄 配置热更新** - 基于 fsnotify，修改配置无需重启
- **💾 多存储后端** - SQLite（单机）/ PostgreSQL（K8s）
- **🐳 云原生友好** - 极小 Docker 镜像，支持水平扩展

## 🎯 适用场景

- 自建/采购 LLM 中转服务，持续跟踪可用性表现
- 多云 LLM 供应商质量对比，观察延迟与错误率
- 外部 API 依赖监测，避免"假活"导致业务故障

## 💰 成本与隐私

- **探测成本极低**：`max_tokens: 1`，每次约 20 input + 1 output tokens；默认每分钟一次，约 3 万 tokens/天/服务
- **数据本地存储**：配置与密钥仅存本地/自托管环境，监测数据不回传

## 🚀 快速开始

### Docker 部署（推荐）

```bash
# 1. 下载配置文件
curl -O https://raw.githubusercontent.com/prehisle/relay-pulse/main/docker-compose.yaml
curl -O https://raw.githubusercontent.com/prehisle/relay-pulse/main/config.yaml.example

# 2. 准备配置
mkdir -p config && cp config.yaml.example config/config.yaml
vi config/config.yaml  # 填入你的 API Key

# 3. 启动服务
docker compose up -d

# 4. 访问 Web 界面
open http://localhost:8080
```

**🎬 完整安装教程**：[QUICKSTART.md](QUICKSTART.md)

### 本地开发

```bash
# 一键 setup（构建前端 + 复制 embed 资源 + 初始化 config.yaml）
./scripts/setup-dev.sh

# 整理 Go 依赖
go mod tidy

# 启动开发服务（带热重载）
make dev

# 或直接运行
go run cmd/server/main.go
```

> ⚠️ 首次构建或前端更新后**必须**先跑 `./scripts/setup-dev.sh`，它会把 `frontend/dist` 复制到 `internal/api/frontend/dist`（`//go:embed` 需要的路径，不支持符号链接）。跳过会报 `pattern frontend/dist: no matching files found`。详见 [CONTRIBUTING.md](CONTRIBUTING.md#首次运行)。

**👨‍💻 开发者指南**：[CONTRIBUTING.md](CONTRIBUTING.md)

## 📖 文档导航

### 快速索引（人类读者）

| 我要...            | 看这个文档 |
|--------------------|------------|
| 🚀 5 分钟内跑起来  | [QUICKSTART.md](QUICKSTART.md) |
| 💻 本地开发/调试   | 本文档的「本地开发」章节 |
| ⚙️ 配置监测项      | [配置手册](docs/user/config.md) |
| 🔔 配置通知推送    | [notifier/README.md](notifier/README.md)（Telegram/QQ Bot） |
| 📡 多模型监测      | [配置手册 - 多模型](docs/user/config.md)（parent-child 继承） |
| 🤝 参与贡献        | [CONTRIBUTING.md](CONTRIBUTING.md) |

---

### 核心文档（建议优先阅读）
- `README.md`（本文件）：项目总览、特性介绍、快速开始、本地开发说明
- `QUICKSTART.md`：面向用户的快速部署与常见问题
- `docs/user/config.md`：配置项说明、环境变量规则、安全实践
- `CONTRIBUTING.md`：贡献流程、代码规范、提交与 PR 约定

### 扩展文档
- [Docker 部署指南](docs/user/docker.md)：高级 Docker 配置（用户/运维）
- [PostgreSQL 部署指南](docs/user/deploy-postgres.md)：PostgreSQL 部署（运维）
- [通知子系统](notifier/README.md)：Telegram/QQ Bot 通知推送（用户/运维）
- [版本检查](docs/developer/version-check.md)：版本信息与检测（运维）
- [监测方法论](docs/user/methodology.md)：探测原理与数据说明（用户）
- [赞助权益](docs/user/sponsorship.md)：赞助体系规则（用户）

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
    sponsor_level: "beacon"    # 可选：public/signal/pulse/beacon/backbone/core
    base_url: "https://api.88code.com"
    template: "cc-haiku-arith"  # 引用 templates/ 目录下的模板
    api_key: "sk-xxx"  # 或通过环境变量 MONITOR_88CODE_CC_API_KEY
    # model 和 request_model 由模板预设
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
# 获取监测状态（24小时）
curl http://localhost:8080/api/status

# 获取 7 天历史
curl http://localhost:8080/api/status?period=7d

# 按板块过滤（hot/secondary/cold/all）
curl http://localhost:8080/api/status?board=hot

# 健康检查
curl http://localhost:8080/health

# 版本信息
curl http://localhost:8080/api/version

# 状态变更事件（需 Bearer Token 鉴权，token 通过 events.api_token 或 EVENTS_API_TOKEN 配置）
curl -H "Authorization: Bearer <token>" http://localhost:8080/api/events
curl -H "Authorization: Bearer <token>" http://localhost:8080/api/events/latest

# 收录内联探测
curl -X POST http://localhost:8080/api/onboarding/test  # 内联探测测试
```

**时间窗口说明**：API 使用**滑动窗口**设计，`period=24h` 返回"从当前时刻倒推 24 小时"的数据。这意味着：
- 每次请求的时间基准不同，时间桶边界会随之微调
- 服务商排名始终反映**最近 24 小时**的真实可用率
- 如需固定时间点数据用于集成，建议按固定频率（如每小时整点）采样

### 状态查询 API（StatusQuery）

用于快速查询特定 provider/service/channel 的当前状态，适合订阅校验、告警集成等场景。

```bash
# 单查：查询 provider 的所有 service/channel
curl "http://localhost:8080/api/status/query?provider=88code"

# 单查：查询特定 service
curl "http://localhost:8080/api/status/query?provider=88code&service=cc"

# 单查：查询特定 channel
curl "http://localhost:8080/api/status/query?provider=88code&service=cc&channel=vip"

# 多查：紧凑格式（最多 20 组）
curl "http://localhost:8080/api/status/query?q=88code/cc/vip&q=anthropic/cc"

# 批量查询（POST，最多 50 组）
curl -X POST http://localhost:8080/api/status/batch \
  -H "Content-Type: application/json" \
  -d '{
    "queries": [
      {"provider": "88code", "service": "cc", "channel": "vip"},
      {"provider": "anthropic", "service": "cc"}
    ]
  }'
```

**响应格式**：
```json
{
  "as_of": "2024-01-15T10:30:00Z",
  "results": [
    {
      "query": {"provider": "88code", "service": "cc", "channel": "vip"},
      "provider": "88code",
      "services": [
        {
          "name": "cc",
          "channels": [
            {
              "name": "vip",
              "status": "up",
              "latency_ms": 234,
              "updated_at": "2024-01-15T10:29:45Z",
              "board": "hot"
            }
          ]
        }
      ]
    }
  ]
}
```

**Board 字段说明**：
- `board` 是 channel 级别的**活跃性折叠结果**（仅二值：`hot` 或 `cold`）
- `hot`：该 channel 仍有活跃监测项（包含配置为 `hot` 或 `secondary` 的项）
- `cold`：该 channel 下（排除 disabled）全部为 `cold`
- 注意：这与 `/api/status` 中逐监测项返回的 `board` (hot|secondary|cold) 不同

> 🔧 API 参考章节正在整理，以上端点示例即当前权威来源。

## 🛠️ 技术栈

**后端**
- Go 1.25+
- Gin (HTTP framework)
- SQLite / PostgreSQL
- fsnotify (配置热更新)

**前端**
- React 19
- TypeScript
- Tailwind CSS v4
- Vite

## 📝 变更日志

查看 [GitHub Releases](https://github.com/prehisle/relay-pulse/releases) 了解版本历史和最新变更。

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！请先阅读 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 📈 Star History

[![Star History Chart](https://star-history.dera.page/svg?repos=prehisle/relay-pulse&type=Date)](https://star-history.dera.page/#prehisle/relay-pulse&Date)

## ⚠️ 免责声明

本项目是基于 MIT 许可证发布的技术监测工具。

**数据性质说明**：RelayPulse 展示的状态与趋势来源于自动化技术探测，可能因网络波动、地域差异、缓存延迟等原因产生误差。展示结果仅供参考，不构成对任何服务的合规性、合法性、资质或商业信誉的判断。详见 [监测方法论说明](docs/user/methodology.md)。

**运营免责**：作者不对任何使用本软件搭建的站点（包括 relaypulse.top）上展示的第三方服务商的内容、可靠性、信誉或资金安全负责。用户与第三方服务商的交互风险自负。

**纠错机制**：如您认为数据存在错误，可通过 [GitHub Issue](https://github.com/prehisle/relay-pulse/issues/new?template=3-data-correction.yml) 提交纠错申诉，我们的目标是在 48 小时内响应（节假日或高峰期可能延迟）。

## 📄 许可证

[MIT License](LICENSE) © 2025

---

- **🌐 在线演示**: https://relaypulse.top
- **📦 镜像仓库**: `ghcr.io/prehisle/relay-pulse:latest`
- **💬 问题反馈**: https://github.com/prehisle/relay-pulse/issues
