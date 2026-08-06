# 配置手册

> **Audience**: 用户 | **Last reviewed**: 2026-04-17

本文档详细说明 Relay Pulse 的配置选项、环境变量和最佳实践。

## 配置文件结构

Relay Pulse 使用 YAML 格式的配置文件，默认路径为 `config.yaml`。

### 完整配置示例

```yaml
# 全局配置（作为最终兜底，模板和 monitor 可覆盖）
interval: "1m"           # 巡检间隔（支持 Go duration 格式）
slow_latency: "5s"       # 慢请求阈值
timeout: "10s"           # 请求超时时间

# 重试配置（可选，全局默认值）
retry: 2                 # 额外重试次数（默认 0，不重试）
retry_base_delay: "200ms"  # 退避基准间隔
retry_max_delay: "2s"      # 退避最大间隔
retry_jitter: 0.2          # 抖动比例（0-1）

# 标签系统（可选）
enable_annotations: true
# key_type 字段控制 API Key 来源标签（默认所有通道自动标注"官方 API"）
# 用户 Key 通道设置 key_type: user 即自动切换为"用户 Key"标签

# 风险服务商配置（旧版兼容，推荐迁移到 annotation_rules）
risk_providers:
  - provider: "risky-provider"
    risks:
      - label: "跑路风险"
        discussion_url: "https://github.com/org/repo/discussions/123"

# 通道技术细节暴露配置（可选）
expose_channel_details: true  # 是否在 API 中返回 probe_url/template_name（默认 true）
channel_details_providers:    # provider 级覆盖
  - provider: "sensitive-provider"
    expose: false              # 隐藏该 provider 的技术细节

# 赞助通道置顶配置
sponsor_pin:
  enabled: true          # 是否启用置顶功能
  max_pinned: 3          # 最多置顶数量
  min_uptime: 95.0       # 最低可用率要求
  min_level: "beacon"    # 最低赞助级别

# 热板/冷板配置
boards:
  enabled: false         # 是否启用热板/冷板功能

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

# 监测项列表
monitors:
  - provider: "88code"         # 服务商标识（必填）
    service: "cc"              # 服务类型（必填）
    category: "commercial"     # 分类（可选，默认 commercial）: commercial 或 public
    sponsor: "团队自有"         # 赞助者（必填）
    sponsor_level: "beacon"    # 赞助等级（可选）: public/signal/pulse/beacon/backbone/core
    key_type: "official"       # API Key 类型（可选）: official（默认）或 user
    channel: "vip"             # 业务通道（可选）
    board: "hot"               # 板块（可选）: hot（默认）、secondary 或 cold
    price_min: 0.05            # 参考倍率下限（可选）
    price_max: 0.2             # 参考倍率（可选）: 显示为 "0.125 / 0.05~0.2"
    listed_since: "2024-06-15" # 收录日期（可选）: 用于计算收录天数
    expires_at: "2025-12-31"   # 到期日期（可选）: 过期后赞助等级自动降为 pulse（板块由可用率决定，不随到期变动）
    base_url: "https://api.88code.com"    # 服务商基础地址（模板模式必填）
    template: "cc-haiku-arith"             # 引用 templates/ 目录下的模板（模板模式必填）
    api_key: "sk-xxx"          # API 密钥（可选，建议用环境变量）
    # model 和 request_model 由模板预设（可选覆盖）
```

### `monitors.d/` 目录化通道管理

除了把所有通道塞进 `config.yaml` 的 `monitors:` 数组外，系统还支持将**每个通道放到独立的 YAML 文件**中，集中存放在 `monitors.d/` 目录。这是管理后台（`/api/admin/monitors/*`）与自助收录（onboarding）落盘通道时的默认方式。

**文件命名约定**：`{provider}--{service}--{channel}.yaml`（父子模型写在同一文件中）。

**单文件结构**：

```yaml
metadata:
  source: "admin"          # admin | onboarding | change_request | manual
  revision: 3              # 修订序号
  created_at: "2026-04-01T10:00:00Z"
  updated_at: "2026-04-17T09:30:00Z"
monitors:
  - provider: "88code"
    service: "cc"
    channel: "vip"
    category: "commercial"
    sponsor: "团队自有"
    base_url: "https://api.88code.com"
    template: "cc-haiku-arith"
    api_key: "sk-xxx"
```

**与 `config.yaml` 的关系**：

- 两者在启动时合并为同一份 monitor 列表。
- **冲突约束**：同一 PSC (`provider/service/channel`) 不能同时出现在 `config.yaml` 和 `monitors.d/`，否则启动报错。
- 热更新同时监听 `config.yaml` 与 `monitors.d/` 目录，新增/修改/删除文件都会触发重载。
- 删除走**软归档**：通过 admin API 删除时，文件被移到 `monitors.d/.archive/` 而非直接清除，方便回溯。

**建议实践**：`config.yaml` 只保留全局设置与少量"硬编码"核心通道，其余通道统一走 `monitors.d/`，便于通过管理后台或自助收录流程增删改。

## 配置项详解

### 全局配置

#### `server`（可信代理边界）

决定服务如何判定"真实客户端 IP"。这个判定是所有按 IP 计数的防护（探测限流、收录/变更每日提交配额、`submitter_ip_hash` 审计）的共同信任根，判错则三者同时失效。

```yaml
server:
  trusted_platform: ""              # 可选；仅在入口会强制覆写该 Header 时设置
  trusted_proxies:                  # 默认仅信任本机回环
    - "127.0.0.1"
    - "::1"
```

- `trusted_platform`
  - **类型**: string
  - **默认值**: `""`（不启用）
  - **说明**: 由可信入口覆写、客户端无法伪造的客户端 IP Header。设置后直接采信该 Header，不再解析 `X-Forwarded-For` 链。
  - **示例**: Cloudflare（含 Tunnel）填 `"CF-Connecting-IP"`
  - ⚠️ **前置条件**: 应用监听端口必须不可被绕过该入口直连（例如只绑 `127.0.0.1`）。否则任何人都能自带这个 Header 伪造来源 IP——这比不设更危险。

- `trusted_proxies`
  - **类型**: string 数组（IP 或 CIDR）
  - **默认值**: `["127.0.0.1", "::1"]`
  - **说明**: 只有来自这些地址的转发，其 `X-Forwarded-For` / `X-Real-IP` 才被采信。反向代理与应用同机（cloudflared / nginx sidecar）时用默认值即可；代理在另一台机器时填该机地址。
  - **不要**填 `0.0.0.0/0`：等于信任所有来源的 XFF，任何客户端都能任意伪造来源 IP。

两项均只在 HTTP 服务创建时读取，**修改后需重启**（热更新不会重建 gin engine）。配置非法时启动/热更新校验会直接失败；万一漏到运行时，会收紧为"不信任任何代理"而非退回宽松默认。

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

#### `timeout`
- **类型**: string (Go duration 格式)
- **默认值**: `"10s"`
- **说明**: 请求超时时间，超过此时间未响应则视为失败（红色状态）
- **示例**: `"10s"`, `"30s"`, `"1m"`

#### `degraded_weight`
- **类型**: float
- **默认值**: `0.7`
- **说明**: 黄色状态在可用率统计中的权重，合法范围 0-1；填 0 视为未配置，使用默认值 0.7
- **计算公式**: `可用率 = (绿色次数 × 1.0 + 黄色次数 × degraded_weight) / 总次数 × 100`

### 展示开关（运行时生效）

以下开关只影响前端展示，**改 yaml 即触发热更新**，不需要重建镜像或重启容器（前端在下次拉取 `/api/status` 时读到新值）。

#### `hide_price_column`
- **类型**: bool
- **默认值**: `false`
- **说明**: 隐藏列表与卡片中的「价格」列。开启后前端会一并清掉 URL 里残留的按价格排序参数。

#### `hide_category_filter`
- **类型**: bool
- **默认值**: `false`
- **说明**: 隐藏列表顶部的「分类」筛选器（公益站 / 商业站）。筛选器多了会把顶部工具栏挤到换行，通道分类维度较少时可以关掉。
- **注意**: 隐藏的只是筛选入口，通道的 `category` 字段照常下发。开启后 URL 里残留的 `?category=` 会被视为未选中、不再过滤数据（参数本身保留，关掉开关即恢复原选择）。

### 重试配置

当探测失败时，系统可按指数退避策略进行重试。重试配置支持全局、模板和监测项三级覆盖。

**优先级**: `monitor` > `template` > `global`

模板通过 `probe` 块配置探测参数（包括 slow_latency、timeout、retry 系列），在 `resolveTemplates` 阶段填入 monitor 级别，作为 monitor 的默认值。

```yaml
# 全局重试配置（最终兜底）
retry: 2                    # 额外重试次数（默认 0，不重试）
retry_base_delay: "200ms"   # 退避基准间隔（默认 200ms）
retry_max_delay: "2s"       # 退避最大间隔（默认 2s）
retry_jitter: 0.2           # 抖动比例，0-1（默认 0.2，0 表示无抖动）

# 监测项级覆盖（在 monitors 内配置，优先级最高）
monitors:
  - provider: "example"
    service: "cc"
    retry: 3                # 覆盖模板和全局
    retry_base_delay: "1s"
    retry_max_delay: "10s"
    retry_jitter: 0.5
    # ...
```

#### `retry`
- **类型**: integer
- **默认值**: `0`（不重试）
- **说明**: 额外重试次数，**不含首次尝试**。设为 2 表示最多尝试 3 次（1 次原始 + 2 次重试）
- **类型区分**: 使用 `*int` 指针，区分"未设置"（继承上级）和"显式设为 0"（不重试）

#### `retry_base_delay`
- **类型**: string (Go duration 格式)
- **默认值**: `"200ms"`
- **说明**: 指数退避的基准间隔。第 N 次重试的延迟 = `min(base_delay × 2^N, max_delay) + random_jitter`

#### `retry_max_delay`
- **类型**: string (Go duration 格式)
- **默认值**: `"2s"`
- **说明**: 退避延迟的上限，防止重试间隔无限增长

#### `retry_jitter`
- **类型**: float
- **默认值**: `0.2`
- **说明**: 抖动比例（0-1），在计算的退避间隔基础上增加随机偏移。0 表示无抖动，1 表示最大 100% 的随机偏移
- **类型区分**: 使用 `*float64` 指针，区分"未设置"（继承上级）和"显式设为 0"（无抖动）

#### 退避公式

```
delay = min(base_delay × 2^attempt, max_delay)
jitter = delay × random(0, jitter_ratio)
actual_delay = delay + jitter
```

例如 `retry=2, base_delay=200ms, max_delay=2s, jitter=0.2`：
- 第 1 次重试: 200ms + 随机 0~40ms
- 第 2 次重试: 400ms + 随机 0~80ms

### 赞助通道置顶配置

用于在页面初始加载时置顶符合条件的赞助通道，用户点击任意排序按钮后置顶失效，刷新页面恢复。

```yaml
sponsor_pin:
  enabled: true           # 是否启用置顶功能（默认 true）
  max_pinned: 3           # 最多置顶数量（默认 3）
  min_uptime: 95.0        # 最低可用率要求（默认 95%）
  min_level: "beacon"     # 最低赞助级别（默认 beacon）
```

#### `sponsor_pin.enabled`
- **类型**: boolean
- **默认值**: `true`
- **说明**: 是否启用赞助通道置顶功能

#### `sponsor_pin.max_pinned`
- **类型**: integer
- **默认值**: `10`
- **说明**: 最多置顶的通道数量（全局硬上限）

#### `sponsor_pin.min_uptime`
- **类型**: float
- **默认值**: `95.0`
- **说明**: 置顶的最低可用率要求（百分比），低于此值的通道不会被置顶

#### `sponsor_pin.min_level`
- **类型**: string
- **默认值**: `"beacon"`
- **可选值**: `"public"`, `"signal"`, `"pulse"`, `"beacon"`, `"backbone"`, `"core"`
- **说明**: 置顶的最低赞助级别，级别低于此值的通道不会被置顶
- **级别权重**: `core` > `backbone` > `beacon` > `pulse` > `signal` > `public`

