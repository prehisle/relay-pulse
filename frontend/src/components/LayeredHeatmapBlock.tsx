import { memo, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import type { MonitorLayer, ProcessedMonitorData } from '../types';
import { STATUS_MAP } from '../types';
import { heatmapBlockToStyle } from '../utils/color';
import { LAYER_GAP_PX, layeredHeatmapHeightPx } from '../utils/layeredHeatmapHeight';

type HeatmapPoint = ProcessedMonitorData['history'][number];
type LayerTimelinePoint = NonNullable<MonitorLayer['timeline']>[number];

// 估算时间轴相邻点间隔（秒），用于推导时间戳对齐容差
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

// 在 timeline 中查找与目标时间戳最接近且在容差内的点
// 返回 undefined 表示没有找到匹配的点（显示为灰色）
// 使用二分查找优化性能：O(log n) 而非 O(n)
function findClosestPointByTimestamp(
  timeline: LayerTimelinePoint[],
  targetTimestamp: number,
  toleranceSeconds: number
): LayerTimelinePoint | undefined {
  if (timeline.length === 0) return undefined;

  // 二分查找：假设 timeline 按 timestamp 升序排列
  let lo = 0;
  let hi = timeline.length - 1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    const ts = timeline[mid].timestamp;
    if (ts === targetTimestamp) return timeline[mid];
    if (ts < targetTimestamp) lo = mid + 1;
    else hi = mid - 1;
  }

  // lo 是第一个 > target 的位置，hi 是最后一个 < target 的位置
  // 只需比较两侧邻居
  let best: LayerTimelinePoint | undefined;
  let bestDiff = Number.POSITIVE_INFINITY;

  const consider = (i: number) => {
    if (i < 0 || i >= timeline.length) return;
    const p = timeline[i];
    const diff = Math.abs(p.timestamp - targetTimestamp);
    if (diff < bestDiff) {
      bestDiff = diff;
      best = p;
    }
  };

  consider(hi);
  consider(lo);

  return bestDiff <= toleranceSeconds ? best : undefined;
}

interface LayeredHeatmapBlockProps {
  /** 所有层的数据 */
  layers: MonitorLayer[];
  /** 时间点索引 */
  timeIndex: number;
  /** 宽度（CSS 字符串） */
  width: string;
  /** 基准总高度（CSS 类名，如 'h-5'），平均分配给所有层；层数多到每层过薄时自动撑高 */
  height?: string;
  /** 悬停回调 */
  onHover: (e: React.MouseEvent<HTMLDivElement>, point: HeatmapPoint) => void;
  /** 离开回调 */
  onLeave: () => void;
  /** 是否为移动端 */
  isMobile?: boolean;
  /** 慢延迟阈值（毫秒） */
  slowLatencyMs: number;
  /** 是否启用延迟渐变着色（仅 90m 时段启用） */
  useLatencyGradient?: boolean;
}

