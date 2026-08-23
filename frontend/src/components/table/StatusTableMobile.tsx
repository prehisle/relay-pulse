import { useMemo } from 'react';
import { List, type RowComponentProps } from 'react-window';
import { ArrowUp, ArrowDown, Zap, Shield } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { StatusDot } from '../StatusDot';
import { HeatmapBlock } from '../HeatmapBlock';
import { ExternalLink } from '../ExternalLink';
import { FavoriteButton } from '../FavoriteButton';
import { AnnotationCell } from '../annotations';
import { VendorBadge } from '../VendorBadge';
import { ChannelCell } from './ChannelCell';
import { getModelDisplayList, getModelTooltip } from './modelNames';
import { QualityScoreCell, shouldShowQualityPending } from '../quality';
import { getCachedServiceIcon } from '../serviceIconCache';
import { availabilityToColor, latencyToColor, sponsorLevelToCardBorderColor, sponsorLevelToPinnedBgClass } from '../../utils/color';
import { aggregateHeatmap } from '../../utils/heatmapAggregator';
import { hasAnyAnnotation } from '../../utils/annotationUtils';
import { lookupRpdiagScore } from '../../hooks/useRpdiagScores';
import type { ProcessedMonitorData, SortConfig } from '../../types';
import type { RpdiagScore, RpdiagScoresResponse } from '../../types/monitor';

type HistoryPoint = ProcessedMonitorData['history'][number];

// 虚拟滚动常量
const MOBILE_ROW_HEIGHT = 160;  // 移动端卡片高度（约 150px 内容 + 10px 间距）
const MOBILE_MAX_HEIGHT = 800;  // 移动端列表最大高度

// react-window v2 虚拟列表行组件（rowComponent 接口）
interface MobileRowProps {
  data: ProcessedMonitorData[];
  slowLatencyMs: number;
  enableAnnotations: boolean;
  showProvider: boolean;
  showSponsor: boolean;
  useLatencyGradient: boolean;
  isFavorite: (id: string) => boolean;
  onToggleFavorite: (id: string) => void;
  onBlockHover: (e: React.MouseEvent<HTMLDivElement>, point: HistoryPoint) => void;
  onBlockLeave: () => void;
  rpdiagScores?: RpdiagScoresResponse;
  rpdiagEnabled: boolean;
  showVendorColumn: boolean;
}

function MobileRow({ index, style, data, slowLatencyMs, enableAnnotations, showProvider, showSponsor, useLatencyGradient, isFavorite, onToggleFavorite, onBlockHover, onBlockLeave, rpdiagScores, rpdiagEnabled, showVendorColumn }: RowComponentProps<MobileRowProps>) {
  const item = data[index];
  return (
    <div style={style}>
      <div style={{ marginBottom: 8 }}>
        <MobileListItem
          item={item}
          slowLatencyMs={slowLatencyMs}
          enableAnnotations={enableAnnotations}
          showProvider={showProvider}
          showSponsor={showSponsor}
          useLatencyGradient={useLatencyGradient}
          isFavorite={isFavorite(item.id)}
          onToggleFavorite={() => onToggleFavorite(item.id)}
          onBlockHover={onBlockHover}
          onBlockLeave={onBlockLeave}
          rpdiagScore={rpdiagEnabled ? lookupRpdiagScore(rpdiagScores, [item.providerName, item.providerId], item.serviceType, item.channelName || item.channel, item.channelId) : undefined}
          rpdiagEnabled={rpdiagEnabled}
          showVendorColumn={showVendorColumn}
        />
      </div>
    </div>
  );
}

