import type { TFunction } from 'i18next';

/**
 * 模型家族：把一个具体模型 ID（`claude-opus-4-7`）归到它所属的产品线（`opus`）。
 *
 * ## 用途边界（重要）
 *
 * 家族**只服务于「模型」筛选器的下拉分组**：让用户一次勾中「所有 Opus」，而不必
 * 把 opus-4.6/4.7/4.8/5 逐个点一遍。它不进 wire、不参与任何可信性判断，也不回答
 * 「这条通道跑的是谁家的模型」——那是 model_vendor 的职责，且那根轴有一条硬红线：
 * **模型是声明的、不是反推的**（见 internal/modelvendor 包注释与 VendorBadge）。
 *
 * 本文件从 request_model 反推家族，为什么不算越过那条红线：模型列渲染的本来就是
 * `shortenModelName(request_model)` 的派生结果（见 table/modelNames.ts），家族只是
 * 在同一条既有派生链上再分一次桶，没有新增任何关于「这条通道可不可信」的断言。
 * 反推错了最多是分组归错，不会把 GLM 说成 Claude。
 *
 * ## 为什么是前缀匹配而不是穷举模型 ID
 *
 * 穷举一张 request_model 全集表看似更"声明式"，实则是持续失效的负担：模型版本在
 * 滚动更新（换版本保历史连续性正是 /rpmigrate 那套纪律在处理的事），新上一个
 * `claude-opus-5-1` 就会静默掉进「其他」组，而筛选器不会报错——用户按家族全选时
 * 无声漏掉它。前缀 + 段边界匹配对版本号演进免疫，代价只是要求家族段本身稳定。
 */

/** 归不了族时的哨兵 code。UI 上作为「其他」分组，不是错误态。 */
export const MODEL_FAMILY_OTHER = 'other';

export interface ModelFamily {
  /** 家族 code，同时是 i18n 键 `modelFamilies.<code>` 的后缀。 */
  code: string;
  /**
   * 匹配前缀（全小写）。命中条件是「前缀 + 段边界」，不是子串包含——
   * 后者会让 `foo-glm-bar` 误判成 glm 家族。
   *
   * 同族常需两类前缀：完整模型 ID 用的（`claude-opus`）与 model 展示名回落用的
   * （`opus`）。native 族模板刻意不声明 request_model，那类通道只有展示名可归。
   */
  prefixes: readonly string[];
}

/**
 * 家族声明表。**数组顺序即筛选器下拉的分组顺序**，不要在调用方重排。
 *
 * 排布口径与 internal/modelvendor 的 catalog 一致：先三家原厂（Anthropic 四族按
 * 能力从高到低、OpenAI、Google），再国内厂商，「其他」永远垫底。
 */
export const MODEL_FAMILIES: readonly ModelFamily[] = [
  { code: 'opus', prefixes: ['claude-opus', 'opus'] },
  { code: 'sonnet', prefixes: ['claude-sonnet', 'sonnet'] },
  { code: 'haiku', prefixes: ['claude-haiku', 'haiku'] },
  { code: 'fable', prefixes: ['claude-fable', 'fable'] },
  { code: 'gpt', prefixes: ['gpt'] },
  // Gemini 的展示名回落写作 Flash/Flash Preview/Flash Thinking，故两种前缀都收。
  { code: 'gemini', prefixes: ['gemini', 'flash'] },
  { code: 'deepseek', prefixes: ['deepseek'] },
  { code: 'doubao', prefixes: ['doubao'] },
  { code: 'glm', prefixes: ['glm'] },
  { code: 'kimi', prefixes: ['kimi'] },
  { code: 'minimax', prefixes: ['minimax'] },
  { code: MODEL_FAMILY_OTHER, prefixes: [] },
];

/** 段边界：模型 ID 用 `-`/`.` 分段，展示名（`Fable 5.1`）用空格。 */
const SEGMENT_BOUNDARY = /[-. ]/;

/** 前缀是否命中：必须整段命中，`glm` 不能匹配 `glmx-1`，也不能匹配 `foo-glm`。 */
function matchesPrefix(candidate: string, prefix: string): boolean {
  if (!candidate.startsWith(prefix)) return false;
  if (candidate.length === prefix.length) return true;
  return SEGMENT_BOUNDARY.test(candidate[prefix.length]);
}

function normalize(value?: string): string {
  return value?.trim().toLowerCase() ?? '';
}

/**
 * 归族。`requestModel` 优先——它是探针实际发出的模型 ID；`model` 只是展示名，
 * 常是跨版本的家族抽象（`Opus` 底下同时躺着 4.6/4.7/4.8/5）。
 *
 * fail-soft：任何归不了的输入返回 {@link MODEL_FAMILY_OTHER}，不抛异常。筛选器
 * 宁可多一个「其他」分组，也不能因为上游冒出个没见过的模型 ID 就整个崩掉。
 */
export function deriveModelFamily(requestModel?: string, model?: string): string {
  const candidate = normalize(requestModel) || normalize(model);
  if (!candidate) return MODEL_FAMILY_OTHER;

  const family = MODEL_FAMILIES.find((entry) =>
    entry.prefixes.some((prefix) => matchesPrefix(candidate, prefix)),
  );
  return family?.code ?? MODEL_FAMILY_OTHER;
}

/**
 * 家族 code → 本地化展示名。与 vendorLabel 同构：**词表外的 code 原样显示 code**，
 * 不猜名字。注意这是动态 i18n 键，locales.parity.test.ts 的字面键扫描守不住它，
 * 四语言 label 的完备性由 modelFamily.test.ts 单独钉死。
 */
export function modelFamilyLabel(t: TFunction, code: string): string {
  return t(`modelFamilies.${code}`, { defaultValue: code });
}
