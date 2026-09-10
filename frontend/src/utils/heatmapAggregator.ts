import type { ProcessedMonitorData } from '../types';
import { BREAKPOINTS, addMediaQueryListener } from './mediaQuery';
import { weightedAvailability } from './monitorDataProcessor';

type HistoryPoint = ProcessedMonitorData['history'][number];

// 聚合数据点的扩展字段类型
interface AggregatedExtension {
  _aggregated?: boolean;
  _originalPoints?: HistoryPoint[];
  _minAvailability?: number;
  _maxAvailability?: number;
}

// 使用 matchMedia 缓存平板/移动端状态，避免在每次渲染时重复计算
let cachedIsTablet: boolean | null = null;
let mediaQueryList: MediaQueryList | null = null;
let cleanupListener: (() => void) | null = null;

function getIsTablet(): boolean {
  if (typeof window === 'undefined') {
    return false;
  }

  // 初始化 matchMedia 监听（只初始化一次）
  if (mediaQueryList === null) {
    mediaQueryList = window.matchMedia(BREAKPOINTS.tablet);
    cachedIsTablet = mediaQueryList.matches;

    // 监听断点变化，更新缓存（兼容 Safari ≤13）
    cleanupListener = addMediaQueryListener(mediaQueryList, (e) => {
      cachedIsTablet = e.matches;
    });
  }

  return cachedIsTablet ?? false;
}

// 清理函数，用于 HMR 或测试环境
export function resetMediaQueryCache(): void {
  if (cleanupListener) {
    cleanupListener();
    cleanupListener = null;
  }
  mediaQueryList = null;
  cachedIsTablet = null;
}

// 在 HMR 环境下自动清理
if (import.meta.hot) {
  import.meta.hot.dispose(() => {
    resetMediaQueryCache();
  });
}

/**
 * 判断是否为原始记录模式（90 分钟窗口）
 * 通过检测时间戳范围来判断，避免依赖外部传参
 */
function isOneHourRange(points: HistoryPoint[]): boolean {
  if (points.length < 2) {
    return false;
  }

  const timestamps = points
    .map(point => point.timestampNum)
    .filter(ts => Number.isFinite(ts));

  if (timestamps.length < 2) {
    return false;
  }

  const minTs = Math.min(...timestamps);
  const maxTs = Math.max(...timestamps);
  // 时间跨度 <= 90 分钟 (5400 秒) 视为 90m 模式
  return (maxTs - minTs) <= 5400;
}

/**
 * 聚合热力图数据点，用于移动端显示
 *
 * @param points 原始历史数据点
 * @param maxBlocks 最大显示块数（默认 50）
 * @returns 聚合后的数据点
 */
export function aggregateHeatmap(
  points: HistoryPoint[],
  maxBlocks = 50
): HistoryPoint[] {
  // 90m 模式不聚合（桌面/移动端一致展示原始数据）
  if (isOneHourRange(points)) {
    return points;
  }

  // 桌面端不聚合（平板/移动端才聚合，使用缓存的 matchMedia 结果）
  if (!getIsTablet()) {
    return points;
  }

  // 如果原始点数少于最大块数，不需要聚合
  if (points.length <= maxBlocks) {
    return points;
  }

  // 计算聚合因子（每 N 个点合并为 1 个）
  const N = Math.ceil(points.length / maxBlocks);
  const aggregated: HistoryPoint[] = [];

  for (let i = 0; i < points.length; i += N) {
    const group = points.slice(i, i + N);

    // 计算该组的聚合数据
    const aggregatedPoint = aggregateGroup(group);
    aggregated.push(aggregatedPoint);
  }

  return aggregated;
}

/**
 * 聚合一组数据点
 * - 使用最严重的状态（max severity）作为颜色
 * - 保留 min/max/avg 用于后续展示
 */
