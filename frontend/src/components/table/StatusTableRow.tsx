import { Zap, Shield, Filter } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { StatusDot } from '../StatusDot';
import { HeatmapBlock } from '../HeatmapBlock';
import { LayeredHeatmapBlock } from '../LayeredHeatmapBlock';
import { ExternalLink } from '../ExternalLink';
import { FavoriteButton } from '../FavoriteButton';
import { AnnotationCell } from '../annotations';
import { VendorBadge } from '../VendorBadge';
import { ChannelCell } from './ChannelCell';
import { getModelDisplayList, getModelTooltip } from './modelNames';
import { QualityScoreCell, shouldShowQualityPending } from '../quality';
import { getCachedServiceIcon } from '../serviceIconCache';
import { availabilityToColor, latencyToColor, sponsorLevelToBorderClass, sponsorLevelToPinnedBgClass } from '../../utils/color';
import { hasAnyAnnotation } from '../../utils/annotationUtils';
import { formatPriceRatioStructured } from '../../utils/format';
import { lookupRpdiagScore } from '../../hooks/useRpdiagScores';
import type { ProcessedMonitorData } from '../../types';
import type { RpdiagScoresResponse } from '../../types/monitor';
import type { StatusTableColumns } from './columns';

type HistoryPoint = ProcessedMonitorData['history'][number];

interface StatusTableRowProps {
  item: ProcessedMonitorData;
  /** 行序号。只用于把首行的标注浮层翻到下方——朝上会探出表格。 */
  rowIndex: number;
  columns: StatusTableColumns;
  slowLatencyMs: number;
  enableAnnotations: boolean;
  showSponsor: boolean;
  useLatencyGradient: boolean;
  isFavorite: (id: string) => boolean;
  onToggleFavorite: (id: string) => void;
  onFilterProvider?: (providerId: string) => void;
  onBlockHover: (e: React.MouseEvent<HTMLDivElement>, point: HistoryPoint) => void;
  onBlockLeave: () => void;
  rpdiagScores?: RpdiagScoresResponse;
}

/** 桌面表格的一行。单元格顺序必须与 StatusTableHead 的 colgroup/thead 严格一致，
 *  两边共吃同一个 columns 对象；列数守卫见 statusTableColumns.test.tsx。 */
export function StatusTableRow({
  item,
  rowIndex,
  columns,
  slowLatencyMs,
  enableAnnotations,
  showSponsor,
  useLatencyGradient,
  isFavorite,
  onToggleFavorite,
  onFilterProvider,
  onBlockHover,
  onBlockLeave,
  rpdiagScores,
}: StatusTableRowProps) {
  const { t, i18n } = useTranslation();
  const ServiceIcon = getCachedServiceIcon(item.serviceType);
  const hasItemAnnotations = hasAnyAnnotation(item, { enableAnnotations });
  const pinnedBg = item.pinned ? sponsorLevelToPinnedBgClass(item.sponsorLevel) : '';

  return (
    <tr
      className={`group hover:bg-elevated/40 transition-[background-color,color] ${pinnedBg} ${sponsorLevelToBorderClass(item.sponsorLevel)}`}
    >
      {/* 注解列 */}
      {columns.annotation && (
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
      {columns.provider && (
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
      {columns.vendor && (
        <td className="px-1.5 py-1 text-secondary text-xs">
          {item.modelVendor
            ? <VendorBadge vendor={item.modelVendor} iconOnly />
            : <span className="text-muted">-</span>}
        </td>
      )}
      {columns.price && (
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
      {columns.quality && (
        <td className="px-1.5 py-1 whitespace-nowrap">
          {(() => {
            const rpScore = lookupRpdiagScore(rpdiagScores, [item.providerName, item.providerId], item.serviceType, item.channelName || item.channel, item.channelId);
            if (rpScore && rpScore.models && rpScore.models.length > 0) {
              return <QualityScoreCell score={rpScore} />;
            }
            if (shouldShowQualityPending({ rpdiagEnabled: columns.quality, hasScore: !!rpScore, serviceType: item.serviceType, board: item.board })) {
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
}
