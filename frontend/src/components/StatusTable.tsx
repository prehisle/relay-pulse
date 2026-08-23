import { useState, useEffect, useMemo, memo } from 'react';
import { List, type RowComponentProps } from 'react-window';
import { ArrowUpDown, ArrowUp, ArrowDown, Zap, Shield, Filter } from 'lucide-react';
import { useTranslation, Trans } from 'react-i18next';
import { StatusDot } from './StatusDot';
import { HeatmapBlock } from './HeatmapBlock';
import { LayeredHeatmapBlock } from './LayeredHeatmapBlock';
import { ChannelTypeIcon, parseChannelType } from './ChannelTypeIcon';
import { ExternalLink } from './ExternalLink';
import { HeaderInfoPopover } from './HeaderInfoPopover';
import { HoverTooltip } from './HoverTooltip';
import { AnnotationCell } from './annotations';
import { FavoriteButton } from './FavoriteButton';
import { QualityScoreCell, shouldShowQualityPending } from './quality';
import { getTimeRanges } from '../constants';
import { availabilityToColor, latencyToColor, sponsorLevelToBorderClass, sponsorLevelToCardBorderColor, sponsorLevelToPinnedBgClass } from '../utils/color';
import { aggregateHeatmap } from '../utils/heatmapAggregator';
import { createMediaQueryEffect } from '../utils/mediaQuery';
import { shortenModelName } from '../utils/modelName';
import { hasAnyAnnotation, hasAnyAnnotationInList } from '../utils/annotationUtils';
import { formatPriceRatioStructured } from '../utils/format';
import { getServiceIconComponent } from './ServiceIcon';
import { VendorBadge } from './VendorBadge';
import { lookupRpdiagScore } from '../hooks/useRpdiagScores';
import type { ProcessedMonitorData, SortConfig } from '../types';
import type { RpdiagScore, RpdiagScoresResponse } from '../types/monitor';

type HistoryPoint = ProcessedMonitorData['history'][number];

// 虚拟滚动常量
const MOBILE_ROW_HEIGHT = 160;  // 移动端卡片高度（约 150px 内容 + 10px 间距）
const MOBILE_MAX_HEIGHT = 800;  // 移动端列表最大高度

// ServiceIcon 模块级缓存，避免重复调用 getServiceIconComponent
const serviceIconCache = new Map<string, ReturnType<typeof getServiceIconComponent>>();
const getCachedServiceIcon = (serviceType: string) => {
  if (!serviceIconCache.has(serviceType)) {
    serviceIconCache.set(serviceType, getServiceIconComponent(serviceType));
  }
  return serviceIconCache.get(serviceType);
};

// 通道单元格组件（带自定义 CSS tooltip，替代原生 title 属性）
interface ChannelCellProps {
  channel?: string;
  probeUrl?: string;
  templateName?: string;
  coldReason?: string;
  boardReason?: string;
  boardReasonModels?: string;
  className?: string;
}

