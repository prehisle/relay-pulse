import { useMemo, memo } from 'react';
import { Activity, Clock, Zap, Shield } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { StatusDot } from './StatusDot';
import { HeatmapBlock } from './HeatmapBlock';
import { LayeredHeatmapBlock } from './LayeredHeatmapBlock';
import { ExternalLink } from './ExternalLink';
import { FavoriteButton } from './FavoriteButton';
import { getTimeRanges } from '../constants';
import { availabilityToColor, latencyToColor, sponsorLevelToCardBorderColor, sponsorLevelToPinnedBgClass } from '../utils/color';
import { formatPriceRatioStructured } from '../utils/format';
import { aggregateHeatmap } from '../utils/heatmapAggregator';
import { getServiceIconComponent } from './ServiceIcon';
import { VendorBadge } from './VendorBadge';
import { AnnotationCell } from './annotations';
import { hasAnyAnnotation } from '../utils/annotationUtils';
import type { ProcessedMonitorData } from '../types';

type HistoryPoint = ProcessedMonitorData['history'][number];

// ServiceIcon 模块级缓存，与 StatusTable 保持一致
const serviceIconCache = new Map<string, ReturnType<typeof getServiceIconComponent>>();
const getCachedServiceIcon = (serviceType: string) => {
  if (!serviceIconCache.has(serviceType)) {
    serviceIconCache.set(serviceType, getServiceIconComponent(serviceType));
  }
  return serviceIconCache.get(serviceType);
};

interface StatusCardProps {
  item: ProcessedMonitorData;
  timeRange: string;
  slowLatencyMs: number;
  enableAnnotations?: boolean;      // 注解系统总开关，默认 true
  showCategoryTag?: boolean; // 是否显示分类标签（推荐/公益），默认 true
  showProvider?: boolean;    // 是否显示服务商名称，默认 true
  showSponsor?: boolean;     // 是否显示赞助者信息，默认 true
  isFavorite?: (id: string) => boolean;  // 检查是否收藏
  onToggleFavorite?: (id: string) => void;  // 切换收藏状态
  hidePriceColumn?: boolean; // runtime 控制：是否隐藏价格列（meta.hide_price_column 派生，默认 false）
  /** 是否展示模型厂商。与表格视图的厂商列同一个开关（调用方基于未筛数据判定），
   *  三个渲染出口共用一个真相源，避免「桌面没有、卡片却有」的分叉。 */
  showVendorColumn?: boolean;
  onBlockHover: (e: React.MouseEvent<HTMLDivElement>, point: HistoryPoint) => void;
  onBlockLeave: () => void;
}