#### 置顶规则

1. **置顶条件**: 通道必须同时满足以下条件才会被置顶：
   - 有 `sponsor_level` 配置
   - 无风险标记（`risks` 数组为空或未配置）
   - 可用率 ≥ `min_uptime`
   - 赞助级别 ≥ `min_level`

2. **排序规则**:
   - 按赞助级别降序排序（`core` > `backbone` > `beacon`）
   - 同级别按可用率降序排序
   - 同可用率按响应延迟升序排序（低延迟优先）
   - 最终置顶数量受 `max_pinned` 全局截断限制
   - 其余项按可用率降序排序

3. **视觉效果**: 置顶项显示对应标签颜色的淡色背景（5% 透明度）

4. **交互行为**:
   - 用户点击任意排序按钮后，置顶效果失效
   - 刷新页面后，置顶效果恢复

5. **多模型组限制**:
   - 多模型组的置顶完全基于父通道的 `sponsor_level` 和 annotations，子通道不独立参与置顶评定
   - 同一 `provider + service` 组合最多占一个置顶名额
   - 不支持反向分组置顶（reverse grouping）

### 板块配置（主板/备板/冷板）

用于将监测项分为三类板块，适用于不同生命周期阶段的通道管理：

| 板块 | 说明 | 探测 | 适用场景 |
|------|------|------|----------|
| **主板 (hot)** | 活跃稳定的通道 | ✅ 正常探测 | 默认板块，稳定运行的服务 |
| **备板 (secondary)** | 观察期通道 | ✅ 正常探测 | 新上线通道、短期不稳定待观察 |
| **冷板 (cold)** | 归档通道 | ❌ 停止探测 | 长期不可用、已下线的历史通道 |

```yaml
# 全局配置
boards:
  enabled: true           # 是否启用板块功能（默认 false）

# 监测项配置
monitors:
  # 主板（默认）
  - provider: "88code"
    service: "cc"
    # board 不配置或配置为 "hot"，默认在主板
    # ...

  # 备板：新上线或观察期通道
  - provider: "newprovider"
    service: "cc"
    board: "secondary"         # 备板：继续探测，单独展示
    # ...

  # 冷板：归档通道
  - provider: "oldprovider"
    service: "cc"
    board: "cold"              # 冷板：停止探测，仅展示历史
    cold_reason: "该渠道长期不稳定，先归档节省探测资源"  # 归档原因（可选）
    # ...

  # 自动冷板恢复：解除 runtime cold override
  - provider: "recover-provider"
    service: "cc"
    auto_cold_exempt: true     # 清除 runtime 自动冷板，保持为 true 期间不再自动冷板
    # ...
```

#### `boards.enabled`
- **类型**: boolean
- **默认值**: `false`
- **说明**: 是否启用板块功能；禁用时所有监测项均视为主板，前端不显示板块切换器

#### 监测项 `board`
- **类型**: string
- **默认值**: `"hot"`
- **可选值**: `"hot"`, `"secondary"`, `"cold"`
- **说明**: 监测项所属板块
  - `hot`: 主板，正常监测，实时更新数据（默认）
  - `secondary`: 备板，正常监测，用于新上线或观察期通道；作为自动移板锚点时**不会被自动升到主板**（仅在可用率过低时自动冷板）
  - `cold`: 冷板，停止监测，仅展示历史数据。可手动配置，也可在启用 `boards.auto_move` 时因 7 天可用率低于 `threshold_cold` 被自动移入；自动冷板为 sticky，不会自动恢复

#### 监测项 `cold_reason`
- **类型**: string
- **默认值**: `""`（空）
- **说明**: 移入冷板的原因说明（仅用于 `board: cold` 的监测项）
- **约束**: 仅当 `board: cold` 时有效；如果在非冷板项中配置，启动时会输出警告并自动清空

#### 监测项 `auto_cold_exempt`
- **类型**: boolean
- **默认值**: `false`
- **说明**: 人工解除自动冷板。设为 `true` 时会立即清除该通道的 runtime cold override，并在保持为 `true` 期间不再自动移入冷板
- **用法**: 管理员将 `auto_cold_exempt: true` 加入 config.yaml → 热更新触发清除 → scheduler 恢复探测 → 通道重新参与可用率评估

#### `boards.auto_move`

基于 7 天可用率自动移板。阈值关系：`threshold_cold < threshold_down < threshold_up`

**锚点语义**：配置板位（`board` 字段）是自动移板的"天花板"，自动移板只在配置板位及以下浮动、绝不向上越板：
- `board=hot`：可用率低于 `threshold_down` 降到 secondary，恢复到 `threshold_up` 升回 hot，过低则冷板；
- `board=secondary`：**永不自动升 hot**（无论可用率多高），仅在可用率低于 `threshold_cold` 时自动冷板；
- 要让通道完全钉死在配置板位（连冷板也不参与），用 `auto_move_exempt: true`。

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | `false` | 是否启用自动移板 |
| `threshold_cold` | float64 | `10.0` | 低于该值自动移入冷板（sticky，不自动恢复） |
| `threshold_down` | float64 | `50.0` | `board=hot` 通道低于该值降到 secondary |
| `threshold_up` | float64 | `55.0` | 被降级的 `board=hot` 通道达到该值升回 hot（**不影响 `board=secondary` 通道**） |
| `min_probes` | int | `10` | 7 天内最少探测次数，不足则跳过判断（保护新通道） |
| `check_interval` | string | `"30m"` | 自动移板评估周期 |

#### 与现有机制的关系

| 状态 | 探测 | 存储 | 展示 | 用途 |
|------|-----|-----|-----|------|
| `disabled: true` | ❌ | ❌ | ❌ | 彻底禁用 |
| `hidden: true` | ✅ | ✅ | ❌ | 临时隐藏但继续监测 |
| `board: hot` | ✅ | ✅ | ✅ | **主板，正常监测** |
| `board: secondary` | ✅ | ✅ | ✅ | **备板，稳定停留不自动升主板** |
| `board: cold` | ❌ | ❌ | ✅ | **冷板，展示历史但不探测** |

#### 前端交互

- 当 `boards.enabled: true` 时，控制栏显示板块下拉菜单（带图标）
  - 🔥 主板 (Hot) - 活跃稳定的通道
  - 📊 备板 (Secondary) - 新上线或观察期通道
  - ❄️ 冷板 (Cold) - 归档的历史通道
  - 🌐 全部 (All) - 显示所有板块
- 切换到冷板时，页面顶部显示提示："冷板监测项已暂停探测"
- URL 支持 `?board=hot|secondary|cold|all` 参数用于分享链接

#### API 查询参数

```bash
# 查询主板（默认）
curl "http://localhost:8080/api/status?board=hot"

# 查询备板
curl "http://localhost:8080/api/status?board=secondary"

# 查询冷板
curl "http://localhost:8080/api/status?board=cold"

# 查询所有板块
curl "http://localhost:8080/api/status?board=all"
```

#### `max_concurrency`
- **类型**: integer
- **默认值**: `10`
- **说明**: 单轮巡检允许的最大并发探测数
- **特殊值**:
  - `0` 或未配置: 使用默认值 10
  - `-1`: 无限制，自动扩容到监测项数量
  - `>0`: 硬上限，超过时排队等待
- **调优建议**:
  - 小规模 (<20 项): 10-20
  - 中等规模 (20-100 项): 50-100
  - 大规模 (>100 项): `-1` 或更高值

#### `stagger_probes`
- **类型**: boolean
- **默认值**: `true`
- **说明**: 是否在单个周期内对探测进行错峰分布，避免流量突发
- **行为**:
  - `true`: 将监测项均匀分散在整个巡检周期内执行（推荐）
  - `false`: 所有监测项同时执行（仅用于调试或压测）

### GitHub 配置

用于 GitHub API 访问的通用配置，目前用于公告通知功能（拉取 GitHub Discussions）。

```yaml
github:
  token: ""                # GitHub Personal Access Token（建议用环境变量）
  proxy: ""                # 代理地址（支持 HTTP/HTTPS/SOCKS5）
  timeout: "30s"           # 请求超时时间
```

#### `github.token`
- **类型**: string
- **默认值**: 空
- **环境变量**: `GITHUB_TOKEN`（优先级高于配置文件）
- **说明**: GitHub Personal Access Token，用于 GraphQL API 访问
- **权限要求**: 只需 `public_repo`（读取公开仓库）或无权限（匿名访问，但容易被限流）
- **获取方式**: GitHub → Settings → Developer settings → Personal access tokens → Tokens (classic) → Generate new token

#### `github.proxy`
- **类型**: string
- **默认值**: 空（回退到 `HTTPS_PROXY` 环境变量）
- **说明**: 访问 GitHub API 的代理地址，适用于网络受限环境
- **支持格式**:
  ```
  # HTTP/HTTPS 代理
  http://host:port
  http://user:pass@host:port
  https://host:port

  # SOCKS5 代理（支持账号密码认证）
  socks5://host:port
  socks5://user:pass@host:port
  socks://host:port              # socks:// 是 socks5:// 的别名
  socks://user:pass@host:port
  ```
- **示例**:
  ```yaml
  github:
    proxy: "socks5://yjxt:password@1.2.3.4:5555"
  ```

#### `github.timeout`
- **类型**: string (Go duration 格式)
- **默认值**: `"30s"`
- **说明**: GitHub API 请求超时时间

### 公告通知配置

用于在前端显示 GitHub Discussions 公告，提示用户关注最新动态。

```yaml
announcements:
  enabled: true            # 是否启用公告功能（默认 true）
  owner: "prehisle"        # GitHub 仓库所有者
  repo: "relay-pulse"      # GitHub 仓库名称
  category_name: "Announcements"  # Discussions 分类名称
  poll_interval: "15m"     # 后端轮询间隔
  window_hours: 72         # 显示近 N 小时内的公告（默认 72，即 3 天）
  max_items: 20            # 最大拉取条数
  api_max_age: 60          # API 响应缓存时间（秒）
```

#### `announcements.enabled`
- **类型**: boolean（指针类型，支持显式 false）
- **默认值**: `true`
- **说明**: 是否启用公告功能；设为 `false` 完全禁用

#### `announcements.owner` / `announcements.repo`
- **类型**: string
- **默认值**: `"prehisle"` / `"relay-pulse"`
- **说明**: GitHub 仓库坐标，用于拉取 Discussions

#### `announcements.category_name`
- **类型**: string
- **默认值**: `"Announcements"`
- **说明**: Discussions 分类名称（不区分大小写）

#### `announcements.poll_interval`
- **类型**: string (Go duration 格式)
- **默认值**: `"15m"`
- **说明**: 后端轮询 GitHub API 的间隔

#### `announcements.window_hours`
- **类型**: integer
- **默认值**: `72`（3 天）
- **说明**: 只显示近 N 小时内创建的公告

#### `announcements.max_items`
- **类型**: integer
- **默认值**: `20`
- **说明**: 每次拉取的最大 Discussions 条数

#### `announcements.api_max_age`
- **类型**: integer
- **默认值**: `60`
- **说明**: 前端 API 响应的 Cache-Control max-age（秒）

### 事件通知配置

用于订阅服务状态变更事件，支持外部系统（如 Cloudflare Worker）轮询获取事件并触发通知（如 Telegram 消息）。

```yaml
events:
  enabled: true           # 是否启用事件功能（默认 false）
  mode: "model"           # 事件检测粒度："model"（默认）或 "channel"
  down_threshold: 2       # 连续 N 次不可用触发 DOWN 事件（默认 2）
  up_threshold: 1         # 连续 N 次可用触发 UP 事件（默认 1）
  channel_down_threshold: 1    # 通道级 DOWN 阈值（mode=channel 时生效）
  channel_count_mode: "recompute"  # 通道级计数模式（mode=channel 时生效）
  api_token: ""           # API 访问令牌（空=无鉴权）
```

