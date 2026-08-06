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

## `model_vendor`「模型厂商」正交轴

- 受控词表真相源是 `internal/modelvendor`（stdlib-only 叶子包，不得 import 仓库内其它包）。code 进 wire 且被 rpdiag 消费，**一经发布不可复用于另一厂商**。
- 取值链与 `Model`/`RequestModel` 同款（`config 行级 > template`），并与 `RequestModel` 一样参与父子继承。
- ⚠️ **别给 vendor 加 fail-closed 运行时闸**（曾有 `CheckRuntimeModelVendors`，Phase 3 已删）——它无法像 `model_id` 那样自动派生，加闸会让手写内联监测行的自托管用户升级即 crash-loop（v2.69.2 同类伤害）。覆盖靠「内置模板全部声明 vendor」，守卫是 `TestBundledTemplatesDeclareModelVendor`。
- 禁止从 `request_model` 前缀反推 vendor；它是声明字段。
- **native 模板族**（`<service>-native-*`，如 `cc-native-arith`）承接第一方厂商兼容端点，**厂商无关**：不声明 `model`/`request_model`/`model_vendor`，三者必须由监测行填。别给它加 vendor——行级漏填时会经回退链静默继承成错厂商。漏填只由 `validateFinal` 告警提示。判定见 `isNativeProbeTemplate`（`SplitN(name,"-",3)`，故四段名如 `cc-native-arith-nothink` 同样算 native）。
- native 族现有 5 个，选型看厂商模型的思考行为（均经真端点实测，别凭猜换）：`cc-native-arith`（`max_tokens:20`，不开思考的模型）／`cc-native-arith-nothink`（`thinking.disabled`+64，**默认开思考且认这个开关**的模型，首选）／`cc-native-arith-512`（仅放大预算，**默认开思考却不认 thinking 开关**的模型，如 kimi）／`cx-native-arith`（`{{BASE_URL}}/v1/responses`）／`cx-native-arith-noreason`（`{{BASE_URL}}/responses` + 无 `reasoning` 字段）。⚠️ 两个 cx 模板的 **base_url 契约相反**：前者要求 base **不含**版本段、后者要求**已含**（如火山方舟 `…/api/coding/v3`）；填错只会在探测时变红，加载期无校验，选模板时先对齐 base_url 形态。
- 给带思考的模型套 `max_tokens:20` 会让 thinking 吃光预算、`stop_reason=max_tokens` 正文为空 → 内容校验判红。这是接第一方厂商最容易踩的坑。
- 前端（Phase 2 已落）：厂商列/筛选/图标/四语言已做。列显隐由 `showVendorColumn` 单一开关驱动，基于**未筛选**数据判定；通道级厂商要求**所有** layer 非空且同值，否则显示 `-`。未收录 code 原样显示 code，不猜名字。
- 细节（校验挂载点为何是 `validateResolvedModelConstraints` 而非 `validate()`、「一个通道一个厂商」不变量）见 `CLAUDE.md`。

## 探针模板硬约束（改 `templates/` 前必读）

- **`cc-*-ping-*` 带 `x-anthropic-billing-header`，其中 `cch` 是整个 body 的 attestation**（saiai 一类网关严格校验）。**body 改一个字节就必须重算** `cc_version` 后缀与 `cch`（算法在 `/workspace/zdy/saiai/saiai-server/backend`，`go run ./cmd/claudebilling -in body.json -replace-body`）。
- ⚠️ **随机题面与整包 attestation 数学上互斥** → **arith 族永远不能带 billing header**；Claude 5 的 arith 版只能照 `cc-opus-arith` 改 `request_model` + 加 `thinking.disabled` + 删 `context_management` + 调 `max_tokens`，别照 ping 模板做。也**别把 arith 通道换成 ping**：ping 只验 `pong`、抓不到 mock 回显作弊，arith 的随机题面才是那道反作弊闸。
- **Claude 5 世代**：`thinking` 默认开且 `max_tokens` 是「思考+正文」总预算，不关思考必 200 却恒红；关思考（仅 effort ≤ high 被接受）必须**同时删 `context_management`**（否则 400）；**fable-5 不接受关思考**，只能放大 `max_tokens`。抓包自带的 `fallbacks` **必须删**（否则目标模型不可用时上游改服回退模型、探针假绿）。
- 别把真实身份（`account_uuid`/`device_id`）烤进模板；本仓公开。
- 抓包形态：`ANTHROPIC_BASE_URL` 指本地 echo server 得到的是「CLI→中转商」形态（**不发 cch**），要官方端点形态必须 mitmproxy 拦截。配方见 meta 仓 `.claude/skills/relay-client-gate/SKILL.md`。

## 技术指南

构建命令、代码风格、测试规范、提交与 PR 约定等详细技术指南，请参考 `CLAUDE.md` 和 `CONTRIBUTING.md`，此处不再重复。
