import { shortenModelName } from './modelName';
import { MODEL_FAMILIES, MODEL_FAMILY_OTHER, deriveModelFamily } from './modelFamily';

/** 家族在声明表里的位置；未知家族排在最后。 */
function familyRank(family: string): number {
  const index = MODEL_FAMILIES.findIndex((entry) => entry.code === family);
  return index === -1 ? MODEL_FAMILIES.length : index;
}

/** 与 ProcessedMonitorData.modelEntries 兼容的最小形状。 */
export interface ModelEntryLike {
  model?: string;
  requestModel?: string;
}

/** 一个模型在筛选器里的身份：key 用于比较，label 用于展示，family 用于分组。 */
export interface ModelFilterOption {
  key: string;
  label: string;
  family: string;
}

/**
 * 模型的展示名。**表格「模型」列与「模型」筛选器共用这一个派生**——两处各算各的，
 * 用户就会在下拉里看到表格中根本不存在的名字，「所见即所筛」随即失效。
 * `components/table/modelNames.ts` 反过来调用本函数，那里不再有第二份表达式。
 *
 * `request_model` 优先：它是探针实际发出的模型 ID；`model` 只是展示名，且常是
 * 跨版本的家族抽象（`Opus` 底下同时躺着 4.6/4.7/4.8/5）。
 */
export function modelDisplayName(entry: ModelEntryLike): string {
  return shortenModelName(entry.requestModel ?? '') || entry.model || '-';
}

/** 比较用的 canonical key：trim + 小写折叠。 */
export function modelFilterKey(displayName: string): string {
  return displayName.trim().toLowerCase();
}

/**
 * 同一 canonical key 被多条 layer 派生出来时，取哪一个当选项的元数据。
 *
 * 必须**完全不依赖遍历顺序**，否则同一个选项会随后端返回顺序变脸。两处会分歧：
 *
 *   1. label 写法不同（`Haiku` vs `haiku`）——取字典序小的。任意但稳定，且恰好
 *      偏向首字母大写的规范写法（大写字母码位更小）。
 *   2. **label 相同但 family 不同**——真实可触发：`{model:'Flash',
 *      requestModel:'gemini-2.5-flash'}` 与行级写死的 `{model:'2.5-flash',
 *      requestModel:''}` 会得到同一个 label `2.5-flash`，前者归 gemini、后者
 *      因为 key 里没有厂商前缀只能归 other。此时**永远取信息量更大的那个**：
 *      非 other 优先；都非 other 时按家族表顺序取靠前的。
 *      少了这一条，那个选项会随数据顺序在 Gemini 组和「其他」组之间跳。
 */
export function preferredModelOption(a: ModelFilterOption, b: ModelFilterOption): ModelFilterOption {
  if (a.family !== b.family) {
    if (a.family === MODEL_FAMILY_OTHER) return b;
    if (b.family === MODEL_FAMILY_OTHER) return a;
    return familyRank(a.family) <= familyRank(b.family) ? a : b;
  }
  return a.label <= b.label ? a : b;
}

/**
 * 派生一个通道的模型筛选身份，**通道内按 key 去重**（同一通道挂两个同模型的
 * layer 仍只是一个选项、也只算一次命中）。
 *
 * 顺序保持输入顺序——展示顺序是 useFilterOptions 的事，工具层不替它做决定。
 */
export function collectModelOptions(entries?: readonly ModelEntryLike[]): ModelFilterOption[] {
  const seen = new Set<string>();
  const options: ModelFilterOption[] = [];

  for (const entry of entries ?? []) {
    const requestModel = entry.requestModel ?? '';
    const model = entry.model ?? '';
    // 两者皆空的占位条目在表格里显示 "-"，它不是一个可筛选的模型。
    if (!requestModel.trim() && !model.trim()) continue;

    const label = modelDisplayName(entry);
    const key = modelFilterKey(label);
    if (!key || seen.has(key)) continue;

    seen.add(key);
    options.push({ key, label, family: deriveModelFamily(requestModel, model) });
  }
  return options;
}

/**
 * 模型筛选的匹配谓词：**any（通道级）语义**——通道只要有任一 layer 命中所选
 * 模型之一，整条通道就保留。
 *
 * ⚠️ 别照搬隔壁 vendor 轴的 all-agree 语义（`deriveChannelVendor` 要求所有 layer
 * 同厂商才认）。那条规则在 vendor 上零成本（生产 0 个通道跨厂商），但生产中 18 个
 * 多 layer 通道**全部是多模型**（saiai 的 `[Opus, Sonnet, Fable 5.1]`、modelflare
 * 的四个 GPT 变体…），要求「所有 layer 都是同一模型」会让它们一条都筛不出来。
 *
 * 未声明任何模型的通道在筛选生效时排除——与 vendor 口径一致，「未知」不属于
 * 任何一个具体选项。
 */
export function matchesModelKeys(
  entries: readonly ModelEntryLike[] | undefined,
  selectedKeys: ReadonlySet<string>,
): boolean {
  if (selectedKeys.size === 0) return true;
  return collectModelOptions(entries).some((option) => selectedKeys.has(option.key));
}
