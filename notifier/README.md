# RelayPulse Notifier

多平台通知服务，用于订阅 RelayPulse 监测状态变更通知。

## 功能特性

- 支持 **Telegram** 和 **QQ** 双平台通知
- 通过 Bot 接收状态变更通知
- 支持一键从网页导入收藏列表（Telegram）
- 可配置的限流和重试机制
- 独立部署，与 RelayPulse 主服务解耦

## 快速开始

### Docker Compose 部署

1. 创建配置文件

```bash
cp config.yaml config.local.yaml
# 编辑 config.local.yaml 配置你的服务
```

2. 创建环境变量文件

```bash
cat > .env << 'EOF'
# Telegram 配置（可选，留空则禁用）
TELEGRAM_BOT_TOKEN=your_bot_token_here

# RelayPulse 配置（必需）
RELAY_PULSE_API_TOKEN=your_api_token_here
RELAY_PULSE_EVENTS_URL=https://your-relay-pulse.com/api/events

TZ=Asia/Shanghai
EOF
```

3. 启动服务

```bash
docker compose up -d
```

4. 查看日志

```bash
docker compose logs -f notifier
```

### 直接运行二进制

```bash
# 编译
go build -o notifier ./cmd/notifier

# 运行（Telegram 模式）
TELEGRAM_BOT_TOKEN=xxx ./notifier -config config.yaml

# 运行（QQ 模式，需先部署 NapCatQQ）
./notifier -config config.yaml
```

## 配置说明

```yaml
relay_pulse:
  events_url: "https://your-relay-pulse.com/api/events"
  api_token: ""                 # 必需，环境变量 RELAY_PULSE_API_TOKEN
  poll_interval: "5s"           # 轮询间隔

telegram:
  bot_token: ""                 # 环境变量 TELEGRAM_BOT_TOKEN，留空则禁用
  bot_username: "RelayPulseBot" # 用于生成 deeplink

qq:
  enabled: false                # 是否启用 QQ 通知
  onebot_http_url: ""           # NapCatQQ HTTP API 地址
  access_token: ""              # OneBot API Token（可选）
  callback_path: "/qq/callback" # 接收上报的路径
  callback_secret: ""           # Webhook 签名密钥（可选）

database:
  driver: "sqlite"
  dsn: "file:data/notifier.db?_journal_mode=WAL&_timeout=5000&_busy_timeout=5000"

api:
  addr: ":8081"                 # HTTP API 监听地址

limits:
  max_subscriptions_per_user: 20
  rate_limit_per_second: 25     # 消息发送限速
  max_retries: 3
  bind_token_ttl: "5m"

screenshot:
  enabled: false                # 是否启用截图功能（/snap 命令）
  base_url: "https://relaypulse.top"  # 截图目标 URL
  timeout: "30s"                # 截图超时时间
  max_concurrent: 3             # 最大并发截图数
```

## 环境变量

| 变量名 | 说明 | 必需 |
|--------|------|------|
| `TELEGRAM_BOT_TOKEN` | Telegram Bot Token | 否* |
| `RELAY_PULSE_API_TOKEN` | RelayPulse Events API Token | 是 |
| `RELAY_PULSE_EVENTS_URL` | RelayPulse Events API URL | 否 |
| `TZ` | 时区（影响日志时间戳等），建议 `Asia/Shanghai` | 否 |

*至少需要配置 Telegram 或 QQ 其中之一

## QQ 通知配置指南

### 前置条件

