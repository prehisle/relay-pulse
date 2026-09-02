# CLAUDE.md

⚠️ 本文档为 AI 助手（如 Claude / ChatGPT）在此代码库中工作的内部指南，**优先由 AI 维护，人类贡献者通常不需要修改本文件**。
如果你是人类开发者，请优先阅读 `README.md` 和 `CONTRIBUTING.md`，只在需要了解更多技术细节时再参考这里的内容。

### 同步检查点

历史同步 / 发版流水账（每版的 commit、版本号、回滚锚点、prod 实证）已迁出到 **[docs/ai-release-ledger.md](docs/ai-release-ledger.md)**，本文件不再累积。

⚠️ **新一轮发版后把检查点写进那个 ledger，不要写回本文件**——它曾涨到 96k 字符、占本文件 62%，把真正的架构契约挤到没人读得到的位置。
当前生产状态直接 `gh release list -L 5` + `git log --oneline -10` 查，比读快照准。

**代码是唯一真相源。** 本文档只记代码读不出来的东西——踩坑、失败契约、设计理由、与默认相反的约定；目录结构 / 字段清单 / 路由清单一律直接查代码，不在这里维护副本。

## 项目概览

这是一个企业级 LLM 服务可用性监测系统，支持配置热更新、SQLite/PostgreSQL 持久化、实时状态追踪，并内建**指数退避重试**、**标签/赞助体系**、**事件通知**、**自助测试**、**自助收录（onboarding）**、**自助变更请求（change requests）**、**管理后台**、**monitors.d/ 目录化通道管理**和**多模型监测（父子通道继承）**等能力。

### 文档策略（供 AI 遵守）

- 回答人类用户时，**优先引用四个核心文档**（`README.md` / `QUICKSTART.md` / `docs/user/config.md` / `CONTRIBUTING.md`），避免让用户跳进 `archive/` 中的大量历史内容。
- 如必须引用 `archive/docs/*` 或 `archive/*.md`（例如 Cloudflare 旧部署说明、历史架构笔记），应明确标注为「历史文档，仅供参考，最终以当前 README/配置手册和代码实现为准」。
- 不主动向人类暴露 `AGENTS.md`、本文件等 AI 内部文档，除非用户明确询问「AI 如何在本仓库工作」一类问题。

## 开发命令

### 首次开发环境设置

```bash
# ⚠️ 首次开发或前端代码更新后必须运行此脚本
./scripts/setup-dev.sh

# 如果前端代码有更新，需要重新构建并复制
./scripts/setup-dev.sh --rebuild-frontend
```

**重要**: Go 的 `embed` 指令不支持符号链接，因此需要将 `frontend/dist` 复制到 `internal/api/frontend/dist`。setup-dev.sh 脚本会自动处理这个问题。

**⚠️ 前端代码修改规则**:
- `internal/api/frontend/` 整个目录被 `.gitignore` 忽略，是从 `frontend/` 复制过来的嵌入目录
- **所有前端源代码修改必须在 `frontend/` 目录进行**，而不是 `internal/api/frontend/`
- 修改后运行 `./scripts/setup-dev.sh --rebuild-frontend` 同步到嵌入目录
- 直接修改 `internal/api/frontend/` 的改动不会被 git 追踪，会在下次构建时丢失

### 后端 (Go)

标准 Go 工具链；`make dev`（Air 热重载）、`make ci`（本地跑全套 CI 闸），具体 target 读 `Makefile`。只有这条不可猜：

```bash
# 验证单个监测项（调试配置问题，逻辑内联在 cmd 下、无 internal/verifier 包）
go run ./cmd/verify/main.go -provider <name> -service <name> [-v]
```

## 架构与设计模式

### 后端架构

`internal/` 分层架构（20 个包 + 独立 `notifier/` 子模块）。**目录结构与包职责直接读代码**——`find internal/ cmd/ -name '*.go' | sort`、`go list ./...`、包内首行注释。下面只记代码读不出来的契约。

**非默认约定与硬契约：**
1. **自动移板**: `automove.Service` 基于 7 天可用率**与 rpdiag 质量信号**移板，配置板位（board）是"锚点/天花板"——只在配置板位及以下浮动、绝不向上越板（board=secondary 不会被自动升 hot；board=hot 可降 secondary 再恢复）。cold 为 sticky，需 `auto_cold_exempt` 手动解除。**双 latch 分离**（v2.65.0）：可用率迟滞与质量 latch 各自独立记忆，合成板位 `sticky-cold/可用率cold > (可用率secondary 或 质量latch) > 配置hot`——某通道任一**活跃** rpdiag 评测模型近3次全 hard-fail（`recent_attempts` 尾3全 null + `hard_fail_active`）→ 质量 latch 封顶 secondary（只封 secondary、绝不推 cold），恢复后连续 `qualityRecoveryDebounce=2` 个新鲜快照升回；feed 拉不到/过 TTL/未接 rpdiag → 冻结现状不动。质量优先，赞助/置顶同样被质量降板（合同例外靠给通道挂 `auto_move_exempt`——同一个 flag 整体豁免可用率+质量）。移板原因 `board_reason`（机器码 `quality_hardfail`）+ `board_reason_models`（触发 model 名）经 `/api/status` 扁平+分组下发前端。**展示两处**：① 通道名 hover tooltip（四语言，`components/table/ChannelCell`）；② **标注列常驻 negative 徽章**（v2.66+，`config.deriveSystemAnnotations` 读运行时注入的 `ServiceConfig.BoardReason=="quality_hardfail"` 派生 id=`quality_hardfail`/priority=5 排风险之后，前端 `AnnotationChip` 按 id 路由 `QualityDemoteIcon`；后端直出中文 label/tooltip）——一眼可见无需 hover。注意质量移板徽章是 negative 家族，会命中 `sortMonitors.meetsPinCriteria` 的「negative→不置顶」既有逻辑，即质量移板通道失去赞助置顶资格（符合「质量优先，赞助/置顶也降」）。注解重算在 `automove.applyOverrideToMonitor`：board 或 sponsor 任一覆盖即用覆盖后字段重算
2. **探测链路统一**: **四处** inline 测试端点（用户自助 `/api/onboarding/test`、**变更请求 `/api/change/test`**、管理员审核 `/api/admin/submissions/:id/test`、监测项管理 `/api/admin/monitors/:key/probe`）都走 `onboarding.BuildServiceConfigFromSubmission`（或 runtime resolved root，**变更侧则直接构造 `ServiceConfig` 字面量**） + `config.ResolveSingleMonitor`（模板填充 + Duration 派生） + `probe.InlineProber.ProbeConfig`，确保与 `scheduler` 调用的 `monitor.Prober` **字段级一致**（headers/body/method/success_contains/timeout/retry 全覆盖）。模板覆盖编辑不允许在 inline 测试时即时生效（返回 422 `TEMPLATE_CHANGE_REQUIRES_SAVE`），需先保存。每次 inline 探测打 `probe_id` 结构化日志便于跨端追踪。**管理员通道管理探测（v2.48.0+）扩展**：① 可逐个探测子通道——`AdminGetMonitor` 附带 `probe_targets`（runtime resolved 的父+子，`model` 为选择器，PSCM 唯一），探测请求带 `target_model` 即按 `(provider,service,channel,model)` 命中 runtime 已解析子通道直接探测、不套草稿覆盖（未生效则报错，不做 raw 半解析）；② **配了代理就自动走代理**（无开关，`AdminProbeMonitor` 显式传 `probe.WithProxy(cfg.Proxy)`，复用 `monitor.NewExplicitProxyTransport` 的 http/socks5 语义，结果带 `via_proxy`）——这是显式钉在调用方的 SSRF 硬边界：**只有** admin 通道管理探测传 `WithProxy`，公开 `onboarding`/`submission` 自测**永不传**、绝不走代理（即使其 cfg 将来出现 proxy 字段）。注意 inline 走代理后上游 IP 的 SSRF 校验天然失效（由代理解析连接），与 scheduler 一致、不额外加严。读响应体失败按真实原因分流（超大→`response_too_large`、读超时→`response_timeout`、其余→`network_error`，v2.48.1）。

  **⚠️ 变更流程的模型来源（v2.79+，别改回信客户端）**：`/api/change/test` 的 `target_key` 必填，`Model`/`RequestModel`/`ModelVendor` 一律由服务端按它从**运行时父行**读取（`buildChangeTestConfig` → `findRuntimeRootByPSC`）。两条理由：① 轮换 key 时请求里带的是**新** key，它还没进 `AuthIndex`，靠 api_key 反查不到通道；② 模型若由客户端指定，就能用便宜模型测通、而实际在跑的是另一个，测试与被测对象脱钩、proof 失去意义。只认父行是因为变更的认证候选本身也只由父行构建（`internal/change/index.go` 跳过 `Parent != ""`），两处必须同口径。

  **⚠️ 解析后模型仍为空则 400，绝不带 `"model": ""` 打上游**：闸在 `runResolvedProbe`（两条公开流程的共同咽喉），回退链与 `{{MODEL}}` 一致（`request_model` 优先，空则 `model`）。唯一会触发的形状是 native 族（`cc-native-*`/`cx-native-*`，刻意不声明模型、要求行级填）漏填模型。**此前正是这个缺陷**：`/api/change/test` 与 `/api/onboarding/test` 共用的构造逻辑只取两条流程的交集、从不填 model，于是火山方舟那批 native 通道走变更流程改 base_url / 轮换 key 时发出 `"model": ""`，上游返回没头没脑的错误、测试恒红、提交不了。

