# Repository Guidelines

> 本文件为仓库中所有「智能体 / 机器人 / 助手」提供协作规范，**仅供 AI 使用和维护**。人类贡献者一般无需阅读或修改本文件。

## 交互与语言约定
- 本项目所有「智能体 / 机器人 / 助手」与维护者互动时，应**始终使用简体中文**进行沟通与回复。
- 评论、Issue、PR 描述和代码内用户可见文案，优先使用中文；如需英文，请保证含义与中文一致，并以中文为主。
- 如果外部工具或脚本只能输出英文，应在说明中简要补充中文解释。

## 文档策略（仅供 AI）
- 面向人类读者，项目方**重点维护的核心文档只有**：
  - `README.md`（入口、快速开始、本地开发）
  - `QUICKSTART.md`（快速部署与常见问题）
  - `docs/user/config.md`（配置与环境变量说明）
  - `CONTRIBUTING.md`（贡献流程与规范）
- `AGENTS.md`、`CLAUDE.md` 视为 AI 内部文档，**不要在面向人类的回复、README 导航等位置主动推荐或暴露**，除非用户明确询问。
- `archive/` 中的文档均为**历史文档**：可以作为补充背景使用，但引用时必须标注「历史文档，仅供参考，以当前核心文档和代码实现为准」。
- AI 不应随意新增顶层文档；如确有必要，应与用户确认后再创建。

## 配置与安全提示
- 禁止提交真实 API Key、数据库密码等敏感信息；仅更新 `config.yaml.example`，实际值通过环境变量或本地未入库配置文件注入。
- 修改与存储相关逻辑时，需同时在 SQLite（默认）和 PostgreSQL 场景下验证。
- **动 monitors.d 写路径（`internal/config/monitor_store.go`）必守两条不变量**：① 子行合并**一对一**——既有行被认领即移出候选，绝不允许一个既有行的 `model_id` 被复制给多个 updated 行（模板驱动子行的 `model` 在磁盘上是空串，多对一会集体撞同一个 id）；② 写盘前跑 `ValidateFileModelIDsUnique` **fail-loud**——重复 `model_id` 落盘即造出 loader 拒绝加载的坏文件（admin 返回 200、热更新 fail-closed 保住旧配置，容器重启才崩，极难归因）。回归测试见 `monitor_store_test.go` 的「子行一对一认领」段，改动前先跑。

## `model_vendor`「模型厂商」正交轴