// 移动端卡片列表项组件
function MobileListItem({
  item,
  slowLatencyMs,
  enableAnnotations = true,
  showProvider = true,
  showSponsor = true,
  useLatencyGradient = false,
  isFavorite,
  onToggleFavorite,
  onBlockHover,
  onBlockLeave,
  rpdiagScore,
  rpdiagEnabled = false,
  showVendorColumn = false,
}: {
  item: ProcessedMonitorData;
  slowLatencyMs: number;
  enableAnnotations?: boolean;
  showProvider?: boolean;
  showSponsor?: boolean;
  useLatencyGradient?: boolean;
  isFavorite: boolean;
  onToggleFavorite: () => void;
  onBlockHover: (e: React.MouseEvent<HTMLDivElement>, point: HistoryPoint) => void;
  onBlockLeave: () => void;
  rpdiagScore?: RpdiagScore;
  rpdiagEnabled?: boolean;
  showVendorColumn?: boolean;
}) {
  const { t, i18n } = useTranslation();
  const ServiceIcon = getCachedServiceIcon(item.serviceType);

  // 聚合热力图数据
  const aggregatedHistory = useMemo(
    () => aggregateHeatmap(item.history, 30),
    [item.history]
  );

  // 检查是否有注解需要显示
  const hasItemAnnotations = hasAnyAnnotation(item, { enableAnnotations });

  // 卡片左边框颜色（仅基于赞助级别，置顶改用背景色）
  const borderColor = sponsorLevelToCardBorderColor(item.sponsorLevel);

  // 是否显示左边框（仅基于赞助级别）
  const hasLeftBorder = !!item.sponsorLevel;

  // 置顶项使用对应注解颜色的极淡背景色
  const pinnedBgClass = item.pinned ? sponsorLevelToPinnedBgClass(item.sponsorLevel) : '';
  const baseBgClass = pinnedBgClass || 'bg-surface/60';

  // 卡片最小高度 = 行高(160) - 行间距(8) = 152px
  // 确保所有卡片高度一致，避免虚拟列表中间距不均
  const cardMinHeight = 152;

  return (
    <div
      className={`${baseBgClass} border border-default rounded-r-xl ${hasLeftBorder ? 'rounded-l-sm border-l-2' : 'rounded-l-xl'} p-3 space-y-2`}
      style={{
        ...(borderColor ? { borderLeftColor: borderColor } : {}),
        minHeight: cardMinHeight,
      }}
    >
      {/* 注解行 - 仅在有注解时显示 */}
      {hasItemAnnotations && (
        <AnnotationCell annotations={item.annotations} />
      )}

      {/* 主要信息行 */}
      <div className="flex items-start justify-between gap-2">
        <div className="flex items-center gap-2 min-w-0 flex-1">
          {/* 服务图标 */}
          <div className="w-8 h-8 flex-shrink-0 rounded-lg bg-elevated flex items-center justify-center border border-default text-primary">
            {ServiceIcon ? (
              <ServiceIcon className="w-4 h-4" />
            ) : item.serviceType === 'cc' ? (
              <Zap className="text-service-cc" size={14} />
            ) : (
              <Shield className="text-service-cx" size={14} />
            )}
          </div>

          {/* 服务商名称 + 收藏按钮 */}
          <div className="min-w-0 flex-1">
            {showProvider && (
              <div className="flex items-center gap-1.5">
                <span className="font-semibold text-primary truncate text-sm leading-tight">
                  <ExternalLink href={item.providerUrl} compact requireConfirm>{item.providerName}</ExternalLink>
                </span>
                <FavoriteButton
                  isFavorite={isFavorite}
                  onToggle={onToggleFavorite}
                  size={12}
                  inline
                />
              </div>
            )}
            <div className="flex items-center gap-2 mt-0.5 text-xs text-secondary">
              {/* 赞助者（放在服务类型前） */}
              {showSponsor && item.sponsor && (
                <span className="text-[10px] text-muted truncate max-w-[80px]">
                  <ExternalLink href={item.sponsorUrl} compact>{item.sponsor}</ExternalLink>
                </span>
              )}
              <span
                className={`px-1.5 py-0.5 rounded text-[10px] font-mono border flex-shrink-0 ${
                  item.serviceType === 'cc'
                    ? 'border-service-cc text-service-cc bg-service-cc'
                    : item.serviceType === 'gm'
                    ? 'border-service-gm text-service-gm bg-service-gm'
                    : 'border-service-cx text-service-cx bg-service-cx'
                }`}
              >
                {item.serviceName.toUpperCase()}
              </span>
              {item.channel && (
                <ChannelCell
                  channel={item.channelName || item.channel}
                  probeUrl={item.probeUrl}
                  templateName={item.templateName}
                  coldReason={item.coldReason}
                  boardReason={item.boardReason}
                  boardReasonModels={item.boardReasonModels}
                  className="text-muted truncate"
                />
              )}
              {item.modelEntries && item.modelEntries.length > 0 && (() => {
                const models = getModelDisplayList(item.modelEntries);
                if (models.length === 0) return null;
                return (
                  <span
                    className="text-[10px] text-muted truncate max-w-[120px]"
                    title={getModelTooltip(item.modelEntries)}
                  >
                    {models.length === 1 ? models[0] : `${models[0]} +${models.length - 1}`}
                  </span>
                );
              })()}
              {/* 厂商：紧跟模型名。移动端只出图标——这一行还挤着通道名/模型名/收录天数，
                  加一段厂商名会把前两者一起挤成省略号（实测），全名走 title。 */}
              {showVendorColumn && (
                <VendorBadge vendor={item.modelVendor} compact iconOnly className="text-[10px] text-muted max-w-[70px]" />
              )}
              {/* 收录时间 */}
              {item.listedDays != null && (
                <span className="text-[10px] text-muted font-mono flex-shrink-0">
                  {item.listedDays}d
                </span>
              )}
            </div>
          </div>
        </div>

        {/* 状态、可用率、时间和延迟 */}
        <div className="flex flex-col items-end gap-1 flex-shrink-0">
          <div className="flex items-center p-1.5 rounded-full bg-elevated border border-default">
            <StatusDot status={item.currentStatus} size="sm" />
          </div>
          <span
            className="text-sm font-mono font-bold"
            style={{ color: availabilityToColor(item.uptime) }}
          >
            {item.uptime >= 0 ? `${item.uptime}%` : '--'}
          </span>
          {rpdiagScore && rpdiagScore.models && rpdiagScore.models.length > 0 ? (
            <QualityScoreCell score={rpdiagScore} compact />
          ) : shouldShowQualityPending({ rpdiagEnabled, hasScore: !!rpdiagScore, serviceType: item.serviceType, board: item.board }) ? (
            // 移动端：有分/待测两态显示；无分且非待测维持原「不渲染」以最小化视觉变化。
            <span className="text-muted text-xs" title={t('table.qualityPendingTooltip')}>
              {t('table.qualityPending')}
            </span>
          ) : null}
          {/* 时间和延迟（总是显示） */}
          <div className="flex items-center gap-2 text-[10px] text-muted font-mono">
            {item.lastCheckTimestamp && (
              <span>
                {new Date(item.lastCheckTimestamp * 1000).toLocaleString(i18n.language, {
                  hour: '2-digit',
                  minute: '2-digit',
                })}
              </span>
            )}
            {item.lastCheckLatency !== undefined && (
              <span style={{ color: item.currentStatus === 'UNAVAILABLE' ? 'hsl(var(--text-muted))' : latencyToColor(item.lastCheckLatency, item.slowLatencyMs ?? slowLatencyMs) }}>
                {item.lastCheckLatency}ms
              </span>
            )}
          </div>
        </div>
      </div>

      {/* 热力图 */}
      <div className="flex items-center gap-[2px] h-5 w-full overflow-hidden rounded-sm">
        {aggregatedHistory.map((point, idx) => (
          <HeatmapBlock
            key={idx}
            point={point}
            width={`${100 / aggregatedHistory.length}%`}
            height="h-full"
            onHover={onBlockHover}
            onLeave={onBlockLeave}
            isMobile
            useLatencyGradient={useLatencyGradient}
          />
        ))}
      </div>
    </div>
  );
}