### 日志系统

统一走 `internal/logger`（标准库 `log/slog` 封装）。

**Request ID 中间件**：
- API 层自动为每个请求生成 8 位短 UUID
- 支持通过 `X-Request-ID` 请求头传入自定义 ID
- 响应头返回 `X-Request-ID` 便于客户端关联

### 配置热更新与环境变量

**热更新契约**：`config.Watcher`（fsnotify）同时监听 `config.yaml` 与 `monitors.d/`；**先验证新配置、验证不过就保留旧配置继续服务**（不是崩、也不是加载半份），失败面经 `/ready` 的 `config_reload` 信息化上报（见「API 端点」）。验证通过后调注册的回调（调度器 / API 服务器），各组件持锁原子更新，调度器下一个周期即用新配置。

**环境变量覆盖**: API 密钥可通过 `MONITOR_<PROVIDER>_<SERVICE>_<CHANNEL>_API_KEY`（优先）或 `MONITOR_<PROVIDER>_<SERVICE>_API_KEY` 设置（大写，`-` → `_`）。也可通过 `env_var_name` 自定义变量名。

### 主题系统

**FOUC 防护**：`index.html` 里有一段内联脚本在 React 挂载前就把 `data-theme` 写到 `<html>` 上——**别删**，删了切主题会闪白底。

**使用规范**:
- ❌ 避免硬编码颜色：`text-slate-500`、`bg-zinc-800`
- ✅ 使用语义化类：`text-muted`、`bg-elevated`
- 透明度变体：`bg-surface/60`、`text-accent/50`

### 国际化架构 (i18n)

四语言：zh-CN（默认，路径 `/`）、en-US（`/en/`）、ru-RU（`/ru/`）、ja-JP（`/ja/`）。语言表与映射读 `frontend/src/i18n/index.ts` + `router.tsx`。下面是**非默认约定**：

- **URL 用简化语言码**（`/en/` 而非 `/en-US/`），但**内部一律用完整 locale**（`en-US`）以兼容 i18next——两者靠 `i18n/index.ts` 的映射表桥接，别在组件里直接拿 URL 段当 locale 用。
- **`/api/*`、`/health`、`/ready`、`/sitemap.xml` 等技术路径不参与 i18n**（路由分层，别给它们加语言前缀）。
- **根路径 `/` 走语言检测**：`localStorage` > 浏览器语言 > 默认 zh-CN。
- `isSupportedLanguage` 类型守卫是唯一合法的语言码校验入口。
- **所有用户可见文本必须走 `t()`**，新增组件时逐个字符串检查。

### 响应式断点系统

前端采用**统一的媒体查询管理系统**（`utils/mediaQuery.ts`），确保断点检测的一致性和浏览器兼容性：

**设计原则：**
1. **使用 matchMedia API**：替代 `resize` 事件监听，避免高频触发
2. **Safari ≤13 兼容**：自动回退到 `addListener/removeListener` API
3. **HMR 安全**：在 Vite 热重载时自动清理监听器，防止内存泄漏
4. **缓存优化**：模块级缓存断点状态，避免重复计算
5. **事件隔离**：移动端禁用鼠标悬停事件，避免闪烁

### 状态码系统

**主状态（status）**：
- `1` = 🟢 绿色（成功、HTTP 2xx、延迟正常）
- `2` = 🟡 黄色（降级：慢响应等）
- `0` = 🔴 红色（不可用：各类错误，包括限流）
- `-1` = ⚪ 灰色（仅用于时间块无数据，不是探测结果）

**HTTP 状态码映射**：
```
HTTP 响应
├── 2xx + 快速 + 内容匹配 → 🟢 绿色
├── 2xx + 慢速 + 内容匹配 → 🟡 波动 (slow_latency)
├── 2xx + 内容不匹配 → 🔴 不可用 (content_mismatch)  ← 无论快慢
├── 3xx → 🔴 不可用 (client_error)  ← client 默认已自动跟随合规重定向，漏到这里的裸 3xx 是畸形重定向，非可用响应
├── 400 → 🔴 不可用 (invalid_request)
├── 401/403 → 🔴 不可用 (auth_error)
├── 429 → 🔴 不可用 (rate_limit)  ← 不做内容校验
├── 其他 4xx → 🔴 不可用 (client_error)
├── 5xx → 🔴 不可用 (server_error)
└── 网络错误 → 🔴 不可用 (network_error)
```

**内容校验（`success_contains`）**：
- 仅对 **2xx 响应**（绿色和慢速黄色）执行内容校验
- **429 限流**：响应体是错误信息，不做内容校验
- **红色状态**：已是最差状态，不需要再校验
- 若 2xx 响应但内容不匹配 → 降级为 🔴 红色（语义失败）

**细分状态（SubStatus）**：

| 主状态 | SubStatus | 标签 | 触发条件 |
|--------|-----------|------|---------|
| 🟡 黄色 | `slow_latency` | 响应慢 | HTTP 2xx 但延迟超过阈值 |
| 🔴 红色 | `rate_limit` | 限流 | HTTP 429 |
| 🔴 红色 | `server_error` | 服务器错误 | HTTP 5xx |
| 🔴 红色 | `client_error` | 客户端错误 | HTTP 4xx（除 400/401/403/429） |
| 🔴 红色 | `auth_error` | 认证失败 | HTTP 401/403 |
| 🔴 红色 | `invalid_request` | 请求参数错误 | HTTP 400 |
| 🔴 红色 | `network_error` | 连接失败 | 网络错误、连接超时 |
| 🔴 红色 | `response_timeout` | 响应超时 | HTTP 连接成功但读取响应体超时 |
| 🔴 红色 | `content_mismatch` | 内容校验失败 | HTTP 2xx 但响应体不含预期内容 |

**可用率计算**：
- 采用**加权平均法**：每个状态按不同权重计入可用率
  - 绿色（status=1）→ **100% 权重**
  - 黄色（status=2）→ **degraded_weight 权重**（默认 70%，可配置）
  - 红色（status=0）→ **0% 权重**
- 每个时间块可用率 = `(累积权重 / 总探测次数) * 100`
- 总可用率 = `平均(所有时间块的可用率)`
- 无数据的时间块（availability=-1）不参与可用率计算，全无数据时显示 "--"
- 所有可用率显示（列表、Tooltip、热力图）统一使用渐变色：
  - 0-60% → 红到黄渐变
  - 60-100% → 黄到绿渐变

**延迟统计**：
- **仅统计可用状态**：只有 status > 0（绿色/黄色）的记录才纳入延迟统计，红色状态不计入
- 每个时间块延迟 = `sum(可用记录延迟) / 可用记录数`
- 延迟显示使用渐变色（基于 `slow_latency` 配置）：
  - < 30% slow_latency → 绿色（优秀）
  - 30%-100% → 绿到黄渐变（良好）
  - 100%-200% → 黄到红渐变（较慢）
  - ≥ 200% → 红色（很慢）
- API 响应 `meta.slow_latency_ms` 返回阈值（毫秒），供前端计算颜色

## 配置管理

配置分为两层：`config.yaml`（全局设置）+ `monitors.d/`（通道配置，按 PSC 一文件一通道）。结构定义于 `internal/config/*.go`。完整字段文档见 `docs/user/config.md`。

