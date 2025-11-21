# 配置手册

> **Audience**: 用户 | **Last reviewed**: 2025-11-21

本文档详细说明 Relay Pulse 的配置选项、环境变量和最佳实践。

## 配置文件结构

Relay Pulse 使用 YAML 格式的配置文件，默认路径为 `config.yaml`。

### 完整配置示例

```yaml
# 全局配置
interval: "1m"           # 巡检间隔（支持 Go duration 格式）
slow_latency: "5s"       # 慢请求阈值

# 存储配置
storage:
  type: "sqlite"         # 存储类型: sqlite 或 postgres
  sqlite:
    path: "monitor.db"   # SQLite 数据库文件路径
  # PostgreSQL 配置（可选）
  postgres:
    host: "localhost"
    port: 5432
    user: "monitor"
    password: "password"  # 建议使用环境变量
    database: "llm_monitor"
    sslmode: "disable"    # 生产环境建议 "require"
    max_open_conns: 25
    max_idle_conns: 5
    conn_max_lifetime: "1h"

# 监控项列表
monitors:
  - provider: "88code"         # 服务商标识（必填）
    service: "cc"              # 服务类型（必填）
    category: "commercial"     # 分类（必填）: commercial 或 public
    sponsor: "团队自有"         # 赞助者（必填）
    channel: "vip"             # 业务通道（可选）
    url: "https://api.88code.com/v1/chat/completions"  # 健康检查端点（必填）
    method: "POST"             # HTTP 方法（必填）
    api_key: "sk-xxx"          # API 密钥（可选，建议用环境变量）
    headers:                   # 请求头（可选）
      Authorization: "Bearer {{API_KEY}}"
      Content-Type: "application/json"
    body: |                    # 请求体（可选）
      {
        "model": "claude-3-opus",
        "messages": [{"role": "user", "content": "hi"}],
        "max_tokens": 1
      }
    success_contains: "content"  # 响应体必须包含的关键字（可选）
```

## 配置项详解

### 全局配置

#### `interval`
- **类型**: string (Go duration 格式)
- **默认值**: `"1m"`
- **说明**: 健康检查的间隔时间
- **示例**: `"30s"`, `"1m"`, `"5m"`, `"1h"`

#### `slow_latency`
- **类型**: string (Go duration 格式)
- **默认值**: `"5s"`
- **说明**: 超过此阈值的请求被标记为"慢请求"（黄色状态）
- **示例**: `"3s"`, `"5s"`, `"10s"`

### 存储配置

#### SQLite（默认）

```yaml
storage:
  type: "sqlite"
  sqlite:
    path: "monitor.db"  # 数据库文件路径（相对或绝对路径）
```

**适用场景**:
- 单机部署
- 开发环境
- 小规模监控（< 100 个监控项）

**限制**:
- 不支持多副本（水平扩展）
- K8s 环境需要 PersistentVolume

#### PostgreSQL

```yaml
storage:
  type: "postgres"
  postgres:
    host: "postgres-service"    # 数据库主机
    port: 5432                  # 端口
    user: "monitor"             # 用户名
    password: "secret"          # 密码（建议用环境变量）
    database: "llm_monitor"     # 数据库名
    sslmode: "require"          # SSL 模式: disable, require, verify-full
    max_open_conns: 25          # 最大打开连接数
    max_idle_conns: 5           # 最大空闲连接数
    conn_max_lifetime: "1h"     # 连接最大生命周期
```

**适用场景**:
- Kubernetes 多副本部署
- 高可用需求
- 大规模监控（> 100 个监控项）

**初始化数据库**:

```sql
CREATE DATABASE llm_monitor;
CREATE USER monitor WITH PASSWORD 'your_password';
GRANT ALL PRIVILEGES ON DATABASE llm_monitor TO monitor;
```

### 数据保留策略