#### `events.enabled`
- **类型**: boolean
- **默认值**: `false`
- **说明**: 是否启用事件检测和 API 端点

#### `events.down_threshold`
- **类型**: integer
- **默认值**: `2`
- **说明**: 连续多少次不可用（红色状态）才触发 DOWN 事件
- **设计意图**: 避免偶发故障产生误报

#### `events.up_threshold`
- **类型**: integer
- **默认值**: `1`
- **说明**: 连续多少次可用（绿色或黄色状态）才触发 UP 事件
- **设计意图**: 服务恢复后尽快通知

#### `events.api_token`
- **类型**: string
- **默认值**: `""`（空，无鉴权）
- **说明**: 事件 API 的访问令牌，用于保护 `/api/events` 端点
- **使用方式**: 请求时需在 Header 中携带 `Authorization: Bearer <token>`

#### `events.mode`
- **类型**: string
- **默认值**: `"model"`
- **可选值**: `"model"`, `"channel"`
- **说明**: 事件检测粒度模式
  - `model`: 模型级事件检测（默认），每个模型独立触发 DOWN/UP 事件
  - `channel`: 通道级事件检测，基于通道内模型的整体状态触发事件
- **使用场景**:
  - `model`: 需要精细监控每个模型状态变化的场景
  - `channel`: 只关心通道整体可用性，减少事件噪音

#### `events.channel_down_threshold`
- **类型**: integer
- **默认值**: `1`
- **说明**: 通道级 DOWN 事件的触发阈值（仅 `mode: channel` 时生效）
- **行为**: 当通道内 DOWN 状态的模型数量 ≥ 此阈值时，触发通道级 DOWN 事件
- **示例**: 设为 `2` 表示至少 2 个模型 DOWN 才触发通道 DOWN

#### `events.channel_count_mode`
- **类型**: string
- **默认值**: `"recompute"`
- **可选值**: `"recompute"`, `"incremental"`
- **说明**: 通道级计数模式（仅 `mode: channel` 时生效）
  - `recompute`（默认）: 每次基于活跃模型集合重新计算 `down_count`/`known_count`
    - ✅ 解决迁移场景（从 `mode: model` 切换到 `mode: channel`）
    - ✅ 解决模型删除导致的状态卡死问题
    - ✅ 自动修复计数异常
    - ⚠️ 每次探测需查询所有模型状态（O(n) 复杂度）
  - `incremental`: 增量维护 `down_count`/`count`
    - ✅ 性能最优（O(1) 复杂度）
    - ⚠️ 迁移场景可能有问题（首次启用时 `channel_states` 为空）
    - ⚠️ 模型删除后计数不会自动回收
    - 🔧 内置自愈机制：检测到计数异常时自动回退到 `recompute` 校准
- **推荐**:
  - 新部署或配置频繁变更：使用 `recompute`（默认）
  - 大规模稳定运行的系统：可切换到 `incremental` 优化性能

**通道级事件配置示例**:

```yaml
events:
  enabled: true
  mode: "channel"              # 启用通道级事件检测
  down_threshold: 2            # 模型级阈值（仍用于模型状态机）
  up_threshold: 1              # 模型级阈值
  channel_down_threshold: 1    # 通道级阈值：1 个模型 DOWN 即触发通道 DOWN
  channel_count_mode: "recompute"  # 推荐：每次重算，更稳定
  api_token: "your-secure-token"
```

**通道级事件 Meta 字段**:

通道级事件（`model` 字段为空）的 `meta` 包含以下额外信息：

| 字段 | 类型 | 说明 |
|------|------|------|
| `scope` | string | 固定为 `"channel"`，标识通道级事件 |
| `trigger_model` | string | 触发此事件的模型名称 |
| `down_count` | int | 当前 DOWN 状态的模型数量 |
| `known_count` | int | 已知状态的模型数量 |
| `total_models` | int | 该通道的活跃模型总数 |
| `channel_down_threshold` | int | 配置的通道 DOWN 阈值 |
| `down_models` | []string | DOWN 状态的模型列表 |
| `up_models` | []string | UP 状态的模型列表 |
| `model_states` | object | 各模型的详细状态 |
| `models` | []string | 兼容字段：DOWN 事件为 `down_models`，UP 事件为 `up_models` |

#### 事件 API 端点

**获取事件列表**:
```bash
# 无鉴权模式
curl "http://localhost:8080/api/events?since_id=0&limit=100"

# 有鉴权模式
curl -H "Authorization: Bearer your-token" \
     "http://localhost:8080/api/events?since_id=0&limit=100"

# 响应示例
{
  "events": [{
    "id": 123,
    "provider": "88code",
    "service": "cc",
    "channel": "standard",
    "type": "DOWN",
    "from_status": 1,
    "to_status": 0,
    "trigger_record_id": 45678,
    "observed_at": 1703232000,
    "created_at": 1703232001,
    "meta": { "http_code": 503, "sub_status": "server_error" }
  }],
  "meta": { "next_since_id": 123, "has_more": false, "count": 1 }
}
```

**获取最新事件 ID**（用于初始化游标）:
```bash
curl "http://localhost:8080/api/events/latest"

# 响应
{ "latest_id": 123 }
```

**查询参数**:
| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `since_id` | integer | `0` | 游标，返回 ID 大于此值的事件 |
| `limit` | integer | `100` | 返回数量上限（最大 500）|
| `provider` | string | - | 按服务商过滤 |
| `service` | string | - | 按服务类型过滤 |
| `channel` | string | - | 按通道过滤 |
| `types` | string | - | 按事件类型过滤，逗号分隔（`DOWN,UP`）|

#### 事件类型说明

| 类型 | 说明 | 触发条件 |
|------|------|----------|
| `DOWN` | 服务不可用 | 稳定态为"可用"，连续 `down_threshold` 次红色 |
| `UP` | 服务恢复 | 稳定态为"不可用"，连续 `up_threshold` 次可用（绿色或黄色）|

#### 状态映射规则

- **绿色（status=1）** → 可用
- **黄色（status=2）** → 可用（视为可用，不触发 DOWN）
- **红色（status=0）** → 不可用

#### 使用示例：Cloudflare Worker 集成

```javascript
// Cloudflare Worker 示例 - 轮询事件并发送 Telegram 通知
// 环境变量：RELAY_PULSE_URL, API_TOKEN, TG_BOT_TOKEN, TG_CHAT_ID

export default {
  // 定时触发（建议每分钟执行）
  async scheduled(event, env, ctx) {
    // 从 KV 获取上次处理的事件 ID
    const lastEventId = parseInt(await env.KV.get('LAST_EVENT_ID') || '0');

    // 获取新事件
    const response = await fetch(
      `${env.RELAY_PULSE_URL}/api/events?since_id=${lastEventId}&limit=100`,
      {
        headers: {
          'Authorization': `Bearer ${env.API_TOKEN}`,
          'Accept-Encoding': 'gzip'
        }
      }
    );

    if (!response.ok) {
      console.error('获取事件失败:', response.status);
      return;
    }

    const data = await response.json();

    // 处理每个事件
    for (const event of data.events) {
      await sendTelegramMessage(env, event);
    }

    // 更新游标
    if (data.meta.next_since_id > lastEventId) {
      await env.KV.put('LAST_EVENT_ID', data.meta.next_since_id.toString());
    }
  }
};

// 发送 Telegram 消息
async function sendTelegramMessage(env, event) {
  const emoji = event.type === 'DOWN' ? '🔴' : '🟢';
  const statusText = event.type === 'DOWN' ? '服务不可用' : '服务已恢复';

  const text = `${emoji} <b>${statusText}</b>

服务商: ${event.provider}
服务: ${event.service}${event.channel ? `\n通道: ${event.channel}` : ''}
状态变更: ${event.from_status} → ${event.to_status}
检测时间: ${new Date(event.observed_at * 1000).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' })}`;

  await fetch(`https://api.telegram.org/bot${env.TG_BOT_TOKEN}/sendMessage`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      chat_id: env.TG_CHAT_ID,
      text: text,
      parse_mode: 'HTML'
    })
  });
}
```

**Cloudflare Worker 配置**：

1. 创建 KV 命名空间用于存储游标：
   ```bash
   wrangler kv:namespace create "RELAY_PULSE_KV"
   ```

2. 配置 `wrangler.toml`：
   ```toml
   name = "relay-pulse-notifier"
   main = "src/index.js"

   [triggers]
   crons = ["* * * * *"]  # 每分钟执行

   [[kv_namespaces]]
   binding = "KV"
   id = "<your-kv-namespace-id>"

   [vars]
   RELAY_PULSE_URL = "https://your-relay-pulse-domain.com"

   # 敏感信息使用 secrets
   # wrangler secret put API_TOKEN
   # wrangler secret put TG_BOT_TOKEN
   # wrangler secret put TG_CHAT_ID
   ```

3. 部署：
   ```bash
   wrangler deploy
   ```

### 自助收录与变更请求配置

开放公共站点时，可启用**自助收录**（新服务商通过前端表单申请入驻）与**变更请求**（已入驻服务商通过 API Key 认证后申请修改自身通道）两套配合使用的模块。启用后允许 `monitors` 为空启动，首次部署可走"先开放收录、后填充通道"的流程。

```yaml
onboarding:
  enabled: true
  admin_token: ""           # 管理后台 Bearer token（必填，建议走环境变量）
  encryption_key: ""        # API Key 加密密钥（32 字节 hex，建议走 ONBOARDING_ENCRYPTION_KEY）
  proof_secret: ""          # 测试证明 HMAC 密钥（必填，建议走环境变量）
  proof_ttl: "5m"           # 测试证明有效期
  max_per_ip_per_day: 5     # 每 IP 每天最大提交数
  contact_info: "QQ:18058344"  # 展示给申请人的联系方式

change_requests:
  enabled: true
  max_per_ip_per_day: 3     # 每 IP 每天最大提交数
  revoked_key_file: ""      # 已公开泄露 API Key 的拒绝名单文件名（主配置目录下，空=关闭）
  revoked_key_count: 0      # 名单预期条目数，必须与文件一致（防截断）
