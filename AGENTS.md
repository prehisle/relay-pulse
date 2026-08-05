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

## 在途机制：`model_vendor`「模型厂商」正交轴

- 受控词表真相源是 `internal/modelvendor`（stdlib-only 叶子包，不得 import 仓库内其它包）。code 进 wire 且被 rpdiag 消费，**一经发布不可复用于另一厂商**。
- 取值链与 `Model`/`RequestModel` 同款（`config 行级 > template`），并与 `RequestModel` 一样参与父子继承。
- ⚠️ **`CheckRuntimeModelVendors` 已实现但故意未接线，别顺手接到 `cmd/server/main.go`**——当前所有监测行与模板的 vendor 都是空的，接线即全站配置加载失败。须等回填完成后再接。
- 禁止从 `request_model` 前缀反推 vendor；它是声明字段。
- 前端（Phase 2 已落）：厂商列/筛选/图标/四语言已做。列显隐由 `showVendorColumn` 单一开关驱动，基于**未筛选**数据判定；通道级厂商要求**所有** layer 非空且同值，否则显示 `-`。未收录 code 原样显示 code，不猜名字。
- 细节（校验挂载点为何是 `validateResolvedModelConstraints` 而非 `validate()`、「一个通道一个厂商」不变量）见 `CLAUDE.md`。

## 技术指南

构建命令、代码风格、测试规范、提交与 PR 约定等详细技术指南，请参考 `CLAUDE.md` 和 `CONTRIBUTING.md`，此处不再重复。