function aggregateGroup(group: HistoryPoint[]): HistoryPoint {
  if (group.length === 0) {
    throw new Error('Cannot aggregate empty group');
  }

  if (group.length === 1) {
    return group[0];
  }

  // 计算可用率的 min/max（展示极值）与加权均值
  const availabilities = group
    .map(p => p.availability)
    .filter(a => a >= 0); // 过滤掉无数据的点 (-1)

  // 与 calculateUptime 同口径：按各点探测次数加权，而非各点等权平均——
  // 否则聚合块的 tooltip 数字会和同一行的可用率列对不上。
  const avgAvailability = weightedAvailability(group);

  const minAvailability = availabilities.length > 0
    ? Math.min(...availabilities)
    : -1;

  const maxAvailability = availabilities.length > 0
    ? Math.max(...availabilities)
    : -1;

  // 找到最严重的状态（优先级：0 红色 > 2 黄色 > 1 绿色 > -1 灰色）
  const statusPriority = { 0: 4, 2: 3, 1: 2, '-1': 1 };
  const mostSevereStatus = group.reduce((worst, point) => {
    const currentPriority = statusPriority[String(point.status) as keyof typeof statusPriority] || 0;
    const worstPriority = statusPriority[String(worst) as keyof typeof statusPriority] || 0;
    return currentPriority > worstPriority ? point.status : worst;
  }, group[0].status);

  // 合并所有状态计数（初始化默认值，确保字段完整）
  const defaultStatusCounts: NonNullable<HistoryPoint['statusCounts']> = {
    available: 0,
    degraded: 0,
    unavailable: 0,
    missing: 0,
    slow_latency: 0,
    rate_limit: 0,
    server_error: 0,
    client_error: 0,
    auth_error: 0,
    invalid_request: 0,
    network_error: 0,
    response_timeout: 0,
    content_mismatch: 0,
  };

  const mergedStatusCounts = group.reduce((acc, point) => {
    if (!point.statusCounts) return acc;

    // 数值字段列表
    const numericKeys = [
      'available', 'degraded', 'unavailable', 'missing',
      'slow_latency', 'rate_limit', 'server_error', 'client_error',
      'auth_error', 'invalid_request', 'network_error', 'response_timeout', 'content_mismatch'
    ] as const;

    // 合并数值字段
    numericKeys.forEach(key => {
      const value = point.statusCounts![key];
      if (typeof value === 'number') {
        acc[key] = (acc[key] || 0) + value;
      }
    });

    // 合并 http_code_breakdown（嵌套对象）
    const breakdown = point.statusCounts!.http_code_breakdown;
    if (breakdown) {
      if (!acc.http_code_breakdown) {
        acc.http_code_breakdown = {};
      }
      Object.entries(breakdown).forEach(([subStatus, codes]) => {
        if (!acc.http_code_breakdown![subStatus]) {
          acc.http_code_breakdown![subStatus] = {};
        }
        Object.entries(codes).forEach(([code, count]) => {
          const codeNum = Number(code);
          acc.http_code_breakdown![subStatus][codeNum] =
            (acc.http_code_breakdown![subStatus][codeNum] || 0) + count;
        });
      });
    }

    return acc;
  }, defaultStatusCounts);

  // 使用第一个点的时间戳（代表该组的起始时间）
  const firstPoint = group[0];

  // 计算平均延迟
  const latencies = group.map(p => p.latency).filter(l => l > 0);
  const avgLatency = latencies.length > 0
    ? Math.round(latencies.reduce((sum, l) => sum + l, 0) / latencies.length)
    : 0;

  // 返回聚合后的数据点（扩展字段通过类型断言添加）
  const result: HistoryPoint & AggregatedExtension = {
    index: firstPoint.index,
    status: mostSevereStatus,
    timestamp: firstPoint.timestamp,
    timestampNum: firstPoint.timestampNum,
    latency: avgLatency,
    availability: avgAvailability,
    statusCounts: mergedStatusCounts,
    model: firstPoint.model,
    requestModel: firstPoint.requestModel,
  };

  // 扩展字段：保留原始数据用于详情展示
  result._aggregated = true;
  result._originalPoints = group;
  result._minAvailability = minAvailability;
  result._maxAvailability = maxAvailability;

  return result as HistoryPoint;
}

/**
 * 根据时间范围自动计算聚合因子
 */
export function getAggregationFactor(timeRange: string): number {
  // 桌面端不聚合（平板/移动端才聚合，使用缓存的 matchMedia 结果）
  if (!getIsTablet()) {
    return 1;
  }

  // 根据时间范围返回推荐的聚合因子
  switch (timeRange) {
    case '90m':
      return 1; // 90m 模式不聚合
    case '24h':
      return 2; // 48 点 → 24 块
    case '7d':
      return 4; // 168 点 → 42 块
    case '30d':
      return 12; // 720 点 → 60 块
    default:
      return 1;
  }
}