- 服务会自动保留最近 30 天的 `probe_history` 数据，后台定时器每 24 小时调用 `CleanOldRecords(30)` 删除更早的样本。
- 该策略对 SQLite 与 PostgreSQL 均生效，无需额外配置即可防止数据库无限增长。
- 保留窗口目前固定为 30 天，如需调整需修改源码或在 Issue 中提出新特性需求。
- 运维层面的验证与手动清理命令请参考 [运维手册 - 数据保留策略](operations.md#数据保留策略)。

### 监控项配置

#### 必填字段

##### `provider`
- **类型**: string
- **说明**: 服务商标识（用于分组和显示）
- **示例**: `"openai"`, `"anthropic"`, `"88code"`

##### `service`
- **类型**: string
- **说明**: 服务类型标识
- **示例**: `"gpt-4"`, `"claude"`, `"cc"`, `"cx"`

##### `category`
- **类型**: string
- **说明**: 分类标识
- **可选值**: `"commercial"`（推广站）, `"public"`（公益站）

##### `sponsor`
- **类型**: string
- **说明**: 提供 API Key 的赞助者名称
- **示例**: `"团队自有"`, `"用户捐赠"`, `"John Doe"`

##### `url`
- **类型**: string
- **说明**: 健康检查的 HTTP 端点
- **示例**: `"https://api.openai.com/v1/chat/completions"`

##### `method`
- **类型**: string
- **说明**: HTTP 请求方法
- **可选值**: `"GET"`, `"POST"`, `"PUT"`, `"DELETE"`, `"PATCH"`

#### 可选字段

##### `channel`
- **类型**: string
- **说明**: 业务通道标识（用于区分同一服务的不同渠道）
- **示例**: `"vip"`, `"free"`, `"premium"`

##### `api_key`
- **类型**: string
- **说明**: API 密钥（强烈建议使用环境变量代替）
- **示例**: `"sk-xxx"`

##### `headers`
- **类型**: map[string]string
- **说明**: 自定义请求头
- **占位符**: `{{API_KEY}}` 会被替换为实际的 API Key
- **示例**:
  ```yaml
  headers:
    Authorization: "Bearer {{API_KEY}}"
    Content-Type: "application/json"
    X-Custom-Header: "value"
  ```

##### `body`
- **类型**: string 或 `!include` 引用
- **说明**: 请求体内容
- **占位符**: `{{API_KEY}}` 会被替换
- **示例**:
  ```yaml
  # 内联方式
  body: |
    {
      "model": "gpt-4",
      "messages": [{"role": "user", "content": "test"}],
      "max_tokens": 1
    }

  # 引用外部文件
  body: "!include data/gpt4_request.json"
  ```

##### `success_contains`
- **类型**: string
- **说明**: 响应体必须包含的关键字（用于语义验证）
- **示例**: `"content"`, `"choices"`, `"success"`
- **行为**: 如果响应体不包含此关键字，即使 HTTP 状态码是 2xx，也会被标记为黄色状态

## 环境变量覆盖

为了安全性，强烈建议使用环境变量来管理 API Key，而不是写在配置文件中。

### API Key 环境变量

**命名规则**:

```
MONITOR_<PROVIDER>_<SERVICE>_API_KEY
```

- `<PROVIDER>`: 配置中的 `provider` 字段（大写，`-` 替换为 `_`）
- `<SERVICE>`: 配置中的 `service` 字段（大写，`-` 替换为 `_`）

**示例**:

| 配置 | 环境变量名 |
|------|-----------|
| `provider: "88code"`, `service: "cc"` | `MONITOR_88CODE_CC_API_KEY` |
| `provider: "openai"`, `service: "gpt-4"` | `MONITOR_OPENAI_GPT4_API_KEY` |
| `provider: "anthropic"`, `service: "claude-3"` | `MONITOR_ANTHROPIC_CLAUDE3_API_KEY` |

**使用方式**:

```bash
# 方式1：直接导出
export MONITOR_88CODE_CC_API_KEY="sk-your-real-key"
./monitor

# 方式2：使用 .env 文件（推荐）
echo "MONITOR_88CODE_CC_API_KEY=sk-your-real-key" > .env
docker compose --env-file .env up -d
```

### 存储配置环境变量

#### SQLite

```bash
MONITOR_STORAGE_TYPE=sqlite
MONITOR_SQLITE_PATH=/data/monitor.db
```

#### PostgreSQL

```bash
MONITOR_STORAGE_TYPE=postgres
MONITOR_POSTGRES_HOST=postgres-service
MONITOR_POSTGRES_PORT=5432
MONITOR_POSTGRES_USER=monitor
MONITOR_POSTGRES_PASSWORD=your_secure_password
MONITOR_POSTGRES_DATABASE=llm_monitor
MONITOR_POSTGRES_SSLMODE=require
```

### CORS 配置

```bash
# 允许额外的跨域来源（逗号分隔）
MONITOR_CORS_ORIGINS=http://localhost:5173,http://localhost:3000
```

### 前端环境变量

前端支持以下环境变量（需在构建时设置）：

#### API 配置

```bash
# API 基础 URL（可选，默认为相对路径）
VITE_API_BASE_URL=http://localhost:8080

# 是否使用 Mock 数据（开发调试用）
VITE_USE_MOCK_DATA=false
```

#### Google Analytics（可选）

```bash
# GA4 Measurement ID（格式: G-XXXXXXXXXX）
VITE_GA_MEASUREMENT_ID=G-XXXXXXXXXX
```

**获取 GA4 Measurement ID**：
1. 访问 [Google Analytics](https://analytics.google.com/)
2. 创建或选择属性
3. 在"管理" > "数据流" > "网站"中查看 Measurement ID

**使用方式**：

```bash
# 开发环境：在 frontend/.env.development 中设置
VITE_GA_MEASUREMENT_ID=

# 生产环境：在 frontend/.env.production 中设置
VITE_GA_MEASUREMENT_ID=G-XXXXXXXXXX

# 或在构建时通过环境变量传入
export VITE_GA_MEASUREMENT_ID=G-XXXXXXXXXX
cd frontend && npm run build
```

**追踪事件**：

GA4 会自动追踪以下事件：
- **页面浏览**（自动） - 用户访问仪表板
- **用户筛选**：
  - `change_time_range` - 切换时间范围（24h/7d/30d）
  - `filter_service` - 筛选服务提供商或服务类型
  - `filter_channel` - 筛选业务通道
  - `filter_category` - 筛选分类（commercial/public）
- **用户交互**：
  - `change_view_mode` - 切换视图模式（table/grid）
  - `manual_refresh` - 点击刷新按钮
  - `click_external_link` - 点击外部链接（查看提供商/赞助商）
- **性能监控**：
  - `api_request` - API 请求性能（包含延迟、成功/失败状态）
  - `api_error` - API 错误（包含错误类型：HTTP_XXX、NETWORK_ERROR）

**注意**：
- 开发环境建议留空 `VITE_GA_MEASUREMENT_ID`，避免污染生产数据
- 如果未设置 Measurement ID，GA4 脚本不会加载

## 配置验证

服务启动时会自动验证配置：

### 验证规则

1. **必填字段检查**: `provider`, `service`, `category`, `sponsor`, `url`, `method`
2. **HTTP 方法校验**: 必须是 `GET`, `POST`, `PUT`, `DELETE`, `PATCH` 之一
3. **唯一性检查**: `provider + service + channel` 组合必须唯一
4. **`category` 枚举**: 必须是 `commercial` 或 `public`
5. **存储类型校验**: 必须是 `sqlite` 或 `postgres`

### 验证失败示例

```bash
# 缺少必填字段
❌ 无法加载配置文件: monitor[0]: 缺少必填字段 'category'

# 重复的 provider + service
❌ 无法加载配置文件: 重复的监控项: provider=88code, service=cc

# 无效的 HTTP 方法
❌ 无法加载配置文件: monitor[0]: 无效的 method 'INVALID'
```

## 配置热更新

Relay Pulse 支持配置文件的热更新，修改配置后无需重启服务。

### 工作原理

1. 使用 `fsnotify` 监听配置文件变更
2. 检测到变更后，先验证新配置
3. 如果验证通过，原子性地更新运行时配置
4. 如果验证失败，保持旧配置并输出错误日志

### 使用示例

```bash
# 启动服务
docker compose up -d

# 修改配置（添加新的监控项）
vi config.yaml

# 观察日志
docker compose logs -f monitor

# 应该看到:
# [Config] 检测到配置文件变更，正在重载...
# [Config] 热更新成功！已加载 3 个监控任务
# [Scheduler] 配置已更新，下次巡检将使用新配置
# [Scheduler] 立即触发巡检
```

### 注意事项

- **存储配置不支持热更新**: 修改 `storage` 配置需要重启服务
- **环境变量不热更新**: 环境变量覆盖的 API Key 不会热更新
- **语法错误**: 如果新配置有语法错误，服务会保持旧配置并输出错误

## 配置最佳实践

### 1. API Key 管理

❌ **不推荐**（不安全）:

```yaml
monitors:
  - provider: "openai"
    api_key: "sk-proj-real-key-here"  # 不要写在配置文件中！
```

✅ **推荐**（安全）:

```yaml
monitors:
  - provider: "openai"
    # api_key 留空，使用环境变量
```

```bash
# .env 文件（添加到 .gitignore）
MONITOR_OPENAI_GPT4_API_KEY=sk-proj-real-key-here
```

### 2. 大型请求体

❌ **不推荐**（配置文件过长）:

```yaml
body: |
  {
    "model": "gpt-4",
    "messages": [/* 很长的消息列表 */],
    "max_tokens": 1000,
    "temperature": 0.7,
    /* 更多配置... */
  }
```

✅ **推荐**（使用 `!include`）:

```yaml
body: "!include data/gpt4_request.json"
```

```json
// data/gpt4_request.json
{
  "model": "gpt-4",
  "messages": [/* 很长的消息列表 */],
  "max_tokens": 1000,
  "temperature": 0.7
}
```

### 3. 环境隔离

```bash
# 开发环境
config.yaml                # 本地开发配置
.env.local                 # 本地 API Keys（添加到 .gitignore）

# 生产环境
config.production.yaml     # 生产配置（不含敏感信息）
deploy/relaypulse.env      # 生产 API Keys（添加到 .gitignore）
```

### 4. 安全加固

1. **所有敏感信息使用环境变量**
2. **生产环境启用 PostgreSQL SSL**: `sslmode: "require"`
3. **限制 CORS**: 只允许信任的域名
4. **定期轮换 API Key**
5. **使用最小权限原则**: 数据库用户只授予必要权限

## 配置示例库

### 示例1：OpenAI GPT-4

```yaml
monitors:
  - provider: "openai"
    service: "gpt-4"
    category: "commercial"
    sponsor: "团队"
    url: "https://api.openai.com/v1/chat/completions"
    method: "POST"
    headers:
      Authorization: "Bearer {{API_KEY}}"
      Content-Type: "application/json"
    body: |
      {
        "model": "gpt-4",
        "messages": [{"role": "user", "content": "hi"}],
        "max_tokens": 1
      }
    success_contains: "choices"
```

### 示例2：Anthropic Claude

```yaml
monitors:
  - provider: "anthropic"
    service: "claude-3"
    category: "public"
    sponsor: "社区"
    url: "https://api.anthropic.com/v1/messages"
    method: "POST"
    headers:
      x-api-key: "{{API_KEY}}"
      anthropic-version: "2023-06-01"
      Content-Type: "application/json"
    body: |
      {
        "model": "claude-3-opus-20240229",
        "messages": [{"role": "user", "content": "hi"}],
        "max_tokens": 1
      }
    success_contains: "content"
```

### 示例3：自定义 REST API

```yaml
monitors:
  - provider: "custom-api"
    service: "health"
    category: "public"
    sponsor: "自有"
    url: "https://api.example.com/health"
    method: "GET"
    success_contains: "ok"
```

## 故障排查

### 配置不生效

1. 检查配置文件路径是否正确
2. 查看日志中的验证错误
3. 确认环境变量格式正确

### 热更新失败

1. 检查配置文件语法（YAML 格式）
2. 验证必填字段是否完整
3. 查看日志中的具体错误信息

### 数据库连接失败

1. PostgreSQL: 检查 `host`, `port`, `user`, `password` 是否正确
2. SQLite: 检查文件路径和权限
3. 查看数据库日志

## 下一步

- [运维手册](operations.md) - 日常运维和故障排查
- [API 规范](../reference/api.md) - REST API 详细文档