**monitors.d/ 目录化管理**：
- 每个 YAML 文件包含 `metadata`（source/revision/timestamps）+ `monitors`（ServiceConfig 数组）
- 文件名格式：`{provider}--{service}--{channel}.yaml`（parent-child 在同一文件）
- config.yaml 和 monitors.d/ 不能有同 PSC，否则启动报冲突
- 管理后台（`/api/admin/monitors/*`）可通过 API 进行 CRUD 操作
- 删除为软删除（归档到 `monitors.d/.archive/`）
- 热更新同时监听 config.yaml 和 monitors.d/ 目录变化
- ⚠️ **写路径两不变量（动 `monitor_store.go` 前必读，v2.77.0 起）**：
  1. **子行合并一对一**——既有子行被某个 updated 行认领即移出候选（`claimed` 数组 + `claimExistingChild`），匹配不到的行走 `BackfillFileIDs` 铸新 id。**绝不允许一个既有行的 `model_id` 被复制给多行**：模板驱动子行的展示名来自模板、行里不写 `model`，磁盘上是空串，多对一会让新加的空 model 子行集体撞同一个 id。两个 pass 是**两个完整循环**（不是行内二级 fallback），保证全局 `model_id` 匹配优先于展示名兜底；副作用是 updated 数组顺序不再决定归属冲突，**稳定 id 恒胜出**（已被测试固定）。
  2. **写盘前 `ValidateFileModelIDsUnique` fail-loud**——一对一只杜绝「复制既有 id」，客户端 payload 自带的重复非空 id 仍会原样落盘（`BackfillFileIDs` 只补空值、绝不覆盖既有 id）。重复即拒、不写盘、不递增 revision；api 层用 `errors.As(&config.DuplicateModelIDError)` 映射 400（toggle 路径刻意保持 5xx：payload 只有 disabled/hidden，重复必来自磁盘历史坏文件=服务端状态问题）。
  - 边界：**不主动修复历史坏行**（自动决定保留哪个 id 有误接历史数据的风险）；跨文件重复仍由 loader 全局 `validateModelIDs` 兜底；`cmd/migrate` 直接调 `BackfillFileIDs`+`AtomicWriteYAML`、不经 MonitorStore，故不受守卫 2 覆盖。
  - 回归测试在 `monitor_store_test.go` 的「子行一对一认领」段，改动前先跑。

### AppConfig / ServiceConfig 字段

**字段清单直接读 struct tag**：`internal/config/app_config.go`（全局）、`internal/config/monitor.go`（监测项）、`storage_config.go`、`features.go`、`external.go`；面向用户的完整说明在 `docs/user/config.md`。下面只记 struct tag 上看不出来的语义：

- `retry` 是**额外**重试次数（不含首次），配 3 = 最多打 4 次。
- `max_concurrency` 的 `-1` = 无限，不是禁用。
- `degraded_weight`（默认 0.7）是黄色状态计入可用率的权重，不是阈值。
- `cache_ttl` 按 period 分档：90m/24h=10s，7d/30d=60s。
- `hide_price_column` / `hide_category_filter` / `hide_vendor_filter` 是**运行时**开关，改 yaml 热更新即生效、经 `/api/status` meta 下发前端。**后两个只隐藏筛选入口**：`category`/`model_vendor` 字段照常下发（`hide_vendor_filter` 下**厂商列也照常渲染**——列是观察维度、筛选器是操作轴，刻意解耦），URL 里残留的 `?category=`/`?vendor=` 视为未选中（参数保留不删，关掉开关即恢复原选择）。三者都走 `useMonitorData` 内的 **effective 派生**而非 effect 抹 URL——后者已被证伪（`setSearchParams` 的 prev 是上一次渲染的，会被紧随其后的 sort 清理覆盖回去）。
- Onboarding 的 `enabled` **改了要重启容器**（不是热更新）；启用 onboarding 时允许**零 monitors 启动**。
- `storage.archive.keep_days` 的 `0` = 永久保留，不是「不保留」。

**配置优先级**: `monitor` > `template` > `global`（适用于 slow_latency、timeout、retry 等所有分级配置；同名字段以更高优先级覆盖，未指定则继承。模板值在 resolveTemplates 阶段填入 monitor 级别作为默认值）

**⚠️ `model` 字段的双重身份（换模板/改名前必读）**: `model` 既是**热力图展示名**，又是**历史数据的 DB 业务键**。
- 各历史表按 `(provider, service, channel, model)` 区分序列：`probe_history`/`status_events` 的真实 PK 是 `id`，但业务键是该四元组（覆盖索引 `idx_probe_history_pscm_ts_cover`）；`service_states`/`monitor_overrides` 的 **PK 直接含 model**；`channel_states` PK 不含 model。**（⚠️2026-06-29 Plan D-1 起：`probe_history` 增稳定 `model_id` 内部键，`/api/status`/`/api/status/query` 等展示读已切按 `model_id` 查 → 改 `model` 展示名不再断 probe_history 历史/时间线；但 `service_states`/`monitor_overrides` 仍 PK 含 model = Plan D-2 后置，admin logs 仍按 PSCM 查孤儿。故下文「改 model 名断历史」对 probe_history 展示读已不成立，对这两张派生表与「回溯历史版本」仍成立。）**
- **probe 写库 `result.Model = cfg.Model`（展示名），且没有 `request_model` 列**——库里只靠展示 `model` 串区分序列，某历史点当时实际请求哪个版本无法回溯。
- **后果**：换探测模板或改 `model` 显示名 = 业务键变 = 历史序列断裂（旧名成孤儿序列）+ automove 的 sticky cold override（按旧键存）失效、通道回 hot。
- **取舍（无免费午餐）**：`model` 带版本号 → 能并排比多版本但每次升版断历史；`model` 不带版本（version-less，把版本放 `request_model`）→ 历史跨版本连续，但同通道不能并存两版本（撞业务键），且无法回溯历史版本。
- 因 `{{MODEL}}`=`request_model`回退`model`，只要模板/monitor 显式设了 `request_model`，改 `model` 展示名不影响 body 发出的真实模型——这是“给 monitor 加 `model: X` 覆盖展示名而不打红”的前提。
- **换模板想保历史**：保持 `model` 串不变、版本只改 `request_model`；若必须改名，需配套 SQL 把旧 model 的历史行 relabel 到新名（`service_states` 因 PK 含 model 要先 dedup）。详见 `/rpmigrate` skill。

**⚠️ `model_vendor`「模型厂商」正交轴（机制层 + 前端 + 收录全线已落地）**：第一方厂商（智谱/月之暗面/MiniMax/DeepSeek/Qwen 等）开放 Anthropic `/v1/messages` 与 OpenAI 兼容端点后，「用 Claude 的协议跑别家的模型」使 `service`（协议族）与 `channel_type`（线路性质）都回答不了「这条通道跑的是谁家的模型」，故抽成独立第三根轴。

- **受控词表单一真相源** = `internal/modelvendor`（stdlib-only 叶子包，`config` 反向 import 它，别在包内 import 仓库其它包）。code 一经发布**不可复用于另一厂商**——它进 `/api/status` wire，并作为跨产品契约被 rpdiag 消费。
- **取值链与 `Model`/`RequestModel` 完全同款**：`config 行级 > template`（`lifecycle.go` 注入），且**与 `RequestModel` 一样参与父子继承**（`inheritCoreBehavior`）——注意与 `Model` 相反，`Model` 是父子的区分字段故刻意不继承，vendor 是通道内共享字段。
- **只有一个校验函数，且刻意没有 fail-closed 运行时闸**（与 `model_id` 的双函数分工**不同**，别照搬）：
  - `validateModelVendors`（宽松，**一直生效**）：挂在 `validateResolvedModelConstraints` 里——**不是** `validate()`。因为 vendor 可能来自 template，挂在模板解析之前则模板声明的 vendor 永不过校验、同通道跨模板的厂商冲突也看不见。它顺带把合法值写回规范形式（小写 trim）。
  - ⚠️ **别加 fail-closed 运行时闸**。Phase 1 曾写过 `CheckRuntimeModelVendors`，Phase 3 已连同其测试一起删除。理由：vendor 无法像 `model_id` 那样由 loader 自动派生（`loader.go` 给内联行补 `md_<uuidv5>`；而从 `request_model` 前缀反推厂商是被明令禁止的），接闸会让任何自己手写内联监测行、不套内置模板的自托管用户升级即 crash-loop——正是 v2.69.2 修过的伤害类别。
  - **我方生产的覆盖改由「内置模板全覆盖」保证**：`templates/` 下所有非 native 模板全部声明 vendor（`cc-*`→`anthropic`、`cx-*`→`openai`、`gm-*`→`google`；kiro 逆向线路跑的仍是 Claude 模型故同为 anthropic），而生产 `monitors.d/` 每一行都引用模板。**唯一例外是 native 族**（厂商无关、由监测行填 vendor），故套 native 模板的行必须行级写 `model_vendor`。守卫是 `TestBundledTemplatesDeclareModelVendor`（双向锁死，已 bite-test 验非真空）。
