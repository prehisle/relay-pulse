import { ArrowUpDown, ArrowUp, ArrowDown } from 'lucide-react';
import { useTranslation, Trans } from 'react-i18next';
import { HeaderInfoPopover } from '../HeaderInfoPopover';
import { getTimeRanges } from '../../constants';
import type { SortConfig } from '../../types';
import type { StatusTableColumns } from './columns';

/** 排序图标：初始排序状态下所有列都不高亮（避免让用户以为自己排过序）。 */
function SortIcon({ columnKey, sortConfig, isInitialSort }: {
  columnKey: string;
  sortConfig: SortConfig;
  isInitialSort: boolean;
}) {
  if (isInitialSort || sortConfig.key !== columnKey) {
    return <ArrowUpDown size={14} className="opacity-30 ml-1" />;
  }
  return sortConfig.direction === 'asc' ? (
    <ArrowUp size={14} className="text-accent ml-1" />
  ) : (
    <ArrowDown size={14} className="text-accent ml-1" />
  );
}

/** 可排序表头的公共外壳：鼠标、Enter、空格三条路径 + 焦点环 + role/tabIndex。
 *  9 个可排序列原本各抄一遍这套键盘与无障碍样板，抄漏一处是看不出来的 a11y 回归。
 *  disabled 供质量列在 rpdiag 分数未加载完成时置灰用（此时不可聚焦、不响应排序，
 *  避免空数据触发的伪排序）。内容整体由调用方给，SortIcon 的位置与显隐也留在调用点。 */
function SortableTh({ sortKey, onSort, disabled = false, children }: {
  sortKey: string;
  onSort: (key: string) => void;
  disabled?: boolean;
  children: React.ReactNode;
}) {
  return (
    <th
      // 两态各写一条完整字面量而不是拼接：两者的 class 顺序照抄抽取前的原文，
      // 使抽取前后渲染出的 DOM 逐字节相同（Tailwind 的类顺序不影响样式，但零 diff
      // 才能让「这次只是搬运」是可证的）。
      className={
        disabled
          ? 'px-1.5 py-3 font-medium whitespace-nowrap focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none text-muted cursor-not-allowed opacity-60'
          : 'px-1.5 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none'
      }
      onClick={() => !disabled && onSort(sortKey)}
      onKeyDown={(e) => {
        if (disabled) return;
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onSort(sortKey);
        }
      }}
      tabIndex={disabled ? -1 : 0}
      role="button"
      aria-disabled={disabled ? true : undefined}
    >
      {children}
    </th>
  );
}

interface StatusTableHeadProps {
  columns: StatusTableColumns;
  sortConfig: SortConfig;
  isInitialSort: boolean;
  onSort: (key: string) => void;
  /** rpdiag 分数是否已加载完成。false 时质量列表头置灰、不响应排序。 */
  rpdiagScoresLoaded: boolean;
  /** 当前时间范围 id，用于趋势列表头右侧那枚范围徽标。 */
  timeRange: string;
}

/** 桌面表格的列骨架：<colgroup> 与 <thead> 一并在此，两者的条件列必须一一对应，
 *  放在同一个组件里才能一眼看出对不对。行侧的第三份在 StatusTableRow。 */
