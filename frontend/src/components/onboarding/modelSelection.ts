import type { ModelOption, OnboardingMeta } from '../../types/onboarding';

/**
 * 「选厂商 → 选该厂商的模型」的纯逻辑，供第二步表单与确认页共用。
 *
 * 表单里不出现「模板」二字：用户选的是模型，模板是随行提交的实现细节
 * （字段仍叫 testVariant，因为后端的 template_name 就是它）。
 *
 * ⚠️ 用户能选的**只有模型**。模型 ID、模型厂商、请求形态三项都由所选模型的探针模板钉死，
 * 表单不提供任何入口——2026-08-21 之前它们是可填的，于是提交方能造出「模型 ID 是豆包、
 * 厂商标成智谱」这种自相矛盾的上架数据，也能把请求形态选错（症状是 HTTP 200 却恒红
 * content_mismatch）。这三项是探针实现细节，只有我们判得了对错。新模型走 /contact。
 */

/** 一次模型选择产生的表单变更。 */
export interface ModelSelection {
  modelKey: string;
  /** 提交给后端的探针模板名 */
  testVariant: string;
}

/** 取某条 service 的可选模型；service 尚未选或后端没下发时返回空数组。 */
export function modelOptionsFor(meta: OnboardingMeta | null, serviceType: string): ModelOption[] {
  return meta?.models_by_service?.[serviceType] ?? [];
}

/** 按 key 查条目。 */
export function findModelOption(options: ModelOption[], key: string): ModelOption | undefined {
  return options.find((o) => o.key === key);
}

/** 把一个目录条目翻译成表单变更。 */
export function selectionFromOption(option: ModelOption): ModelSelection {
  return {
    modelKey: option.key,
    testVariant: option.template,
  };
}

/**
 * 解析默认选择：优先与该 service 的自助默认模板同源的条目（后端 self_serve_default 声明，
 * 各 service 最便宜、上游覆盖面最广的那个），否则退回目录首项。
 *
 * 返回 null 表示这条 service 没有任何可选模型——那属于部署异常（模板目录空/全被标隐藏），
 * 调用方据此提示而不是硬选一个不存在的。
 */
export function resolveDefaultSelection(
  meta: OnboardingMeta | null,
  serviceType: string,
): ModelSelection | null {
  const options = modelOptionsFor(meta, serviceType);
  if (options.length === 0) return null;

  const defaultVariant = meta?.test_types?.find((t) => t.id === serviceType)?.default_variant;
  const preferred = defaultVariant
    ? options.find((o) => o.template === defaultVariant)
    : undefined;

  return selectionFromOption(preferred ?? options[0]);
}

/**
 * 当前选择在目录里是否仍然成立。service 改了、后端下线了某个模型、或旧草稿里存着已不开放的
 * 选项时都会返回 false，调用方据此回落到默认选择。
 */
export function isSelectionValid(
  meta: OnboardingMeta | null,
  serviceType: string,
  modelKey: string,
): boolean {
  if (!modelKey) return false;
  return findModelOption(modelOptionsFor(meta, serviceType), modelKey) !== undefined;
}

/** 确认页/摘要里展示的模型名。 */
export function describeModelSelection(
  meta: OnboardingMeta | null,
  serviceType: string,
  modelKey: string,
): string {
  const option = findModelOption(modelOptionsFor(meta, serviceType), modelKey);
  return option?.label ?? modelKey;
}