- **native 模板族**（`<service>-native-*`：`cc-native-arith` / `cx-native-arith`）承接第一方厂商的 Anthropic Messages / OpenAI Responses 兼容端点，是**厂商无关**的：刻意不声明 `model`/`request_model`/`model_vendor`，三者必须由监测行按厂商填写。

  ⚠️ **自 2026-08-21 起本族全部标了 `self_serve_visible: false`，是 admin-only 的**——公开表单已不给提交方任何填模型的入口（见下方「Onboarding 通道标识派生」），一个要求行级填模型的模板留在表单里只会让人选中后卡在「模板未声明模型」。生产上那 10 条火山方舟通道继续跑在本族模板上，**变更流程完全不受影响**：`internal/change/index.go` 构建候选与 `defaultTestVariant` 都不看可见性，`buildChangeTestConfig` 走的是 `ResolveVariant` 而非 onboarding 的 `resolveSelfServeTemplate`（守卫 `TestAuthIndex_Rebuild_SelfServeHiddenVariantsStillUsable`，已 bite-test 验非真空）。**要给自助表单加第一方厂商模型，是复制本族模板做一个专属模板，不是把本族标回可见。**

  **别给它补 vendor**——行级漏填时会经 `config > template` 回退链静默继承成错误厂商（`config.IsNativeProbeTemplate` 与守卫测试锁死这条）。请求形态与 `cc-haiku-arith`/`cx-gpt54-arith` 逐字一致（含 Claude Code / Codex CLI 私有 header 与身份串）：厂商开放兼容端点正是为承接这两个客户端，且保持严格形态使 native 模板**不构成** mock 回显作弊的宽松入口（spec 决策 D7 那条「宽松模板绝不回流给中转商通道」的硬规则因此无从触发；若某厂商确实不认某个头，另拆该厂商专用模板，别放宽通用模板）。行级漏填 vendor 时由 `validateFinal` 出一条**告警**（不阻断——挂 `validateFinal` 而非 `validateModelVendors` 是时序决定的：后者早于父子继承，在那里判空会误伤「vendor 只写父行、子行继承」）。判定函数是 `SplitN(name,"-",3)`，故**四段名同样算 native**（`cc-native-arith-nothink` → `["cc","native","arith-nothink"]`）。

  **族内现有 5 个，按厂商模型的思考行为选型（全部经真端点实测得出，别凭猜换）**：

  | 模板 | 形态差异 | 适用 |
  |------|----------|------|
  | `cc-native-arith` | `max_tokens:20` | 不开思考的模型 |
  | `cc-native-arith-nothink` | 加 `thinking.disabled`、`max_tokens:64` | 默认开思考**且认这个开关**的模型（**首选**：最快最省） |
  | `cc-native-arith-512` | 仅 `max_tokens:512`，不碰 thinking | 默认开思考**却不认 thinking 开关**的模型（实测 kimi-k2.7-code 对 `thinking.disabled` 返 400） |
  | `cx-native-arith` | `{{BASE_URL}}/v1/responses` + `reasoning` 字段 | base_url **不含**版本段的端点 |
  | `cx-native-arith-noreason` | `{{BASE_URL}}/responses`、删 `reasoning` | base_url **已含**版本段的端点（如火山方舟 `…/api/coding/v3`）；某些模型对 `reasoning` 返 400 |

  ⚠️ **两个 cx 模板的 base_url 契约相反**（一个要求含版本段、一个要求不含），填错只在探测时变红、加载期没有校验——选模板前先对齐 base_url 形态。契约写在各自 `_comment` 里，但 admin 模板下拉只显示文件名不显示注释，属已知运维限制。
  ⚠️ **接第一方厂商最容易踩的坑**：给默认开思考的模型套 `max_tokens:20`，thinking 会吃光预算 → `stop_reason=max_tokens`、正文一个字都没有 → 内容校验判红。症状是「HTTP 200 却恒红 content_mismatch」。
- **收录来源**：`ChannelSourceCatalog` 的 `cc`/`cx` 各有一条 `nat`「厂商官方 API（自有模型）」（`Category=official` → 自动落 `O`）。`gm` 不加，Gemini 的 `api`（AI Studio）本身就是第一方入口。
- **「一个通道一个厂商」不变量**：同一 PSC 三元组下**均非空**的 vendor 必须一致。只比非空值是刻意的——回填期必然出现同通道半填状态。聚合平台请按厂商拆成不同通道（复用 `channel_group`）。
- **禁止从 `request_model` 前缀反推 vendor**（无论 relay-pulse 还是 rpdiag）：模型 ID 命名不稳、中转商可改写、同模型多别名，必然产生 join 漂移。vendor 是**声明**的，不是猜的。
- **前端（Phase 2 已落）**：「模型厂商」列位于「模型」列右侧，可排序（sort key `modelVendor`，按 **code** 字典序、未声明厂商恒沉底）、可筛选（URL 参数 `vendor`，**受 `hide_vendor_filter` 运行时开关门控**）。三个渲染出口（桌面表 / 移动卡片 / grid `StatusCard`）共用同一个 `showVendorColumn` 开关——`StatusCard` 那个 prop **刻意是必填**：它原本可选带 `false` 默认值，两个 grid 调用点都漏传，卡片视图便永远不显示厂商（Phase 3 修复），现在漏传直接编译不过。
  - **列显隐是数据驱动的，且基于「未筛选」数据判定**（App 用 `rawData`、ProviderPage 用按本 provider 过滤的 `rawData`）：全站没有任何通道声明厂商时（回填前的现状）整列 + 筛选器 + 服务列 ⓘ 全部不渲染，对用户零变化；用已筛数据算会让「筛一下列就冒出来/消失」。
  - **通道级厂商由 `deriveChannelVendor` 从各 layer 推导，规则严于后端校验**：后端只要求同通道**非空**值一致（回填期半填合法），前端要求**所有** layer 非空且同值，否则视为未知显示 `-`。半填状态显示成某厂商 = 用一半证据给出十成确定性，而厂商列的全部价值就是「别把 GLM 当成 Claude」。
  - 厂商展示名 `vendors.<code>` 四语言在前端，**词表本身仍只有后端一份**；未收录的 code 原样显示 code、不出图标，绝不猜名字。
  - 「服务」列表头加了 ⓘ 说明「服务=接入协议族，模型是谁家的看厂商列」——**与厂商列同生共死**（厂商列不在时该文案会指向一个看不见的列）。站长 2026-08-05 拍板：筛选下拉**保留客户端名**（`Claude Code (CC)`），不按 spec 字面改成协议名（用户是按「我用哪个客户端」找的）。
  - `channel_type=O` 文案已按 spec 改为「官方直连 / 官方转售」，措辞保持既有的不背书口径（「服务商声称/标记」）。
- **第一家第一方厂商已接（2026-08-05，v2.73.0）**：火山方舟 `ark`，5 厂商 × cc/cx 共 10 条通道，`channel_type=O` + `channel_source=nat` + `key_type=user`，**board=secondary 观察期**。故生产厂商值现有 8 种（原厂三家 + bytedance/zhipu/moonshot/minimax/deepseek）。
  - ⚠️ **收录 native 模板通道必须走 admin API，不能手写 monitors.d**：手写文件缺 `model_id` 会被 fail-closed 闸拒绝整份加载，admin 写路径（`MonitorStore.Create/Update` → `BackfillFileIDs`）才会自动补 `model_id`/`channel_id`。
  - ⚠️ **admin JSON API 传不了 `request_model`**（`ServiceConfig.RequestModel` 标了 `json:"-"`，admin UI 也没这个字段——历史上它一律来自模板，native 族是第一个要求行级填的）。解法是 `model` 直接写完整模型 ID，靠 `{{MODEL}}` 的 `request_model → model` 回退链；副作用正面：`model` 作 DB 业务键更稳、不会因改展示名断历史。若将来要「短展示名 + 长请求 ID」，得先给 `RequestModel` 开 json tag。
**⚠️ 「模型」筛选器（v2.82+，前端独有，不进 wire）**：按**版本级模型名**筛（`opus-4.8` / `gpt-5.6-terra`），下拉按**家族**分组、组标题一键全选该家族全部版本。四条踩过的坑：

