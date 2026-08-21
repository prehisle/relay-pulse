import type { ModelOption, OnboardingMeta, RequestShapeOption } from '../../types/onboarding';

/**
 * 「选厂商 → 选该厂商的模型」的纯逻辑，供第二步表单与确认页共用。
 *
 * 表单里已经不出现「模板」二字：用户选的是模型，模板是随行提交的实现细节
 * （字段仍叫 testVariant，因为后端的 template_name 就是它）。
 */

/** 「其他（自填模型 ID）」这一选项的 key。真实条目的 key 由后端给出，不会与它冲突。 */
export const CUSTOM_MODEL_KEY = '__custom__';

/** 一次模型选择产生的表单变更。 */
export interface ModelSelection {
  modelKey: string;
  /** 行级模型 ID：仅第一方厂商模型与自填时非空 */
  model: string;
  /** 行级模型厂商 code，可留空 */
  modelVendor: string;
  /** 提交给后端的探针模板名 */
  testVariant: string;
}

/** 取某条 service 的可选模型；service 尚未选或后端没下发时返回空数组。 */
export function modelOptionsFor(meta: OnboardingMeta | null, serviceType: string): ModelOption[] {
  return meta?.models_by_service?.[serviceType] ?? [];
}

/** 取某条 service 的请求形态（native 模板）。为空表示该 service 不支持自填模型。 */
export function requestShapesFor(meta: OnboardingMeta | null, serviceType: string): RequestShapeOption[] {
  return meta?.request_shapes_by_service?.[serviceType] ?? [];
}

/** 按 key 查条目。 */
export function findModelOption(options: ModelOption[], key: string): ModelOption | undefined {
  return options.find((o) => o.key === key);
}

/** 该选择是否需要用户自己给出模型 ID（自填，或第一方厂商的可改条目）。 */
export function needsModelId(options: ModelOption[], modelKey: string): boolean {
  if (modelKey === CUSTOM_MODEL_KEY) return true;
  return findModelOption(options, modelKey)?.editable ?? false;
}

/** 把一个目录条目翻译成表单变更。 */
export function selectionFromOption(option: ModelOption): ModelSelection {
  return {
    modelKey: option.key,
    model: option.model,
    modelVendor: option.vendor,
    testVariant: option.template,
  };
}

/** 「其他（自填）」的初始表单变更：模型 ID 与厂商留空，请求形态取第一个。 */
export function selectionForCustom(shapes: RequestShapeOption[]): ModelSelection {
  return {
    modelKey: CUSTOM_MODEL_KEY,
    model: '',
    modelVendor: '',
    testVariant: shapes[0]?.template ?? '',
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
    ? options.find((o) => o.template === defaultVariant && !o.editable)
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
  if (modelKey === CUSTOM_MODEL_KEY) return requestShapesFor(meta, serviceType).length > 0;
  return findModelOption(modelOptionsFor(meta, serviceType), modelKey) !== undefined;
}

/** 确认页/摘要里展示的模型名：自填与第一方厂商显示用户填的 ID，其余显示目录条目的人话名。 */
export function describeModelSelection(
  meta: OnboardingMeta | null,
  serviceType: string,
  modelKey: string,
  model: string,
): string {
  if (modelKey === CUSTOM_MODEL_KEY) return model;
  const option = findModelOption(modelOptionsFor(meta, serviceType), modelKey);
  if (!option) return model || modelKey;
  if (option.editable) return model ? `${option.label}（${model}）` : option.label;
  return option.label;
}