// 移动端排序菜单
function MobileSortMenu({
  sortConfig,
  isInitialSort,
  onSort,
  hidePriceColumn,
  rpdiagScoresLoaded,
  rpdiagEnabled,
  showVendorColumn,
}: {
  sortConfig: SortConfig;
  isInitialSort?: boolean;
  onSort: (key: string) => void;
  hidePriceColumn: boolean;
  rpdiagScoresLoaded: boolean;
  rpdiagEnabled: boolean;
  showVendorColumn: boolean;
}) {
  const { t } = useTranslation();

  const sortOptions: Array<{ key: string; label: string; disabled?: boolean }> = [
    { key: 'providerName', label: t('table.sorting.provider') },
    { key: 'uptime', label: t('table.sorting.uptime') },
    { key: 'lastCheck', label: t('table.sorting.lastCheck') },
    { key: 'serviceType', label: t('table.sorting.service') },
    // 厂商未回填时移动端不提供"按厂商排序"（与桌面端隐藏厂商列一致）
    ...(showVendorColumn ? [{ key: 'modelVendor', label: t('table.sorting.modelVendor') }] : []),
    ...(hidePriceColumn ? [] : [{ key: 'priceRatio', label: t('table.sorting.priceRatio') }]),
    { key: 'listedDays', label: t('table.sorting.listedDays') },
    // rpdiag 关闭时移动端不提供"按质量排序"（与桌面端隐藏质量列一致）
    ...(rpdiagEnabled ? [{ key: 'qualityScore', label: t('table.sorting.quality'), disabled: !rpdiagScoresLoaded }] : []),
  ];

  return (
    <div className="flex items-center gap-2 mb-2 overflow-x-auto pb-2">
      <span className="text-xs text-muted flex-shrink-0">{t('controls.sortBy')}</span>
      {sortOptions.map((option) => {
        // rpdiag 未加载完成时质量按钮置灰、不响应点击；其他按钮无变化
        const isDisabled = option.disabled === true;
        // 初始状态下不高亮任何排序按钮
        const isActive = !isDisabled && !isInitialSort && sortConfig.key === option.key;
        return (
          <button
            key={option.key}
            onClick={() => !isDisabled && onSort(option.key)}
            disabled={isDisabled}
            className={`flex items-center gap-1 px-2.5 py-1.5 rounded-lg text-xs font-medium transition-colors flex-shrink-0 focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none ${
              isActive
                ? 'bg-accent/20 text-accent border border-accent/30'
                : isDisabled
                ? 'bg-elevated text-muted border border-default opacity-60 cursor-not-allowed'
                : 'bg-elevated text-secondary border border-default hover:text-primary'
            }`}
          >
            {option.label}
            {isActive && (
              sortConfig.direction === 'asc' ? (
                <ArrowUp size={12} />
              ) : (
                <ArrowDown size={12} />
              )
            )}
          </button>
        );
      })}
    </div>
  );
}