- **命中是 any（通道级）语义，绝不能照搬 vendor 的 all-agree**。`deriveChannelVendor` 要求一个通道所有 layer 同厂商才认，这在 vendor 上零成本（生产 0 个通道跨厂商）；但生产 18 个多 layer 通道**全部是多模型**（saiai 的 `[Opus, Sonnet, Fable 5.1]`、modelflare 的四个 GPT 变体…），要求「所有 layer 同模型」会让它们一条都筛不出来。
- **过滤必须在 `useFilteredData` 与 `useMonitorData` 两处同时加**（两者是复制关系，注释里写明「逐字一致」）。顶部「正常运行/异常告警」统计走后者，只改前者会让表格 6 行、统计仍显示 66/18。
- **家族是展示层反推，不越「模型是声明的、不是反推的」那条红线**：模型列渲染的本来就是 `shortenModelName(request_model)` 的派生结果，家族只是同一条既有派生链上再分一桶，不进 wire、不参与可信性判断、归错只影响分组。判定用「前缀 + 段边界」而非穷举模型 ID——后者会让新上的 `claude-opus-5-1` 静默掉进「其他」组且不报错。词表在 `frontend/src/utils/modelFamily.ts`，家族间前缀互不吞并有结构性断言守着。
- **选项的 label/family 取自未经筛选的全量数据**。已选项被联动条件排除后仍要标 `(0)` 保留，此时它已不在子集里；Gemini 尤其明显——展示名被剥了厂商前缀（`gemini-2.5-flash` → `2.5-flash`），从 canonical key 反推不出家族，只有全量映射知道。选项构建由 `utils/modelFilter.buildModelOptions` 单一提供，首页与服务商页共用（那两页的筛选链路本就是各写一套的平行实现，别落下第三份）。

- **残**：`internal/api/meta.go` 四语言 SSR 首页描述**仍未提**第一方厂商（原因是此前一条都没有，现已具备条件、属独立文案决策）；这批通道**未接 rpdiag 质量盲评**（落地顺序第 3 步，成本在给每 vendor 定档位阶梯 + 攒基线）。

**模板占位符**: URL/headers/body 中的占位符在探测时由 `internal/monitor/probe.go` 的 `InjectVariables` 统一替换。支持：`{{BASE_URL}}`、`{{API_KEY}}`、`{{MODEL}}`（=`request_model`，为空回退 `model`）、`{{REQUEST_MODEL}}`、`{{USER_ID}}`、`{{USER_ID_HASH}}`、`{{USER_ACCOUNT_UUID}}`、`{{RAND_UUID}}`、`{{RAND_UUID2}}`、`{{PROMPT}}`、`{{EXPECTED_ANSWER}}`、`{{ARITH_A}}`、`{{ARITH_B}}`（同一次注入中两个 `{{RAND_UUID}}` 取同一值）。注意：`body` 按模板文件中的**原始字节**发送（仅 `TrimSpace`，不 re-marshal/不 compact），占位符按字符串替换；需与抓包字节一致时 body 要写成压缩单行、且不放占位符。

**引用文件**: 对于大型请求体，使用 `body: "!include templates/filename.json"`（必须在 `templates/` 目录下）。

**ping 模板族与 `cch` 整包 attestation（改带 billing header 的模板前必读）**：`cc-*-ping-*` 是最小 ping 探针（system 加 `Only reply pong.`、`tools:[]`、`success_contains: "pong"`），body 由真 claude-cli 抓包冻结成字节。其 `system[0]` 的 `x-anthropic-billing-header: cc_version=X.Y.Z.<3hex>; cc_entrypoint=...; cch=<5hex>;` 里，`cc_version` 后缀由 **prompt 文本**算、`cch` 由**整个 body** 算，saiai 一类网关严格校验（不匹配直接拒），故 **body 改一个字节两个值全作废、必须重算**（算法在 `/workspace/zdy/saiai/saiai-server/backend/internal/pkg/claudebilling`，CLI 是 `go run ./cmd/claudebilling -in body.json -replace-body`；已用真实样本在 2.1.195/2.1.220 两个版本档验证可精确复现）。三条硬约束：

1. ⚠️ **带 cch 的模板 body 内不能有任何占位符** —— `{{PROMPT}}`/`{{EXPECTED_ANSWER}}` 每次请求都变，与整包 attestation 在数学上互斥。**所以 arith 族（随机题面）永远不能带 billing header**；Claude 5 的 arith 版只能照 `cc-opus-arith` 改 `request_model` + 加 `thinking.disabled` + 删 `context_management` + 调 `max_tokens`，**别照 ping 模板的工艺做**。同理也**别图省事把 arith 通道整体换成 ping**：ping 只验 `pong`、抓不到 mock 回显作弊，arith 的随机题面才是那道反作弊闸（见「探测链路统一」与 `prompt.go` 变体池）。`metadata` 的 device_id/session_id 同理只能冻结成常量（代价：所有引用通道共用一个 device_id）。
2. **UA 版本必须与 body 里 `cc_version` 一致**（网关交叉校验，2026-09-02 实证：只抬 UA、body 留旧版本会被 saiai 反篡改门 400 拒「Please use the official Claude Code client…」）。升级 claude-cli 版本 = 重抓 + 重算，不能只改 UA 串。
3. **别把真实身份烤进模板** —— `metadata.user_id` 的 `account_uuid` 留空串（claude-cli 2.1.220 官方端点本来就发空）、`device_id` 用固定串的 sha256。本仓公开，2026-08-06 清过一批历史泄漏。
4. **新模型可能带客户端版本闸，且不必重抓包也能过** —— 2026-09-02 接 `claude-fable-5-1` 时上游 400 且报文点名版本：「Claude Code 2.1.220 does not support this model; version 2.1.251 or newer is required」，而同批 2.1.220 形态的 `claude-fable-5`/`claude-opus-5` 全绿 → **模型级要求，不是 key/网关/cch 问题，别去查 key**。修法是 `go run ./cmd/claudebilling -in body.json -version <新版本> -header-only` 把 billing header 整体重算并同步抬 UA（2.1.172+ 同属 `CCHInputModeFilteredBodyV2` 一档，2.1.251 已由真打验证沿用）。**重算前先用原版本号校准工具**——同一 body 按旧版本重算应逐字复现模板里冻结的值，对上了才信新档输出。代价是 body 成了「旧抓包 + 新 billing header」的混合体（`X-Stainless-*`/`anthropic-beta` 仍是旧客户端取值），saiai 收；撞上校验更细的网关就得用新版 CLI 真抓一次。

**抓包形态别抓错**：把 `ANTHROPIC_BASE_URL` 指向本地 echo server 抓到的是「CLI→中转商」形态（**不发 cch**），要「CLI→官方端点」形态必须用 mitmproxy 拦截。完整配方（含三个必踩坑）在 meta 仓 `.claude/skills/relay-client-gate/SKILL.md` 与 `/rptmpl`。

**给自助表单加一个第一方厂商模型 = 建一个专属模板**（2026-08-21 起的唯一做法）：复制该模型对应的 native 模板（cc 侧按思考行为、cx 侧一律 `cx-native-arith`），**body 逐字节保留**，只补 `model` / `request_model` / `model_vendor` / `self_serve_label` 四项。

- `model` 写**规范模型 ID**（`glm-5.2`）而不是人话名：它同时是 DB 业务键与热力图展示名，与既有 native 通道写法保持同一个串，将来把那 10 条老行切到专属模板才不会断历史；人话名放 `self_serve_label`。
- `model_vendor` 必须取自 `internal/modelvendor` 受控词表；别声明 `self_serve_default`（每 service 的默认模板只能是 `cc-haiku-arith`/`cx-gpt-arith`/`gm-flash-arith`）。
- **cx 侧一律 `{{BASE_URL}}/v1/responses`**：base_url 含不含版本段是**中转商端点的属性**、不是模型属性，「base_url 不含版本段」是自助收录的统一约定（也是全部 5 个 cx 官方线模板的口径）。火山方舟那种自带版本段的端点继续走 `cx-native-arith-noreason` + admin 手工上架。
- ⚠️ **`_comment` 里要如实区分「实测过的」与「推断的」**。现有 10 个模板的思考行为/reasoning 取舍都源自 2026-08-05 的**火山方舟**实测，中转商自建网关未逐一验证；`cx-kimi-k27code-arith` 的「`/v1/responses` + 无 `reasoning`」更是个**从未在真端点跑过的组合**（拆自「URL 由端点定、reasoning 认不认由模型定」两条正交推断）。这类不确定性代价有界——自助流程本就是「测通了才准提交」，测不过只是提交不了、不会产生坏数据，走 `/contact` 由我们评估另拆模板。
- 守卫：`TestBuildModelCatalog_FirstPartyVendorsAreSelfServable`（五家厂商在 cc/cx 各至少一条可选模型，漏建一个模板 = 悄悄下线一个厂商）。

**新增模板不得改动自助流程的默认探测目标（v2.79.0 起由模板显式声明）**：`templates/*.json` 的 `"self_serve_default": true` 声明本模板是所属 service（文件名首段）在**自助收录第二步**与**变更请求测试步**里默认选中的探针，每个 service 至多一份，现为 `cc-haiku-arith` / `cx-gpt-arith` / `gm-flash-arith`（各 service 最便宜、上游覆盖面最广的 arith 模板）。字段只影响表单默认值，**与调度器无关**；一份都没声明时回退到 `InitTemplates` 的历史行为（文件名字典序第一个），故自建部署带自己的模板目录时零影响。

