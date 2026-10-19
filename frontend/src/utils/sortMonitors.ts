import type { ProcessedMonitorData, SortConfig, StatusKey, SponsorPinConfig } from '../types';

/** 状态排序权重（仅用于排序比较，不含样式或 i18n） */
const STATUS_WEIGHT: Record<string, number> = {
  AVAILABLE: 3,
  DEGRADED: 2,
  UNAVAILABLE: 1,
  MISSING: 0,
};
import { SPONSOR_WEIGHTS } from './annotationUtils';

/**
 * 对监控数据进行排序
 *
 * 排序规则：
 * 1. 按主排序字段排序（支持 asc/desc）
 * 2. 特殊字段处理：
 *    - currentStatus: 按状态权重排序
 *    - uptime: uptime < 0 视为无数据，始终排最后
 *    - latency: 不可用状态的延迟不参与排序，排最后（无二级排序）
 * 3. 二级排序：主字段相等时，按 lastCheckLatency 升序（延迟主排序除外）
 *
 * @param data 监控数据数组
 * @param sortConfig 排序配置
 * @returns 排序后的新数组（不修改原数组）
 */
export function sortMonitors(
  data: ProcessedMonitorData[],
  sortConfig: SortConfig
): ProcessedMonitorData[] {
  if (!sortConfig.key) {
    return [...data];
  }

  return [...data].sort((a, b) => {
    const comparison = comparePrimary(a, b, sortConfig);
    if (comparison !== 0) {
      return comparison;
    }
    // 延迟/最后监测主排序时不使用二级排序（内部已包含完整排序逻辑）
    if (sortConfig.key === 'latency' || sortConfig.key === 'lastCheck') {
      return 0;
    }
    // 其他字段：二级排序按延迟升序
    return compareLatency(a.lastCheckLatency, b.lastCheckLatency);
  });
}

/**
 * 主排序比较函数
 */
function comparePrimary(
  a: ProcessedMonitorData,
  b: ProcessedMonitorData,
  sortConfig: SortConfig
): number {
  const { key, direction } = sortConfig;

  let aValue: number | string;
  let bValue: number | string;

  // 特殊字段处理
  if (key === 'currentStatus') {
    aValue = STATUS_WEIGHT[a.currentStatus as StatusKey] ?? 0;
    bValue = STATUS_WEIGHT[b.currentStatus as StatusKey] ?? 0;
  } else if (key === 'uptime') {
    return compareUptime(a.uptime, b.uptime, direction);
  } else if (key === 'priceRatio') {
    return comparePriceRatio(a.priceMin, a.priceMax, b.priceMin, b.priceMax, direction);
  } else if (key === 'qualityScore') {
    return compareQualityScore(a.qualityScore, b.qualityScore, direction);
  } else if (key === 'listedDays') {
    return compareListedDays(a.listedDays, b.listedDays, direction);
  } else if (key === 'lastCheck') {
    return compareLastCheck(a, b, direction);
  } else if (key === 'latency') {
    return compareLatencyPrimary(a, b, direction);
  } else {
    aValue = a[key as keyof ProcessedMonitorData] as number | string;
    bValue = b[key as keyof ProcessedMonitorData] as number | string;
  }

  if (aValue < bValue) return direction === 'asc' ? -1 : 1;
  if (aValue > bValue) return direction === 'asc' ? 1 : -1;
  return 0;
}

/**
 * uptime 特殊排序：uptime < 0 视为无数据，始终排最后
 */
function compareUptime(
  aUptime: number,
  bUptime: number,
  direction: 'asc' | 'desc'
): number {
  const aHasData = aUptime >= 0;
  const bHasData = bUptime >= 0;

  // 无数据的始终排最后
  if (aHasData && !bHasData) return -1;
  if (!aHasData && bHasData) return 1;
  if (!aHasData && !bHasData) return 0;

  // 两者都有数据，正常比较
  if (aUptime < bUptime) return direction === 'asc' ? -1 : 1;
  if (aUptime > bUptime) return direction === 'asc' ? 1 : -1;
  return 0;
}

/**
 * 计算价格排序用的代表值（上限优先）
 * 用户心理：关心"最多付多少"，按上限排序更保护用户
 */
function getPriceValue(
  priceMin: number | null | undefined,
  priceMax: number | null | undefined
): number | null {
  // 优先使用上限（用户最坏情况）
  if (priceMax != null) return priceMax;
  if (priceMin != null) return priceMin;
  return null;
}

/**
 * priceRatio 特殊排序：null 值始终排最后，按上限比较
 */
