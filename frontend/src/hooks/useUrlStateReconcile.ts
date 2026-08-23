import { useEffect } from 'react';
import type { BoardFilter } from '../types';

export interface UrlStateReconcileParams {
  /** 运行时开关：价格列已隐藏。 */
  hidePriceColumn: boolean;
  /** 运行时开关：rpdiag 质量列可用。 */
  rpdiagEnabled: boolean;
  /** 板块配置是否已由 API 返回（未返回前不得改写 URL）。 */
  boardsEnabledLoaded: boolean;
  boardsEnabled: boolean;
  board: BoardFilter;
  setBoard: (board: BoardFilter) => void;
  /** 未筛选数据的条数——厂商列判定的首屏闸。 */
  rawDataLength: number;
  /** 厂商列是否可见（由 rawData 派生）。 */
  showVendorColumn: boolean;
  clearPriceRatioSort: () => void;
  clearQualityScoreSort: () => void;
  clearModelVendorSort: () => void;
}

/**
 * 运行时开关 ↔ URL 状态的归一：列被隐藏后抹掉指向它的旧排序、板块功能关闭后归一 board。
 *
 * 四条 effect 刻意保持独立（不合并成一个依赖并集的 effect），依赖数组与守卫逐字保留。
 */
export function useUrlStateReconcile({
  hidePriceColumn,
  rpdiagEnabled,
  boardsEnabledLoaded,
  boardsEnabled,
  board,
  setBoard,
  rawDataLength,
  showVendorColumn,
  clearPriceRatioSort,
  clearQualityScoreSort,
  clearModelVendorSort,
}: UrlStateReconcileParams): void {
  // 运行时 hide_price_column 切到 true 后，主动抹掉旧的 priceRatio_* URL 排序，
  // 避免点 hide 后用户带着隐藏列的"按价格排序"链接刷新仍触发该排序。
  // 使用 clearPriceRatioSort（不写 hasManualSort=true），刷新仍可恢复置顶语义。
  useEffect(() => {
    if (hidePriceColumn) {
      clearPriceRatioSort();
    }
  }, [hidePriceColumn, clearPriceRatioSort]);

  // 同理：rpdiag 关闭（私有部署）后质量列消失，抹掉残留的 qualityScore_* URL 排序，
  // 避免带"按质量排序"链接刷新仍指向已隐藏的列。
  useEffect(() => {
    if (!rpdiagEnabled) {
      clearQualityScoreSort();
    }
  }, [rpdiagEnabled, clearQualityScoreSort]);

  // 板块功能禁用时，自动归一 board 到 hot
  // 解决：用户手动输入 ?board=cold 但功能未启用时的 URL 混乱问题
  // 注意：仅当 API 已返回板块配置后才执行，避免在初始加载时覆盖 URL 参数
  useEffect(() => {
    if (!boardsEnabledLoaded) return;  // API 未返回前不执行，尊重 URL 参数
    if (!boardsEnabled && board !== 'hot') {
      setBoard('hot');
    }
  }, [boardsEnabledLoaded, boardsEnabled, board, setBoard]);

  // 同理：厂商列不可见时抹掉残留的 modelVendor_* URL 排序。
  // ⚠️ 必须等 rawData 到货再判——首屏 rawData 为空时 showVendorColumn 恒 false，
  // 不加这道闸会把用户带 ?sort=modelVendor_asc 的分享链接在数据到达前就清掉。
  useEffect(() => {
    if (rawDataLength > 0 && !showVendorColumn) {
      clearModelVendorSort();
    }
  }, [rawDataLength, showVendorColumn, clearModelVendorSort]);
}
