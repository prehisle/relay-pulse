import { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import { useLocation } from 'react-router-dom';
import { Server } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Helmet } from 'react-helmet-async';
import { Header } from './components/Header';
import { Controls } from './components/Controls';
import { StatusTable } from './components/StatusTable';
import { StatusCard } from './components/StatusCard';
import { vendorLabel } from './components/VendorBadge';
import { Tooltip } from './components/Tooltip';
import { Footer } from './components/Footer';
import { EmptyFavorites } from './components/EmptyFavorites';
import { AnnouncementsBanner } from './components/AnnouncementsBanner';
import { useMonitorData } from './hooks/useMonitorData';
import { useSeoMeta } from './hooks/useSeoMeta';
import { useUrlState } from './hooks/useUrlState';
import { useFavorites } from './hooks/useFavorites';
import { useAnnouncements } from './hooks/useAnnouncements';
import { useRpdiagScores } from './hooks/useRpdiagScores';
import { createMediaQueryEffect } from './utils/mediaQuery';
import { trackPeriodChange, trackServiceFilter, trackEvent } from './utils/analytics';
import type { MultiSelectOption } from './components/MultiSelect';
import type { TooltipState, ProcessedMonitorData, ChannelOption } from './types';

// localStorage key for time align preference
const STORAGE_KEY_TIME_ALIGN = 'relay-pulse-time-align';

