import { useState, useEffect, memo } from 'react';
import { StatusTableHead, StatusTableMobile, StatusTableRow, type StatusTableColumns } from './table';
import { createMediaQueryEffect } from '../utils/mediaQuery';
import { hasAnyAnnotationInList } from '../utils/annotationUtils';
import type { ProcessedMonitorData, SortConfig } from '../types';
import type { RpdiagScoresResponse } from '../types/monitor';

type HistoryPoint = ProcessedMonitorData['history'][number];

interface StatusTableProps {
  data: ProcessedMonitorData[];
  sortConfig: SortConfig;
  isInitialSort?: boolean;   // 是否为初始排序状态（控制高亮显示）
  timeRange: string;
  slowLatencyMs: number;
  enableAnnotations?: boolean;      // 注解系统总开关，默认 true
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
  const [isMobile, setIsMobile] = useState(false);

  // 检测是否为平板/移动端（tablet 断点 = max-width:1023px，见 utils/mediaQuery.ts BREAKPOINTS；兼容 Safari ≤13）
  useEffect(() => {
    const cleanup = createMediaQueryEffect('tablet', setIsMobile);
    return cleanup;
  }, []);

  const useLatencyGradient = timeRange === '90m';

  // 移动端：虚拟滚动卡片列表视图
  if (isMobile) {
    return (
      <StatusTableMobile
        data={data}
        sortConfig={sortConfig}
        isInitialSort={isInitialSort}
        onSort={onSort}
        slowLatencyMs={slowLatencyMs}
        enableAnnotations={enableAnnotations}
        showProvider={showProvider}
        showSponsor={showSponsor}
        useLatencyGradient={useLatencyGradient}
        isFavorite={isFavorite}
        onToggleFavorite={onToggleFavorite}
        onBlockHover={onBlockHover}
        onBlockLeave={onBlockLeave}
        rpdiagScores={rpdiagScores}
        rpdiagScoresLoaded={rpdiagScoresLoaded}
        rpdiagEnabled={rpdiagEnabled}
        hidePriceColumn={hidePriceColumn}
        showVendorColumn={showVendorColumn}
      />
    );
  }

  // 列可见性单一真相源：表头（colgroup + thead）与每一行共吃这一个对象，三处不再
  // 各算各的。标注列是数据驱动（本屏无任何标注就不出列），其余四列来自 props /
  // runtime 开关。刻意放在移动分支之后：annotation 那一项要扫一遍 data，而卡片
  // 视图根本不需要它——移动端每 30 秒一轮的渲染不该白扫。
  const columns: StatusTableColumns = {
    annotation: hasAnyAnnotationInList(data, { enableAnnotations }),
    provider: showProvider,
    vendor: showVendorColumn,
    price: !hidePriceColumn,
    // rpdiag 未启用（私有部署）时质量列整列消失：表头 + 格子 + colgroup
    quality: rpdiagEnabled,
  };

  // 桌面端：表格视图
  return (
    <div className="overflow-x-auto rounded-2xl border border-default/50 shadow-xl bg-surface/40 backdrop-blur-sm">
      <table className="w-full text-left border-collapse bg-transparent">
        <StatusTableHead
          columns={columns}
          sortConfig={sortConfig}
          isInitialSort={isInitialSort}
          onSort={onSort}
          rpdiagScoresLoaded={rpdiagScoresLoaded}
          timeRange={timeRange}
        />
        <tbody className="divide-y divide-default/50 text-sm">
          {data.map((item, rowIndex) => (
            <StatusTableRow
              key={item.id}
              item={item}
              rowIndex={rowIndex}
              columns={columns}
              slowLatencyMs={slowLatencyMs}
              enableAnnotations={enableAnnotations}
              showSponsor={showSponsor}
              useLatencyGradient={useLatencyGradient}
              isFavorite={isFavorite}
              onToggleFavorite={onToggleFavorite}
              onFilterProvider={onFilterProvider}
              onBlockHover={onBlockHover}
              onBlockLeave={onBlockLeave}
              rpdiagScores={rpdiagScores}
            />
          ))}
        </tbody>
      </table>
    </div>
  );
}

export const StatusTable = memo(StatusTableComponent);