- **为什么必须显式声明**：默认值原先就是「字典序第一个」，2026-08-06 新增 `cc-fable-ping-20260806` 后（`cc-f…` < `cc-h…`）cc 的默认探测目标静默变成 claude-fable-5——几乎没有中转商提供该模型，于是**新申请人一进第二步、老通道走变更流程改 base_url/轮换 key，测试一律红、卡死无法提交**（用户实际反馈，2026-08-20 定位）。
- **变更流程还多一层**：`change.defaultTestVariant` 优先取**这条通道自己在跑的模板**（`ServiceConfig.Template`，仅当它确实是该 service 已注册变体时），拿不到才回落注册表默认值——要证明的是「这条通道照旧能用」，不是让中转商为一个自己没上架的模型作证。
- **可选项不受影响**：下拉里仍是该 service 的**全部**模板（含新加的 Claude 5 / gpt-5.6），用户可手动改选；`Order` 也仍是纯字典序。
- 守卫：`TestInitTemplates_BundledDefaultsAreExplicit`（内置默认值 + 每 service 恰好一份声明）、`TestAuthIndex_Rebuild_DefaultTestVariant`（变更流程跟随自身模板）。部署后可直接从启动日志核对：`msg=探测模板已刷新 … defaults="cc=cc-haiku-arith cx=cx-gpt-arith gm=gm-flash-arith"`。

**Claude 5 世代 body 约束**：`thinking` 默认开且 `max_tokens` 是「思考+正文」总预算 → 不关思考必 `stop_reason=max_tokens`、正文为空、**HTTP 200 却恒红 `content_mismatch`**；关思考（仅 effort ≤ high 时被接受）就**必须同时删 `context_management`**（`clear_thinking_20251015` 与 `disabled` 互斥、直接 400）；**fable-5 根本不接受关思考**（always-on adaptive），只能放大 `max_tokens`。另外抓包自带的 `fallbacks:[{model:...}]` **必须删**——目标模型不可用时上游会自动改服回退模型、探针照样绿。

### 存储与功能模块

字段清单见上（读 struct tag）；部署细节见 `docs/user/deploy-postgres.md`、赞助体系见 `docs/user/sponsorship.md`。

## API 端点

路由注册在 `internal/api/server.go`（`router.GET/POST/...` 那一段；行号会漂，用 grep 定位别记数字）

**完整路由清单读代码**：`grep -nE 'router\.(GET|POST|PUT|DELETE|HEAD)' internal/api/server.go`。下面只记路由表里看不出来的契约：

- **`GET /api/events` / `/api/events/latest`**：**强制鉴权**，未配置 `events.api_token` 时返回 **503**（不是 401、也不是静默开放）。
- **`POST /api/change/test`**：`target_key`（`provider--service--channel`）**必填**；`Model`/`RequestModel`/`ModelVendor` 一律由服务端按该 key 从运行时**父行**读取，**不信客户端**（理由见「探测链路统一」）。
- **`POST /api/admin/monitors/:key/probe`**：走完整 `ServiceConfig`，与 scheduler **字段级一致**；且**配了代理就自动走代理**（公开 onboarding/change 自测永不走）。
- **`DELETE /api/admin/monitors/:key`** 是**软删除**（归档到 `monitors.d/.archive/`），不是真删。
- **公开写入端点一律 IP 限流**：`POST /api/onboarding/submit`、`/api/onboarding/test`、`/api/change/test` 都按 IP 日配额限流（`onboarding.max_per_ip_per_day` 默认 **5**；`onboarding.change_requests.max_per_ip_per_day` 默认 **3**，两者**独立计数**、change 侧与 onboarding 解耦）。2026-07-25 遭过系统化探测，别在新增公开写入端点时忘了挂限流。
- **全部 `/api/admin/*` 走 Bearer token 鉴权**（`onboarding.admin_token`）——包括 changes / submissions / monitors 三组。新增 admin 路由必须挂进已有鉴权组，别单独注册。
- **`POST /api/admin/changes/:id/apply` 仅 auto 模式可用**；`POST /api/admin/monitors/:key/toggle` 只切 `disabled`/`hidden` 两个 flag（payload 只有这两项，故重复 model_id 之类错误必来自磁盘历史坏文件 = 服务端状态问题，刻意保持 5xx 不降 400）。
- **`GET /api/events`** 除强制鉴权外走**游标分页**；**`GET /api/admin/monitors/:key/logs`** 支持 `since`/`limit`/`model` 且返回 `error_detail`。
- **`GET /ready`**：含存储连通性；热更新未被应用时 GET body 附 `config_reload{last_skipped_at,last_error,skipped_count}` **信息化**，HTTP 状态**恒不因此翻 503**。两条静默失败路径已全覆盖（v2.77.0）：① `CheckRuntimeModelIDs` fail-closed 闸跳过——`last_error` 是该闸原文（只含 provider/service/channel/model + 固定文案，已知可公开）；② `loadOrRollback` 加载/`validate()` 失败后「保留旧配置」（model_id 重复、yaml 语法错等）——`last_error` 是**固定脱敏串**「配置热更新失败，保持旧配置；详情见服务端日志」，原始错误只进日志（`/ready` 无鉴权，loader 错误可能含 yaml 路径与解析细节）。
  ⚠️ 该状态是「**进程生命周期内曾发生过**」，成功热更新**不会**清除、进程重启才归零——报警要看 `skipped_count` 增量或 `last_skipped_at` 是否推进，别把「字段存在」直接读成「当前仍卡住」；定位仍需 `level=ERROR msg=重载失败` 日志。

**/api/status 查询参数**:
- `period`: `90m` / `24h`（默认，`1d` 为别名）/ `7d` / `30d`
- `align`: `hour`（整点对齐，可选）
- `time_filter`: `HH:MM-HH:MM`（UTC 时段过滤，仅 7d/30d 可用，支持跨午夜）
- `provider` / `service`: 按名称过滤
- `board`: `hot` / `secondary` / `cold` / `all`（板块过滤）
- `include_hidden`: 调试用，包含隐藏项

`data` 与 `groups` 按监测项有无 `model` 分流（`query.go::queryAndSerialize`）：**model 为空 → `data`**（无 model 监测项 + 旧前端兼容），**model 非空 → `groups`**。前端 `useMonitorData` 合并消费两者（`[...legacy, ...groups]`）。生产所有监测项均已配 model，故 **prod 实际返回 `data=[]`、`meta.count=0`，属预期非回归**（2026-07-06 查证，item 57b）。响应形状读 `internal/api/handler.go` 的 JSON tag。

## 常见模式与陷阱

### Scheduler 中的并发

调度器使用两个锁：
- `cfgMu` (RWMutex): 保护配置访问
- `mu` (Mutex): 保护调度器状态（运行标志、定时器、任务堆、代次）

对于只读配置访问，始终使用 `RLock()/RUnlock()`。

**⚠️ 两条静默失败契约（动 `rebuildTasks` / `dispatchDue` 前必读，v2.84.1 起）**：

1. **任务代次 `generation`**：`dispatchDue` 把到期任务弹出堆后会**解锁**执行探测、再加锁推回，这段「堆外窗口」内任务不在 `s.tasks` 里。若期间发生 `rebuildTasks`，堆里已有该监测项的新任务，旧任务无条件推回就会让**同一通道每周期被探测两次**（直到下次重建才自愈）。故 `rebuildTasks` 与 `Stop` 在持 `mu` 时递增 `s.generation`（**含空配置、全 disabled/cold 两条提前 return**——漏一条闸就失效），新建 task 写入当前代次，回填前比对 `next.generation == s.generation && s.running`。**反向失败更危险**：新建 task 若没带上当前代次，它第一次跑完就被判成旧代丢弃，**该通道从此永久消失、不再被探测**且无任何报错——守卫是 `TestDispatchDue_NewTaskIsRequeuedAfterItsFirstRun`。
2. **热更新按「组」保留 `nextRun`**：组间错峰的总展开可能远大于最短巡检周期（现网 95 组展开 10m33s、最短 2m30s，基距被两个 4 模型通道的组内展开顶高），所以**绝不能每次热更新都重排全部任务**——那会让短周期通道每次热更新丢好几个周期，热力图缺块。现行规则：PSC 组的成员身份键集合与各自**有效** interval（含 `s.fallback` 回退）都没变就沿用旧 `nextRun`。**粒度必须是组不是单个任务**：组内模型按固定 2s 排布、多模型热力图靠时间戳对齐渲染，只重排组内一个成员会让那一行错位出空洞。身份键用 `ModelID`、为空回退 PSCM 四元组（`validateModelIDs` 允许空 `ModelID`，本包是库不能假设调用方跑过 `CheckRuntimeModelIDs`）。需要重排的组把首次延迟封顶到自己的 interval，**但 startup 路径刻意不封顶**（那时本就该铺开填满周期）。验证手法与残留问题见 memory `reference_rp_heatmap_blocks_and_probe_cadence`。