- 受控词表真相源是 `internal/modelvendor`（stdlib-only 叶子包，不得 import 仓库内其它包）。code 进 wire 且被 rpdiag 消费，**一经发布不可复用于另一厂商**。
- 取值链与 `Model`/`RequestModel` 同款（`config 行级 > template`），并与 `RequestModel` 一样参与父子继承。
- ⚠️ **别给 vendor 加 fail-closed 运行时闸**（曾有 `CheckRuntimeModelVendors`，Phase 3 已删）——它无法像 `model_id` 那样自动派生，加闸会让手写内联监测行的自托管用户升级即 crash-loop（v2.69.2 同类伤害）。覆盖靠「内置模板全部声明 vendor」，守卫是 `TestBundledTemplatesDeclareModelVendor`。
- 禁止从 `request_model` 前缀反推 vendor；它是声明字段。（前端 `utils/modelFamily.ts` 从前缀反推的是**模型家族**，只用于筛选器下拉分组：不进 wire、不参与可信性判断、归错只影响分组归属——不在本条禁令范围内，别混为一谈。）
- **native 模板族**（`<service>-native-*`，如 `cc-native-arith`）承接第一方厂商兼容端点，**厂商无关**：不声明 `model`/`request_model`/`model_vendor`，三者必须由监测行填。别给它加 vendor——行级漏填时会经回退链静默继承成错厂商。漏填只由 `validateFinal` 告警提示。判定见 `config.IsNativeProbeTemplate`（`SplitN(name,"-",3)`，故四段名如 `cc-native-arith-nothink` 同样算 native）。
- ⚠️ **native 族自 2026-08-21 起全部 `self_serve_visible: false`，是 admin-only 的**：它要求行级填模型，而公开表单已不给提交方任何填模型的入口。生产上那 10 条火山方舟通道继续跑在它上面，**变更流程照常可用**（`change` 不按可见性过滤，守卫 `TestAuthIndex_Rebuild_SelfServeHiddenVariantsStillUsable`）。要给自助表单加第一方厂商模型，**建专属模板**（见下条），别把 native 标回可见。
- native 族现有 5 个，选型看厂商模型的思考行为（均经真端点实测，别凭猜换）：`cc-native-arith`（`max_tokens:20`，不开思考的模型）／`cc-native-arith-nothink`（`thinking.disabled`+64，**默认开思考且认这个开关**的模型，首选）／`cc-native-arith-512`（仅放大预算，**默认开思考却不认 thinking 开关**的模型，如 kimi）／`cx-native-arith`（`{{BASE_URL}}/v1/responses`）／`cx-native-arith-noreason`（`{{BASE_URL}}/responses` + 无 `reasoning` 字段）。⚠️ 两个 cx 模板的 **base_url 契约相反**：前者要求 base **不含**版本段、后者要求**已含**（如火山方舟 `…/api/coding/v3`）；填错只会在探测时变红，加载期无校验，选模板时先对齐 base_url 形态。
- 给带思考的模型套 `max_tokens:20` 会让 thinking 吃光预算、`stop_reason=max_tokens` 正文为空 → 内容校验判红。这是接第一方厂商最容易踩的坑。
- 前端（Phase 2 已落）：厂商列/筛选/图标/四语言已做。列显隐由 `showVendorColumn` 单一开关驱动，基于**未筛选**数据判定；通道级厂商要求**所有** layer 非空且同值，否则显示 `-`。未收录 code 原样显示 code，不猜名字。
- **自助收录：提交方只选模型，模型 ID / 厂商 / 请求形态一律由模板钉死**（站长 2026-08-21 裁定，推翻了同日早些时候「让用户填」的做法）。第一方厂商模型各有一个**专属模板**（`cc-glm52-arith`、`cx-doubao-seed21turbo-arith` …），与 Anthropic/OpenAI/Google 的模板同一形态。理由：厂商下拉与模型选择解绑就能提交出「模型 ID 是豆包、厂商标成智谱」，请求形态选错的症状是 HTTP 200 却恒红 `content_mismatch`——这三项只有我们判得了对错。新模型走 `/contact`，我们评估后加模板。
  - 公开请求体（`SubmitRequest` / `inlineTestRequest`）**没有** `model`/`model_vendor` 字段，客户端塞了会被 `ShouldBindJSON` 丢弃——不变量由结构体形状保证，不靠纪律。⚠️ 别为了「让提交方填模型」把字段加回来：`ValidateModelSelection` 对**非 native + 仅 vendor 非空**是放行的，而行级 vendor 在回退链里优先于模板值，字段一旦存在，直连 API 就能把 GLM 通道标成 Anthropic。
  - `Submission.Model/ModelVendor`、DB 列、`AdminUpdate`（支持**显式清空**）、`AdminPublish` 的 `validateMonitorConfig` 全部**保留**——那是 admin 逃生口，火山方舟那批 native 通道靠它。
  - 两条公开入口仍调 `ValidateModelSelection(template, "", "")` 作第二道 fail-closed：万一有人把 native 模板标回可见，它会在测试入口就报可读错误，而不是带着 `"model": ""` 去打上游。
  - `/api/onboarding/meta` 的 `request_shapes_by_service` 字段**已删除**（wire 变更）；`model_vendors` 保留（主表格厂商列、下拉分组标题、admin 表单仍在用）。
- 细节（校验挂载点为何是 `validateResolvedModelConstraints` 而非 `validate()`、「一个通道一个厂商」不变量）见 `CLAUDE.md`。

## 探针模板硬约束（改 `templates/` 前必读）