function StatusCardComponent({
  item,
  timeRange,
  slowLatencyMs,
  enableAnnotations = true,
  showProvider = true,
  isFavorite,
  onToggleFavorite,
  hidePriceColumn = false,
  showVendorColumn = false,
  onBlockHover,
  onBlockLeave
}: StatusCardProps) {
  const { t, i18n } = useTranslation();

  // 聚合热力图数据（移动端）
  const aggregatedHistory = useMemo(
    () => aggregateHeatmap(item.history, 50),
    [item.history]
  );

  const currentTimeRange = getTimeRanges(t).find((r) => r.id === timeRange);
  const useLatencyGradient = timeRange === '90m';
  const ServiceIcon = getCachedServiceIcon(item.serviceType);

  // 检查是否有注解需要显示
  const hasAnnotations = hasAnyAnnotation(item, { enableAnnotations });

  // 卡片左边框颜色（仅基于赞助级别，置顶改用背景色）
  const borderColor = sponsorLevelToCardBorderColor(item.sponsorLevel);

  // 是否显示左边框（仅基于赞助级别）
  const hasLeftBorder = !!item.sponsorLevel;

  // 置顶项使用对应注解颜色的极淡背景色
  const pinnedBgClass = item.pinned ? sponsorLevelToPinnedBgClass(item.sponsorLevel) : '';
  const baseBgClass = pinnedBgClass || 'bg-surface/60';

  return (
    <div
      className={`group relative ${baseBgClass} border border-default hover:border-accent/30 ${hasLeftBorder ? 'rounded-l-sm border-l-2' : 'rounded-l-2xl'} rounded-r-2xl p-4 sm:p-6 transition-all duration-300 hover:shadow-accent-lg backdrop-blur-sm overflow-hidden`}
      style={borderColor ? { borderLeftColor: borderColor } : undefined}
    >
      {/* 注解行 - 仅在有注解时显示 */}
      {hasAnnotations && (
        <div className="mb-4">
          <AnnotationCell annotations={item.annotations} />
        </div>
      )}

      {/* 头部信息 - 使用 Grid 布局响应式 */}
      <div className="grid grid-cols-1 sm:grid-cols-[1fr_auto] gap-4 mb-6">
        {/* 左侧：图标 + 服务信息 */}
        <div className="flex gap-3 sm:gap-4 items-start sm:items-center">
          <div className="w-10 h-10 sm:w-12 sm:h-12 flex-shrink-0 rounded-xl bg-elevated flex items-center justify-center border border-default group-hover:border-strong transition-colors text-primary">
            {ServiceIcon ? (
              <ServiceIcon className="w-5 h-5 sm:w-6 sm:h-6" />
            ) : item.serviceType === 'cc' ? (
              <Zap className="text-service-cc" size={20} />
            ) : (
              <Shield className="text-service-cx" size={20} />
            )}
          </div>
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-2 flex-wrap">
              {showProvider && (
                <h3 className="text-base sm:text-lg font-bold text-primary">
                  <ExternalLink href={item.providerUrl} requireConfirm>{item.providerName}</ExternalLink>
                </h3>
              )}
              {/* 收藏按钮 */}
              {isFavorite && onToggleFavorite && (
                <FavoriteButton
                  isFavorite={isFavorite(item.id)}
                  onToggle={() => onToggleFavorite(item.id)}
                  size={14}
                  inline
                />
              )}
              <span
                className={`px-2 py-0.5 rounded text-[10px] font-mono border flex-shrink-0 ${
                  item.serviceType === 'cc'
                    ? 'border-service-cc text-service-cc bg-service-cc'
                    : item.serviceType === 'gm'
                    ? 'border-service-gm text-service-gm bg-service-gm'
                    : 'border-service-cx text-service-cx bg-service-cx'
                }`}
              >
                {item.serviceType.toUpperCase()}
              </span>
              {/* 厂商徽章紧跟服务徽章：一眼读成「用这套协议接入、跑这家的模型」 */}
              {showVendorColumn && (
                <VendorBadge vendor={item.modelVendor} compact className="text-[10px] text-secondary" />
              )}
            </div>
            <div className="flex items-center gap-3 mt-1 text-xs font-mono flex-wrap">
              <span className="flex items-center gap-1">
                <Activity size={12} className="text-secondary" />
                <span style={{ color: availabilityToColor(item.uptime) }}>
                  {t('card.uptime')} {item.uptime >= 0 ? `${item.uptime}%` : '--'}
                </span>
              </span>
              {!hidePriceColumn && (item.priceMin != null || item.priceMax != null) && (() => {
                const priceData = formatPriceRatioStructured(item.priceMin, item.priceMax);
                if (!priceData) return null;
                return (
                  <span className="text-secondary">
                    {t('table.headers.priceRatio')}: <span className="text-secondary">{priceData.base}</span>
                    {priceData.sub && <span className="text-muted text-[10px] ml-0.5">({priceData.sub})</span>}
                  </span>
                );
              })()}
              {item.listedDays != null && (
                <span className="text-secondary">
                  {t('table.headers.listedDays')}: <span className="text-secondary">{item.listedDays}d</span>
                </span>
              )}
            </div>
          </div>
        </div>

        {/* 右侧：状态 + 延迟 + 时间 */}
        <div className="flex sm:flex-col items-start sm:items-end gap-2 sm:gap-1.5">
          <div className="flex items-center gap-1.5">
            <StatusDot status={item.currentStatus} />
            {item.lastCheckTimestamp ? (
              <div className="text-[10px] text-muted font-mono flex flex-col items-start sm:items-end gap-0.5">
                {item.lastCheckLatency !== undefined && (
                  <span style={{ color: item.currentStatus === 'UNAVAILABLE' ? 'hsl(var(--text-muted))' : latencyToColor(item.lastCheckLatency, item.slowLatencyMs ?? slowLatencyMs) }}>
                    {item.lastCheckLatency}ms
                  </span>
                )}
                <span className="whitespace-nowrap">
                  {new Date(item.lastCheckTimestamp * 1000).toLocaleString(i18n.language, {
                    month: '2-digit',
                    day: '2-digit',
                    hour: '2-digit',
                    minute: '2-digit',
                  })}
                </span>
              </div>
            ) : (
              <span className="text-muted text-xs">-</span>
            )}
          </div>
        </div>
      </div>

      {/* 热力图 */}
      <div>
        <div className="flex justify-between text-xs text-muted mb-2">
          <span className="flex items-center gap-1">
            <Clock size={12} /> {currentTimeRange?.label || timeRange}
          </span>
          <span>{t('common.now')}</span>
        </div>
        {/* 多模型 vs 单模型热力图（与表格视图一致） */}
        <div className={`flex gap-[2px] w-full overflow-hidden rounded-sm ${item.isMultiModel && item.layers ? 'h-10' : 'h-10'}`}>
          {item.isMultiModel && item.layers ? (
            // 多模型：垂直分层热力图（使用原始 history 长度，不聚合）
            item.history.map((_, idx) => (
              <LayeredHeatmapBlock
                key={idx}
                layers={item.layers!}
                timeIndex={idx}
                width={`${100 / item.history.length}%`}
                height="h-10"
                onHover={onBlockHover}
                onLeave={onBlockLeave}
                isMobile={false}
                slowLatencyMs={item.slowLatencyMs ?? slowLatencyMs}
                useLatencyGradient={useLatencyGradient}
              />
            ))
          ) : (
            // 单模型：传统热力图（聚合后）
            aggregatedHistory.map((point, idx) => (
              <HeatmapBlock
                key={idx}
                point={point}
                width={`${100 / aggregatedHistory.length}%`}
                height="h-10"
                onHover={onBlockHover}
                onLeave={onBlockLeave}
                isMobile={false}
                useLatencyGradient={useLatencyGradient}
              />
            ))
          )}
        </div>

        {/* 移动端提示：点击查看详情（仅单模型聚合时显示） */}
        {!item.isMultiModel && aggregatedHistory.length < item.history.length && (
          <div className="mt-2 text-[10px] text-muted text-center sm:hidden">
            {t('table.heatmapHint', { from: item.history.length, to: aggregatedHistory.length })}
          </div>
        )}
      </div>
    </div>
  );
}

export const StatusCard = memo(StatusCardComponent);