```

#### `onboarding.enabled`

- **类型**: boolean
- **默认值**: `false`
- **说明**: 启用后，前端出现"服务商入驻"入口，且允许 `monitors` 为空启动（方便从零部署后再通过流程填充通道）。启用或关闭后**需要重启**容器生效。

#### `onboarding.admin_token`

- **类型**: string
- **必填**: 启用时必填
- **说明**: `/api/admin/*` 系列端点的 Bearer token。生产环境建议用环境变量 `MONITOR_ONBOARDING_ADMIN_TOKEN` 注入，避免落在 config.yaml 里。

#### `onboarding.encryption_key`

- **类型**: string（32 字节 hex 编码）
- **必填**: 启用时必填
- **说明**: 用于加密已收录通道的 API Key（AES-256-GCM）。可以通过环境变量 `ONBOARDING_ENCRYPTION_KEY` 注入。**切勿在 rotate 后丢失旧值，否则无法解密历史通道**。

#### `onboarding.proof_secret`

- **类型**: string
- **必填**: 启用时必填
- **说明**: 申请人点击"测试连通性"后服务端签发的 HMAC 证明密钥（`proof_ttl` 内有效）。建议走环境变量。

#### `onboarding.proof_ttl`

- **类型**: string（Go duration）
- **默认值**: `"5m"`
- **说明**: 测试证明的有效时长。过短会导致用户填完表单时证明已过期；过长会放大被重放的窗口。

#### `onboarding.max_per_ip_per_day`

- **类型**: integer
- **默认值**: `5`
- **说明**: 同一 IP 每天提交的上限。超限返回 429。

#### `onboarding.contact_info`

- **类型**: string
- **默认值**: `""`
- **说明**: 展示在入驻页底部的人类联系方式（QQ、邮箱等）。前端直接渲染，注意不要塞敏感信息。

#### `change_requests.enabled`

- **类型**: boolean
- **默认值**: `false`
- **说明**: 启用后，已收录通道的持有者可以通过 API Key 认证提交变更（改价格、改链接、改 Key 等）。认证共享 `onboarding.encryption_key`，管理后台审批共享 `onboarding.admin_token`，因此通常与 onboarding 一起启用。

#### `change_requests.max_per_ip_per_day`

- **类型**: integer
- **默认值**: `3`
- **说明**: 同一 IP 每天提交变更请求的上限，比收录更严格。

#### `change_requests.revoked_key_file`

- **类型**: string（主配置目录下的**直接子文件名**，不接受绝对路径与子目录）
- **默认值**: `""`（关闭）
- **说明**: 「已公开泄露 API Key」拒绝名单。命中名单的 key 一律无法通过变更流程认证（`/api/change/auth` 与 `/api/change/submit` 都拦），也不能作为 `new_api_key` 写回配置；管理后台批准/应用时还会追溯校验历史请求的认证指纹，命中的只能驳回。
  文件格式：每行一个 `sha256(明文 api_key)` 的十六进制摘要，`#` 开头为注释、空行忽略，上限 1 MiB。
  用**无密钥 SHA-256** 而不是内部 HMAC 指纹，是因为名单里的 key 已经公开、其摘要不构成新增泄露，而无密钥摘要让名单可以离线生成、不必接触 `encryption_key`。**若要收录尚未公开的凭据，这个前提不成立，须改用带 pepper 的摘要。**
  只允许放主配置目录的直接子文件，是为了与配置监听器（对该目录非递归监听）的可见范围一致——否则会出现「配置能加载到、改动却永不触发热更新」。

#### `change_requests.revoked_key_count`

- **类型**: integer
- **默认值**: `0`
- **说明**: 名单去重后的预期条目数。配了 `revoked_key_file` 就必须为正数，且必须与文件实际条目数一致，否则**整次配置加载失败**（启动期拒绝启动、热更新期保留上一份配置）。
  这道闸挡的是「名单被部分写入或意外截断」——截断后的文件前缀往往仍是合法摘要行，只靠格式校验发现不了，而静默接受一份变短的名单等于让部分已泄露 key 复活。
  **运维含义**：增删名单条目时必须同步改这个数字；部署名单请用「同目录临时文件 + 原子 rename」，不要就地覆写。

> **上线名单时必做的两件事**（都是设计上的已知边界，不是可以省的步骤）：
>
> 1. **名单要随容器重建生效，不要靠热更新**。热更新路径上，配置重载由防抖定时器的独立
>    goroutine 执行、彼此不串行（既有行为，对所有配置段一样）：极端时序下一次较慢的旧重载可能
>    在新重载之后完成，把旧名单写回去并持续生效到下一次成功重载。首次启用时若加载失败，
>    热更新也只会保留「尚无名单」的旧配置——这对安全目标而言**不是** fail-closed。
>    所以部署名单请走容器重建（启动期加载是权威的、失败即拒绝启动），之后核对启动日志里
>    `API Key 认证索引已重建` 那行的 `revoked_keys=` 等于预期条目数。
>    注意条目数相同、内容不同时数字看不出差别，所以「改名单内容」同样应走重建而非热更新。
> 2. **人工处置存量的 pending/approved 变更请求**。admin 侧对「认证 key」的追溯依赖
>    「名单 ∩ 当前配置在用 key」推导 HMAC 指纹，两种情况会漏检：该 key 已被轮换出配置；
>    或 `onboarding.encryption_key` 轮换过（历史指纹用旧密钥算、与新派生对不上）。
>    请求携带的 `new_api_key` 因密文可解是精确判定，不受此限。所以名单上线时应把此前的
>    待审队列过一遍，该驳回的驳回。

> **安全提示**：`admin_token` / `encryption_key` / `proof_secret` 三个机密字段都支持 `${...}` 语法或同名环境变量覆盖，详见[环境变量覆盖](#环境变量覆盖)。生产部署**务必**放到环境变量中，并为 `encryption_key` 做异地备份。

### 通道技术细节暴露配置

用于控制 API 是否返回通道的技术细节（`probe_url` 和 `template_name` 字段）。

```yaml
# 全局配置（默认 true，保持向后兼容）
expose_channel_details: false

# provider 级覆盖
channel_details_providers:
  - provider: "sensitive-provider"
    expose: false  # 隐藏该 provider 的技术细节
  - provider: "open-provider"
    expose: true   # 显式开启（可用于覆盖全局 false）
```

#### `expose_channel_details`
- **类型**: boolean（指针类型）
- **默认值**: `true`（未配置时默认暴露，保持向后兼容）
- **说明**: 全局控制 API 是否返回 `probe_url` 和 `template_name` 字段
- **行为**:
  - `true`（默认）: API 响应包含 `probe_url` 和 `template_name`
  - `false`: API 响应中不包含这两个字段
- **适用场景**:
  - 隐藏探测端点 URL，防止被恶意利用
  - 保护 API 架构细节不对外暴露

#### `channel_details_providers`
- **类型**: array
- **说明**: 针对特定 provider 覆盖全局 `expose_channel_details` 设置
- **字段**:
  | 字段 | 类型 | 说明 |
  |------|------|------|
  | `provider` | string | provider 名称，匹配时忽略大小写和首尾空格 |
  | `expose` | boolean | 是否暴露该 provider 的通道技术细节 |
- **优先级**: `channel_details_providers` 中的配置优先于全局 `expose_channel_details`

**示例配置**:
```yaml
# 全局隐藏，但对特定 provider 开启
expose_channel_details: false
channel_details_providers:
  - provider: "relaypulse"
    expose: true  # 官方基准通道可以暴露

# 全局开启，但对敏感 provider 隐藏
expose_channel_details: true
channel_details_providers:
  - provider: "sensitive-relay"
    expose: false
```

#### `public_base_url`
- **类型**: string
- **默认值**: `"https://relaypulse.top"`
- **说明**: 对外访问的基础 URL，用于生成 sitemap、分享链接等
- **环境变量**: `MONITOR_PUBLIC_BASE_URL`
- **格式要求**: 必须是 `http://` 或 `https://` 协议

#### `enable_concurrent_query`
- **类型**: boolean
- **默认值**: `false`
- **说明**: 启用 API 并发查询优化，显著降低 `/api/status` 接口响应时间
- **性能提升**: 10 个监测项查询时间从 ~2s 降至 ~300ms（-85%）
- **适用场景**:
  - ✅ PostgreSQL 存储（推荐）
  - ❌ SQLite 存储（无效果，会产生警告）
- **注意事项**:
  - 需要确保数据库连接池配置充足（建议 `max_open_conns >= 50`）
  - 默认关闭，向后兼容现有配置

**示例配置：**
```yaml
# 启用并发查询优化（推荐 PostgreSQL 用户启用）
enable_concurrent_query: true
```

#### `concurrent_query_limit`
- **类型**: integer
- **默认值**: `10`
- **说明**: 并发查询时的最大并发度，限制同时执行的数据库查询数量
- **仅当** `enable_concurrent_query=true` **时生效**
- **配置建议**:
  ```
  max_open_conns >= concurrent_query_limit × 并发请求数 × 1.2
  ```
  - 示例：`50 >= 10 × 3 × 1.2 = 36`（安全）
  - 如果配置不当，启动时会看到警告：
    ```
    [Config] 警告: max_open_conns(25) < concurrent_query_limit(10)，可能导致连接池等待
    ```

**示例配置：**
```yaml
enable_concurrent_query: true
concurrent_query_limit: 10  # 根据数据库连接池大小调整
```

#### `enable_batch_query`
- **类型**: boolean
- **默认值**: `false`
- **说明**: 启用批量查询优化，将 N 个监测项的查询从 2N 次往返降为 2 次
- **适用场景**:
  - ✅ 7d/30d 长周期查询（数据量大，效果明显）
  - ❌ 90m/24h 短周期查询（数据量小，自动回退到并发模式）
- **性能影响**:
  - 减少数据库往返次数，但会一次性加载大量数据到内存
  - 对于大规模部署（>50 监测项），建议配合 `enable_db_timeline_agg` 使用
- **注意事项**:
  - 启用后，7d/30d 查询会自动使用批量模式
  - 如果批量查询失败，会自动回退到并发/串行模式

**示例配置：**
```yaml
enable_batch_query: true
```

#### `enable_db_timeline_agg`
- **类型**: boolean
- **默认值**: `false`
- **说明**: 启用 DB 侧时间轴聚合优化，将 bucket 聚合下推到 PostgreSQL
- **仅支持**: PostgreSQL（SQLite 会自动回退到应用层聚合）
- **依赖**: 需要同时启用 `enable_batch_query: true` 才能生效
- **性能提升**:
  - 7d 查询：数据传输从 ~50 万行降至 7 行（per 监测项）
  - 30d 查询：数据传输从 ~216 万行降至 30 行（per 监测项）
  - 显著减少网络传输和应用层内存占用
- **适用场景**:
  - ✅ PostgreSQL 存储 + 大规模监测（>50 项）
  - ✅ 7d/30d 长周期查询
  - ❌ SQLite 存储（自动回退）
  - ❌ 90m/24h 短周期查询（不触发）

**示例配置（PostgreSQL 高性能部署）：**
```yaml
# 启用所有查询优化
enable_concurrent_query: true
concurrent_query_limit: 20
enable_batch_query: true
enable_db_timeline_agg: true  # 仅 PostgreSQL 生效

# PostgreSQL 连接池
storage:
  type: "postgres"
  postgres:
    max_open_conns: 100
    max_idle_conns: 20
```

### API 响应缓存配置

用于控制 `/api/status` 接口的响应缓存时间，不同的查询周期可以配置不同的 TTL。

```yaml
cache_ttl:
  90m: "10s"    # 近 90 分钟查询的缓存 TTL（默认 10s）
  24h: "10s"    # 近 24 小时查询的缓存 TTL（默认 10s）
  7d: "60s"     # 近 7 天查询的缓存 TTL（默认 60s）
  30d: "60s"    # 近 30 天查询的缓存 TTL（默认 60s）
```

#### `cache_ttl.90m`
- **类型**: string (Go duration 格式)
- **默认值**: `"10s"`
- **说明**: 近 90 分钟（`period=90m`）查询的缓存有效期

#### `cache_ttl.24h`
- **类型**: string (Go duration 格式)
- **默认值**: `"10s"`
- **说明**: 近 24 小时（`period=24h` 或 `period=1d`）查询的缓存有效期

#### `cache_ttl.7d`
- **类型**: string (Go duration 格式)
- **默认值**: `"60s"`
- **说明**: 近 7 天（`period=7d`）查询的缓存有效期
- **建议**: 7d 数据量较大，建议保持 60s 以上以减少数据库压力

#### `cache_ttl.30d`
- **类型**: string (Go duration 格式)
- **默认值**: `"60s"`
- **说明**: 近 30 天（`period=30d`）查询的缓存有效期
- **建议**: 30d 数据量最大，可适当增加到 120s 以优化性能

**设计考量**：
- **短周期（90m/24h）**：数据变化频繁，用户期望实时性，默认 10s
- **长周期（7d/30d）**：数据量大、计算开销高，默认 60s 平衡性能与时效性
- 所有周期的 TTL 也会通过 HTTP `Cache-Control` 头传递给 CDN（如 Cloudflare）

**示例配置**：
```yaml
# 高性能场景：增加长周期缓存
cache_ttl:
  90m: "10s"
  24h: "10s"
  7d: "120s"   # 2 分钟，减少 DB 压力
  30d: "180s"  # 3 分钟
```

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
- 小规模监测（< 100 个监测项）

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
    max_open_conns: 50          # 最大打开连接数（自动调整）
    max_idle_conns: 10          # 最大空闲连接数（自动调整）
    conn_max_lifetime: "1h"     # 连接最大生命周期
```

**连接池自动调整**：
- 如果未配置 `max_open_conns` 和 `max_idle_conns`，系统会根据 `enable_concurrent_query` 自动设置：
  - **并发模式**（`enable_concurrent_query=true`）：`50` / `10`
  - **串行模式**（`enable_concurrent_query=false`）：`25` / `5`
- 如果已配置，则使用配置值（不会自动调整）

**连接池大小建议**：

| 配置项 | 计算公式 | 示例 |
|--------|---------|------|
| `max_open_conns` | `max_concurrency + concurrent_query_limit × 并发请求数 + 缓冲` | `15 + 10 × 2 + 5 = 40` |
| `max_idle_conns` | `max_open_conns / 3 ~ 5` | `40 / 4 = 10` |

- **`max_concurrency`**：探测并发数（配置中的 `max_concurrency`，默认 15）
- **`concurrent_query_limit`**：API 查询并发数（配置中的 `concurrent_query_limit`，默认 10）
- **并发请求数**：预期同时访问 `/api/status` 的用户数

**示例配置**（42 个监测项，生产环境）：

```yaml
# 探测配置
max_concurrency: 15        # 探测并发数
stagger_probes: true       # 错峰调度

# API 查询优化
enable_concurrent_query: true
concurrent_query_limit: 10

# PostgreSQL 连接池
storage:
  type: "postgres"
  postgres:
    max_open_conns: 40     # 15 + 10 × 2 + 5 = 40
    max_idle_conns: 10
    conn_max_lifetime: "1h"
```

**适用场景**:
- Kubernetes 多副本部署
- 高可用需求
- 大规模监测（> 100 个监测项）

**初始化数据库**:

```sql
CREATE DATABASE llm_monitor;
CREATE USER monitor WITH PASSWORD 'your_password';
GRANT ALL PRIVILEGES ON DATABASE llm_monitor TO monitor;
```

### 数据保留与清理

RelayPulse 支持自动清理过期的历史数据，避免数据库无限增长。清理功能**默认禁用**，需要显式开启。

> **注意**：`retention` 和 `archive` 配置修改后需要**重启服务**才能生效（不支持热更新）。

#### 清理配置（retention）

```yaml
storage:
  type: "postgres"
  # ... 其他存储配置 ...

  retention:
    enabled: true              # 是否启用清理（默认 false，需显式开启）
    days: 36                   # 保留天数（默认 36，建议比用户可见的 30 天多几天缓冲）
    cleanup_interval: "1h"     # 清理任务执行间隔（默认 1h）
    batch_size: 10000          # 每批删除的最大行数（默认 10000）
    max_batches_per_run: 100   # 单轮最多执行批次（默认 100，避免长时间占用）
    startup_delay: "1m"        # 启动后延迟多久开始首次清理（默认 1m）
    jitter: 0.2                # 调度抖动比例（默认 0.2，避免多实例同时执行）
```

**配置项说明**：

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| `enabled` | `false` | 是否启用清理任务（需显式开启） |
| `days` | `36` | 保留天数（超过此天数的数据将被删除） |
| `cleanup_interval` | `"1h"` | 清理任务执行间隔 |
| `batch_size` | `10000` | 每批删除的最大行数 |
| `max_batches_per_run` | `100` | 单轮最多执行批次 |
| `startup_delay` | `"1m"` | 启动后延迟首次清理 |
| `jitter` | `0.2` | 调度抖动比例（0-1） |

**多实例部署**：
- PostgreSQL 使用 advisory lock 确保同一时刻只有一个实例执行清理
- cutoff 时间按 UTC 计算，避免时区/DST 导致边界不一致
- SQLite 单连接模式，无需额外处理

**启用清理**：
```yaml
storage:
  retention:
    enabled: true
    days: 36
```

#### 归档配置（archive）

归档功能用于将过期数据导出到文件备份，**默认禁用**，仅 PostgreSQL 支持。

```yaml
storage:
  type: "postgres"
  # ... 其他存储配置 ...

  archive:
    enabled: true              # 是否启用归档（默认 false）
    schedule_hour: 19          # 归档执行时间（UTC 小时，默认 3；19=北京时间次日 03:00）
    output_dir: "./archive"    # 归档文件输出目录（默认 ./archive）
    format: "csv.gz"           # 归档格式（默认 csv.gz，可选 csv）
    archive_days: 35           # 归档多少天前的数据（默认 35）
    backfill_days: 7           # 回溯补齐天数（默认 7，1=仅归档单日）
    keep_days: 365             # 归档文件保留天数（默认 365，0=永久）
```

**配置项说明**：

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| `enabled` | `false` | 是否启用归档（需显式开启） |
| `schedule_hour` | `3` | 归档执行时间（UTC 小时，0-23）。例如：`19` 表示 UTC 19:00（北京时间次日 03:00） |
| `output_dir` | `"./archive"` | 归档文件输出目录 |
| `format` | `"csv.gz"` | 归档格式：`csv` 或 `csv.gz` |
| `archive_days` | `35` | 归档阈值天数（归档此天数之前的数据） |
| `backfill_days` | `7` | 回溯补齐天数（每次运行最多补齐多少天的缺口） |
| `keep_days` | `365` | 归档文件保留天数（0=永久） |

**归档流程**：
1. 启动后会立即执行一次归档检查，并在每天 `schedule_hour`（UTC）执行
2. 每次运行以 `now - archive_days` 为"最新可归档日"，并在 `backfill_days` 窗口内逐日补齐缺失的归档文件
3. PostgreSQL 多实例使用 advisory lock 确保同一天只会被一个实例归档（按日期互斥，不同日期可能并行归档）
4. 文件命名格式：`probe_history_2024-01-15.csv.gz`，并自动清理超过 `keep_days` 的旧归档文件

**多实例部署注意事项**：
> **重要**：归档文件写入实例本地的 `output_dir` 目录。多实例部署时，必须确保：
> - `output_dir` 挂载到**共享持久化存储**（如 NFS、RWX PVC、云存储挂载等）
> - 或者只让**单个实例/独立 CronJob** 执行归档任务
>
> 否则归档文件可能随 Pod/容器重建而丢失，导致备份不完整。

**配置协调**：
- `archive_days` 应小于 `retention.days`，否则数据可能在归档前被清理
- 如希望"停机补齐窗口"内的数据也能稳定导出，建议 `retention.days >= archive_days + backfill_days`
- 推荐配置：`archive_days: 35`，`backfill_days: 7`，`retention.days: 43`

**示例：完整的清理+归档配置**：
```yaml
storage:
  type: "postgres"
  postgres:
    host: "localhost"
    port: 5432
    user: "monitor"
    password: "secret"
    database: "llm_monitor"

  retention:
    enabled: true
    days: 43                   # >= archive_days + backfill_days
    cleanup_interval: "1h"

  archive:
    enabled: true
    output_dir: "./archive"
    format: "csv.gz"
    archive_days: 35
    backfill_days: 7           # 支持停机 7 天后自动补齐
    keep_days: 365
```

**手动清理命令**（如需手动清理）：
```sql
-- PostgreSQL: 删除 30 天前的数据
DELETE FROM probe_history
WHERE timestamp < EXTRACT(EPOCH FROM NOW() - INTERVAL '30 days');

-- SQLite: 删除 30 天前的数据
DELETE FROM probe_history
WHERE timestamp < strftime('%s', 'now', '-30 days');
```

### 监测项配置

#### 必填字段

##### `provider`
- **类型**: string
- **说明**: 服务商标识（用于分组和显示）
- **示例**: `"openai"`, `"anthropic"`, `"88code"`

##### `service`
- **类型**: string
- **说明**: 服务类型标识（必填）
- **推荐值**: `"cc"`（Claude Code）, `"cx"`（其他 LLM 服务）, `"gm"`（Google Gemini）
- **示例**: `"cc"`, `"cx"`, `"gm"`, `"gpt-4"`, `"claude-3"` 等
- **注意**: 前端筛选系统优先识别 cc/cx/gm，其他值也支持但不会被前端特殊处理

##### `category`
- **类型**: string
- **说明**: 分类标识
- **可选值**: `"commercial"`（推广站）, `"public"`（公益站）

##### `sponsor`
- **类型**: string
- **说明**: 提供 API Key 的赞助者名称
- **示例**: `"团队自有"`, `"用户捐赠"`, `"John Doe"`

##### `base_url`（模板模式必填）
- **类型**: string
- **说明**: 服务商基础地址，模板通过 `{{BASE_URL}}` 注入
- **示例**: `"https://api.openai.com"`、`"https://api.anthropic.com"`

##### `template`（模板模式必填）
- **类型**: string
- **说明**: 引用 `templates/` 目录下的 JSON 模板文件（不含扩展名），定义完整的请求方式（url/method/headers/body/success_contains）
- **示例**: `"cx-gpt-arith"`、`"cc-haiku-arith"`、`"gm-flash-arith"`

##### `method`（传统模式必填，模板模式可选）
- **类型**: string
- **说明**: HTTP 请求方法（使用模板时由模板提供，可显式覆盖）
- **可选值**: `"GET"`, `"POST"`, `"PUT"`, `"DELETE"`, `"PATCH"`

#### 可选字段

##### `provider_slug`
- **类型**: string
- **说明**: 服务商的 URL 短标识，用于生成 `/p/<slug>` 专属页面链接
- **默认值**: 未配置时自动使用 `provider` 的小写形式
- **格式要求**: 仅允许小写字母 (a-z)、数字 (0-9)、连字符 (-)，不能以连字符开头或结尾，不能有连续连字符
- **示例**: `"88code"`, `"openai"`, `"my-provider"`

##### `provider_url`
- **类型**: string
- **说明**: 服务商官网链接（可选），前端展示为外部跳转
- **格式要求**: 必须是 `http://` 或 `https://` 协议
- **示例**: `"https://88code.com"`, `"https://openai.com"`

##### `sponsor_url`
- **类型**: string
- **说明**: 赞助者展示用链接（可选），例如个人主页或组织网站
- **格式要求**: 必须是 `http://` 或 `https://` 协议
- **示例**: `"https://example.com/sponsor"`

##### `sponsor_level`
- **类型**: string
- **说明**: 赞助等级（可选，按通道赞助），在前端显示对应图标
- **注意**: 按通道赞助语义，`sponsor_level` 不会从父通道继承，必须显式配置
- **有效值**:
  | 值 | 名称 | 图标 | 说明 |
  |---|---|---|---|
  | `public` | 公益链路 | 🛡️ | 公益服务商，免费接入监测 |
  | `signal` | 信号链路 | · | 个人用户通道 |
  | `pulse` | 脉冲链路 | ◆ | 基础服务商通道 |
  | `beacon` | 信标链路 | 🔺 | 商业赞助通道，高频监测 |
  | `backbone` | 骨干链路 | ⬢ | 商业进阶通道 |
  | `core` | 核心链路 | 💠 | 最高级赞助通道 |
- **向后兼容**: `basic`→`pulse`, `advanced`→`backbone`, `enterprise`→`core`（自动迁移 + 日志警告）
- **示例**: `"beacon"`

##### `channel`
- **类型**: string
- **说明**: 业务通道标识（用于区分同一服务的不同渠道）
- **示例**: `"vip"`, `"free"`, `"premium"`

##### `model`
- **类型**: string
- **说明**: 模型名称，用于多模型监测组功能
- **使用场景**:
  - 同一个 `provider + service + channel` 下监测多个不同模型
  - 需要配合 `parent` 字段使用以建立父子关系
- **约束**:
  - 同一 `provider + service + channel + model` 组合必须唯一
  - 如果配置了 `parent`，则 `model` 为必填
- **示例**: `"claude-sonnet-4-20250514"`, `"gpt-4o"`

##### `model_vendor`
- **类型**: string（受控词表，非自由文本）
- **说明**: 模型厂商。`service` 表达的是**接入协议族**、`channel_type` 表达的是**线路性质**，两者都回答不了「这条通道跑的是谁家的模型」——第一方厂商开放 Anthropic / OpenAI 兼容端点后（用 Claude Code 的协议、跑厂商自家的模型），这成了独立的一根轴。
- **可选值**: `anthropic`、`openai`、`google`、`zhipu`、`moonshot`、`minimax`、`deepseek`、`qwen`（词表在 `internal/modelvendor`；`/api/onboarding/meta` 会下发完整列表）
- **取值链**: 与 `model`/`request_model` 同款——监测行显式值优先，为空时取 `template` 的声明；并与 `request_model` 一样参与父子继承（同一通道各层必属同一厂商）
- **约束**: 同一 `provider + service + channel` 下**已填写**的 `model_vendor` 必须一致（聚合平台请按厂商拆成不同通道）
- **留空**: 合法。留空只是前端厂商列显示为未知，不影响监测；私有部署不关心厂商时无需填写
- **说明**: 内置模板均已声明厂商，套用模板即自动带上；只有 `<service>-native-*` 这族**厂商无关**的通用模板（用于第一方厂商兼容端点）需要在监测行里显式填写

##### `parent`
- **类型**: string
- **说明**: 父通道引用，格式为 `provider/service/channel`，用于建立父子继承关系
- **继承规则**:
  - 子项**必填** `parent` 和 `model`
  - 其他所有字段均从父通道继承，子项可按需覆盖
  - `provider/service/channel` 从 parent 路径自动继承，不支持覆盖为不同值
  - `interval/slow_latency/timeout` 从父通道继承（包括解析后的 Duration 值）
  - `headers` 采用合并策略（父为基础，子覆盖同名 key）
  - `disabled/hidden` 采用级联逻辑（父禁用/隐藏则子也禁用/隐藏）
- **约束**:
  - 父通道必须存在且配置了 `model` 字段
  - 不允许循环引用
  - 同一 `provider/service/channel` 下只能有一个父层
- **示例**: `"88code/cc/vip"`

##### 多模型监测组配置示例

```yaml
# 父通道：定义公共配置（模板 + base_url）
- provider: "88code"
  service: "cc"
  channel: "vip"
  category: "commercial"
  sponsor: "团队"
  sponsor_level: "backbone"
  base_url: "https://api.88code.com"
  template: "cc-sonnet-arith"        # 模板定义 url/method/headers/body/model/request_model

# 子通道：完整继承，指定不同 template 切换模型
- template: "cc-opus-arith"
  parent: "88code/cc/vip"  # 自动继承 provider/service/channel/base_url 等
  # category 会从父通道继承，也可显式覆盖
```

##### 完整示例：一父三子（Haiku / Sonnet / Opus）

```yaml
# 父通道：承载共用身份信息（PSC、sponsor、base_url、api_key）
- provider: "88code"
  service: "cc"
  channel: "vip"
  category: "commercial"
  sponsor: "团队自有"
  sponsor_level: "beacon"          # 只在父通道声明，子通道组级代表即为此
  base_url: "https://api.88code.com"
  template: "cc-haiku-arith"        # 父通道本身监测 Haiku
  api_key: "sk-xxx"

# 子通道：Sonnet，仅指定差异字段
- parent: "88code/cc/vip"
  template: "cc-sonnet-arith"

# 子通道：Opus，同理
- parent: "88code/cc/vip"
  template: "cc-opus-arith"
```

要点：

- 同一 PSC 下只能有一个父通道；父通道的 `template` 决定了自己监测的模型，子通道各自声明自己的 `template`。
- 子通道无需重复 `provider/service/channel/base_url/api_key` 等，全部从父继承；如需覆盖，子通道中显式写出该字段即可（`sponsor_level` 例外，不继承）。
- `monitors.d/` 模式下，父子三条记录写入**同一个** YAML 文件（文件名仍是 `88code--cc--vip.yaml`），便于统一管理。

**前端显示**：
- 热力图采用垂直分层显示，父层在上，子层在下
- 组级状态取所有层的最差状态（红 > 黄 > 绿）
- 组级可用率取所有层的最小值

**置顶限制**：多模型组的 `sponsor_level` 以父通道为准，子通道不独立参与 sponsor pin 判定。`sponsor_level` 不从父通道继承（需显式配置），但前端置顶展示以父通道元数据为组级代表。不支持反向分组。

##### `price_min`
- **类型**: number（可选）
- **说明**: 服务商声明的参考倍率下限
- **约束**: 不能为负数；若同时配置 `price_max`，则 `price_min` 必须 ≤ `price_max`
- **示例**: `0.05`

##### `price_max`
- **类型**: number（可选）
- **说明**: 服务商声明的参考倍率（用于排序和显示）
- **约束**: 不能为负数；若同时配置 `price_min`，则 `price_max` 必须 ≥ `price_min`
- **排序**: 按此值排序（用户关心"最多付多少"），未配置的排最后
- **显示逻辑**:
  - 若 `price_min == price_max`：只显示单个值
  - 若不同：显示中心值 + 区间，如 `0.125 / 0.05~0.2`
- **示例**: `0.2`（配合 `price_min: 0.05` 显示为 "0.125 / 0.05~0.2"）

##### `listed_since`
- **类型**: string（可选，格式 `YYYY-MM-DD`）
- **说明**: 服务商收录日期，用于在前端显示"收录天数"
- **约束**: 必须为有效日期格式，如 `"2024-06-15"`
- **排序**: 支持在表格中按收录天数排序，未配置的排最后
- **示例**: `"2024-06-15"`（API 返回 `listed_days` 为从该日期到今天的天数）

##### `expires_at`
- **类型**: string（可选，格式 `YYYY-MM-DD`）
- **说明**: 赞助到期日期。过期后赞助等级自动降级为 `pulse`；板块由可用率决定，不随到期变动（到期不再移入备板）
- **约束**: 必须为有效日期格式，如 `"2025-12-31"`。到期日当天仍有效，次日起生效
- **示例**: `"2025-12-31"`

##### `api_key`
- **类型**: string
- **说明**: API 密钥（强烈建议使用环境变量代替）
- **示例**: `"sk-xxx"`

##### `env_var_name`
- **类型**: string（可选）
- **说明**: 自定义环境变量名，用于覆盖自动生成的环境变量命名规则
- **使用场景**:
  - **中文 channel 名称**：如 `"cx专用"`、`"cc测试key"` 等，自动生成的变量名语义不清晰
  - **channel 名称冲突**：如同一 provider 有多个相似 channel（`"cc专用"` vs `"cc专用-特价"`）
  - **特殊字符处理**：channel 包含无法清晰映射为变量名的字符
- **优先级规则**:
  1. 🥇 **自定义 `env_var_name`**（如果配置了）
  2. 🥈 **标准格式（含 channel）**：`MONITOR_<PROVIDER>_<SERVICE>_<CHANNEL>_API_KEY`
  3. 🥉 **标准格式（不含 channel）**：`MONITOR_<PROVIDER>_<SERVICE>_API_KEY`（向后兼容）
- **示例**:
  ```yaml
  # 示例1：中文 channel，自定义语义化英文名称
  - provider: "duckcoding"
    service: "cx"
    channel: "cx专用"
    env_var_name: "MONITOR_DUCKCODING_CX_CX_DEDICATED_API_KEY"
    # ...

  # 示例2：解决同名冲突
  - provider: "duckcoding"
    service: "cc"
    channel: "cc专用"
    env_var_name: "MONITOR_DUCKCODING_CC_CC_DEDICATED_API_KEY"

  - provider: "duckcoding"
    service: "cc"
    channel: "cc专用-特价"
    env_var_name: "MONITOR_DUCKCODING_CC_CC_DISCOUNT_API_KEY"  # 避免冲突
  ```

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
  body: "!include templates/gpt4_request.json"
  ```

##### `success_contains`
- **类型**: string
- **说明**: 响应体必须包含的关键字（用于语义验证）
- **示例**: `"content"`, `"choices"`, `"success"`, `"pong"`
- **行为**:
  - 仅在 HTTP 返回 **2xx 状态码**、且非 429 限流场景下生效；
  - 当响应内容（包含常见流式 SSE 响应聚合后的文本）**不包含**此关键字时，
    会将该次探测标记为 **红色不可用**（`content_mismatch`），即使 HTTP 状态码是 2xx；
  - 支持常见的流式响应格式（如 Anthropic 的 `content_block_delta`、
    OpenAI 的 `choices[].delta.content`），会自动拼接增量文本再进行关键字匹配。

##### `proxy`
- **类型**: string（可选）
- **说明**: 该监测项使用的代理地址，用于需要通过代理访问的 API 端点
- **默认**: 不配置时使用系统环境变量代理（`HTTP_PROXY`/`HTTPS_PROXY`）
- **支持格式**:
  ```
  # HTTP/HTTPS 代理
  http://host:port
  http://user:pass@host:port
  https://host:port

  # SOCKS5 代理（支持账号密码认证）
  socks5://host:port
  socks5://user:pass@host:port
  socks://host:port              # socks:// 是 socks5:// 的别名
  socks://user:pass@host:port
  ```
- **示例**:
  ```yaml
  monitors:
    - provider: "88code"
      service: "cc"
      proxy: "socks5://user:password@proxy.example.com:1080"
      # ... 其他配置
  ```
- **使用场景**:
  - 某些 API 端点需要通过特定代理访问（如地理位置限制）
  - 不同监测项使用不同的代理线路
  - 代理认证需要账号密码
- **注意事项**:
  - 同一 `provider + proxy` 组合会复用 HTTP 客户端配置；但探测请求会主动关闭连接，不复用目标站的 TCP/TLS 长连接
  - 密码中的特殊字符需要 URL 编码（如 `#` → `%23`）

##### `interval`
- **类型**: string (Go duration 格式)
- **说明**: 该监测项的自定义巡检间隔（可选），覆盖全局 `interval`
- **示例**: `"30s"`, `"1m"`, `"5m"`
- **使用场景**:
  - **高频监测**：商务/赞助等级通道可按需配置更短的监测间隔（如 `"1m"`）
  - **低频监测**：成本敏感或稳定服务使用更长间隔（如 `"15m"`）
- **未配置时的兜底与赞助等级相关**：商务等级（beacon/backbone/core）直接使用全局 `interval`；免费档（pulse 及以下）取 `max(全局 interval, 5m)`（免费收录保底 5 分钟一测）。显式配置的 `interval` 始终最高优先，不受等级影响；子通道未配置时继承父通道已解析的间隔
- **配置示例**:
  ```yaml
  interval: "5m"  # 本例的全局 interval：5 分钟
  monitors:
    - provider: "高优先级服务商"
      interval: "1m"   # 覆盖：每 1 分钟监测一次
      # ...
    - provider: "普通服务商"
      # 不配置 interval，本例中回退到全局的 5 分钟
      # ...
  ```

##### `slow_latency`
- **类型**: string (Go duration 格式)
- **说明**: 该监测项的自定义慢请求阈值（可选），覆盖模板和全局 `slow_latency`
- **优先级**: `monitor.slow_latency` > `template.probe.slow_latency` > 全局 `slow_latency`
- **示例**: `"5s"`, `"15s"`, `"30s"`
- **使用场景**:
  - 同一服务的不同通道使用不同的测试模型或 payload，响应时间差异大
  - 特定通道需要更宽松或更严格的延迟阈值

##### `timeout`
- **类型**: string (Go duration 格式)
- **说明**: 该监测项的自定义超时时间（可选），覆盖模板和全局 `timeout`
- **优先级**: `monitor.timeout` > `template.probe.timeout` > 全局 `timeout`
- **示例**: `"10s"`, `"30s"`, `"1m"`
- **使用场景**:
  - 特定通道的 API 响应较慢，需要更长超时
  - 测试 payload 较大，需要更多处理时间
- **注意**: 如果 `slow_latency >= timeout`，系统会打印警告，因为慢响应黄灯可能不会触发

##### `hidden`
- **类型**: boolean
- **默认值**: `false`
- **说明**: 临时下架该监测项（隐藏但继续监测）
- **行为**:
  - 调度器继续探测，存储结果（用于整改证据）
  - API `/api/status` 默认不返回（可加 `?include_hidden=true` 调试）
  - 前端不展示
  - sitemap 不包含
- **示例**:
  ```yaml
  - provider: "问题商家"
    service: "cc"
    hidden: true
    hidden_reason: "服务质量不达标，待整改"
  ```

##### `hidden_reason`
- **类型**: string
- **说明**: 下架原因（可选，用于运维审计）
- **示例**: `"服务质量不达标，待整改"`, `"该通道临时维护"`

##### `disabled`
- **类型**: boolean
- **默认值**: `false`
- **说明**: 彻底停用该监测项（不探测、不存储、不展示）
- **行为**:
  - 调度器不创建任务，不探测
  - 不写入数据库
  - API `/api/status` 不返回（即使加 `?include_hidden=true` 也不返回）
  - 前端不展示
  - sitemap 不包含
- **适用场景**: 商家已彻底关闭、不再需要监测
- **示例**:
  ```yaml
  - provider: "已关站商家"
    service: "cc"
    disabled: true
    disabled_reason: "商家已跑路"
  ```

##### `disabled_reason`
- **类型**: string
- **说明**: 停用原因（可选，用于运维审计）
- **示例**: `"商家已跑路"`, `"服务永久关闭"`

### 标签系统配置

用于在监测项上显示各类标签（如赞助等级、分类标签、风险警告、监测频率、API Key 来源等）。采用统一的 `Annotation` 模型，后端直出所有展示信息（label/tooltip/icon），前端仅负责渲染。

#### `enable_annotations`
- **类型**: boolean
- **默认值**: `false`
- **说明**: 标签系统总开关，控制 API 是否返回 `annotations[]` 字段
- **行为**:
  - `true`: 启用标签系统，API 返回 `annotations[]`
  - `false`: 禁用标签系统，API 不返回标签相关字段
- **注意**:
  - 此开关**仅控制标签展示**，不影响 `category`、`sponsor_level` 等事实字段的返回
  - 禁用时前端无法判断负向标签，因此负向标签的置顶排除规则也会失效。如需使用赞助置顶功能，建议保持 `enable_annotations: true`

**示例**:
```yaml
enable_annotations: true
```

#### 语义分组 (AnnotationFamily)

每个标签属于一个语义分组，决定渲染区域和视觉样式：

| Family | 含义 | 前端颜色 | 适用场景 |
|--------|------|---------|---------|
| `positive` | 正向 | 绿色 | 赞助等级、官方标识 |
| `neutral` | 中性 | 蓝色 | 公益站、高频监测、信息类标签 |
| `negative` | 负向 | 黄色/红色 | 风险警告（前端用 `\|` 分隔符与正向/中性标签隔开） |

**排序规则**：标签按 `family 分组`（positive → neutral → negative）→ `priority 降序` → `id 字母序` 排列。

#### 系统自动派生标签

以下标签由后端根据监测项事实属性自动生成，**无需配置**：

| 事实属性 | 条件 | 生成的标签 | Family | 图标 |
|---------|------|-----------|--------|------|
| `key_type` | 空/`official` | `key_type`（官方 API） | positive | shield-check |
| `key_type` | `= "user"` | `key_type`（用户 Key） | neutral | user |
| `category` | `= "public"` | `public_service`（公益站） | neutral | heart |
| `sponsor_level` | 有效等级 | `sponsor_{level}`（赞助链路） | positive | 按等级映射 |
| `interval` | 始终生成 | `monitor_frequency`（监测间隔） | neutral | activity |

**赞助等级标签映射**：

| SponsorLevel | 标签 ID | Label | Priority |
|-------------|---------|-------|----------|
| `public` | `sponsor_public` | 公益链路 | 10 |
| `signal` | `sponsor_signal` | 信号链路 | 20 |
| `pulse` | `sponsor_pulse` | 脉冲链路 | 40 |
| `beacon` | `sponsor_beacon` | 信标链路 | 60 |
| `backbone` | `sponsor_backbone` | 骨干链路 | 80 |
| `core` | `sponsor_core` | 核心链路 | 100 |

**`key_type` 字段**：所有通道默认自动派生"官方 API"标签（`id: key_type`），设置 `key_type: user` 时自动切换为"用户 Key"标签。`annotation_rules` 可通过 `remove: ["key_type"]` 移除或覆盖此标签。

#### 标签规则 (`annotation_rules`)

通过规则引擎为匹配条件的监测项添加或移除标签，替代原有的 `badge_definitions` + `badge_providers`：

```yaml
annotation_rules:
  # 为风险服务商添加警告标签
  - match:
      provider: "risky-vendor"
    add:
      - id: "risk_flight"
        family: "negative"
        icon: "alert-triangle"
        label: "跑路风险"
        tooltip: "该服务商存在跑路风险，请谨慎充值"
        href: "https://github.com/org/repo/discussions/123"
        priority: 90

  # 覆盖系统派生的 key_type 标签（如需自定义文案）
  - match:
      provider: "88code"
    remove: ["key_type"]
    add:
      - id: "key_type"
        family: "positive"
        icon: "shield-check"
        label: "平台托管 Key"
        priority: 90

  # 移除特定通道的 key_type 标签
  - match:
      provider: "88code"
      channel: "test-channel"
    remove: ["key_type"]
```

##### 匹配条件 (`match`)

所有非空字段必须同时匹配（AND 逻辑），空字段表示不限。匹配时忽略大小写。

| 字段 | 类型 | 说明 |
|------|------|------|
| `provider` | string | 匹配服务商名称 |
| `service` | string | 匹配服务类型 |
| `channel` | string | 匹配通道名称 |
| `model` | string | 匹配模型名称 |
| `category` | string | 匹配分类（commercial/public） |
| `sponsor_level` | string | 匹配赞助等级 |

##### 标签字段 (`add[]`)

| 字段 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| `id` | string | 是 | - | 唯一标识（同 id 去重，后规则覆盖先规则） |
| `family` | string | 否 | `neutral` | 语义分组：`positive`/`neutral`/`negative` |
| `icon` | string | 否 | - | 图标标识（前端 icon registry 映射） |
| `label` | string | 是 | - | 显示文本（后端直出，无需前端 i18n） |
| `tooltip` | string | 否 | - | 提示文本 |
| `href` | string | 否 | - | 可选的点击跳转链接 |
| `priority` | number | 否 | `50` | 排序权重，越大越靠前。未指定或设为 0 时自动填充为 50 |

##### 移除标签 (`remove[]`)

字符串数组，指定要移除的标签 `id`。可用于子通道取消继承的标签。

##### 规则执行顺序

1. 按配置顺序依次应用所有匹配的规则
2. 每条规则内先执行 `remove`，再执行 `add`
3. 同 `id` 标签去重，后添加的覆盖先添加的
4. 规则标签（origin: `rule`）与系统自动派生标签（origin: `system`）合并后统一排序

#### 前端显示

- 标签显示在监测项的"标签"列
- Label 和 Tooltip 直接使用后端 API 返回的字段（不走前端 i18n）
- 标签按 family 分组渲染：positive（绿区）→ neutral（蓝区）→ negative（红区，前有分隔符）
- 有 `href` 的标签可点击跳转
- 有 `negative` family 标签的监测项**不会被赞助通道置顶**

#### 兼容旧配置：`risk_providers`

`risk_providers` 仍可使用，系统会自动将其转换为 `annotation_rules`（family: `negative`）。建议迁移到 `annotation_rules` 以获得更灵活的控制。

```yaml
# 旧写法（仍支持）
risk_providers:
  - provider: "risky-provider"
    risks:
      - label: "跑路风险"
        discussion_url: "https://github.com/org/repo/discussions/123"

# 等价的新写法（推荐）
annotation_rules:
  - match:
      provider: "risky-provider"
    add:
      - id: "risk_跑路风险"
        family: "negative"
        icon: "warning"
        label: "跑路风险"
        href: "https://github.com/org/repo/discussions/123"
        priority: 50
```

#### 完整配置示例

```yaml
# 启用标签系统
enable_annotations: true

# 标签规则（key_type 标签由系统自动派生，无需手动配置）
annotation_rules:
  # 为基准通道添加标识
  - match:
      provider: "relaypulse"
    add:
      - id: "official_baseline"
        family: "neutral"
        icon: "crosshair"
        label: "官方基准"
        tooltip: "RelayPulse 官方基准监测通道"
        priority: 80

  # 社区贡献标记
  - match:
      provider: "community-relay"
    add:
      - id: "community_key"
        family: "neutral"
        icon: "info"
        label: "社区 Key"
        tooltip: "社区用户提供的 API Key"
        priority: 50

  # 风险标记
  - match:
      provider: "risky-vendor"
    add:
      - id: "risk_flight"
        family: "negative"
        icon: "alert-triangle"
        label: "跑路风险"
        href: "https://github.com/org/repo/discussions/456"
        priority: 90

# 以下标签由系统自动派生，无需配置：
# - 所有监测项 → key_type 标签（默认"官方 API"，key_type: user 时"用户 Key"）
# - category=public 的监测项 → public_service 标签
# - 有 sponsor_level 的监测项 → sponsor_{level} 标签
# - 所有监测项 → monitor_frequency 标签（显示监测间隔）

monitors:
  - provider: "88code"
    service: "cc"
    # 自动获得：key_type（系统派生，默认"官方 API"）+ sponsor_core（系统派生）
    # ...

  - provider: "community-relay"
    service: "cc"
    category: "public"
    key_type: "user"   # 标注"用户 Key"
    # 自动获得：key_type（系统派生，"用户 Key"）+ community_key（规则）+ public_service（系统派生）
    # ...
```

### 临时下架配置

用于临时下架服务商（如商家不配合整改），支持两种级别：

#### Provider 级别下架

批量下架整个服务商的所有监测项：

```yaml
hidden_providers:
  - provider: "问题商家A"
    reason: "服务质量不达标，待整改"
  - provider: "问题商家B"
    reason: "API 频繁超时，沟通整改中"

monitors:
  - provider: "问题商家A"  # 自动继承 hidden=true
    service: "cc"
    # ...
```

#### Monitor 级别下架

下架单个监测项：

```yaml
monitors:
  - provider: "正常商家"
    service: "cc"
    hidden: true                    # 临时下架
    hidden_reason: "该通道临时维护"  # 下架原因
    # ...
```

#### 优先级规则

| Provider Hidden | Monitor Hidden | 最终状态 | 原因来源 |
|-----------------|----------------|----------|----------|
| ✅ | ❌ | **隐藏** | provider.reason |
| ❌ | ✅ | **隐藏** | monitor.hidden_reason |
| ✅ | ✅ | **隐藏** | monitor.hidden_reason（优先） |
| ❌ | ❌ | **显示** | - |

#### 调试接口

```bash
# 查看包含隐藏项的完整列表（内部调试用）
curl "http://localhost:8080/api/status?include_hidden=true"
```

### 彻底停用配置

用于彻底停用服务商（如商家已跑路、永久关闭），与"临时下架"的区别是**不会继续探测和存储数据**。

#### Provider 级别停用

批量停用整个服务商的所有监测项：

```yaml
disabled_providers:
  - provider: "已跑路商家A"
    reason: "商家已跑路，不再监测"
  - provider: "已关站商家B"
    reason: "服务永久关闭"

monitors:
  - provider: "已跑路商家A"  # 自动继承 disabled=true
    service: "cc"
    # ...
```

#### Monitor 级别停用

停用单个监测项：

```yaml
monitors:
  - provider: "正常商家"
    service: "legacy-channel"
    disabled: true                    # 彻底停用
    disabled_reason: "该通道已废弃"   # 停用原因
    # ...
```

#### 优先级规则

| Provider Disabled | Monitor Disabled | 最终状态 | 原因来源 |
|-------------------|------------------|----------|----------|
| ✅ | ❌ | **停用** | provider.reason |
| ❌ | ✅ | **停用** | monitor.disabled_reason |
| ✅ | ✅ | **停用** | monitor.disabled_reason（优先） |
| ❌ | ❌ | 继续检查 hidden | - |

#### Disabled vs Hidden 对比

| 特性 | `disabled=true` | `hidden=true` |
|------|-----------------|---------------|
| 调度器探测 | ❌ 不探测 | ✅ 继续探测 |
| 数据存储 | ❌ 不存储 | ✅ 继续存储 |
| API 返回 | ❌ 永不返回 | ❌ 默认不返回，可用 `include_hidden=true` 查看 |
| 适用场景 | 商家跑路、服务永久关闭 | 临时整改、待观察 |

### 风险服务商配置（旧版兼容）

> **迁移提示**：`risk_providers` 仍可使用，系统会自动将其转换为 `annotation_rules`（family: `negative`）。建议迁移到 `annotation_rules` 以获得更灵活的控制。详见[标签系统配置](#标签系统配置)中的"兼容旧配置"部分。

```yaml
risk_providers:
  - provider: "risky-provider"       # provider 名称（需精确匹配 monitors 中的 provider）
    risks:
      - label: "跑路风险"             # 简短标签（前端红色标签显示）
        discussion_url: "https://github.com/org/repo/discussions/123"  # 讨论链接（可选）
      - label: "资金安全存疑"
```

#### 行为说明

- 风险标签会自动转换为 `negative` family 的 annotation，注入到匹配 provider 的所有监测项
- 前端以红色/黄色样式展示负向标签
- 有负向标签的监测项**不会被赞助通道置顶**
- 与 `hidden_providers`、`disabled_providers` 独立生效，可同时配置

## 环境变量覆盖

为了安全性，强烈建议使用环境变量来管理 API Key，而不是写在配置文件中。

### API Key 环境变量

**命名规则**（按优先级）：

1. **自定义环境变量名**（最高优先级）：
   ```
   配置中指定的 env_var_name 字段值
   ```

2. **标准格式（含 channel）**：
   ```
   MONITOR_<PROVIDER>_<SERVICE>_<CHANNEL>_API_KEY
   ```

3. **标准格式（不含 channel）**（向后兼容）：
   ```
   MONITOR_<PROVIDER>_<SERVICE>_API_KEY
   ```

**命名转换规则**:
- 所有字母转为**大写**
- 特殊字符（`-`、空格、中文等）替换为 `_`
- 连续的 `_` 合并为一个
- 去除首尾下划线

**示例**:

| 配置 | 环境变量名（按优先级） | 说明 |
|------|----------------------|------|
| `provider: "88code"`, `service: "cc"` | `MONITOR_88CODE_CC_API_KEY` | 无 channel，使用标准格式 |
| `provider: "88code"`, `service: "cc"`, `channel: "vip3"` | `MONITOR_88CODE_CC_VIP3_API_KEY` | 有 channel，优先匹配带 channel 格式 |
| `provider: "duckcoding"`, `service: "cx"`, `channel: "cx专用"` | `MONITOR_DUCKCODING_CX_CX专用_API_KEY` | 中文 channel，建议使用 `env_var_name` |
| `provider: "duckcoding"`, `service: "cx"`, `channel: "cx专用"`, `env_var_name: "MONITOR_DUCKCODING_CX_CX_DEDICATED_API_KEY"` | `MONITOR_DUCKCODING_CX_CX_DEDICATED_API_KEY` | 自定义环境变量名，优先级最高 |

**使用方式**:

```bash
# 方式1：直接导出
export MONITOR_88CODE_CC_VIP3_API_KEY="sk-your-real-key"
./monitor

# 方式2：使用 .env 文件（推荐）
cat > .env <<EOF
MONITOR_88CODE_CC_VIP3_API_KEY=sk-xxx
MONITOR_DUCKCODING_CX_CX_DEDICATED_API_KEY=sk-yyy
EOF

# Docker Compose 自动加载 .env 文件
docker compose up -d

# 或手动指定
docker compose --env-file .env up -d
```

**最佳实践**:
- ✅ 使用 `.env` 文件集中管理（已在 `.gitignore`，不会提交）
- ✅ 中文 channel 使用 `env_var_name` 指定语义化英文名称
- ✅ 生产环境使用 Secret 管理工具（Vault、K8s Secrets）
- ❌ 避免在配置文件中硬编码 `api_key` 字段

### API Key 责任说明

> **重要**：使用 API Key 进行监测时，请仔细阅读以下责任说明。

#### Key 的提供与管理责任

使用者自行提供并管理用于第三方服务的 API Key/Token。使用者应确保：

- ✅ Key 的获取与使用符合第三方服务的条款、政策与适用法律
- ✅ Key 不被泄露、不被写入公开仓库/日志/截图
- ✅ 及时轮换与撤销疑似泄露的 Key
- ✅ 监测对应 Key 的异常使用情况（如流量异常、费用异常）

**RelayPulse 不提供任何第三方 Key**，也不对使用者 Key 的合法性、授权范围、配额/费用、封禁风险承担责任。

#### 费用与配额

若第三方服务对 API 调用计费或限制配额，相关费用由使用者自行承担。RelayPulse 仅按配置发起技术请求，不对第三方计费策略或结算结果负责。

#### 误用与合规

使用者不得利用 RelayPulse 或其集成接口从事违反第三方条款或适用法律的行为。对于因使用者配置或使用方式导致的 Key 封禁、服务中断、索赔或争议，RelayPulse 不承担责任。

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

### rpdiag 质量列集成（可选，默认关闭）

首页状态表的「质量」列消费的是 **rpdiag** 导出的排名数据；页脚与质量列表头的「中转站检测」入口外链到 rpdiag 站点（diag.relaypulse.top）的专题页。rpdiag 是一套**独立的、未开源的**质量盲评平台，仅 relaypulse.top 官方部署接入。对于私有化 / 自部署，本功能**默认关闭**——不配置即可，监测主功能完全不受影响。

```bash
# 启用质量列 + 检测入口外链（需自行运行 rpdiag 后端并提供导出地址）
# 接受 1 / true / yes / on（大小写不敏感）；其余值或未设均视为关闭
MONITOR_RPDIAG_ENABLED=true

# 排名导出地址（可选；默认指向官方 diag.relaypulse.top）
MONITOR_RPDIAG_EXPORT_URL=https://diag.relaypulse.top/api/v1/ranking/export?scoring_version=all

# 导出缓存 TTL（可选；Go duration 语法，默认 10m）
MONITOR_RPDIAG_CACHE_TTL=10m
```

**开关行为**（单一信号收口所有出口，要么全有、要么全无）：

| `MONITOR_RPDIAG_ENABLED` | 表现 |
|--------------------------|------|
| 未设 / 关闭（默认） | 状态表**不渲染**「质量」列、移动端无质量排序项、页脚无「中转站检测」入口；旧 `/detect` 路径不跳转，返回 `noindex` 的 SPA 兜底页。自部署看到的就是一张纯净的可用性监测表。 |
| 开启但导出不可达 | 列**保留**、内容暂空（fail-open，不阻断主功能；缓存命中旧快照时继续用旧数据）。 |
| 开启且导出正常 | 完整质量列 + 指向 diag.relaypulse.top 的「中转站检测」入口；旧 `/detect` 路径（含四语言与尾斜杠别名）301 到 rpdiag 站点。 |

> 注：环境变量在容器**启动期一次性读取**，改值需重启容器（不参与配置热更新）。

### Events API 配置

Events API 用于向外部服务（如 Notifier）提供状态变更事件流。

```bash
# Events API 访问令牌（必需，启用 /api/events 端点鉴权）
# 外部服务需要在请求头中携带 Authorization: Bearer <token>
EVENTS_API_TOKEN=your-secure-token-here
```

**安全建议**：
- 生成高熵随机 token：`openssl rand -hex 32`
- 仅通过 HTTPS 传输
- 定期轮换 token

### 前端环境变量

前端支持以下环境变量（需在构建时设置）：

#### API 配置

```bash
# API 基础 URL（可选，默认为相对路径）
VITE_API_BASE_URL=http://localhost:8080

# 是否使用 Mock 数据（开发调试用）
VITE_USE_MOCK_DATA=false
```

#### 列展示控制

```bash
# 是否在公开列表/卡片中隐藏"价格 ¥/$"列（自报倍率，无法验证）
# 值为 true 时隐藏，默认或留空表示显示
# 仅影响前端公开展示，admin 录入端始终可见
VITE_HIDE_PRICE_COLUMN=
```

**说明**：
- 此变量为**构建期烧进 bundle 的部署策略**，需在 `npm run build` 前设置（Dockerfile 已透传 ARG）
- 使用预构建镜像（`image:` 而非 `build:`）时此 env 在运行期无效；如需切换必须重新构建镜像
- 旧链接的 `?sort=priceRatio_desc` 在隐藏后会被解析层归一化回默认排序，不影响赞助置顶语义

#### Notifier 配置（订阅通知功能）

```bash
# Notifier 服务 URL（可选，不设置则隐藏订阅按钮）
# 用于启用 Telegram 订阅通知功能
VITE_NOTIFIER_API_URL=https://notifier.example.com
```

**说明**：
- 此变量为**构建时变量**，需在 `npm run build` 前设置
- 如果未设置或为空，订阅按钮将自动隐藏
- Notifier 是独立的通知服务，详见 `notifier/README.md`

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
- **性能监测**：
  - `api_request` - API 请求性能（包含延迟、成功/失败状态）
  - `api_error` - API 错误（包含错误类型：HTTP_XXX、NETWORK_ERROR）

**注意**：
- 开发环境建议留空 `VITE_GA_MEASUREMENT_ID`，避免污染生产数据
- 如果未设置 Measurement ID，GA4 脚本不会加载

## 配置验证

服务启动时会自动验证配置：

### 验证规则

1. **必填字段检查**: `provider`, `service`，以及在未配置 `template` 时的 `base_url` 和 `method`
2. **HTTP 方法校验**: 必须是 `GET`, `POST`, `PUT`, `DELETE`, `PATCH` 之一
3. **唯一性检查**: `provider + service + channel` 组合必须唯一
4. **`category` 枚举**: 若填写必须是 `commercial` 或 `public`（留空默认 `commercial`）
5. **存储类型校验**: 必须是 `sqlite` 或 `postgres`

### 验证失败示例

```bash
# 缺少必填字段
❌ 无法加载配置文件: monitor[0]: 缺少必填字段 'category'

# 重复的 provider + service + channel
❌ 无法加载配置文件: 重复的监测项: provider=88code, service=cc, channel=

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

# 修改配置（添加新的监测项）
vi config.yaml

# 观察日志
docker compose logs -f monitor

# 应该看到:
# [Config] 检测到配置文件变更，正在重载...
# [Config] 热更新成功！已加载 3 个监测任务
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
body: "!include templates/gpt4_request.json"
```

```json
// templates/gpt4_request.json
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
.env                       # 生产 API Keys（添加到 .gitignore）
```

### 4. 安全加固

1. **所有敏感信息使用环境变量**
2. **生产环境启用 PostgreSQL SSL**: `sslmode: "require"`
3. **限制 CORS**: 只允许信任的域名
4. **定期轮换 API Key**
5. **使用最小权限原则**: 数据库用户只授予必要权限

## 配置示例库

### 示例1：OpenAI GPT-4.1

```yaml
monitors:
  - provider: "openai"
    service: "cx"
    category: "commercial"
    sponsor: "团队"
    base_url: "https://api.openai.com"
    template: "cx-gpt-arith"        # 模板预设 url/method/headers/body/model/request_model
```

### 示例2：Anthropic Claude

```yaml
monitors:
  - provider: "anthropic"
    service: "cc"
    category: "public"
    sponsor: "社区"
    base_url: "https://api.anthropic.com"
    template: "cc-haiku-arith"      # 模板预设 url/method/headers/body/model/request_model
```

### 示例3：自定义 REST API（传统格式）

```yaml
monitors:
  - provider: "custom-api"
    service: "cx"
    category: "public"
    sponsor: "自有"
    base_url: "https://api.example.com"
    url_pattern: "{{BASE_URL}}/health"
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

- [运维手册（历史文档，仅供参考）](../../archive/docs/user/operations.md) - 日常运维与故障排查
- [API 端点示例](../../README.md#-api-端点) - 当前权威参考（正式 API 规范整理中）