### Storage Factory 与驱动选择

`storage.Factory` 根据 `storage.type` 选择 SQLite 或 PostgreSQL 实现。新增存储驱动时先实现 `storage.Storage` 接口，再在 Factory 中注册。

### Parent-child 继承

父通道定义公共配置（url/headers/body 等），子通道通过 `model` + `parent`（格式 `provider/service/channel`）继承。继承逻辑集中在 `internal/config/parent_inheritance.go`，校验确保父通道存在。

### 指数退避重试

`retry` 表示**额外重试次数**（不含首次尝试）。退避公式：`min(base_delay * 2^attempt, max_delay) + random_jitter`。配置见 `internal/config/app_config.go`，实现见 `internal/monitor/probe.go`。

### 事件状态机与鉴权

`events.Detector` 使用连续计数阈值防止状态抖动（flapping）：连续 N 次不可用才触发 DOWN，连续 M 次恢复才触发 UP。`/api/events*` 端点**强制鉴权**：未配置 `api_token` 时返回 503 拒绝所有请求；已配置时需要 `Authorization: Bearer <token>`。

### 批量查询优化

7d/30d 等长周期查询可通过 `enable_batch_query` 将 N 个监测项的 2N 次数据库往返降为 2 次。配合 `enable_db_timeline_agg`（仅 PostgreSQL）可将聚合计算下推到数据库层。回退链路：batch → concurrent → serial。

### SQLite 并发

使用 WAL 模式（`_journal_mode=WAL`）允许写入时并发读取。连接 DSN：`file:monitor.db?_journal_mode=WAL`

### Probe 中的错误处理

- 网络错误 → 状态 0（红色）
- HTTP 4xx/5xx → 状态 0（红色）
- HTTP 2xx + 慢延迟 → 状态 2（黄色）
- HTTP 2xx + 快速 + 内容匹配 → 状态 1（绿色）

### Onboarding 通道标识派生

收录申请提交时，channel code 由 `deriveChannelCode(channelType, channelSource, channelGroup)` 派生为三段 `{type}-{source}-{group}`（全小写；group 为空时回退两段，仅用于兼容旧数据）。例如 type="O" + source="max" + group="us" → `o-max-us`。提交即强制校验（提交流程在 `internal/onboarding/submit.go`，通道标识词表与派生在 `channel_source_catalog.go` / `channel_code.go`）：
- **provider_name** 为服务商展示名（经 `displayname.ValidateProviderName`：允许中文等常规可见 Unicode 文本，≤100 rune，拒控制字符 Cc/格式字符 Cf 含 bidi·零宽/行段分隔符 Zl·Zp，**必填**；首尾「空白∪Cc/Cf/Zl/Zp」规范化剥除，仅内部出现才拒），用户提交与 AdminUpdate 均过同一校验；发布时 provider PSC slug 由 `BuildServiceConfigFromSubmission` 从它派生（`lower(空格转-)`，仅 ASCII 名可得合法 slug）或由管理员 `target_provider` 覆盖——非 ASCII 展示名派生出非法 slug 且未填 `target_provider` 时，`AdminPublish` 返 `InvalidProviderSlugError`（handler 特判为 4xx 可操作指引、不落文件），提示管理员填英文代号；`AdminConfigJSON` 整份覆盖发布不经此字段级校验（管理员逃生口）。（`change-request` submit/apply 现也过同一 `displayname` 校验——submit 把规范值写回 `proposed_changes`、apply 再校验防历史脏数据，**v2.63.0 起 item -16 已闭合**。发布门校验从 `pscSegmentPattern` 改用 loader 同一函数 `config.ValidateProviderSlug`，消 `a--b` 派生 slug「写盘成功热加载失败」= item -17b。）
- **channel_source** 必须是 `ChannelSourceCatalog`（per-service 受控词表，单一真相源，同时供 `/api/onboarding/meta` 下发前端）中的 2-5 位小写代码；如需新增来源改这一处 map；
- **channel_type ↔ channel_source 须自洽**：`channelTypeAllowedCategories`（`channel_source_catalog.go` 里与词表并列的另一单一真相源，同样经 `/api/onboarding/meta` 下发）规定 O→{subscription,official,cloud}、R→{reverse}、M→{mixed}；`validateChannelTypeSource` 在 Submit 与 AdminUpdate 四元组重派生前校验所选来源的 Category 落在该类型允许集合内，否则拒绝（官方通道不可选 kiro 等逆向来源）。前端来源下拉据此 map 同步过滤；
- **channel_group** 为 1-8 位小写字母/数字（中转商自定义分组代号，仅用于派生 channel_code，不作展示），留空默认 `main`；
- **channel_name** 为可选的通道展示名（经 `displayname.ValidateChannelName`：允许中文等常规可见 Unicode 文本，≤40 rune，拒绝控制字符 Cc/格式字符 Cf 含 bidi·零宽/行段分隔符 Zl·Zp；首尾规范化同 provider），仅用于 UI 显示、不参与 channel_code/PSC 派生；用户提交、AdminUpdate 与 change-request submit/apply 均过同一校验，留空时前端回退显示 channel code。注意 `AdminConfigJSON` 整份覆盖发布与 admin monitors CRUD 不经此字段级校验——与 `target_channel` 同属故意保留的管理员逃生口。

- **model / model_vendor：公开表单一律填不了，全部由所选模型的模板钉死**（站长 2026-08-21 裁定）。

  起因是同日早些时候的做法——第一方厂商模型走 native 通用模板 + 让提交方填「模型 ID / 模型厂商 / 请求形态」三格——被实际使用戳穿：厂商下拉与模型选择解绑，选中「豆包 Seed 2.1 Turbo」后照样能把厂商改成「智谱」，而 `ValidateModelSelection` 只校验 vendor 在受控词表内、**不校验它与模型/模板是否自洽**，这份自相矛盾会一路写进 `monitors.d` 的 `model_vendor` 并显示在站点厂商列上；请求形态同理，选错的症状是 HTTP 200 却恒红 `content_mismatch`；模型 ID 可编辑的理由（同平台 ID 写法不同）也不成立——上游命名不统一不该由提交方替监测平台适配，各家自填各的还会让跨通道的 `model` 串不可比。这三项是探针实现细节，只有我们判得了对错。

  现在的形态：**每个第一方厂商模型各有一个专属模板**（`cc-glm52-arith` / `cc-kimi-k27code-arith` / `cc-minimax-m3-arith` / `cc-deepseek-v4pro-arith` / `cc-doubao-seed21turbo-arith` 及对应 5 个 `cx-*`），与 Anthropic/OpenAI/Google 的模板完全同一形态；表单第二步只剩一个「监测的模型」下拉，选中后只读展示 `request_model`。中转商要上我们还没建模板的模型，走 `/contact`。

  - **公开请求体没有这两个字段**：`SubmitRequest` 与 `inlineTestRequest`（`/api/onboarding/test` 与 `/api/change/test` 共用）都不含 `model`/`model_vendor`，客户端塞了会被 `ShouldBindJSON` 丢弃——不变量由结构体形状保证，不靠纪律。⚠️ **别加回来**：`ValidateModelSelection` 对「非 native + 仅 vendor 非空」是放行的（vendor 只过 `Normalize`，没有 native 门控），而行级 vendor 在 `config > template` 回退链里优先于模板值，字段一旦存在，直连 API 就能把 GLM 通道标成 Anthropic。
  - **两条公开入口仍调 `ValidateModelSelection(templateName, "", "")`** 作第二道 fail-closed：校验的是「这个模板自己声明得起模型吗」。唯一会失败的形状是 native 族，它本就被 `resolveSelfServeTemplate` 的可见性闸挡在外面；万一将来有人把某个 native 模板标回可见，这道闸会在测试入口就报可读错误，而不是带着 `"model": ""` 去打上游。
  - **admin 侧一字未动**：`Submission.Model/ModelVendor`、DB 两列、`AdminUpdate`（model/model_vendor **刻意支持显式清空**：模板从 native 改回普通模板时必须能清，否则组合校验会把这条申请永久卡死）、`AdminPublish` 的 `validateMonitorConfig`（在 `AdminConfigJSON` 整份覆盖**之后**跑）全部保留——那是管理员逃生口，火山方舟那批 native 通道靠它上架。`ValidateModelSelection` 的完整规则（native 漏填即拒、非 native 多填即拒、model 走严格 ASCII 白名单 `^[A-Za-z0-9][A-Za-z0-9._:@/+-]*$` ≤128 字节——理由是 body 按**原始字节**发送、`{{MODEL}}` 是纯字符串替换且同时作用于 URL/headers/body/success_contains）在 admin 路径上照旧全部生效。
  - **proof 绑定**：`ProofClaims.Model` 在公开路径上恒为空串（签发端与校验端同为 `ValidateModelSelection` 的返回值），模板由 `Variant` 绑住——公开流程的模型由模板唯一决定，绑住模板即绑住模型。⚠️ 这意味着**任何 `claims.Model` 非空的 proof 在公开提交侧一律验签失败**，部署瞬间在途的浏览器测试需重测（TTL 5min，与 v2.79.0 那次 proof v2 升级同类）。
  - **模板闸 `resolveSelfServeTemplate`** 不变：模板必须是**本 service** 已注册变体且 `self_serve_visible`——管理员改 `template_name` 上架内部模板仍是有意保留的逃生口。
  - **wire 变更**：`/api/onboarding/meta` 的 `request_shapes_by_service` 字段已删除（它只服务于「自填模型」那条已下线的路径）；`model_vendors` 保留——主表格厂商列（`useModelVendors`）、模型下拉的 optgroup 分组标题、admin 表单都还在消费它。

