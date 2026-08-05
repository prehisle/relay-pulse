import i18n from '../i18n';
import type {
  MonitorGroup,
  MonitorLayer,
  MonitorResult,
  ProcessedMonitorData,
  SponsorLevel,
  StatusCounts,
} from '../types';
import { STATUS_MAP } from '../types';

// ─── 字符串规范化 ───────────────────────────────────────────

/** Provider / channel 名称标准化（小写、去空格），用于筛选和去重 */
export function canonicalize(value?: string): string {
  return value?.trim().toLowerCase() ?? '';
}

/** Provider 显示标签格式化（保留原始大小写，首字母大写） */
function formatProviderLabel(value?: string): string {
  const trimmed = value?.trim();
  if (!trimmed) return i18n.t('common.unknownProvider');
  return trimmed.charAt(0).toUpperCase() + trimmed.slice(1);
}

// ─── 模型厂商 ───────────────────────────────────────────────

/** 厂商 code 归一：trim + 转小写。后端 validateModelVendors 已写回规范形式，
 *  这里再归一一次只是让展示层对任何来源（含旧 wire / 手工 mock）都稳定。 */
export function canonicalVendor(value?: string): string {
  return value?.trim().toLowerCase() ?? '';
}

/**
 * 由各 layer 的 model_vendor 推导通道级厂商。
 *
 * 配置侧的「一个通道一个厂商」不变量只校验**非空**值互相一致（回填期必然出现同通道
 * 半填状态，见 spec 决策记录），因此「一行 zhipu、一行空」在后端是合法的。
 * 展示层刻意更严：**所有** layer 都非空且同值才提升为通道级厂商，否则视为未知。
 *
 * 理由：厂商列的全部价值是「别把 GLM 误当成便宜的 Claude」。把半填状态显示成某个
 * 厂商，等于用一半的证据给出十成的确定性——宁可显示「-」也不能显示错的那家。
 */
export function deriveChannelVendor(layers?: Array<{ model_vendor?: string }>): string | undefined {
  if (!layers || layers.length === 0) return undefined;
  const first = canonicalVendor(layers[0].model_vendor);
  if (!first) return undefined;
  return layers.every((layer) => canonicalVendor(layer.model_vendor) === first) ? first : undefined;
}

// ─── URL 校验 ───────────────────────────────────────────────

function validateUrl(url: string | undefined): string | null {
  if (!url || url.trim() === '') return null;
  try {
    new URL(url);
    return url;
  } catch {
    console.warn(`Invalid URL: ${url}`);
    return null;
  }
}

// ─── 赞助等级规范化 ─────────────────────────────────────────

const SPONSOR_LEVELS: readonly SponsorLevel[] = ['public', 'signal', 'pulse', 'beacon', 'backbone', 'core'];

const DEPRECATED_SPONSOR_MAP: Record<string, SponsorLevel> = {
  basic: 'pulse',
  advanced: 'backbone',
  enterprise: 'core',
};

function normalizeSponsorLevel(level?: string): SponsorLevel | undefined {
  if (!level) return undefined;
  const normalized = level.trim().toLowerCase();
  if (SPONSOR_LEVELS.includes(normalized as SponsorLevel)) {
    return normalized as SponsorLevel;
  }
  return DEPRECATED_SPONSOR_MAP[normalized];
}

// ─── 状态计数处理 ───────────────────────────────────────────

/** 映射状态计数，为缺失字段提供默认值以保持向后兼容 */
function mapStatusCounts(counts?: StatusCounts): StatusCounts {
  return {
    available: counts?.available ?? 0,
    degraded: counts?.degraded ?? 0,
    unavailable: counts?.unavailable ?? 0,
    missing: counts?.missing ?? 0,
    slow_latency: counts?.slow_latency ?? 0,
    rate_limit: counts?.rate_limit ?? 0,
    server_error: counts?.server_error ?? 0,
    client_error: counts?.client_error ?? 0,
    auth_error: counts?.auth_error ?? 0,
    invalid_request: counts?.invalid_request ?? 0,
    network_error: counts?.network_error ?? 0,
    response_timeout: counts?.response_timeout ?? 0,
    content_mismatch: counts?.content_mismatch ?? 0,
    http_code_breakdown: counts?.http_code_breakdown,
  };
}

// ─── 状态严重程度 ───────────────────────────────────────────

/** 状态严重程度：0（红）> 2（黄）> 1（绿）> 3/-1（灰/缺失） */
function statusSeverity(status: number): number {
  switch (status) {
    case 0: return 3;
    case 2: return 2;
    case 1: return 1;
    default: return 0;
  }
}

function pickWorstStatus(a: number, b: number): number {
  return statusSeverity(b) > statusSeverity(a) ? b : a;
}