QQ 通知依赖 [NapCatQQ](https://github.com/NapNeko/NapCatQQ) 作为协议端：

```
┌─────────────────────┐         ┌─────────────────────┐
│  Windows 电脑        │   HTTP  │  Linux 服务器        │
│                     │ ──────→ │                     │
│  - NTQQ 客户端      │         │  - notifier         │
│  - NapCatQQ         │ ←────── │  - relay-pulse      │
│                     │   HTTP  │                     │
└─────────────────────┘         └─────────────────────┘
```

### 步骤 1：部署 NapCatQQ

1. 在 Windows 电脑上安装 [NTQQ](https://im.qq.com/pcqq/index.shtml)（QQ 官方桌面版）
2. 下载并安装 [NapCatQQ](https://github.com/NapNeko/NapCatQQ/releases)
3. 启动 NTQQ 并登录机器人 QQ 号

### 步骤 2：配置 NapCatQQ

编辑 NapCatQQ 配置文件，启用 HTTP 接口：

```json
{
  "http": {
    "enable": true,
    "host": "0.0.0.0",
    "port": 3000,
    "accessToken": "your_access_token"
  },
  "httpPost": {
    "enable": true,
    "urls": [
      "http://你的服务器IP:8081/qq/callback"
    ],
    "secret": "your_callback_secret"
  }
}
```

### 步骤 3：配置 notifier

```yaml
qq:
  enabled: true
  onebot_http_url: "http://Windows电脑IP:3000"
  access_token: "your_access_token"        # 与 NapCatQQ 配置一致
  callback_path: "/qq/callback"
  callback_secret: "your_callback_secret"  # 与 NapCatQQ 配置一致
```

### 步骤 4：测试连通性

```bash
# 测试 NapCatQQ API（从服务器）
curl http://Windows电脑IP:3000/get_login_info

# 启动 notifier
./notifier -config config.yaml

# 在 QQ 群/私聊中发送
/help
```

## Bot 命令

### Telegram 命令

| 命令 | 说明 |
|------|------|
| `/start` | 开始使用 / 导入收藏 |
| `/list` | 查看当前订阅 |
| `/add <provider> <service> [channel]` | 添加订阅 |
| `/remove <provider> <service> [channel]` | 移除订阅 |
| `/clear` | 清空所有订阅 |
| `/snap` | 生成订阅服务的状态截图 |
| `/status` | 查看服务状态 |
| `/help` | 显示帮助 |

### QQ 命令

| 命令 | 权限 | 说明 |
|------|------|------|
| `/list` | 所有人 | 查看当前订阅 |
| `/add <provider> <service> [channel]` | 群管理员/私聊 | 添加订阅 |
| `/remove <provider> <service> [channel]` | 群管理员/私聊 | 移除订阅 |
| `/clear` | 群管理员/私聊 | 清空所有订阅 |
| `/snap` | 所有人 | 生成订阅服务的状态截图 |
| `/status` | 所有人 | 查看服务状态 |
| `/help` | 所有人 | 显示帮助 |

**QQ 全局指令**（群聊无需 @机器人）：

| 指令 | 说明 |
|------|------|
| `状态检查` | 快速截图订阅服务状态（30秒冷却） |

**QQ 权限说明**：
- 群聊：仅群主/管理员可执行 `/add`、`/remove`、`/clear`
- 私聊：好友可直接使用所有命令（好友即白名单）

**截图功能说明**（`/snap` 命令）：
- 需要在配置中启用 `screenshot.enabled: true`
- 依赖 [playwright-go](https://github.com/mxschmitt/playwright-go) 进行浏览器截图
- 首次截图时服务会自行下载驱动与 Chromium；要预先装好（例如构建镜像时）用与 `go.mod` 同版本的 CLI：
  `go run github.com/mxschmitt/playwright-go/cmd/playwright@$(grep -oE 'playwright-go v\S+' go.mod | cut -d' ' -f2) install --with-deps chromium`
  ⚠️ 别用 `npx playwright install`：npm 版装的驱动版本与 `go.mod` 钉的不一定一致，而 playwright-go 只认 `~/.cache/ms-playwright-go/<driver 版本>/`（如 `1.62.1/`，是 driver 版本不是模块版本）下的那一份驱动。浏览器本体在另一个目录 `~/.cache/ms-playwright/`，两边共用
- 截图内容为当前订阅服务的状态监测图

## API 端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/health` | GET | 健康检查 |
| `/api/bind-token` | POST | 创建绑定 token（Telegram 专用） |
| `/api/bind-token/{token}` | GET | 获取并消费 token |
| `/qq/callback` | POST | QQ 消息上报回调（可配置路径） |

## 前端集成

在前端设置环境变量指向 notifier 服务：

```bash
# .env.production
VITE_NOTIFIER_API_URL=https://notifier.example.com
```

点击"订阅通知"按钮后，前端会：
1. 调用 `/api/bind-token` 创建临时 token
2. 打开 Telegram deeplink 跳转到 Bot
3. Bot 自动解析 token 并导入收藏列表

## 架构

```
┌─────────────────────────────────────────────────────────────────┐
│                    relay-pulse-notifier                          │
│                                                                  │
│  ┌─────────┐    ┌─────────┐    ┌─────────┐    ┌─────────────┐  │
│  │ Poller  │───▶│ Router  │───▶│ Sender  │───▶│  Telegram   │  │
│  │         │    │         │    │  (多平台) │    │    API      │  │
│  └─────────┘    └─────────┘    └─────────┘    ├─────────────┤  │
│       ↑              ↑              │         │  OneBot API │  │
│       │              │              ▼         │   (QQ)      │  │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │                       SQLite                             │   │
│  │  poll_cursor | chats | subscriptions | deliveries |     │   │
│  │  bind_tokens (platform 字段区分 telegram/qq)            │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                  │
│  ┌─────────────┐    ┌─────────────┐                            │
│  │ Telegram Bot│    │   QQ Bot    │                            │
│  │ Long Polling│    │ HTTP 回调   │                            │
│  └─────────────┘    └─────────────┘                            │
└─────────────────────────────────────────────────────────────────┘
```

## 数据库迁移

从旧版（仅 Telegram）升级到多平台版本时，服务启动会**自动迁移**：

1. 检测到 `telegram_chats` 表（旧版 schema）
2. 创建新的多平台表（`chats`、`subscriptions`、`deliveries`）
3. 迁移现有数据，`platform` 字段设为 `telegram`
4. 旧表重命名为 `*_legacy`（保留回滚能力）

**无需手动操作**，直接更新二进制/镜像即可。

## 常见问题

### 启动时报错：`database is locked (SQLITE_BUSY)`

这通常表示**同一个 SQLite 文件**正在被另一个进程占用（例如重复启动了 notifier、容器和本地同时跑、或用 SQLite GUI 打开了数据库）。

处理建议：

- 确保只有一个 notifier 实例在运行（包含 Docker 容器/本地进程）。
- 如果你配置了相同的 `database.dsn`，给不同环境使用不同的数据库文件路径。
- 关闭可能打开了该数据库文件的 SQLite 查看/编辑工具后重试。

## 开发

```bash
# 安装依赖
go mod download

# 运行测试
go test ./...

# 构建
go build ./cmd/notifier
```

## 许可证

MIT
