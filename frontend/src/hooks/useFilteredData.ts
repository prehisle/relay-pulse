import { useMemo } from 'react';
import type { ProcessedMonitorData } from '../types';
import { matchesModelKeys } from '../utils/modelFilter';

/** 顶部状态统计（总数 / 健康数 / 异常数）。 */
export interface MonitorStats {
  total: number;
  healthy: number;
  issues: number;
}

export interface FilteredDataParams {
  /** 已按板块/排序处理过的数据。 */
  data: ProcessedMonitorData[];
  /** 未被筛选器过滤的原始数据。 */
  rawData: ProcessedMonitorData[];
  showFavoritesOnly: boolean;
  favorites: Set<string>;
  filterProvider: string[];
  filterService: string[];
  filterChannel: string[];
  effectiveFilterCategory: string[];
  filterVendor: string[];
  /** 模型筛选值：版本级展示名的 canonical key（见 utils/modelFilter）。 */
  filterModel: string[];
  /** 全板块状态统计（非收藏模式下原样透传）。 */
  stats: MonitorStats;
}

export interface FilteredData {
  /** 计算筛选器选项用的基础数据（不含各筛选器自身的过滤）。 */
  optionsBaseData: ProcessedMonitorData[];
  /** 应用全部筛选器后的最终数据。 */
  filteredData: ProcessedMonitorData[];
  /** 收藏模式下按 filteredData 重算的状态统计。 */
  effectiveStats: MonitorStats;
}

/**
 * 表格数据的筛选链：收藏筛选 → 全筛选器 → 状态统计。
 */
export function useFilteredData({
  data,
  rawData,
  showFavoritesOnly,
  favorites,
  filterProvider,
  filterService,
  filterChannel,
  effectiveFilterCategory,
  filterVendor,
  filterModel,
  stats,
}: FilteredDataParams): FilteredData {
  // 基础数据：应用收藏筛选后的数据（如适用）
  const baseData = useMemo(() => {
    if (!showFavoritesOnly) return data;
    return data.filter(item => favorites.has(item.id));
  }, [data, showFavoritesOnly, favorites]);

  // 选项基础数据：基于 rawData（未被筛选器过滤），用于计算 effectiveXxx
  // 这避免了循环依赖：选择一个 provider 后，其他 provider 仍然可见
  const optionsBaseData = useMemo(() => {
    if (!showFavoritesOnly) return rawData;
    return rawData.filter(item => favorites.has(item.id));
  }, [rawData, showFavoritesOnly, favorites]);

  // 最终过滤后的数据（应用所有筛选器）
  const filteredData = useMemo(() => {
    // 预构建 Set 优化 O(n) includes → O(1) has
    const providerSet = filterProvider.length > 0 ? new Set(filterProvider) : null;
    const serviceSet = filterService.length > 0 ? new Set(filterService) : null;
    const channelSet = filterChannel.length > 0 ? new Set(filterChannel) : null;
    const categorySet = effectiveFilterCategory.length > 0 ? new Set(effectiveFilterCategory) : null;
    const vendorSet = filterVendor.length > 0 ? new Set(filterVendor) : null;
    const modelSet = filterModel.length > 0 ? new Set(filterModel) : null;

    return baseData.filter(item => {
      if (providerSet && !providerSet.has(item.providerId)) return false;
      if (serviceSet && !serviceSet.has(item.serviceType.toLowerCase())) return false;
      if (channelSet && !(item.channel && channelSet.has(item.channel))) return false;
      if (categorySet && !categorySet.has(item.category)) return false;
      // 未声明厂商的通道在厂商筛选生效时排除（与 useMonitorData 的过滤口径逐字一致）
      if (vendorSet && !(item.modelVendor && vendorSet.has(item.modelVendor))) return false;
      // 模型是 any 语义：任一 layer 命中即保留整条通道（口径同样与 useMonitorData 一致）
      if (modelSet && !matchesModelKeys(item.modelEntries, modelSet)) return false;
      return true;
    });
  }, [baseData, filterProvider, filterService, filterChannel, effectiveFilterCategory, filterVendor, filterModel]);

  // 收藏模式下重新计算状态统计（基于 filteredData 而非全板块数据）
  const effectiveStats = useMemo(() => {
    if (!showFavoritesOnly) return stats;
    const total = filteredData.length;
    const healthy = filteredData.filter(i => i.currentStatus === 'AVAILABLE').length;
    return { total, healthy, issues: total - healthy };
  }, [showFavoritesOnly, stats, filteredData]);

  return { optionsBaseData, filteredData, effectiveStats };
}