export function StatusTableHead({
  columns,
  sortConfig,
  isInitialSort,
  onSort,
  rpdiagScoresLoaded,
  timeRange,
}: StatusTableHeadProps) {
  const { t } = useTranslation();
  const currentTimeRange = getTimeRanges(t).find((r) => r.id === timeRange);
  const sortIcon = (columnKey: string) => (
    <SortIcon columnKey={columnKey} sortConfig={sortConfig} isInitialSort={isInitialSort} />
  );

  return (
    <>
      <colgroup>
        {columns.annotation && <col className="w-px" />}
        {columns.provider && <col className="w-px" />}
        <col className="w-px" /> {/* service */}
        <col className="w-px" /> {/* channel */}
        <col className="w-px" /> {/* model */}
        {columns.vendor && <col className="w-px" />} {/* modelVendor */}
        {columns.price && <col className="w-px" />} {/* priceRatio */}
        <col className="w-px" /> {/* listedDays */}
        <col className="w-px" /> {/* uptime */}
        <col className="w-px" /> {/* lastCheck */}
        {columns.quality && <col className="w-px" />} {/* quality */}
        <col className="w-full" /> {/* trend */}
      </colgroup>
      <thead>
        <tr className="border-b border-default/50 text-secondary text-[11px] uppercase">
          {/* 注解列 - 仅在有注解时显示 */}
          {columns.annotation && (
            <th className="px-1 py-3 font-medium whitespace-nowrap">
              {t('table.headers.annotation')}
            </th>
          )}
          {/* 服务商列（合并赞助者） */}
          {columns.provider && (
            <SortableTh sortKey="providerName" onSort={onSort}>
              <div className="flex items-center">
                {t('table.headers.provider')} {sortIcon('providerName')}
              </div>
            </SortableTh>
          )}
          <SortableTh sortKey="serviceType" onSort={onSort}>
            <div className="flex items-center">
              {t('table.headers.service')}
              {/* 服务列的语义是**接入协议族**（用哪套 API / 哪个客户端），不是「模型是谁家的」。
                  第一方厂商开放兼容端点后二者解耦（用 Claude 协议跑智谱模型），故在此点明，
                  并把「谁家的模型」指向厂商列。ⓘ 与价格/质量列同款组件。
                  与厂商列同生共死：厂商列不在时这段文案会指向一个看不见的列，反而更费解。 */}
              {columns.vendor && (
                <HeaderInfoPopover className="ml-1" align="center" widthClass="w-64">
                  {t('table.headers.serviceTooltip')}
                </HeaderInfoPopover>
              )}
              {sortIcon('serviceType')}
            </div>
          </SortableTh>
          <SortableTh sortKey="channel" onSort={onSort}>
            <div className="flex items-center">
              {t('table.headers.channel')} {sortIcon('channel')}
            </div>
          </SortableTh>
          <th className="px-1.5 py-3 font-medium whitespace-nowrap">
            {t('table.headers.model')}
          </th>
          {/* 模型厂商列：紧邻模型列——两者一起回答「跑的是谁家的什么模型」，
              与左边「服务=接入协议族」正交（服务列表头 ⓘ 解释了这层关系）。 */}
          {columns.vendor && (
            <SortableTh sortKey="modelVendor" onSort={onSort}>
              <div className="flex items-center">
                {t('table.headers.modelVendor')} {sortIcon('modelVendor')}
              </div>
            </SortableTh>
          )}
          {columns.price && (
            <SortableTh sortKey="priceRatio" onSort={onSort}>
              <div className="flex items-center">
                <div className="flex flex-col leading-tight">
                  <span>{t('table.headers.priceRatioLine1')}</span>
                  <span className="text-[10px] opacity-50 font-normal">{t('table.headers.priceRatioLine2')}</span>
                </div>
                <HeaderInfoPopover className="ml-1" align="center" widthClass="w-48">
                  {t('table.headers.priceRatioTooltip')}
                </HeaderInfoPopover>
                {sortIcon('priceRatio')}
              </div>
            </SortableTh>
          )}
          <SortableTh sortKey="listedDays" onSort={onSort}>
            <div className="flex items-center">
              <div className="flex flex-col leading-tight">
                <span>{t('table.headers.listedDaysLine1')}</span>
                <span className="text-[10px] opacity-50 font-normal">{t('table.headers.listedDaysLine2')}</span>
              </div>
              {sortIcon('listedDays')}
            </div>
          </SortableTh>
          <SortableTh sortKey="uptime" onSort={onSort}>
            <div className="flex items-center">
              {t('table.headers.uptime')} {sortIcon('uptime')}
            </div>
          </SortableTh>
          <SortableTh sortKey="lastCheck" onSort={onSort}>
            <div className="flex items-center">
              <div className="flex flex-col leading-tight">
                <span>{t('table.headers.lastCheckLine1')}</span>
                <span className="text-[10px] opacity-50 font-normal">{t('table.headers.lastCheckLine2')}</span>
              </div>
              {sortIcon('lastCheck')}
            </div>
          </SortableTh>
          {/* 质量列表头：rpdiag 关闭时整列隐藏；启用时未加载完成前置灰不响应排序，避免空数据触发的伪排序 */}
          {columns.quality && (
            <SortableTh sortKey="qualityScore" onSort={onSort} disabled={!rpdiagScoresLoaded}>
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
                {rpdiagScoresLoaded && sortIcon('qualityScore')}
              </div>
            </SortableTh>
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
    </>
  );
}
