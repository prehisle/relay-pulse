import { useEffect } from 'react';
import { trackPeriodChange, trackServiceFilter, trackEvent } from '../utils/analytics';
import type { ViewMode } from '../types';

export interface HomeAnalyticsParams {
  timeRange: string;
  filterProvider: string[];
  filterService: string[];
  filterChannel: string[];
  filterVendor: string[];
  filterModel: string[];
  effectiveFilterCategory: string[];
  /** 实际显示的视图模式（移动端/截图模式会强制 table）。 */
  effectiveViewMode: ViewMode;
}

/**
 * 首页的筛选/视图埋点。
 *
 * ⚠️ 六条 effect 的声明顺序即事件上报顺序（多项状态同一次变化时可观测），
 * 不要合并、不要调换。
 */
export function useHomeAnalytics({
  timeRange,
  filterProvider,
  filterService,
  filterChannel,
  filterVendor,
  filterModel,
  effectiveFilterCategory,
  effectiveViewMode,
}: HomeAnalyticsParams): void {
  // 追踪时间范围变化
  useEffect(() => {
    trackPeriodChange(timeRange);
  }, [timeRange]);

  // 追踪服务筛选变化
  useEffect(() => {
    trackServiceFilter(
      filterProvider.length > 0 ? filterProvider.join(',') : undefined,
      filterService.length > 0 ? filterService.join(',') : undefined
    );
  }, [filterProvider, filterService]);

  // 追踪通道筛选变化
  useEffect(() => {
    if (filterChannel.length > 0) {
      trackEvent('filter_channel', { channel: filterChannel.join(',') });
    }
  }, [filterChannel]);

  // 追踪厂商筛选变化
  useEffect(() => {
    if (filterVendor.length > 0) {
      trackEvent('filter_model_vendor', { vendor: filterVendor.join(',') });
    }
  }, [filterVendor]);

  useEffect(() => {
    if (filterModel.length > 0) {
      trackEvent('filter_model', { model: filterModel.join(',') });
    }
  }, [filterModel]);

  // 追踪分类筛选变化
  useEffect(() => {
    if (effectiveFilterCategory.length > 0) {
      trackEvent('filter_category', { category: effectiveFilterCategory.join(',') });
    }
  }, [effectiveFilterCategory]);

  // 追踪视图模式切换（使用实际显示的视图模式）
  useEffect(() => {
    trackEvent('change_view_mode', { mode: effectiveViewMode });
  }, [effectiveViewMode]);
}