// 默认状态计数（用于缺失数据点）
const defaultStatusCounts: HeatmapPoint['statusCounts'] = {
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

/**
 * 分层热力图块组件（Phase B）
 *
 * 垂直堆叠显示多个 layer 的同一时间点数据
 * - 每层占一个子块，父层在上，子层在下
 * - 总高度取基准高度，N 层时每层均分；层数多到每层低于最小层高时整体撑高（见 layeredHeatmapHeightPx）
 * - 悬停某层时，Tooltip 显示该层的数据
 * - 当某层缺少该时间点数据时，显示灰色（无数据）
 */
export const LayeredHeatmapBlock = memo(function LayeredHeatmapBlock({
  layers,
  timeIndex,
  width,
  height = 'h-5',
  onHover,
  onLeave,
  isMobile = false,
  slowLatencyMs,
  useLatencyGradient = false,
}: LayeredHeatmapBlockProps) {
  const { t } = useTranslation();

  // 按 layer_order 排序（父层在上）
  const sortedLayers = useMemo(
    () => [...layers].sort((a, b) => a.layer_order - b.layer_order),
    [layers]
  );

  // 选择基准层：优先父层（layer_order=0），否则取第一层
  const baseLayer = useMemo(
    () => sortedLayers.find((l) => l.layer_order === 0) ?? sortedLayers[0],
    [sortedLayers]
  );

  // 时间戳对齐容差（秒）：
  // - 90m 模式常见步长约 180s（3 分钟），调度/网络抖动可能到几十秒
  // - 取步长一半作为容差，并钳制在 [10s, 120s]，避免跨桶误匹配
  const toleranceSeconds = useMemo(() => {
    const baseTimeline = baseLayer?.timeline ?? [];
    const stepSeconds = estimateTimelineStepSeconds(baseTimeline);
    return Math.max(10, Math.min(120, Math.floor(stepSeconds / 2)));
  }, [baseLayer]);

  // 参考时间点：当某层缺失该 index 时，用其他层的时间信息填充
  // 注意：必须在条件返回前调用所有 Hooks
  const referenceTimePoint = useMemo(() => {
    for (const l of sortedLayers) {
      const p = l.timeline?.[timeIndex];
      if (p) return p;
    }
    return undefined;
  }, [sortedLayers, timeIndex]);

  // 边界情况：空数组时不渲染
  if (sortedLayers.length === 0) {
    return null;
  }

  // 提取高度数值（如 'h-5' → 5 → 1.25rem → 20px）
  // Tailwind h-5 = 1.25rem = 20px，h-8 = 2rem = 32px，h-10 = 2.5rem = 40px
  const heightMap: Record<string, number> = {
    'h-full': 20, // 默认 20px
    'h-5': 20,
    'h-8': 32,
    'h-10': 40,
  };
  const totalHeightPx = layeredHeatmapHeightPx(sortedLayers.length, heightMap[height] || 20);
  const gapPx = LAYER_GAP_PX;
  const totalGapPx = (sortedLayers.length - 1) * gapPx;
  const layerHeightPx = (totalHeightPx - totalGapPx) / sortedLayers.length;

  // 将时间点数据转换为 HeatmapPoint 格式
  // 当该层缺少数据时，返回占位点（availability=-1 -> 灰色）而不是 null
  // 使用时间戳对齐而非索引访问，解决多模型组因探测时间不同导致的 timeline 长度不一致问题
  const convertToHeatmapPoint = (layer: MonitorLayer, index: number): HeatmapPoint => {
    // 获取目标时间戳：从参考时间点获取，用于在其他层中按时间戳查找
    const targetTimestamp = referenceTimePoint?.timestamp;

    // 使用时间戳对齐查找：优先按时间戳匹配，回退到索引访问
    const ownTimePoint = targetTimestamp != null
      ? findClosestPointByTimestamp(layer.timeline ?? [], targetTimestamp, toleranceSeconds)
      : layer.timeline?.[index];

    const timePointForTimestamp = ownTimePoint ?? referenceTimePoint;

    const layerRequestModel = layer.request_model ?? layer.model;

    // 极端兜底：所有层都没有该 index（理论上不该发生）
    if (!timePointForTimestamp) {
      return {
        index,
        status: 'MISSING',
        timestamp: '',
        timestampNum: 0,
        latency: 0,
        availability: -1,
        statusCounts: { ...defaultStatusCounts, missing: 1 },
        slowLatencyMs,
        model: layer.model,
        requestModel: layerRequestModel,
        layerOrder: layer.layer_order,
      };
    }

    // 该层缺失该时间点：返回占位点（灰色），保持层数一致
    if (!ownTimePoint) {
      return {
        index,
        status: 'MISSING',
        timestamp: timePointForTimestamp.time,
        timestampNum: timePointForTimestamp.timestamp,
        latency: 0,
        availability: -1,
        statusCounts: { ...defaultStatusCounts, missing: 1 },
        slowLatencyMs,
        model: layer.model,
        requestModel: layerRequestModel,
        layerOrder: layer.layer_order,
      };
    }

    // 正常情况：该层有数据
    // 使用展开运算符确保 statusCounts 所有字段都存在（防止后端返回不完整对象）
    return {
      index,
      status: STATUS_MAP[ownTimePoint.status] || 'UNAVAILABLE',
      timestamp: ownTimePoint.time,
      timestampNum: ownTimePoint.timestamp,
      latency: ownTimePoint.latency,
      availability: ownTimePoint.availability,
      statusCounts: { ...defaultStatusCounts, ...ownTimePoint.status_counts },
      slowLatencyMs,
      model: layer.model,
      requestModel: layerRequestModel,
      layerOrder: layer.layer_order,
    };
  };

  // 处理点击事件（移动端）
  const handleLayerClick = (e: React.MouseEvent<HTMLDivElement>, layer: MonitorLayer) => {
    e.stopPropagation();
    const point = convertToHeatmapPoint(layer, timeIndex);
    // 只有有效数据才触发 hover（灰色块不弹 tooltip）
    if (point.availability >= 0) {
      onHover(e, point);
    }
  };

  return (
    <div
      className="flex flex-col overflow-hidden"
      style={{ width, height: `${totalHeightPx}px` }}
    >
      {sortedLayers.map((layer, layerIdx) => {
        const point = convertToHeatmapPoint(layer, timeIndex);
        const blockStyle = heatmapBlockToStyle(point, useLatencyGradient);
        const isFirst = layerIdx === 0;
        const isLast = layerIdx === sortedLayers.length - 1;

        return (
          <div
            key={`${layer.model}-${layer.layer_order}`}
            role="button"
            tabIndex={0}
            className={`transition-all duration-200 hover:brightness-110 active:brightness-105 cursor-pointer opacity-80 hover:opacity-100 active:opacity-100 relative hover:z-10 ${isFirst ? 'rounded-t-sm' : ''} ${isLast ? 'rounded-b-sm' : ''}`}
            style={{
              height: `${layerHeightPx}px`,
              ...blockStyle,
              // 层间间隙（非第一层加 marginTop，兼容 Safari ≤13）
              ...(layerIdx > 0 ? { marginTop: `${gapPx}px` } : {}),
            }}
            // 鼠标事件（仅桌面端，灰色块不弹 tooltip）
            onMouseEnter={isMobile ? undefined : (e) => {
              const p = convertToHeatmapPoint(layer, timeIndex);
              if (p.availability >= 0) onHover(e, p);
            }}
            onMouseLeave={isMobile ? undefined : onLeave}
            // 点击事件（移动端）
            onClick={(e) => handleLayerClick(e, layer)}
            // 键盘可访问性（仅桌面端）
            onFocus={isMobile ? undefined : (e) => {
              const p = convertToHeatmapPoint(layer, timeIndex);
              if (p.availability >= 0) onHover(e as unknown as React.MouseEvent<HTMLDivElement>, p);
            }}
            onBlur={isMobile ? undefined : onLeave}
            // 无障碍标签
            aria-label={
              point.availability >= 0
                ? `${layer.model} (${t('multiModel.layer', { order: layer.layer_order })}): ${t('accessibility.uptimeBlock', { uptime: point.availability.toFixed(1) })}`
                : `${layer.model} (${t('multiModel.layer', { order: layer.layer_order })}): ${t('accessibility.noDataBlock')}`
            }
          />
        );
      })}
    </div>
  );
});