/** 合并多个时间点的状态计数 */
function mergeStatusCounts(points: Array<{ status_counts?: StatusCounts }>): StatusCounts {
  const merged: StatusCounts = {
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

  points.forEach((p) => {
    const counts = p.status_counts;
    if (!counts) return;
    merged.available += counts.available ?? 0;
    merged.degraded += counts.degraded ?? 0;
    merged.unavailable += counts.unavailable ?? 0;
    merged.missing += counts.missing ?? 0;
    merged.slow_latency += counts.slow_latency ?? 0;
    merged.rate_limit += counts.rate_limit ?? 0;
    merged.server_error += counts.server_error ?? 0;
    merged.client_error += counts.client_error ?? 0;
    merged.auth_error += counts.auth_error ?? 0;
    merged.invalid_request += counts.invalid_request ?? 0;
    merged.network_error += counts.network_error ?? 0;
    merged.response_timeout += counts.response_timeout ?? 0;
    merged.content_mismatch += counts.content_mismatch ?? 0;

    if (counts.http_code_breakdown) {
      if (!merged.http_code_breakdown) {
        merged.http_code_breakdown = {};
      }
      Object.entries(counts.http_code_breakdown).forEach(([subStatus, codes]) => {
        if (!merged.http_code_breakdown![subStatus]) {
          merged.http_code_breakdown![subStatus] = {};
        }
        Object.entries(codes).forEach(([code, count]) => {
          const codeNum = parseInt(code, 10);
          merged.http_code_breakdown![subStatus][codeNum] =
            (merged.http_code_breakdown![subStatus][codeNum] ?? 0) + count;
        });
      });
    }
  });

  return merged;
}

// ─── 时间轴处理 ─────────────────────────────────────────────

type LayerTimelinePoint = NonNullable<MonitorLayer['timeline']>[number];

/** 估算时间轴相邻点间隔（秒），用于推导时间戳对齐容差 */
function estimateTimelineStepSeconds(timeline: Array<{ timestamp: number }>): number {
  const deltas: number[] = [];
  for (let i = 1; i < timeline.length; i += 1) {
    const delta = Math.abs(timeline[i].timestamp - timeline[i - 1].timestamp);
    if (delta > 0) deltas.push(delta);
    if (deltas.length >= 10) break;
  }
  if (deltas.length === 0) return 60;
  deltas.sort((a, b) => a - b);
  return deltas[Math.floor(deltas.length / 2)];
}

/** 二分查找：在已排序 timeline 中查找与目标时间戳最接近且在容差内的点 */
function findClosestPointByTimestamp(
  sortedTimeline: LayerTimelinePoint[],
  targetTimestamp: number,
  toleranceSeconds: number
): LayerTimelinePoint | undefined {
  if (sortedTimeline.length === 0) return undefined;

  let left = 0;
  let right = sortedTimeline.length;
  while (left < right) {
    const mid = (left + right) >> 1;
    if (sortedTimeline[mid].timestamp < targetTimestamp) {
      left = mid + 1;
    } else {
      right = mid;
    }
  }

  const candidates: LayerTimelinePoint[] = [];
  if (left < sortedTimeline.length) candidates.push(sortedTimeline[left]);
  if (left - 1 >= 0) candidates.push(sortedTimeline[left - 1]);

  let best: LayerTimelinePoint | undefined;
  let bestDiff = Number.POSITIVE_INFINITY;
  candidates.forEach((p) => {
    const diff = Math.abs(p.timestamp - targetTimestamp);
    if (diff < bestDiff) {
      bestDiff = diff;
      best = p;
    }
  });

  return best && bestDiff <= toleranceSeconds ? best : undefined;
}

/**
 * 从多个层构建综合时间轴（每个时间点取最差状态）。
 * 使用时间戳对齐而非索引对齐，解决多模型组 timeline 长度不一致问题。
 */
function buildCompositeTimelineFromLayers(
  layers: MonitorLayer[]
): Array<{
  status: number;
  time: string;
  timestamp: number;
  latency: number;
  availability: number;
  status_counts: StatusCounts;
}> {
  if (layers.length === 0) return [];

  const baseLayer = layers.find((layer) => layer.layer_order === 0) ?? layers[0];
  const baseTimeline = baseLayer.timeline ?? [];
  if (baseTimeline.length === 0) return [];

  const baseStepSeconds = estimateTimelineStepSeconds(baseTimeline);
  const toleranceSeconds = Math.max(10, Math.min(120, Math.floor(baseStepSeconds / 2)));

  const sortedLayerTimelines = layers.map((layer) => {
    const timeline = layer.timeline ?? [];
    let isSorted = true;
    for (let i = 1; i < timeline.length && isSorted; i++) {
      if (timeline[i].timestamp < timeline[i - 1].timestamp) {
        isSorted = false;
      }
    }
    return {
      layer_order: layer.layer_order,
      timeline: isSorted ? timeline : timeline.slice().sort((a, b) => a.timestamp - b.timestamp),
    };
  });

  return baseTimeline.map((basePoint) => {
    const targetTimestamp = basePoint.timestamp;

    const points = sortedLayerTimelines
      .map(({ timeline }) => findClosestPointByTimestamp(timeline, targetTimestamp, toleranceSeconds))
      .filter((p): p is LayerTimelinePoint => Boolean(p));

    let worstStatus = -1;
    points.forEach((p) => {
      worstStatus = pickWorstStatus(worstStatus, p.status);
    });

    const availabilities = points.map((p) => p.availability).filter((a) => a >= 0);
    const availability = availabilities.length > 0 ? Math.min(...availabilities) : -1;

    const latencies = points.map((p) => p.latency).filter((l) => l > 0);
    const latency = latencies.length > 0 ? Math.max(...latencies) : 0;

    const status_counts = mergeStatusCounts(points);

    return {
      status: worstStatus,
      time: basePoint.time,
      timestamp: basePoint.timestamp,
      latency,
      availability,
      status_counts,
    };
  });
}

// ─── 可用率 ─────────────────────────────────────────────────

/** 计算可用率：仅统计有数据的时间块（availability >= 0） */
export function calculateUptime(points: Array<{ availability: number }>): number {
  const validPoints = points.filter((point) => point.availability >= 0);
  if (validPoints.length === 0) return -1;
  return parseFloat((
    validPoints.reduce((acc, point) => acc + point.availability, 0) / validPoints.length
  ).toFixed(2));
}

// ─── history 构建 ───────────────────────────────────────────

function buildHistoryFromTimeline(
  timeline: Array<{
    status: number;
    time: string;
    timestamp: number;
    latency: number;
    availability: number;
    status_counts?: StatusCounts;
  }>,
  slowLatencyMs: number,
  model?: string,
  requestModel?: string,
  layerOrder?: number
): ProcessedMonitorData['history'] {
  return (timeline || []).map((point, index) => ({
    index,
    status: STATUS_MAP[point.status] || 'UNAVAILABLE',
    timestamp: point.time,
    timestampNum: point.timestamp,
    latency: point.latency,
    availability: point.availability,
    statusCounts: mapStatusCounts(point.status_counts),
    slowLatencyMs,
    model,
    requestModel,
    layerOrder,
  }));
}

// ─── 层选择 ─────────────────────────────────────────────────

/** 选择最后检查时所用的层（优先父层 layer_order=0，否则取时间戳最新的层） */
function pickLastCheckLayer(layers: MonitorLayer[]): MonitorLayer | undefined {
  if (layers.length === 0) return undefined;
  const parent = layers.find((layer) => layer.layer_order === 0);
  if (parent) return parent;
  return layers.reduce((latest, layer) => {
    const latestTs = latest.current_status?.timestamp ?? 0;
    const currentTs = layer.current_status?.timestamp ?? 0;
    return currentTs > latestTs ? layer : latest;
  }, layers[0]);
}

// ─── 数据转换（对外导出） ───────────────────────────────────

/** 将 legacy MonitorResult 转换为 ProcessedMonitorData（单层） */
export function convertLegacyDataToProcessedData(
  item: MonitorResult,
  globalSlowLatencyMs: number
): ProcessedMonitorData {
  const itemSlowLatencyMs = item.slow_latency_ms ?? globalSlowLatencyMs;

  const legacyModel = item.model?.trim() || item.request_model?.trim() || undefined;
  const legacyRequestModel = item.request_model?.trim() || legacyModel;
  const modelEntries = legacyModel
    ? [{ model: legacyModel, requestModel: legacyRequestModel ?? legacyModel }]
    : undefined;

  const history = buildHistoryFromTimeline(
    item.timeline, itemSlowLatencyMs, legacyModel, legacyRequestModel
  );
  const uptime = calculateUptime(history);

  const currentStatus = item.current_status
    ? STATUS_MAP[item.current_status.status] || 'UNAVAILABLE'
    : 'MISSING';

  const providerKey = canonicalize(item.provider);
  const providerLabel = item.provider_name || formatProviderLabel(item.provider);
  const serviceName = item.service_name || item.service;
  const channelName = item.channel_name || item.channel;

  return {
    id: `${providerKey || item.provider}-${item.service}-${item.channel || 'default'}`,
    providerId: providerKey || item.provider,
    providerSlug: item.provider_slug || canonicalize(item.provider),
    providerName: providerLabel,
    providerUrl: validateUrl(item.provider_url),
    serviceType: item.service,
    serviceName,
    category: item.category,
    sponsor: item.sponsor,
    sponsorUrl: validateUrl(item.sponsor_url),
    sponsorLevel: normalizeSponsorLevel(item.sponsor_level),
    annotations: item.annotations,
    priceMin: item.price_min ?? null,
    priceMax: item.price_max ?? null,
    listedDays: item.listed_days ?? null,
    channel: item.channel || undefined,
    channelName: channelName || undefined,
    channelId: item.channel_id || undefined,
    modelVendor: canonicalVendor(item.model_vendor) || undefined,
    board: item.board || 'hot',
    coldReason: item.cold_reason || undefined,
    boardReason: item.board_reason || undefined,
    boardReasonModels: item.board_reason_models || undefined,
    probeUrl: item.probe_url,
    templateName: item.template_name,
    intervalMs: item.interval_ms ?? 0,
    slowLatencyMs: itemSlowLatencyMs,
    modelEntries,
    history,
    currentStatus,
    uptime,
    lastCheckTimestamp: item.current_status?.timestamp,
    lastCheckLatency: item.current_status?.latency,
    isMultiModel: false,
  };
}

/** 将 MonitorGroup 转换为 ProcessedMonitorData（多层，多模型） */
export function convertGroupToProcessedData(
  group: MonitorGroup,
  globalSlowLatencyMs: number
): ProcessedMonitorData {
  const itemSlowLatencyMs = group.slow_latency_ms ?? globalSlowLatencyMs;

  // 构建模型映射
  const modelEntries = group.layers
    .map((layer) => {
      const model = layer.model?.trim() || '';
      const requestModel = layer.request_model?.trim() || model;
      return (model || requestModel) ? { model, requestModel } : null;
    })
    .filter((entry): entry is { model: string; requestModel: string } => entry !== null);

  // composite history 的模型标识：单层直接用该层，多层用 "/" 拼接
  const compositeModel = modelEntries.length <= 1
    ? modelEntries[0]?.model
    : modelEntries.map((e) => e.model).join(' / ');
  const compositeRequestModel = modelEntries.length <= 1
    ? modelEntries[0]?.requestModel
    : modelEntries.map((e) => e.requestModel).join(' / ');

  const compositeTimeline = buildCompositeTimelineFromLayers(group.layers);
  const history = compositeTimeline.length > 0
    ? buildHistoryFromTimeline(
        compositeTimeline,
        itemSlowLatencyMs,
        compositeModel,
        compositeRequestModel,
        undefined
      )
    : [];

  const layerUptimes = group.layers.map((layer) =>
    calculateUptime(buildHistoryFromTimeline(layer.timeline, itemSlowLatencyMs))
  ).filter((u) => u >= 0);
  const uptime = layerUptimes.length > 0 ? Math.min(...layerUptimes) : -1;

  const currentStatus = STATUS_MAP[group.current_status] || 'MISSING';

  const lastCheckLayer = pickLastCheckLayer(group.layers);
  const lastCheckTimestamp = lastCheckLayer?.current_status?.timestamp;
  const lastCheckLatency = lastCheckLayer?.current_status?.latency;

  const providerKey = canonicalize(group.provider);
  const providerLabel = group.provider_name || formatProviderLabel(group.provider);
  const serviceName = group.service_name || group.service;
  const channelName = group.channel_name || group.channel;

  return {
    id: `${providerKey || group.provider}-${group.service}-${group.channel || 'default'}`,
    providerId: providerKey || group.provider,
    providerSlug: group.provider_slug || canonicalize(group.provider),
    providerName: providerLabel,
    providerUrl: validateUrl(group.provider_url),
    serviceType: group.service,
    serviceName,
    category: group.category,
    sponsor: group.sponsor,
    sponsorUrl: validateUrl(group.sponsor_url),
    sponsorLevel: normalizeSponsorLevel(group.sponsor_level),
    annotations: group.annotations,
    priceMin: group.price_min ?? null,
    priceMax: group.price_max ?? null,
    listedDays: group.listed_days ?? null,
    channel: group.channel || undefined,
    channelName: channelName || undefined,
    channelId: group.channel_id || undefined,
    modelVendor: deriveChannelVendor(group.layers),
    board: group.board || 'hot',
    coldReason: group.cold_reason || undefined,
    boardReason: group.board_reason || undefined,
    boardReasonModels: group.board_reason_models || undefined,
    probeUrl: group.probe_url,
    templateName: group.template_name,
    intervalMs: group.interval_ms ?? 0,
    slowLatencyMs: itemSlowLatencyMs,
    modelEntries: modelEntries.length > 0 ? modelEntries : undefined,
    history,
    currentStatus,
    uptime,
    lastCheckTimestamp,
    lastCheckLatency,
    isMultiModel: group.layers.length > 1,
    layers: group.layers,
  };
}