function comparePriceRatio(
  aMin: number | null | undefined,
  aMax: number | null | undefined,
  bMin: number | null | undefined,
  bMax: number | null | undefined,
  direction: 'asc' | 'desc'
): number {
  const aValue = getPriceValue(aMin, aMax);
  const bValue = getPriceValue(bMin, bMax);

  const aHasData = aValue != null;
  const bHasData = bValue != null;

  // null 值始终排最后
  if (aHasData && !bHasData) return -1;
  if (!aHasData && bHasData) return 1;
  if (!aHasData && !bHasData) return 0;

  // 两者都有数据，正常比较
  if (aValue! < bValue!) return direction === 'asc' ? -1 : 1;
  if (aValue! > bValue!) return direction === 'asc' ? 1 : -1;
  return 0;
}

/**
 * qualityScore 特殊排序：rpdiag 未覆盖（null/undefined）的项始终排最后，
 * 与 priceRatio/listedDays 的 null-sink 范式一致。
 */
function compareQualityScore(
  aScore: number | null | undefined,
  bScore: number | null | undefined,
  direction: 'asc' | 'desc'
): number {
  const aHasData = aScore != null;
  const bHasData = bScore != null;

  if (aHasData && !bHasData) return -1;
  if (!aHasData && bHasData) return 1;
  if (!aHasData && !bHasData) return 0;

  if (aScore! < bScore!) return direction === 'asc' ? -1 : 1;
  if (aScore! > bScore!) return direction === 'asc' ? 1 : -1;
  return 0;
}

/**
 * listedDays 特殊排序：null 值始终排最后
 */
function compareListedDays(
  aDays: number | null | undefined,
  bDays: number | null | undefined,
  direction: 'asc' | 'desc'
): number {
  const aHasData = aDays != null;
  const bHasData = bDays != null;

  // null 值始终排最后
  if (aHasData && !bHasData) return -1;
  if (!aHasData && bHasData) return 1;
  if (!aHasData && !bHasData) return 0;

  // 两者都有数据，正常比较
  if (aDays! < bDays!) return direction === 'asc' ? -1 : 1;
  if (aDays! > bDays!) return direction === 'asc' ? 1 : -1;
  return 0;
}

/**
 * 延迟二级排序：升序排列，undefined 排最后
 */
function compareLatency(
  aLatency: number | undefined,
  bLatency: number | undefined
): number {
  // 两者都无数据，保持原顺序
  if (aLatency === undefined && bLatency === undefined) return 0;
  // 无数据的排最后
  if (aLatency === undefined) return 1;
  if (bLatency === undefined) return -1;
  // 按延迟升序（低延迟优先）
  return aLatency - bLatency;
}

/**
 * 最后监测组合排序：状态优先，状态相同时按延迟升序
 *
 * 规则：
 * 1. MISSING 状态始终排最后（不受 asc/desc 影响）
 * 2. 其他状态按 STATUS_WEIGHT 排序（受 direction 影响）
 * 3. 状态相同时，按延迟升序（低延迟优先，undefined 排最后）
 */
function compareLastCheck(
  a: ProcessedMonitorData,
  b: ProcessedMonitorData,
  direction: 'asc' | 'desc'
): number {
  const aWeight = STATUS_WEIGHT[a.currentStatus as StatusKey] ?? 0;
  const bWeight = STATUS_WEIGHT[b.currentStatus as StatusKey] ?? 0;

  // MISSING 始终排最后
  if (aWeight === 0 && bWeight !== 0) return 1;
  if (aWeight !== 0 && bWeight === 0) return -1;

  // 按状态权重排序
  if (aWeight !== bWeight) {
    return direction === 'asc' ? aWeight - bWeight : bWeight - aWeight;
  }

  // 状态相同：按延迟升序
  return compareLatency(a.lastCheckLatency, b.lastCheckLatency);
}

/**
 * 延迟主排序：不可用状态的延迟不参与排序，排最后
 *
 * 优先级（升序时）：
 * 1. 可用状态 + 有延迟值 → 按延迟排序
 * 2. 可用状态 + 无延迟值 → 排在有延迟的后面
 * 3. UNAVAILABLE 状态 → 始终排最后（无论延迟值）
 */