function ChannelCell({ channel, probeUrl, templateName, coldReason, boardReason, boardReasonModels, className = '' }: ChannelCellProps) {
  const { t } = useTranslation();
  const channelType = parseChannelType(channel);
  const isQualityHardFail = boardReason === 'quality_hardfail';
  const hasTooltip = !!(channelType || probeUrl || templateName || coldReason || isQualityHardFail);

  const channelContent = (
    <>
      <ChannelTypeIcon channel={channel} />
      <span className="min-w-0 truncate">{channel || '-'}</span>
    </>
  );

  if (!hasTooltip) {
    return <span className={`inline-flex items-center gap-1 ${className}`}>{channelContent}</span>;
  }

  return (
    <HoverTooltip
      triggerClassName={`gap-1 cursor-help ${className}`}
      content={
        <span className="flex flex-col gap-1">
          {channelType && (
            <span className="flex flex-col">
              <span className="text-muted text-[10px]">{t('table.channelTooltip.channelType')}</span>
              <span className="text-primary text-[11px]">
                {t(`table.channelType.${channelType}`)} — {t(`table.channelType.${channelType}Desc`)}
              </span>
            </span>
          )}
          {probeUrl && (
            <span className="flex flex-col">
              <span className="text-muted text-[10px]">{t('table.channelTooltip.probeUrl')}</span>
              <span className="text-primary font-mono text-[11px] break-all">{probeUrl}</span>
            </span>
          )}
          {templateName && (
            <span className="flex flex-col">
              <span className="text-muted text-[10px]">{t('table.channelTooltip.template')}</span>
              <span className="text-primary font-mono text-[11px] break-all">{templateName}</span>
            </span>
          )}
          {coldReason && (
            <span className="flex flex-col">
              <span className="text-muted text-[10px]">{t('table.channelTooltip.coldReason', '冷板原因')}</span>
              <span className="text-warning text-[11px] break-all">{coldReason}</span>
            </span>
          )}
          {isQualityHardFail && (
            <span className="flex flex-col">
              <span className="text-muted text-[10px]">{t('table.channelTooltip.qualityHardFail.label', '质量移板')}</span>
              <span className="text-warning text-[11px] break-all">
                {boardReasonModels
                  ? t('table.channelTooltip.qualityHardFail.text', '{{models}} 近3次评测均未取得可评分响应，已暂移备用板', { models: boardReasonModels })
                  : t('table.channelTooltip.qualityHardFail.textNoModels', '近3次评测均未取得可评分响应，已暂移备用板')}
              </span>
            </span>
          )}
        </span>
      }
    >
      {channelContent}
    </HoverTooltip>
  );
}

// ─── 模型列辅助函数 ───────────────────────────────────────────

function getModelDisplayList(modelEntries?: ProcessedMonitorData['modelEntries']): string[] {
  if (!modelEntries || modelEntries.length === 0) return [];
  return modelEntries
    .map((entry) => shortenModelName(entry.requestModel) || entry.model || '-')
    .filter(Boolean);
}

function getModelTooltip(modelEntries?: ProcessedMonitorData['modelEntries']): string | undefined {
  if (!modelEntries || modelEntries.length === 0) return undefined;
  return modelEntries
    .map((entry) => entry.requestModel || entry.model || '-')
    .join('\n');
}

interface StatusTableProps {
  data: ProcessedMonitorData[];
  sortConfig: SortConfig;
  isInitialSort?: boolean;   // 是否为初始排序状态（控制高亮显示）
  timeRange: string;
  slowLatencyMs: number;
  enableAnnotations?: boolean;      // 注解系统总开关，默认 true
  showCategoryTag?: boolean; // 是否显示分类标签（推荐/公益），默认 true
  showProvider?: boolean;    // 是否显示服务商名称，默认 true
  showSponsor?: boolean;     // 是否显示赞助者信息，默认 true
  isFavorite: (id: string) => boolean;  // 检查是否已收藏
  onToggleFavorite: (id: string) => void; // 切换收藏状态
  onSort: (key: string) => void;
  onBlockHover: (e: React.MouseEvent<HTMLDivElement>, point: HistoryPoint) => void;
  onBlockLeave: () => void;
  onFilterProvider?: (providerId: string) => void; // 按服务商筛选
  /** rpdiag 质量分索引（按 "provider|service|channel" 键）。空对象表示功能未启用或上游不可达。 */
  rpdiagScores?: RpdiagScoresResponse;
  /** rpdiag 质量分是否已加载完成。false 时质量列排序按钮置灰（避免空数据触发的伪排序）。 */
  rpdiagScoresLoaded?: boolean;
  /** rpdiag 质量功能总开关（meta.rpdiag_enabled 派生）。false 时整列消失（私有部署未接 rpdiag）。默认 true（fail-open）。 */
  rpdiagEnabled?: boolean;
  /** runtime 价格列隐藏开关（meta.hide_price_column 派生）。默认 false（显示）。 */
  hidePriceColumn?: boolean;
  /** 是否显示「模型厂商」列。由调用方基于**未筛选**的全量数据判定（见 App/ProviderPage），
   *  刻意不在本组件内按已筛数据算——否则筛选一下列就会凭空出现/消失，表格布局抖动。
   *  厂商全站未回填（Phase 3 之前）时恒 false，对用户完全无感。 */
  showVendorColumn?: boolean;
}

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