function App() {
  const { t, i18n } = useTranslation();
  const location = useLocation();
  const seo = useSeoMeta({ pathname: location.pathname, language: i18n.language });

  // 检测截图模式
  const isScreenshotMode = useMemo(() => {
    return new URLSearchParams(location.search).get('screenshot') === '1';
  }, [location.search]);

  // 截图模式下强制使用 default-dark 主题
  useEffect(() => {
    if (!isScreenshotMode) return;
    const root = document.documentElement;
    root.setAttribute('data-theme', 'default-dark');
    root.style.colorScheme = 'dark';
  }, [isScreenshotMode]);

  // 截图时间戳（组件挂载时记录）
  const screenshotTimestamp = useMemo(() => {
    if (!isScreenshotMode) return '';
    return new Date().toLocaleString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
      timeZone: 'Asia/Shanghai',
    });
  }, [isScreenshotMode]);

  // 截图标题（群名专属标识）
  const screenshotTitle = useMemo(() => {
    if (!isScreenshotMode) return '';
    const raw = new URLSearchParams(location.search).get('title') || '';
    // 清理控制字符，限制长度
    const cleaned = raw.replace(/[\r\n\t]+/g, ' ').trim();
    const chars = Array.from(cleaned);
    if (chars.length > 60) return chars.slice(0, 60).join('') + '…';
    return cleaned;
  }, [isScreenshotMode, location.search]);

  // 使用 URL 状态同步 Hook，支持收藏和分享
  const [urlState, urlActions] = useUrlState();
  const {
    timeRange,
    timeFilter,      // 每日时段过滤
    board,           // 板块：hot/secondary/cold/all
    filterProvider,
    filterService,
    filterChannel,
    filterCategory,
    filterVendor,
    showFavoritesOnly,  // 仅显示收藏
    viewMode,
    sortConfig,
    isInitialSort,  // 是否为初始排序状态（用于赞助商置顶）
  } = urlState;

  // 移动端筛选抽屉状态（移到 App 层级，Header 和 Controls 共用）
  const [showFilterDrawer, setShowFilterDrawer] = useState(false);
  const {
    setTimeRange,
    setTimeFilter,   // 每日时段过滤
    setBoard,        // 切换板块
    setFilterProvider,
    setFilterService,
    setFilterChannel,
    setFilterCategory,
    setFilterVendor,
    setViewMode,
    setSortConfig,
    clearPriceRatioSort,
    clearQualityScoreSort,
    clearModelVendorSort,
    enterFavoritesMode,  // 进入收藏模式（保存快照）
    exitFavoritesMode,   // 退出收藏模式（恢复快照）
  } = urlActions;

  // 收藏管理 Hook
  const { favorites, isFavorite, toggleFavorite, cleanupMissingFavorites, count: favoritesCount } = useFavorites();

  // 公告通知 Hook（截图模式下禁用，避免不必要的网络请求）
  const {
    data: announcementsData,
    loading: announcementsLoading,
    shouldShowBanner: shouldShowAnnouncementsBanner,
    dismiss: dismissAnnouncements,
  } = useAnnouncements(!isScreenshotMode);

  // 时间对齐模式（使用 localStorage 持久化，不影响分享链接）
  const [timeAlign, setTimeAlignState] = useState<string>(() => {
    if (typeof window === 'undefined') return '';
    return localStorage.getItem(STORAGE_KEY_TIME_ALIGN) ?? 'hour';
  });

  // 包装 setter 以同步到 localStorage
  const setTimeAlign = useCallback((align: string) => {
    setTimeAlignState(align);
    if (typeof window !== 'undefined') {
      if (align) {
        localStorage.setItem(STORAGE_KEY_TIME_ALIGN, align);
      } else {
        localStorage.removeItem(STORAGE_KEY_TIME_ALIGN);
      }
    }
    // 追踪时间对齐模式变化
    trackEvent('change_time_align', { align: align || 'dynamic' });
  }, []);

  // 移动端检测（< 960px）
  const [isMobile, setIsMobile] = useState(false);
  useEffect(() => {
    const cleanup = createMediaQueryEffect('tablet', setIsMobile);
    return cleanup;
  }, []);

  // 移动端强制使用 table 视图，截图模式也强制 table
  const effectiveViewMode = isScreenshotMode ? 'table' : (isMobile ? 'table' : viewMode);

  const [tooltip, setTooltip] = useState<TooltipState>({
    show: false,
    x: 0,
    y: 0,
    data: null,
  });

  // 刷新冷却状态（5秒内重复刷新显示提示）
  const REFRESH_COOLDOWN_MS = 5000;
  const lastRefreshRef = useRef<number>(0);
  const [refreshCooldown, setRefreshCooldown] = useState(false);

  // 自动刷新开关（持久化到 localStorage，默认开启）
  const AUTO_REFRESH_KEY = 'relay-pulse-auto-refresh';
  const [autoRefresh, setAutoRefresh] = useState(() => {
    try {
      const stored = localStorage.getItem(AUTO_REFRESH_KEY);
      if (stored === null) return true; // 无值时默认开启
      return stored === 'true'; // 有值则尊重用户选择
    } catch {
      return true; // 异常也默认开启
    }
  });

  // 切换自动刷新并持久化
  const handleToggleAutoRefresh = () => {
    setAutoRefresh(prev => {
      const next = !prev;
      try {
        localStorage.setItem(AUTO_REFRESH_KEY, String(next));
      } catch {
        // ignore
      }
      return next;
    });
  };

  const { scores: rpdiagScores, loaded: rpdiagScoresLoaded } = useRpdiagScores();

  const { loading, error, data, rawData, stats, providers, slowLatencyMs, enableAnnotations, boardsEnabled, boardsEnabledLoaded, boardCounts, allMonitorIds, allMonitorIdsSupported, hidePriceColumn, rpdiagEnabled, refetch } = useMonitorData({
    timeRange,
    timeAlign,
    timeFilter,
    board,
    filterService,
    filterProvider,
    filterChannel,
    filterCategory,
    filterVendor,
    sortConfig,
    isInitialSort,
    // 冷板数据不更新，禁用自动刷新以节省资源
    autoRefresh: autoRefresh && board !== 'cold',
    rpdiagScores,
    rpdiagScoresLoaded,
  });

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

  // 统计激活的筛选器数量（用于移动端 Header 显示）
  const activeFiltersCount = [
    showFavoritesOnly,
    filterCategory.length > 0,
    providers.length > 0 && filterProvider.length > 0,
    filterService.length > 0,
    filterChannel.length > 0,
    filterVendor.length > 0,
  ].filter(Boolean).length;

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
    const categorySet = filterCategory.length > 0 ? new Set(filterCategory) : null;
    const vendorSet = filterVendor.length > 0 ? new Set(filterVendor) : null;

    return baseData.filter(item => {
      if (providerSet && !providerSet.has(item.providerId)) return false;
      if (serviceSet && !serviceSet.has(item.serviceType.toLowerCase())) return false;
      if (channelSet && !(item.channel && channelSet.has(item.channel))) return false;
      if (categorySet && !categorySet.has(item.category)) return false;
      // 未声明厂商的通道在厂商筛选生效时排除（与 useMonitorData 的过滤口径逐字一致）
      if (vendorSet && !(item.modelVendor && vendorSet.has(item.modelVendor))) return false;
      return true;
    });
  }, [baseData, filterProvider, filterService, filterChannel, filterCategory, filterVendor]);

  // 收藏模式下重新计算状态统计（基于 filteredData 而非全板块数据）
  const effectiveStats = useMemo(() => {
    if (!showFavoritesOnly) return stats;
    const total = filteredData.length;
    const healthy = filteredData.filter(i => i.currentStatus === 'AVAILABLE').length;
    return { total, healthy, issues: total - healthy };
  }, [showFavoritesOnly, stats, filteredData]);

  // 动态 Provider 选项：联动筛选 + 保留已选项
  const effectiveProviders = useMemo(() => {
    // 预构建 Set 优化查询性能
    const serviceSet = filterService.length > 0 ? new Set(filterService) : null;
    const channelSet = filterChannel.length > 0 ? new Set(filterChannel) : null;
    const categorySet = filterCategory.length > 0 ? new Set(filterCategory) : null;
    const vendorSet = filterVendor.length > 0 ? new Set(filterVendor) : null;
    const providerSet = new Set(filterProvider);

    // 1. 应用其他筛选条件（不包括 provider 自身）
    const filtered = optionsBaseData.filter(item => {
      if (serviceSet && !serviceSet.has(item.serviceType.toLowerCase())) return false;
      if (channelSet && !(item.channel && channelSet.has(item.channel))) return false;
      if (categorySet && !categorySet.has(item.category)) return false;
      if (vendorSet && !(item.modelVendor && vendorSet.has(item.modelVendor))) return false;
      return true;
    });

    // 2. 收集当前可用的 provider（带计数）
    const availableMap = new Map<string, { label: string; count: number }>();
    filtered.forEach(item => {
      if (!availableMap.has(item.providerId)) {
        availableMap.set(item.providerId, { label: item.providerName, count: 1 });
      } else {
        availableMap.get(item.providerId)!.count++;
      }
    });

    // 3. 确保已选的 provider 始终可见（从全量数据中补充 label）
    filterProvider.forEach(providerId => {
      if (!availableMap.has(providerId)) {
        const item = optionsBaseData.find(d => d.providerId === providerId);
        if (item) {
          availableMap.set(providerId, { label: item.providerName, count: 0 });
        }
      }
    });

    // 4. 转换为选项数组，标记无数据的已选项
    return Array.from(availableMap.entries())
      .sort((a, b) => a[1].label.localeCompare(b[1].label, 'zh-CN'))
      .map(([value, { label, count }]) => ({
        value,
        label: count === 0 && providerSet.has(value) ? `${label} (0)` : label,
      }));
  }, [optionsBaseData, filterService, filterChannel, filterCategory, filterVendor, filterProvider]);

  // 动态 Service 选项：联动筛选 + 保留已选项
  const effectiveServices = useMemo(() => {
    // 预构建 Set 优化查询性能
    const providerSet = filterProvider.length > 0 ? new Set(filterProvider) : null;
    const channelSet = filterChannel.length > 0 ? new Set(filterChannel) : null;
    const categorySet = filterCategory.length > 0 ? new Set(filterCategory) : null;
    const vendorSet = filterVendor.length > 0 ? new Set(filterVendor) : null;
    const serviceSet = new Set(filterService);

    // 1. 应用其他筛选条件（不包括 service 自身）
    const filtered = optionsBaseData.filter(item => {
      if (providerSet && !providerSet.has(item.providerId)) return false;
      if (channelSet && !(item.channel && channelSet.has(item.channel))) return false;
      if (categorySet && !categorySet.has(item.category)) return false;
      if (vendorSet && !(item.modelVendor && vendorSet.has(item.modelVendor))) return false;
      return true;
    });

    // 2. 收集当前可用的 service（带计数）
    const availableMap = new Map<string, number>();
    filtered.forEach(item => {
      const service = item.serviceType.toLowerCase();
      availableMap.set(service, (availableMap.get(service) || 0) + 1);
    });

    // 3. 确保已选的 service 始终可见
    filterService.forEach(service => {
      if (!availableMap.has(service)) {
        availableMap.set(service, 0);
      }
    });

    // 4. 转换为数组，标记无数据的已选项
    return Array.from(availableMap.entries())
      .sort((a, b) => a[0].localeCompare(b[0]))
      .map(([value, count]) =>
        count === 0 && serviceSet.has(value) ? `${value} (0)` : value
      );
  }, [optionsBaseData, filterProvider, filterChannel, filterCategory, filterVendor, filterService]);

  // 动态 Channel 选项：联动筛选 + 保留已选项
  const effectiveChannels = useMemo<ChannelOption[]>(() => {
    // 预构建 Set 优化查询性能
    const providerSet = filterProvider.length > 0 ? new Set(filterProvider) : null;
    const serviceSet = filterService.length > 0 ? new Set(filterService) : null;
    const categorySet = filterCategory.length > 0 ? new Set(filterCategory) : null;
    const vendorSet = filterVendor.length > 0 ? new Set(filterVendor) : null;
    const channelSet = new Set(filterChannel);

    // 1. 应用其他筛选条件（不包括 channel 自身）
    const filtered = optionsBaseData.filter(item => {
      if (providerSet && !providerSet.has(item.providerId)) return false;
      if (serviceSet && !serviceSet.has(item.serviceType.toLowerCase())) return false;
      if (categorySet && !categorySet.has(item.category)) return false;
      if (vendorSet && !(item.modelVendor && vendorSet.has(item.modelVendor))) return false;
      return true;
    });

    // 2. 收集当前可用的 channel（带计数）+ channelName 映射
    const availableMap = new Map<string, { count: number; label: string }>();
    filtered.forEach(item => {
      if (item.channel) {
        const existing = availableMap.get(item.channel);
        if (existing) {
          existing.count++;
        } else {
          availableMap.set(item.channel, {
            count: 1,
            label: item.channelName || item.channel,
          });
        }
      }
    });

    // 3. 确保已选的 channel 始终可见（从全量数据中查找 channelName）
    filterChannel.forEach(channel => {
      if (!availableMap.has(channel)) {
        // 从全量数据中查找 channelName
        const found = optionsBaseData.find(item => item.channel === channel);
        availableMap.set(channel, {
          count: 0,
          label: found?.channelName || channel,
        });
      }
    });

    // 4. 转换为 ChannelOption[]，按 label 排序，标记无数据的已选项
    return Array.from(availableMap.entries())
      .sort((a, b) => a[1].label.localeCompare(b[1].label, 'zh-CN'))
      .map(([value, { count, label }]) => ({
        value,
        label: count === 0 && channelSet.has(value) ? `${label} (0)` : label,
      }));
  }, [optionsBaseData, filterProvider, filterService, filterCategory, filterVendor, filterChannel]);

  // 动态 Category 选项：联动筛选 + 保留已选项
  const effectiveCategories = useMemo(() => {
    // 预构建 Set 优化查询性能
    const providerSet = filterProvider.length > 0 ? new Set(filterProvider) : null;
    const serviceSet = filterService.length > 0 ? new Set(filterService) : null;
    const channelSet = filterChannel.length > 0 ? new Set(filterChannel) : null;
    const vendorSet = filterVendor.length > 0 ? new Set(filterVendor) : null;
    const categorySet = new Set(filterCategory);

    // 1. 应用其他筛选条件（不包括 category 自身）
    const filtered = optionsBaseData.filter(item => {
      if (providerSet && !providerSet.has(item.providerId)) return false;
      if (serviceSet && !serviceSet.has(item.serviceType.toLowerCase())) return false;
      if (channelSet && !(item.channel && channelSet.has(item.channel))) return false;
      if (vendorSet && !(item.modelVendor && vendorSet.has(item.modelVendor))) return false;
      return true;
    });

    // 2. 收集当前可用的 category（带计数）
    const availableMap = new Map<string, number>();
    filtered.forEach(item => {
      availableMap.set(item.category, (availableMap.get(item.category) || 0) + 1);
    });

    // 3. 确保已选的 category 始终可见
    filterCategory.forEach(category => {
      if (!availableMap.has(category)) {
        availableMap.set(category, 0);
      }
    });

    // 4. 转换为数组，标记无数据的已选项
    return Array.from(availableMap.entries())
      .sort((a, b) => a[0].localeCompare(b[0]))
      .map(([value, count]) =>
        count === 0 && categorySet.has(value) ? `${value} (0)` : value
      );
  }, [optionsBaseData, filterProvider, filterService, filterChannel, filterVendor, filterCategory]);

  // 动态模型厂商选项：联动筛选 + 保留已选项。
  // 与 provider 选项同款是 {value,label} 结构（value=受控 code，label=本地化厂商名），
  // 按 label 排序——下拉里用户读的是名字；表格排序那侧按 code（见 sortMonitors）。
  const effectiveVendors = useMemo<MultiSelectOption[]>(() => {
    const providerSet = filterProvider.length > 0 ? new Set(filterProvider) : null;
    const serviceSet = filterService.length > 0 ? new Set(filterService) : null;
    const channelSet = filterChannel.length > 0 ? new Set(filterChannel) : null;
    const categorySet = filterCategory.length > 0 ? new Set(filterCategory) : null;
    const vendorSet = new Set(filterVendor);

    // 1. 应用其他筛选条件（不包括 vendor 自身）
    const filtered = optionsBaseData.filter(item => {
      if (providerSet && !providerSet.has(item.providerId)) return false;
      if (serviceSet && !serviceSet.has(item.serviceType.toLowerCase())) return false;
      if (channelSet && !(item.channel && channelSet.has(item.channel))) return false;
      if (categorySet && !categorySet.has(item.category)) return false;
      return true;
    });

    // 2. 收集当前可用的 vendor（带计数）
    const availableMap = new Map<string, number>();
    filtered.forEach(item => {
      if (item.modelVendor) {
        availableMap.set(item.modelVendor, (availableMap.get(item.modelVendor) || 0) + 1);
      }
    });

    // 3. 确保已选的 vendor 始终可见
    filterVendor.forEach(vendor => {
      if (!availableMap.has(vendor)) availableMap.set(vendor, 0);
    });

    // 4. 转换为选项数组（本地化 label），标记无数据的已选项
    return Array.from(availableMap.entries())
      .map(([value, count]) => ({
        value,
        label: count === 0 && vendorSet.has(value) ? `${vendorLabel(t, value)} (0)` : vendorLabel(t, value),
      }))
      .sort((a, b) => a.label.localeCompare(b.label, 'zh-CN'));
  }, [optionsBaseData, filterProvider, filterService, filterChannel, filterCategory, filterVendor, t]);

  // 厂商列显隐：基于**未筛选**的全量数据判定，避免"筛一下列就冒出来/消失"的布局抖动。
  // Phase 3 回填厂商前恒 false —— 整列与筛选器对用户完全不存在。
  const showVendorColumn = useMemo(() => rawData.some(item => !!item.modelVendor), [rawData]);

  // 同理：厂商列不可见时抹掉残留的 modelVendor_* URL 排序。
  // ⚠️ 必须等 rawData 到货再判——首屏 rawData 为空时 showVendorColumn 恒 false，
  // 不加这道闸会把用户带 ?sort=modelVendor_asc 的分享链接在数据到达前就清掉。
  useEffect(() => {
    if (rawData.length > 0 && !showVendorColumn) {
      clearModelVendorSort();
    }
  }, [rawData.length, showVendorColumn, clearModelVendorSort]);

  // 收藏模式切换（使用事务性方法，保存/恢复筛选状态快照）
  const handleFavoritesModeChange = useCallback((enabled: boolean) => {
    if (enabled) {
      enterFavoritesMode();
    } else {
      exitFavoritesMode();
    }
  }, [enterFavoritesMode, exitFavoritesMode]);

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

  // 追踪分类筛选变化
  useEffect(() => {
    if (filterCategory.length > 0) {
      trackEvent('filter_category', { category: filterCategory.join(',') });
    }
  }, [filterCategory]);

  // 追踪视图模式切换（使用实际显示的视图模式）
  useEffect(() => {
    trackEvent('change_view_mode', { mode: effectiveViewMode });
  }, [effectiveViewMode]);

  const handleSort = (key: string) => {
    let direction: 'asc' | 'desc' = 'desc';
    // 初始状态（置顶模式）下，首次点击任何排序都使用降序
    // 非初始状态下，点击同一字段切换升降序
    if (!isInitialSort && sortConfig.key === key && sortConfig.direction === 'desc') {
      direction = 'asc';
    }
    setSortConfig({ key, direction });
  };

  const handleBlockHover = useCallback((
    e: React.MouseEvent<HTMLDivElement>,
    point: ProcessedMonitorData['history'][number]
  ) => {
    const rect = e.currentTarget.getBoundingClientRect();
    setTooltip({
      show: true,
      x: rect.left + rect.width / 2,
      y: rect.top - 10,
      blockBottom: rect.bottom + 10,
      data: point,
    });
  }, []);

  const handleBlockLeave = useCallback(() => {
    setTooltip((prev) => ({ ...prev, show: false }));
  }, []);

  const handleRefresh = () => {
    const now = Date.now();
    const elapsed = now - lastRefreshRef.current;

    if (elapsed < REFRESH_COOLDOWN_MS) {
      // 冷却中，显示提示
      setRefreshCooldown(true);
      setTimeout(() => setRefreshCooldown(false), 2000); // 提示显示 2 秒
      return;
    }

    lastRefreshRef.current = now;
    trackEvent('manual_refresh');
    refetch(true); // 绕过浏览器缓存
  };

  return (
    <>
      {/* 动态更新 HTML meta 标签（canonical/hreflang 由后端 SSR 注入，避免重复） */}
      <Helmet>
        <html lang={seo.htmlLang} />
        <title>{t('meta.title')}</title>
        <meta name="description" content={t('meta.description')} />
        {/* 截图模式禁用所有动画 */}
        {isScreenshotMode && (
          <style>{`
            *, *::before, *::after {
              animation: none !important;
              transition: none !important;
            }
          `}</style>
        )}
      </Helmet>

      <div
        className={isScreenshotMode
          ? "bg-page text-primary font-sans selection-accent overflow-x-hidden"
          : "min-h-screen bg-page text-primary font-sans selection-accent overflow-x-hidden"
        }
        data-ready={isScreenshotMode && !loading ? 'true' : undefined}
        data-error={isScreenshotMode && error ? error : undefined}
      >
        {/* 全局 Tooltip - 截图模式下隐藏 */}
        {!isScreenshotMode && (
          <Tooltip tooltip={tooltip} onClose={handleBlockLeave} slowLatencyMs={slowLatencyMs} timeRange={timeRange} />
        )}

        {/* 背景装饰 - 截图模式下隐藏 */}
        {!isScreenshotMode && (
          <div className="fixed top-0 left-0 w-full h-full overflow-hidden pointer-events-none z-0">
            <div className="absolute top-[-10%] right-[-10%] w-[600px] h-[600px] bg-accent/10 rounded-full blur-[120px]" />
            <div className="absolute bottom-[-10%] left-[-10%] w-[600px] h-[600px] bg-accent/10 rounded-full blur-[120px]" />
          </div>
        )}

        <div className={isScreenshotMode
          ? "relative z-10 w-[1200px] mx-auto px-4 py-4"
          : "relative z-10 max-w-7xl mx-auto px-4 py-4 sm:py-6 sm:px-6 lg:px-8"
        }>
          {/* 头部 - 截图模式下隐藏 */}
          {!isScreenshotMode && (
            <Header
              stats={effectiveStats}
              onFilterClick={() => setShowFilterDrawer(true)}
              onRefresh={handleRefresh}
              loading={loading}
              refreshCooldown={refreshCooldown}
              autoRefresh={autoRefresh}
              onToggleAutoRefresh={handleToggleAutoRefresh}
              activeFiltersCount={activeFiltersCount}
            />
          )}

          {/* 公告横幅 - 截图模式下隐藏 */}
          {!isScreenshotMode && (
            <AnnouncementsBanner
              className="mb-4"
              data={announcementsData}
              loading={announcementsLoading}
              shouldShowBanner={shouldShowAnnouncementsBanner}
              onDismiss={dismissAnnouncements}
            />
          )}

          {/* 控制栏 - 截图模式下隐藏 */}
          {!isScreenshotMode && (
            <Controls
              filterProvider={filterProvider}
              filterService={filterService}
              filterChannel={filterChannel}
              filterCategory={filterCategory}
              filterVendor={filterVendor}
              showFavoritesOnly={showFavoritesOnly}
              favorites={favorites}
              favoritesCount={effectiveFavoritesCount}
              timeRange={timeRange}
              timeAlign={timeAlign}
              timeFilter={timeFilter}
              board={board}
              boardsEnabled={boardsEnabled}
              boardCounts={boardCounts}
              viewMode={viewMode}
              loading={loading}
              channels={effectiveChannels}
              providers={effectiveProviders}
              effectiveServices={effectiveServices}
              effectiveCategories={effectiveCategories}
              effectiveVendors={effectiveVendors}
              isMobile={isMobile}
              showFilterDrawer={showFilterDrawer}
              onFilterDrawerClose={() => setShowFilterDrawer(false)}
              onProviderChange={setFilterProvider}
              onServiceChange={setFilterService}
              onChannelChange={setFilterChannel}
              onCategoryChange={setFilterCategory}
              onVendorChange={setFilterVendor}
              onShowFavoritesOnlyChange={handleFavoritesModeChange}
              onTimeRangeChange={setTimeRange}
              onTimeAlignChange={setTimeAlign}
              onTimeFilterChange={setTimeFilter}
              onBoardChange={setBoard}
              onViewModeChange={setViewMode}
              onRefresh={handleRefresh}
              refreshCooldown={refreshCooldown}
              autoRefresh={autoRefresh}
              onToggleAutoRefresh={handleToggleAutoRefresh}
            />
          )}

          {/* 截图模式标题栏 */}
          {isScreenshotMode && (
            <div className="mb-3 px-3 py-2 bg-elevated border border-default rounded-lg text-xs text-secondary">
              {/* 群专属标题行 - 仅当有 title 时显示 */}
              {screenshotTitle && (
                <div className="text-sm text-primary font-medium mb-1 truncate">
                  {screenshotTitle}
                </div>
              )}
              {/* 时间和服务信息行 */}
              <div className="flex items-center justify-between">
                <span className="font-mono">{screenshotTimestamp}</span>
                <span>
                  {filteredData.length} 个服务 | {timeRange}
                </span>
              </div>
            </div>
          )}

          {/* 内容区域 */}
          {/* 冷板提示条 - 截图模式下隐藏 */}
          {!isScreenshotMode && boardsEnabled && board === 'cold' && (
            <div className="mb-4 px-4 py-3 bg-info/10 border border-info/30 rounded-lg text-info text-sm flex items-center gap-2">
              <svg className="w-4 h-4 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
              </svg>
              <span>{t('controls.boards.coldNotice')}</span>
            </div>
          )}
          {error ? (
            <div className="flex flex-col items-center justify-center py-20 text-danger">
              <Server size={64} className="mb-4 opacity-20" />
              <p className="text-lg">{t('common.error', { message: error })}</p>
            </div>
          ) : loading && data.length === 0 ? (
            <div className="flex flex-col items-center justify-center h-64 text-muted gap-4">
              <div className="w-12 h-12 border-4 border-accent/20 rounded-full animate-spin" style={{ borderTopColor: 'hsl(var(--accent))' }} />
              <p className="animate-pulse">{t('common.loading')}</p>
            </div>
          ) : data.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-20 text-muted">
              <Server size={64} className="mb-4 opacity-20" />
              <p className="text-lg">{t('common.noData')}</p>
            </div>
          ) : showFavoritesOnly && filteredData.length === 0 ? (
            // 开启收藏筛选但无收藏时显示空状态
            <EmptyFavorites onClearFilter={exitFavoritesMode} />
          ) : (
            <>
              {effectiveViewMode === 'table' && (
                <StatusTable
                  data={filteredData}
                  sortConfig={sortConfig}
                  isInitialSort={isInitialSort}
                  timeRange={timeRange}
                  slowLatencyMs={slowLatencyMs}
                  enableAnnotations={isScreenshotMode ? false : enableAnnotations}
                  showCategoryTag={!isScreenshotMode}
                  showSponsor={!isScreenshotMode}
                  isFavorite={isFavorite}
                  onToggleFavorite={toggleFavorite}
                  onSort={handleSort}
                  onBlockHover={handleBlockHover}
                  onBlockLeave={handleBlockLeave}
                  onFilterProvider={(providerId) => setFilterProvider([providerId])}
                  rpdiagScores={rpdiagScores}
                  rpdiagScoresLoaded={rpdiagScoresLoaded}
                  rpdiagEnabled={rpdiagEnabled}
                  hidePriceColumn={hidePriceColumn}
                  showVendorColumn={showVendorColumn}
                />
              )}

              {effectiveViewMode === 'grid' && (
                <div data-heatmap-container className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-6">
                  {filteredData.map((item) => (
                    <StatusCard
                      key={item.id}
                      item={item}
                      timeRange={timeRange}
                      slowLatencyMs={slowLatencyMs}
                      enableAnnotations={enableAnnotations}
                      isFavorite={isFavorite}
                      onToggleFavorite={toggleFavorite}
                      hidePriceColumn={hidePriceColumn}
                      showVendorColumn={showVendorColumn}
                      onBlockHover={handleBlockHover}
                      onBlockLeave={handleBlockLeave}
                    />
                  ))}
                </div>
              )}
            </>
          )}

          {/* 免责声明 - 截图模式下隐藏 */}
          {!isScreenshotMode && <Footer rpdiagEnabled={rpdiagEnabled} />}
        </div>
      </div>
    </>
  );
}

export default App;