function compareLatencyPrimary(
  a: ProcessedMonitorData,
  b: ProcessedMonitorData,
  direction: 'asc' | 'desc'
): number {
  const aIsUnavailable = a.currentStatus === 'UNAVAILABLE';
  const bIsUnavailable = b.currentStatus === 'UNAVAILABLE';

  // UNAVAILABLE 状态始终排最后（优先级最低）
  if (aIsUnavailable && !bIsUnavailable) return 1;
  if (!aIsUnavailable && bIsUnavailable) return -1;
  if (aIsUnavailable && bIsUnavailable) return 0; // 两者都是 UNAVAILABLE，保持原顺序

  // 此时两者都是可用状态，判断是否有延迟值
  const aHasLatency = a.lastCheckLatency !== undefined;
  const bHasLatency = b.lastCheckLatency !== undefined;

  // 无延迟值排在有延迟值的后面
  if (aHasLatency && !bHasLatency) return -1;
  if (!aHasLatency && bHasLatency) return 1;
  if (!aHasLatency && !bHasLatency) return 0; // 两者都无延迟，保持原顺序

  // 两者都有延迟值，按延迟比较
  if (a.lastCheckLatency! < b.lastCheckLatency!) return direction === 'asc' ? -1 : 1;
  if (a.lastCheckLatency! > b.lastCheckLatency!) return direction === 'asc' ? 1 : -1;
  return 0;
}

/**
 * 判断监控项是否满足置顶条件
 */
function meetsPinCriteria(
  item: ProcessedMonitorData,
  config: SponsorPinConfig
): boolean {
  // 必须有赞助级别
  if (!item.sponsorLevel) return false;

  // 有负向注解的不参与置顶
  if (item.annotations?.some(a => a.family === 'negative')) return false;

  // 可用率必须达标（-1 表示无数据，不符合条件）
  if (item.uptime < 0 || item.uptime < config.min_uptime) return false;

  // 赞助级别必须达到最低要求
  const itemWeight = SPONSOR_WEIGHTS[item.sponsorLevel] || 0;
  const minWeight = SPONSOR_WEIGHTS[config.min_level] || 0;
  return itemWeight >= minWeight;
}

/**
 * 带置顶逻辑的排序函数
 *
 * 在页面初始加载时，将符合条件的赞助通道置顶显示。
 * 用户点击任意排序按钮后，置顶失效，恢复正常排序。
 *
 * 置顶规则（按通道级赞助等级）：
 * - 筛选满足条件的通道 → 按等级/可用率/延迟排序 → 取 max_pinned 个
 *
 * @param data 监控数据数组
 * @param sortConfig 用户排序配置
 * @param pinConfig 置顶配置（来自 API）
 * @param enablePinning 是否启用置顶（初始状态才启用）
 * @returns 排序后的数据，置顶项带 pinned: true 标记
 */
export function sortMonitorsWithPinning(
  data: ProcessedMonitorData[],
  sortConfig: SortConfig,
  pinConfig: SponsorPinConfig | null,
  enablePinning: boolean
): ProcessedMonitorData[] {
  // 复制数据避免修改原数组
  const items = [...data];

  // 置顶逻辑：配置存在、功能启用、且处于初始排序状态
  const shouldPin = pinConfig?.enabled && enablePinning && pinConfig.max_pinned > 0;

  if (!shouldPin) {
    // 不启用置顶：使用常规排序，清除所有 pinned 标记
    return sortMonitors(items, sortConfig).map(item => ({
      ...item,
      pinned: false,
    }));
  }

  // 1. 筛选符合置顶条件的项
  const pinnedCandidates = items.filter(item => meetsPinCriteria(item, pinConfig));

  // 2. 候选项全局排序：赞助级别 > 可用率 > 延迟
  pinnedCandidates.sort((a, b) => {
    const aWeight = SPONSOR_WEIGHTS[a.sponsorLevel!] || 0;
    const bWeight = SPONSOR_WEIGHTS[b.sponsorLevel!] || 0;
    if (aWeight !== bWeight) return bWeight - aWeight;
    // 同级别按可用率降序
    const uptimeDiff = b.uptime - a.uptime;
    if (uptimeDiff !== 0) return uptimeDiff;
    // 同级别 + 同可用率：按延迟升序（低延迟优先）
    return compareLatency(a.lastCheckLatency, b.lastCheckLatency);
  });

  // 3. 同一服务商相同服务只置顶一个通道（保留排名最高的）
  const seenProviderService = new Set<string>();
  const dedupedCandidates = pinnedCandidates.filter(item => {
    const key = `${item.providerId}|${item.serviceType}`;
    if (seenProviderService.has(key)) return false;
    seenProviderService.add(key);
    return true;
  });

  // 4. 按 max_pinned 截断
  const pinnedItems = dedupedCandidates.slice(0, pinConfig.max_pinned);

  const pinnedIds = new Set(pinnedItems.map(item => item.id));

  // 5. 其余项按可用率降序排序
  const remainingItems = items.filter(item => !pinnedIds.has(item.id));
  const sortedRemaining = sortMonitors(remainingItems, { key: 'uptime', direction: 'desc' });

  // 6. 合并结果，标记置顶项
  return [
    ...pinnedItems.map(item => ({ ...item, pinned: true })),
    ...sortedRemaining.map(item => ({ ...item, pinned: false })),
  ];
}