function StatusTableComponent({
  data,
  sortConfig,
  isInitialSort = false,
  timeRange,
  slowLatencyMs,
  enableAnnotations = true,
  showProvider = true,
  showSponsor = true,
  isFavorite,
  onToggleFavorite,
  onSort,
  onBlockHover,
  onBlockLeave,
  onFilterProvider,
  rpdiagScores,
  rpdiagScoresLoaded = false,
  rpdiagEnabled = true,
  hidePriceColumn = false,
  showVendorColumn = false,
}: StatusTableProps) {
  const { t, i18n } = useTranslation();
  const [isMobile, setIsMobile] = useState(false);

  // 检测是否为平板/移动端（tablet 断点 = max-width:1023px，见 utils/mediaQuery.ts BREAKPOINTS；兼容 Safari ≤13）
  useEffect(() => {
    const cleanup = createMediaQueryEffect('tablet', setIsMobile);
    return cleanup;
  }, []);

  // 排序图标：初始状态下不显示高亮
  const SortIcon = ({ columnKey }: { columnKey: string }) => {
    // 初始状态下所有排序图标都不高亮
    if (isInitialSort || sortConfig.key !== columnKey) {
      return <ArrowUpDown size={14} className="opacity-30 ml-1" />;
    }
    return sortConfig.direction === 'asc' ? (
      <ArrowUp size={14} className="text-accent ml-1" />
    ) : (
      <ArrowDown size={14} className="text-accent ml-1" />
    );
  };

  const currentTimeRange = getTimeRanges(t).find((r) => r.id === timeRange);
  const useLatencyGradient = timeRange === '90m';
  // 质量列总开关：rpdiag 未启用（私有部署）时整列（表头 + 格子 + colgroup + 移动端排序项）消失
  const showQualityColumn = rpdiagEnabled;

  // 移动端：虚拟滚动卡片列表视图
  if (isMobile) {
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
          rpdiagEnabled={showQualityColumn}
          showVendorColumn={showVendorColumn}
        />
        <List
          style={{ height: mobileListHeight, width: '100%' }}
          rowCount={data.length}
          rowHeight={MOBILE_ROW_HEIGHT}
          overscanCount={3}
          rowComponent={MobileRow}
          rowProps={{ data, slowLatencyMs, enableAnnotations, showProvider, showSponsor, useLatencyGradient, isFavorite, onToggleFavorite, onBlockHover, onBlockLeave, rpdiagScores, rpdiagEnabled: showQualityColumn, showVendorColumn }}
        />
      </div>
    );
  }

  // 检查是否有任何注解需要显示
  const hasAnnotations = hasAnyAnnotationInList(data, { enableAnnotations });

  // 桌面端：表格视图
  return (
    <div className="overflow-x-auto rounded-2xl border border-default/50 shadow-xl bg-surface/40 backdrop-blur-sm">
      <table className="w-full text-left border-collapse bg-transparent">
        <colgroup>
          {hasAnnotations && <col className="w-px" />}
          {showProvider && <col className="w-px" />}
          <col className="w-px" /> {/* service */}
          <col className="w-px" /> {/* channel */}
          <col className="w-px" /> {/* model */}
          {showVendorColumn && <col className="w-px" />} {/* modelVendor */}
          {!hidePriceColumn && <col className="w-px" />} {/* priceRatio */}
          <col className="w-px" /> {/* listedDays */}
          <col className="w-px" /> {/* uptime */}
          <col className="w-px" /> {/* lastCheck */}
          {showQualityColumn && <col className="w-px" />} {/* quality */}
          <col className="w-full" /> {/* trend */}
        </colgroup>
        <thead>
          <tr className="border-b border-default/50 text-secondary text-[11px] uppercase">
            {/* 注解列 - 仅在有注解时显示 */}
            {hasAnnotations && (
              <th className="px-1 py-3 font-medium whitespace-nowrap">
                {t('table.headers.annotation')}
              </th>
            )}
            {/* 服务商列（合并赞助者） */}
            {showProvider && (
              <th
                className="px-1.5 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
                onClick={() => onSort('providerName')}
                onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('providerName'))}
                tabIndex={0}
                role="button"
              >
                <div className="flex items-center">
                  {t('table.headers.provider')} <SortIcon columnKey="providerName" />
                </div>
              </th>
            )}
            <th
              className="px-1.5 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
              onClick={() => onSort('serviceType')}
              onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('serviceType'))}
              tabIndex={0}
              role="button"
            >
              <div className="flex items-center">
                {t('table.headers.service')}
                {/* 服务列的语义是**接入协议族**（用哪套 API / 哪个客户端），不是「模型是谁家的」。
                    第一方厂商开放兼容端点后二者解耦（用 Claude 协议跑智谱模型），故在此点明，
                    并把「谁家的模型」指向厂商列。ⓘ 与价格/质量列同款组件。
                    与厂商列同生共死：厂商列不在时这段文案会指向一个看不见的列，反而更费解。 */}
                {showVendorColumn && (
                  <HeaderInfoPopover className="ml-1" align="center" widthClass="w-64">
                    {t('table.headers.serviceTooltip')}
                  </HeaderInfoPopover>
                )}
                <SortIcon columnKey="serviceType" />
              </div>
            </th>
            <th
              className="px-1.5 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
              onClick={() => onSort('channel')}
              onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('channel'))}
              tabIndex={0}
              role="button"
            >
              <div className="flex items-center">
                {t('table.headers.channel')} <SortIcon columnKey="channel" />
              </div>
            </th>
            <th className="px-1.5 py-3 font-medium whitespace-nowrap">
              {t('table.headers.model')}
            </th>
            {/* 模型厂商列：紧邻模型列——两者一起回答「跑的是谁家的什么模型」，
                与左边「服务=接入协议族」正交（服务列表头 ⓘ 解释了这层关系）。 */}
            {showVendorColumn && (
              <th
                className="px-1.5 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
                onClick={() => onSort('modelVendor')}
                onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('modelVendor'))}
                tabIndex={0}
                role="button"
              >
                <div className="flex items-center">
                  {t('table.headers.modelVendor')} <SortIcon columnKey="modelVendor" />
                </div>
              </th>
            )}
            {!hidePriceColumn && (
              <th
                className="px-1.5 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
                onClick={() => onSort('priceRatio')}
                onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('priceRatio'))}
                tabIndex={0}
                role="button"
              >
                <div className="flex items-center">
                  <div className="flex flex-col leading-tight">
                    <span>{t('table.headers.priceRatioLine1')}</span>
                    <span className="text-[10px] opacity-50 font-normal">{t('table.headers.priceRatioLine2')}</span>
                  </div>
                  <HeaderInfoPopover className="ml-1" align="center" widthClass="w-48">
                    {t('table.headers.priceRatioTooltip')}
                  </HeaderInfoPopover>
                  <SortIcon columnKey="priceRatio" />
                </div>
              </th>
            )}
            <th
              className="px-1.5 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
              onClick={() => onSort('listedDays')}
              onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('listedDays'))}
              tabIndex={0}
              role="button"
            >
              <div className="flex items-center">
                <div className="flex flex-col leading-tight">
                  <span>{t('table.headers.listedDaysLine1')}</span>
                  <span className="text-[10px] opacity-50 font-normal">{t('table.headers.listedDaysLine2')}</span>
                </div>
                <SortIcon columnKey="listedDays" />
              </div>
            </th>
            <th
              className="px-1.5 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
              onClick={() => onSort('uptime')}
              onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('uptime'))}
              tabIndex={0}
              role="button"
            >
              <div className="flex items-center">
                {t('table.headers.uptime')} <SortIcon columnKey="uptime" />
              </div>
            </th>
            <th
              className="px-1.5 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
              onClick={() => onSort('lastCheck')}
              onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('lastCheck'))}
              tabIndex={0}
              role="button"
            >
              <div className="flex items-center">
                <div className="flex flex-col leading-tight">
                  <span>{t('table.headers.lastCheckLine1')}</span>
                  <span className="text-[10px] opacity-50 font-normal">{t('table.headers.lastCheckLine2')}</span>
                </div>
                <SortIcon columnKey="lastCheck" />
              </div>
            </th>
            {/* 质量列表头：rpdiag 关闭时整列隐藏；启用时未加载完成前置灰不响应排序，避免空数据触发的伪排序 */}
            {showQualityColumn && (
            <th
              className={`px-1.5 py-3 font-medium whitespace-nowrap focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none ${
                rpdiagScoresLoaded
                  ? 'cursor-pointer hover:text-accent transition-colors'
                  : 'text-muted cursor-not-allowed opacity-60'
              }`}
              onClick={() => rpdiagScoresLoaded && onSort('qualityScore')}
              onKeyDown={(e) => {
                if (!rpdiagScoresLoaded) return;
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault();
                  onSort('qualityScore');
                }
              }}
              tabIndex={rpdiagScoresLoaded ? 0 : -1}
              role="button"
              aria-disabled={rpdiagScoresLoaded ? undefined : true}
            >
              <div className="flex items-center gap-1">
                {t('table.headers.quality', '质量')}
                <HeaderInfoPopover align="right" widthClass="w-56">
                  {/* 文案内 <diag> 占位 → diag.relaypulse.top 外链（新标签打开 rpdiag 站点本身）。 */}
                  <Trans
                    i18nKey="table.headers.qualityTooltip"
                    components={{
                      diag: (
                        <a
                          href="https://diag.relaypulse.top"
                          target="_blank"
                          rel="noopener noreferrer"
                          className="text-accent hover:underline"
                          onClick={(e) => e.stopPropagation()}
                        />
                      ),
                    }}
                  />
                  {/* 了解评分方法 → rpdiag 站点（本站旧 /detect 专题页已下线，内容由 diag 承载） */}
                  <a
                    href="https://diag.relaypulse.top"
                    target="_blank"
                    rel="noopener noreferrer"
                    onClick={(e) => e.stopPropagation()}
                    className="mt-1.5 block text-accent hover:underline font-medium"
                  >
                    {t('table.qualityHintLink')} →
                  </a>
                </HeaderInfoPopover>
                {rpdiagScoresLoaded && <SortIcon columnKey="qualityScore" />}
              </div>
            </th>
            )}
            <th className="pl-1.5 pr-2 py-3 font-medium min-w-[224px]">
              <div className="flex items-center gap-2">
                {t('table.headers.trend')}
                <span className="text-[10px] normal-case opacity-50 border border-default px-1 rounded">
                  {currentTimeRange?.label}
                </span>
              </div>
            </th>
          </tr>
        </thead>
        <tbody className="divide-y divide-default/50 text-sm">
          {data.map((item, rowIndex) => {
            const ServiceIcon = getCachedServiceIcon(item.serviceType);
            const hasItemAnnotations = hasAnyAnnotation(item, { enableAnnotations });
            const pinnedBg = item.pinned ? sponsorLevelToPinnedBgClass(item.sponsorLevel) : '';
            return (
            <tr
              key={item.id}
              className={`group hover:bg-elevated/40 transition-[background-color,color] ${pinnedBg} ${sponsorLevelToBorderClass(item.sponsorLevel)}`}
            >
              {/* 注解列 */}
              {hasAnnotations && (
                <td className="px-1 py-1 whitespace-nowrap">
                  {hasItemAnnotations ? (
                    <AnnotationCell
                      annotations={item.annotations}
                      tooltipPlacement={rowIndex === 0 ? 'bottom' : 'top'}
                    />
                  ) : null}
                </td>
              )}
              {/* 服务商列（两行紧贴，整体垂直居中） */}
              {showProvider && (
                <td className="px-1.5 py-1.5">
                  <div className="flex items-center h-8 group/provider">
                    <div className="flex flex-col gap-0 flex-1 min-w-0 max-w-[13rem]">
                      <div className="flex items-center gap-1.5">
                        <span className="font-medium text-primary text-sm leading-tight truncate">
                          <ExternalLink href={item.providerUrl} inline requireConfirm>{item.providerName}</ExternalLink>
                        </span>
                        {/* 收藏按钮：始终显示，未收藏时弱化 */}
                        <div className="flex-shrink-0">
                          <FavoriteButton
                            isFavorite={isFavorite(item.id)}
                            onToggle={() => onToggleFavorite(item.id)}
                            size={12}
                            inline
                          />
                        </div>
                        {/* 过滤按钮：悬浮时显示 */}
                        {onFilterProvider && (
                          <button
                            type="button"
                            onClick={(e) => {
                              e.stopPropagation();
                              onFilterProvider(item.providerId);
                            }}
                            className="flex-shrink-0 p-0.5 rounded opacity-0 group-hover/provider:opacity-60 hover:!opacity-100 hover:text-accent transition-opacity cursor-pointer"
                            title={t('table.filterByProvider')}
                          >
                            <Filter size={10} />
                          </button>
                        )}
                      </div>
                      {showSponsor && item.sponsor && (
                        <span className="text-[9px] text-muted leading-none">
                          <ExternalLink href={item.sponsorUrl} inline>{item.sponsor}</ExternalLink>
                        </span>
                      )}
                    </div>
                  </div>
                </td>
              )}
              <td className="px-1.5 py-1">
                <span
                  className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-mono border ${
                    item.serviceType === 'cc'
                      ? 'border-service-cc text-service-cc bg-service-cc'
                      : item.serviceType === 'gm'
                      ? 'border-service-gm text-service-gm bg-service-gm'
                      : 'border-service-cx text-service-cx bg-service-cx'
                  }`}
                >
                  {ServiceIcon ? (
                    <ServiceIcon className="w-3.5 h-3.5 mr-1 text-primary" />
                  ) : (
                    <>
                      {item.serviceType === 'cc' && <Zap size={10} className="mr-1 text-primary" />}
                      {item.serviceType === 'cx' && <Shield size={10} className="mr-1 text-primary" />}
                    </>
                  )}
                  {item.serviceName.toUpperCase()}
                </span>
              </td>
              <td className="px-1.5 py-1 text-secondary text-xs">
                <ChannelCell
                  channel={item.channelName || item.channel}
                  probeUrl={item.probeUrl}
                  templateName={item.templateName}
                  coldReason={item.coldReason}
                  boardReason={item.boardReason}
                  boardReasonModels={item.boardReasonModels}
                  className="max-w-[10rem]"
                />
              </td>
              <td className="px-1.5 py-1 text-secondary text-xs max-w-[14rem]">
                {(() => {
                  const models = getModelDisplayList(item.modelEntries);
                  if (models.length === 0) return <span className="text-muted">-</span>;
                  if (models.length === 1) {
                    return (
                      <span className="block truncate" title={getModelTooltip(item.modelEntries)}>
                        {models[0]}
                      </span>
                    );
                  }
                  return (
                    <div className="flex flex-col gap-0.5" title={getModelTooltip(item.modelEntries)}>
                      {models.map((m, i) => (
                        <span key={i} className="block truncate">{m}</span>
                      ))}
                    </div>
                  );
                })()}
              </td>
              {/* 厂商列只出图标不出厂商名：表头已给出列语义，商标本身就是最省宽度的厂商信号，
                  全名留在 title/aria-label 里。未收录 code 无图标时 VendorBadge 自动退回文字。 */}
              {showVendorColumn && (
                <td className="px-1.5 py-1 text-secondary text-xs">
                  {item.modelVendor
                    ? <VendorBadge vendor={item.modelVendor} iconOnly />
                    : <span className="text-muted">-</span>}
                </td>
              )}
              {!hidePriceColumn && (
                <td className="px-1.5 py-1 font-mono text-xs whitespace-nowrap">
                  {(() => {
                    const priceData = formatPriceRatioStructured(item.priceMin, item.priceMax);
                    if (!priceData) return <span className="text-muted">-</span>;
                    return (
                      <div className="flex flex-col leading-tight">
                        <span className="text-secondary">{priceData.base}</span>
                        {priceData.sub && (
                          <span className="text-[10px] text-muted">{priceData.sub}</span>
                        )}
                      </div>
                    );
                  })()}
                </td>
              )}
              <td className="px-1.5 py-1 font-mono text-xs text-secondary whitespace-nowrap">
                {item.listedDays != null ? `${item.listedDays}d` : '-'}
              </td>
              <td className="px-1.5 py-1 font-mono font-bold whitespace-nowrap">
                <span style={{ color: availabilityToColor(item.uptime) }}>
                  {item.uptime >= 0 ? `${item.uptime}%` : '--'}
                </span>
              </td>
              <td className="px-1.5 py-1">
                <div className="flex items-center gap-1.5">
                  <StatusDot status={item.currentStatus} size="sm" />
                  {item.lastCheckTimestamp ? (
                    <div className="text-xs text-secondary font-mono flex flex-col gap-0.5">
                      {item.lastCheckLatency !== undefined && (
                        <span
                          className="text-[10px] font-mono"
                          style={{ color: item.currentStatus === 'UNAVAILABLE' ? 'hsl(var(--text-muted))' : latencyToColor(item.lastCheckLatency, item.slowLatencyMs ?? slowLatencyMs) }}
                        >
                          {item.lastCheckLatency}ms
                        </span>
                      )}
                      <span className="text-[10px] text-muted">{new Date(item.lastCheckTimestamp * 1000).toLocaleString(i18n.language, { hour: '2-digit', minute: '2-digit' })}</span>
                    </div>
                  ) : (
                    <span className="text-muted text-xs">-</span>
                  )}
                </div>
              </td>
              {showQualityColumn && (
              <td className="px-1.5 py-1 whitespace-nowrap">
                {(() => {
                  const rpScore = lookupRpdiagScore(rpdiagScores, [item.providerName, item.providerId], item.serviceType, item.channelName || item.channel, item.channelId);
                  if (rpScore && rpScore.models && rpScore.models.length > 0) {
                    return <QualityScoreCell score={rpScore} />;
                  }
                  if (shouldShowQualityPending({ rpdiagEnabled: showQualityColumn, hasScore: !!rpScore, serviceType: item.serviceType, board: item.board })) {
                    return (
                      <span className="text-muted text-xs" title={t('table.qualityPendingTooltip')}>
                        {t('table.qualityPending')}
                      </span>
                    );
                  }
                  return <span className="text-muted text-xs">-</span>;
                })()}
              </td>
              )}
              <td className="pl-1.5 pr-2 py-1.5 align-middle">
                <div className="flex items-center gap-[2px] h-5 w-full overflow-hidden rounded-sm">
                  {/* 热力图：多层 vs 单层 */}
                  {item.isMultiModel && item.layers ? (
                    // Phase B: 多层垂直堆叠热力图
                    item.history.map((_, idx) => (
                      <LayeredHeatmapBlock
                        key={idx}
                        layers={item.layers!}
                        timeIndex={idx}
                        width={`${100 / item.history.length}%`}
                        height="h-full"
                        onHover={onBlockHover}
                        onLeave={onBlockLeave}
                        isMobile={false}
                        slowLatencyMs={item.slowLatencyMs ?? slowLatencyMs}
                        useLatencyGradient={useLatencyGradient}
                      />
                    ))
                  ) : (
                    // Phase A: 单层传统热力图
                    item.history.map((point, idx) => (
                      <HeatmapBlock
                        key={idx}
                        point={point}
                        width={`${100 / item.history.length}%`}
                        height="h-full"
                        onHover={onBlockHover}
                        onLeave={onBlockLeave}
                        isMobile={false}
                        useLatencyGradient={useLatencyGradient}
                      />
                    ))
                  )}
                </div>
              </td>
            </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

export const StatusTable = memo(StatusTableComponent);