- **`cc-*-ping-*` 带 `x-anthropic-billing-header`，其中 `cch` 是整个 body 的 attestation**（saiai 一类网关严格校验）。**body 改一个字节就必须重算** `cc_version` 后缀与 `cch`（算法在 `/workspace/zdy/saiai/saiai-server/backend`，`go run ./cmd/claudebilling -in body.json -replace-body`）。
- ⚠️ **随机题面与整包 attestation 数学上互斥** → **arith 族永远不能带 billing header**；Claude 5 的 arith 版只能照 `cc-opus-arith` 改 `request_model` + 加 `thinking.disabled` + 删 `context_management` + 调 `max_tokens`，别照 ping 模板做。也**别把 arith 通道换成 ping**：ping 只验 `pong`、抓不到 mock 回显作弊，arith 的随机题面才是那道反作弊闸。
- **Claude 5 世代**：`thinking` 默认开且 `max_tokens` 是「思考+正文」总预算，不关思考必 200 却恒红；关思考（仅 effort ≤ high 被接受）必须**同时删 `context_management`**（否则 400）；**fable-5 不接受关思考**，只能放大 `max_tokens`。抓包自带的 `fallbacks` **必须删**（否则目标模型不可用时上游改服回退模型、探针假绿）。
- **新模型可能带客户端版本闸**：上游 400 且报文点名 `cc_version`（`… does not support this model; version X.Y.Z or newer is required`）＝**模型级版本要求，不是 key/网关/cch 问题**（同批老模型全绿即可判定）。修法是 `claudebilling -version <新版本> -header-only` 重算 billing header + **同步抬 UA**（只抬 UA 会被反篡改门拒，2026-09-02 实证），重算前先按原版本号校准工具能否复现冻结值。见 `cc-fable51-ping-20260902`。
- 别把真实身份（`account_uuid`/`device_id`）烤进模板；本仓公开。
- **新增模板必须一并声明自助元数据**（v2.80+）：`"self_serve_label"` 是表单里给用户看的模型名（表单已不出现模板名，缺了它就会以文件名形态示人；守卫 `TestBundledTemplates_SelfServeVisibleOnesAreLabelled` 还要求同 service 内不重名）；内部/特化模板（历史冻结版本、单通道定制、中转商专用指纹版）加 `"self_serve_visible": false`——该字段是 `*bool`，**没写即可见**，反过来会让自建部署的模板集体消失。可见性**只**作用于 onboarding 的 meta/test/submit，registry、变更候选、admin 模板列表一律不受影响（跑 `*-anyrouter` 的老通道轮换 key 还得选得中自己那个模板；native 族同理）。
  - 给自助表单加一个第一方厂商模型 = **复制对应 native 模板 + 补 `model`/`request_model`/`model_vendor`/`self_serve_label` 四项**（body 逐字节保留，别顺手改请求形态）。`model` 写**规范模型 ID** 不写人话名：它同时是 DB 业务键，与既有 native 通道保持同一个串，将来把老行切到专属模板才不断历史；人话名放 `self_serve_label`。`model_vendor` 必须取自 `internal/modelvendor` 受控词表。守卫是 `TestBuildModelCatalog_FirstPartyVendorsAreSelfServable`（五家厂商在 cc/cx 各至少一条）。
- **自助流程的默认模板由 `"self_serve_default": true` 显式声明**（每 service 至多一份，现为 `cc-haiku-arith`/`cx-gpt-arith`/`gm-flash-arith`）。此前是「文件名字典序第一个」，2026-08-06 新增 `cc-fable-ping-20260806` 就把 cc 默认值换成了几乎无人提供的 fable-5，收录申请人与走变更流程的老通道**一律卡在测试步**。新增模板时别动这三个声明；守卫是 `TestInitTemplates_BundledDefaultsAreExplicit`。变更流程另有一层：默认跟随**通道自己在跑的模板**（`change.defaultTestVariant`），拿不到才回落注册表默认值。
- 抓包形态：`ANTHROPIC_BASE_URL` 指本地 echo server 得到的是「CLI→中转商」形态（**不发 cch**），要官方端点形态必须 mitmproxy 拦截。配方见 meta 仓 `.claude/skills/relay-client-gate/SKILL.md`。

## 技术指南

构建命令、代码风格、测试规范、提交与 PR 约定等详细技术指南，请参考 `CLAUDE.md` 和 `CONTRIBUTING.md`，此处不再重复。