// 平板/移动端视图：虚拟滚动的卡片列表 + 一条横向排序按钮。
// 注意这一屏是在**表格视图内部**切出来的——上层 App/ProviderPage 在同一个 tablet
// 断点已把 viewMode 强制成 table，所以「表格视图」在窄屏实际渲染的是这里的卡片。
//
// 各开关一律由 StatusTable 传入已解析好的值、没有默认值：质量列等开关在外壳与
// 内部卡片组件上的默认值方向相反（外壳 fail-open 默认开、卡片默认关），留默认值
// 会让漏传变成静默关列。
interface StatusTableMobileProps {
  data: ProcessedMonitorData[];
  sortConfig: SortConfig;
  isInitialSort: boolean;
  onSort: (key: string) => void;
  slowLatencyMs: number;
  enableAnnotations: boolean;
  showProvider: boolean;
  showSponsor: boolean;
  useLatencyGradient: boolean;
  isFavorite: (id: string) => boolean;
  onToggleFavorite: (id: string) => void;
  onBlockHover: (e: React.MouseEvent<HTMLDivElement>, point: HistoryPoint) => void;
  onBlockLeave: () => void;
  rpdiagScores?: RpdiagScoresResponse;
  rpdiagScoresLoaded: boolean;
  rpdiagEnabled: boolean;
  hidePriceColumn: boolean;
  showVendorColumn: boolean;
}

export function StatusTableMobile({
  data,
  sortConfig,
  isInitialSort,
  onSort,
  slowLatencyMs,
  enableAnnotations,
  showProvider,
  showSponsor,
  useLatencyGradient,
  isFavorite,
  onToggleFavorite,
  onBlockHover,
  onBlockLeave,
  rpdiagScores,
  rpdiagScoresLoaded,
  rpdiagEnabled,
  hidePriceColumn,
  showVendorColumn,
}: StatusTableMobileProps) {
  // 计算虚拟列表高度（最大 MOBILE_MAX_HEIGHT，最小为所有项目高度）
  const mobileListHeight = Math.min(
    data.length * MOBILE_ROW_HEIGHT,
    MOBILE_MAX_HEIGHT
  );

  return (
    <div>
      <MobileSortMenu
        sortConfig={sortConfig}
        isInitialSort={isInitialSort}
        onSort={onSort}
        hidePriceColumn={hidePriceColumn}
        rpdiagScoresLoaded={rpdiagScoresLoaded}
        rpdiagEnabled={rpdiagEnabled}
        showVendorColumn={showVendorColumn}
      />
      <List
        style={{ height: mobileListHeight, width: '100%' }}
        rowCount={data.length}
        rowHeight={MOBILE_ROW_HEIGHT}
        overscanCount={3}
        rowComponent={MobileRow}
        rowProps={{ data, slowLatencyMs, enableAnnotations, showProvider, showSponsor, useLatencyGradient, isFavorite, onToggleFavorite, onBlockHover, onBlockLeave, rpdiagScores, rpdiagEnabled, showVendorColumn }}
      />
    </div>
  );
}