**PSC 各段（含三个 `target_*` 覆盖值）一律走 `config.ValidateProviderSlug`**——小写字母、数字、短横线，不能首尾短横线、**不能连续短横线**、≤100 字符。收紧到与 loader 同源是必须的：段内出现 `--` 会让 `ParseMonitorFileKey`（`SplitN(key,"--",3)`）把 `{provider}--{service}--{channel}` 文件名切错位，且加载期的 `ValidateProviderSlug` 会拒掉整份配置 = 「上架 200、热加载失败、重启拉不起来」。管理员填错覆盖值时返 `InvalidPSCOverrideError`（handler 映射 400、消息点名是哪一格），与「没填覆盖值、展示名又派生不出合法 slug」的 `InvalidProviderSlugError` 分开——两者处置动作不同。`AdminUpdate` 保存时对本次请求带来的 `target_*` 同规则 fail-fast（空串合法=清空覆盖；只校验本次改的字段，故存量脏值不会卡死无关编辑）。`AdminUpdate` 仅当 service/type/source/group 四元组真正变化时才重派生 channel_code（保护 legacy 两段记录），并对 channel_type(O/R/M)、service_type(cc/cx/gm) 做枚举校验。管理员可在发布前通过 `target_channel` 覆盖派生值（**故意保留的逃生口，不受三段命名约束**——但字符集规则同上，用于 legacy 与特殊命名）。前端 `ChannelTypeIcon` 通过首字母（大小写不敏感）识别通道类型图标（o→官方、r→逆向、m→混合）。

**入驻须知逐条确认**：`SubmitRequest.AgreementAccepted` 必须为 true（前端 `ConfirmStep` 据《入驻须知与确认》拆 6 条独立勾选，全勾才放行），否则 Submit 在前置环节即拒。落库时后端盖戳 `agreement_accepted/agreement_accepted_at/agreement_version`（`const AgreementVersion`，不信客户端），store 三列沿用 `channel_group` 幂等迁移模式（sqlite PRAGMA 预检 / pgx `ADD COLUMN IF NOT EXISTS`）。

**变更流程泄露 Key 拒绝名单**：`change_requests.revoked_key_file`（主配置目录下的直接子文件，每行一个 `sha256(明文 api_key)` hex）+ `revoked_key_count`（预期条目数，不一致即整次配置加载失败——挡名单被截断）。加载在 `config.loadRevokedKeyFile`，fail-closed（启动期拒启动 / 热更新期保留上一份含旧名单的配置）；配置监听器显式放行该文件（`Watcher.currentRevokedKeyPath`），否则单改名单永不生效。运行时集合挂在 `ChangeRequestConfig.RevokedKeySHA256`（`json:"-"`、加载后只读），经 `UpdateConfig` 喂给 `AuthIndex.Rebuild`，**整体替换不做并集**（移除条目要能生效）。四道闸：① `AuthIndex.Lookup` 返回 `(candidates, revoked)`，命中即拒不返回候选 —— `Auth` 与 `Submit` 两个入口共用这唯一咽喉点；② `Submit` 的 `new_api_key` 也查名单（轮换目标不能又是泄露 key）；③ `AdminApprove`/`AdminApply` 经 `ensureRequestKeysNotRevoked` 追溯校验**两处**：`cr.AuthFingerprint`（已落库请求只存 HMAC 指纹、无明文，故 `Rebuild` 顺带派生「泄露名单 ∩ 当前配置在用 key」的 HMAC 集合 `revokedAuthFP`——**这不是完备覆盖**：某把泄露 key 若已被轮换出配置或整条 monitor 被删，就无法从 SHA-256 名单反推其 HMAC 指纹，用它认证过的历史请求会漏检，须靠名单上线时人工冻结/驳回存量队列补齐）与 `cr.NewKeyEncrypted`（密文可解，故这一侧是**精确判定**，既拦「提交时新 key 已在名单但早于本闸落库」也拦「新 key 事后才进名单」；放在 Approve 与 Apply 两处而非只在 Apply，因为 manual 模式的请求根本不走 Apply）。命中只能驳回 —— 与 v2.69.0 反作弊 admin 闸同一类后门；④ handler 映射独立错误码 `REVOKED_API_KEY`（不混进 `UNAUTHORIZED` 的防枚举统一文案，被动受害的中转商需要可行动提示）。哈希空间刻意用**无密钥 SHA-256** 而非 `apikey.KeyCipher` 的 HMAC：名单里的 key 已经公开、摘要不构成新增泄露，且离线可生成、不必接触 `encryption_key`；**若将来要收录尚未公开的凭据，此前提不成立，须改带 pepper 的摘要**。运维：增删条目须同步改 `revoked_key_count`，部署名单用「同目录临时文件 + 原子 rename」。

**变更流程反作弊 re-attestation**：change-request 改 `base_url` 或 API Key（`requiresTest` = `'base_url' in changes || newApiKey !== ''`，与后端 `fieldsRequiringTest` 逐字镜像；前端由单一 helper `changeRequiresTest` 供 hook 与 `ReviewStep` 共用）时，前端 `ReviewStep` 条件渲染单条「禁止监测作弊」re-attestation 勾选框（复用 onboarding `clauseNoCheat` 文案）并门控提交；后端 `change.Submit` fail-closed 校验（`requiresTest && !AgreementAccepted` 早于 proof 校验即拒），通过后把 `agreement_accepted/agreement_accepted_at/agreement_version`（版本复用 `onboarding.AgreementVersion`、时间由后端定，均不信客户端）盖在该 `ChangeRequest` 上作审计。纯展示变更（如改 `provider_name`/`channel_name`）不要求、也不盖戳。**同一闸也守住 admin 侧**：`AdminApprove`/`AdminApply` 在状态校验后、任何落地前同样 `requiresTest && !AgreementAccepted` fail-closed 拒绝——迁移前已提交的历史请求（`requires_test=1` 且 `agreement_accepted=0`）只能驳回、不能批准/应用（新提交因 Submit 闸恒满足此不变量，本闸只对历史/异常行生效），堵住"绕开创建期闸、直接批准历史未确认请求"这条残余后门。三列走 change store 已有 `ensureColumns` 幂等迁移（sqlite `INTEGER` / pgx `BIGINT`），`Update` 不写这三列（审计不可变、Save-only）；admin `ChangeRequestList` 据 `requires_test × agreement_accepted` 渲染三态审计行（不适用／已确认+版本时间／⚠ 未确认）。

### 零 monitors 启动

当 `onboarding.enabled = true` 时，`validate()` 允许 `monitors` 数组为空。这支持 "onboarding-first" 部署场景：先启动空系统，再通过收录流程添加通道。

### 前端数据获取

`useMonitorData` Hook 每 30 秒轮询 `/api/status`。组件卸载时需禁用轮询以防止内存泄漏。

- 每次提交代码前记得检测, 是否有变动需要同步到文档
- 在commit前应先进行代码格式检查

## 同步检查清单

更新本文档时只需核对这几条（**目录/文件清单类的同步项已随清单一并删除——那些直接读代码**）：

- [ ] 改了状态映射 / 可用率权重 → 同步「状态码系统」小节
- [ ] 改了 `monitors.d/` 写路径、模板占位符、proof 绑定 → 同步对应契约小节
- [ ] 改了 API 的 wire 形状（新字段 / 分流规则 / 错误码）→ 同步「API 端点」小节
- [ ] 发版后 → 检查点写进 `docs/ai-release-ledger.md`，**不要写回本文件**
- [ ] 硬性约束有变 → 同步 [AGENTS.md](AGENTS.md)（双 source-of-truth 会漂）
