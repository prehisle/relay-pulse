import { useEffect, useMemo } from 'react';

export interface EffectiveFavoritesParams {
  loading: boolean;
  error: string | null;
  /** 本地收藏集合。 */
  favorites: Set<string>;
  /** 本地收藏数量（未与后端全量列表求交）。 */
  favoritesCount: number;
  /** 后端下发的跨板块全量监控项 id。 */
  allMonitorIds: Set<string>;
  /** 后端是否支持 all_monitor_ids（旧后端缺失该字段）。 */
  allMonitorIdsSupported: boolean;
  cleanupMissingFavorites: (allMonitorIds: Set<string>) => void;
}

/**
 * 有效收藏计数（favorites ∩ allMonitorIds），并静默清理已从配置中删除的收藏。
 */
export function useEffectiveFavorites({
  loading,
  error,
  favorites,
  favoritesCount,
  allMonitorIds,
  allMonitorIdsSupported,
  cleanupMissingFavorites,
}: EffectiveFavoritesParams): number {
  // 有效收藏计数：favorites ∩ allMonitorIds
  // - loading/error 时回退到本地数量，避免短暂显示 0
  // - 旧后端不支持 all_monitor_ids 时也回退
  const effectiveFavoritesCount = useMemo(() => {
    if (loading || error) return favoritesCount;
    if (!allMonitorIdsSupported) return favoritesCount; // 旧后端不支持
    if (favoritesCount === 0) return 0;

    let count = 0;
    favorites.forEach((id) => {
      if (allMonitorIds.has(id)) count++;
    });
    return count;
  }, [loading, error, favorites, favoritesCount, allMonitorIds, allMonitorIdsSupported]);

  // 静默清理无效收藏：移除已从配置中删除的监控项
  // - 仅在 API 成功返回且后端支持 all_monitor_ids 时执行
  // - allMonitorIds 是跨板块的全量列表，不会误删移动板块的收藏
  useEffect(() => {
    if (loading || error) return;
    if (!allMonitorIdsSupported) return; // 旧后端不支持，跳过
    if (favorites.size === 0) return;

    cleanupMissingFavorites(allMonitorIds);
  }, [loading, error, allMonitorIds, allMonitorIdsSupported, favorites.size, cleanupMissingFavorites]);

  return effectiveFavoritesCount;
}
