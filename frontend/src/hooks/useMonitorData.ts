import { useState, useEffect, useMemo, useCallback, useRef } from 'react';
import type {
  ApiResponseWithGroups,
  ProcessedMonitorData,
  SortConfig,
  ProviderOption,
  ChannelOption,
  SponsorPinConfig,
  BoardFilter,
  BoardCounts,
} from '../types';
import { API_BASE_URL, HIDE_PRICE_COLUMN, USE_MOCK_DATA } from '../constants';
import { fetchMockMonitorData } from '../utils/mockMonitor';
import { trackAPIPerformance, trackAPIError } from '../utils/analytics';
import { sortMonitorsWithPinning } from '../utils/sortMonitors';
import { convertLegacyDataToProcessedData, convertGroupToProcessedData } from '../utils/monitorDataProcessor';
import { lookupRpdiagScore } from './useRpdiagScores';
import type { RpdiagScoresResponse } from '../types/monitor';

/** 稳定的空筛选数组：模块级常量，避免每次派生出新引用白白让下游 memo 失效。 */
const EMPTY_FILTER: string[] = [];

// 请求节流间隔（毫秒）- 防止快速切换参数导致过多请求
const FETCH_THROTTLE_MS = 300;

// 自动轮询间隔（毫秒）- 与后端探测频率 interval: "1m" 保持一致
const POLL_INTERVAL_MS = 60_000;

interface UseMonitorDataOptions {
  timeRange: string;
  timeAlign?: string;        // 时间对齐模式：空=动态滑动窗口, "hour"=整点对齐
  timeFilter?: string | null; // 每日时段过滤：null=全天, "09:00-17:00"=自定义
  board?: BoardFilter;       // 板块过滤：hot/secondary/cold/all（默认 hot）
  filterService: string[];   // 多选服务，空数组表示"全部"
  filterProvider: string[];  // 多选服务商，空数组表示"全部"
  filterChannel: string[];   // 多选通道，空数组表示"全部"
  filterCategory: string[];  // 多选分类，空数组表示"全部"
  filterVendor: string[];    // 多选模型厂商，空数组表示"全部"
  sortConfig: SortConfig;
  isInitialSort: boolean;    // 是否为初始排序状态（用于赞助商置顶）
  autoRefresh?: boolean;     // 自动刷新开关，默认开启
  // rpdiag 质量分（异步），用于 sort 前给 ProcessedMonitorData 注入 qualityScore。
  // 缺省表示 rpdiag 集成未启用 / 调用方不关心质量排序。
  rpdiagScores?: RpdiagScoresResponse;
  rpdiagScoresLoaded?: boolean;
}

export function useMonitorData({
  timeRange,
  timeAlign = '',
  timeFilter = null,
  board = 'hot',
  filterService,
  filterProvider,
  filterChannel,
  filterCategory,
  filterVendor,
  sortConfig,
  isInitialSort,
  autoRefresh = true,
  rpdiagScores,
  rpdiagScoresLoaded = false,
}: UseMonitorDataOptions) {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [rawData, setRawData] = useState<ProcessedMonitorData[]>([]);
  const [reloadToken, setReloadToken] = useState(0);
  const skipCacheRef = useRef(false); // 使用 ref 避免触发 effect 重新执行
  const [slowLatencyMs, setSlowLatencyMs] = useState<number>(5000); // 默认 5 秒
  const [enableAnnotations, setEnableAnnotations] = useState<boolean>(true); // 注解系统总开关（默认启用）
  const [boardsEnabled, setBoardsEnabled] = useState<boolean>(false); // 板块功能开关（默认禁用）
  const [boardsEnabledLoaded, setBoardsEnabledLoaded] = useState<boolean>(false); // 是否已从 API 获取板块开关状态
  const [boardCounts, setBoardCounts] = useState<BoardCounts | undefined>(undefined); // 各板块通道数量
  const [sponsorPinConfig, setSponsorPinConfig] = useState<SponsorPinConfig | null>(null); // 赞助商置顶配置
  const [allMonitorIds, setAllMonitorIds] = useState<Set<string>>(new Set()); // 全量监控项 ID（用于清理无效收藏）
  const [allMonitorIdsSupported, setAllMonitorIdsSupported] = useState<boolean>(false); // 后端是否支持 all_monitor_ids
  // runtime 价格列隐藏开关。默认采用 build-time HIDE_PRICE_COLUMN，确保 meta 到达前 UI 行为可预测；
  // /api/status 回来后用 meta.hide_price_column 覆盖（旧后端缺该字段时继续走 build-time 值）。
  const [hidePriceColumn, setHidePriceColumn] = useState<boolean>(HIDE_PRICE_COLUMN);
  // runtime 分类筛选器隐藏开关。与价格列不同，这里**不设 build-time 兜底常量**：
  // 它只是个展示入口开关，没有"打包时就要定"的场景，缺失即显示（旧后端行为零变化）。
  const [hideCategoryFilter, setHideCategoryFilter] = useState<boolean>(false);
  // rpdiag 质量功能总开关。默认 true（fail-open）：滚动发布或旧后端缺 meta.rpdiag_enabled 时
  // 行为零变化；仅当新后端明确返回 false（私有部署未接 rpdiag）才隐藏质量列 + diag 外链入口。
  const [rpdiagEnabled, setRpdiagEnabled] = useState<boolean>(true);

  // 统一的刷新触发器，供手动刷新与自动轮询复用
  // skipCache: 是否绕过浏览器缓存（手动刷新时应为 true）
  const triggerRefetch = useCallback((skipCache = false) => {
    setLoading(true);
    if (skipCache) {
      skipCacheRef.current = true; // 使用 ref 设置标志
    }
    setReloadToken((token) => token + 1);
  }, []);

  // 数据获取 - 支持双模式（Mock / API）
  // 使用 debounce 防止快速切换参数导致过多请求
  useEffect(() => {
    let isMounted = true;
    let debounceTimer: ReturnType<typeof setTimeout> | null = null;

    const fetchData = async () => {
      setLoading(true);
      setError(null);

      // 记录开始时间（在 try 外面，确保网络错误也能追踪性能）
      const startTime = USE_MOCK_DATA ? 0 : performance.now();

      try {
        let processed: ProcessedMonitorData[];

        if (USE_MOCK_DATA) {
          // 使用模拟数据
          processed = await fetchMockMonitorData(timeRange);
          // Mock 数据模式：视为板块功能可用（便于本地调试）
          setBoardsEnabled(true);
          setBoardsEnabledLoaded(true);
          setRpdiagEnabled(true);
        } else {
          // 使用真实 API
          // align 参数仅在 24h 模式下有效
          const alignParam = (timeAlign && timeRange === '24h') ? `&align=${encodeURIComponent(timeAlign)}` : '';
          // time_filter 参数仅在 7d/30d 模式下有效（90m/24h 关闭）
          const timeFilterParam =
            (timeFilter && timeRange !== '24h' && timeRange !== '90m')
              ? `&time_filter=${encodeURIComponent(timeFilter)}`
              : '';
          // board 参数：默认 hot
          const boardParam = `&board=${encodeURIComponent(board)}`;
          const url = `${API_BASE_URL}/api/status?period=${timeRange}${alignParam}${timeFilterParam}${boardParam}`;

          // 读取并重置 skipCache 标志
          const shouldSkipCache = skipCacheRef.current;
          if (shouldSkipCache) {
            skipCacheRef.current = false; // 立即重置，避免影响后续请求
          }

          // 手动刷新时绕过浏览器缓存
          const fetchOptions: RequestInit = shouldSkipCache ? { cache: 'no-store' } : {};
          const response = await fetch(url, fetchOptions);

          const duration = Math.round(performance.now() - startTime);

          if (!response.ok) {
            // 追踪 HTTP 错误（失败的性能和错误事件）
            trackAPIPerformance('/api/status', duration, false);
            trackAPIError('/api/status', `HTTP_${response.status}`, 'HTTP Error');
            throw new Error(`HTTP error! status: ${response.status}`);
          }

          const json: ApiResponseWithGroups = await response.json();

          // 追踪成功的 API 性能
          trackAPIPerformance('/api/status', duration, true);

          // 提取慢延迟阈值（用于延迟颜色渐变）
          if (json.meta.slow_latency_ms && json.meta.slow_latency_ms > 0) {
            setSlowLatencyMs(json.meta.slow_latency_ms);
          }

          // 提取注解系统总开关（默认 true）
          setEnableAnnotations(json.meta.enable_annotations !== false);

          // 提取价格列隐藏开关（兼容旧后端：字段缺失时回落到 build-time 默认）
          setHidePriceColumn(json.meta.hide_price_column ?? HIDE_PRICE_COLUMN);

          // 提取分类筛选器隐藏开关（兼容旧后端：字段缺失即显示）
          setHideCategoryFilter(json.meta.hide_category_filter === true);

          // 提取 rpdiag 质量功能总开关（fail-open：仅显式 false 才关，缺失/undefined 视为启用）
          setRpdiagEnabled(json.meta.rpdiag_enabled !== false);

          // 提取赞助商置顶配置
          if (json.meta.sponsor_pin) {
            setSponsorPinConfig(json.meta.sponsor_pin);
          }

          // 提取板块功能开关（默认禁用，兼容旧后端）
          setBoardsEnabled(json.meta.boards?.enabled === true);
          setBoardsEnabledLoaded(true);

          // 提取板块计数（兼容旧后端）
          const counts = json.meta.board_counts;
          if (counts && typeof counts.hot === 'number' && typeof counts.secondary === 'number' && typeof counts.cold === 'number') {
            setBoardCounts(counts);
          } else {
            setBoardCounts(undefined);
          }

          // 提取全量监控项 ID（用于清理无效收藏，兼容旧后端）
          // 字段缺失时重置为空集，避免保留旧值导致误删
          if (Array.isArray(json.meta.all_monitor_ids)) {
            // 过滤非字符串元素并 trim，确保数据干净
            const validIds = json.meta.all_monitor_ids
              .filter((id): id is string => typeof id === 'string')
              .map((id) => id.trim())
              .filter((id) => id !== '');
            setAllMonitorIds(new Set(validIds));
            setAllMonitorIdsSupported(true); // 后端支持该字段
          } else {
            setAllMonitorIds(new Set());
            setAllMonitorIdsSupported(false); // 旧后端不支持
          }

          // 统一转换：合并 legacy data 和 groups
          const globalSlowLatencyMs = json.meta.slow_latency_ms ?? 5000;
          const legacy = (json.data || []).map((item) =>
            convertLegacyDataToProcessedData(item, globalSlowLatencyMs)
          );
          const groups = (Array.isArray(json.groups) ? json.groups : []).map((g) =>
            convertGroupToProcessedData(g, globalSlowLatencyMs)
          );
          processed = [...legacy, ...groups];
        }

        // 防止组件卸载后的状态更新
        if (!isMounted) return;
        setRawData(processed);
      } catch (err) {
        if (!isMounted) return;
        const errorMessage = err instanceof Error ? err.message : 'Unknown error';
        setError(errorMessage);

        // 只追踪真正的网络错误（fetch 失败、连接超时等）
        // HTTP 错误已经在上面追踪过了，避免重复
        if (!USE_MOCK_DATA && !errorMessage.startsWith('HTTP error!')) {
          const duration = Math.round(performance.now() - startTime);
          // 追踪网络错误的性能和错误事件
          trackAPIPerformance('/api/status', duration, false);
          trackAPIError('/api/status', 'NETWORK_ERROR', 'Network failure');
        }
      } finally {
        if (isMounted) {
          setLoading(false);
        }
      }
    };

    // 使用 debounce 延迟请求，防止快速切换参数
    // 注意：skipCacheRef 是 ref，不会触发 effect，直接在 fetchData 中读取
    debounceTimer = setTimeout(fetchData, FETCH_THROTTLE_MS);

    return () => {
      isMounted = false;
      if (debounceTimer) {
        clearTimeout(debounceTimer);
      }
    };
  }, [timeRange, timeAlign, timeFilter, board, reloadToken]);

  // 页面可见性驱动的自动轮询
  useEffect(() => {
    // SSR 环境保护
    if (typeof document === 'undefined') return;

    let intervalId: ReturnType<typeof setInterval> | null = null;

    const startPolling = () => {
      // 自动刷新关闭时不启动轮询
      if (!autoRefresh) return;
      if (document.visibilityState !== 'visible' || intervalId) return;
      intervalId = setInterval(triggerRefetch, POLL_INTERVAL_MS);
    };

    const stopPolling = () => {
      if (!intervalId) return;
      clearInterval(intervalId);
      intervalId = null;
    };

    const handleVisibilityChange = () => {
      if (document.visibilityState === 'visible') {
        // 仅在自动刷新开启时才触发刷新和启动轮询
        if (autoRefresh) {
          triggerRefetch(); // 页面重新可见时立即刷新
          startPolling();
        }
      } else {
        stopPolling();
      }
    };

    // 初始化：仅在页面可见且自动刷新开启时启动轮询
    if (autoRefresh) {
      startPolling();
    }
    document.addEventListener('visibilitychange', handleVisibilityChange);

    return () => {
      stopPolling();
      document.removeEventListener('visibilitychange', handleVisibilityChange);
    };
  }, [triggerRefetch, autoRefresh]);

  // 提取所有通道列表（去重并排序）
  // 返回 ChannelOption[] 格式，支持 label/value 分离
  const channels = useMemo<ChannelOption[]>(() => {
    const map = new Map<string, string>();  // value (channel) -> label (channelName)
    rawData.forEach((item) => {
      if (item.channel) {
        // 如果同一个 channel 有多个 channelName，保留第一个
        if (!map.has(item.channel)) {
          map.set(item.channel, item.channelName || item.channel);
        }
      }
    });
    return Array.from(map.entries())
      .sort((a, b) => a[1].localeCompare(b[1], 'zh-CN'))  // 按 label 排序
      .map(([value, label]) => ({ value, label }));
  }, [rawData]);

  // 提取所有服务商列表（去重并排序）
  // 返回 ProviderOption[] 格式，支持 label/value 分离
  const providers = useMemo<ProviderOption[]>(() => {
    const map = new Map<string, string>();  // value -> label
    rawData.forEach((item) => {
      if (item.providerId) {
        // 如果同一个 providerId 有多个 providerName，保留第一个
        if (!map.has(item.providerId)) {
          map.set(item.providerId, item.providerName);
        }
      }
    });
    return Array.from(map.entries())
      .sort((a, b) => a[1].localeCompare(b[1], 'zh-CN'))  // 按 label 排序
      .map(([value, label]) => ({ value, label }));
  }, [rawData]);

  // 分类筛选器被 runtime 开关隐藏后，URL 里残留的 ?category= 一律视为未选中。
  //
  // 为什么在这里派生、而不是用一个 effect 去把 URL 参数抹掉：react-router 的
  // setSearchParams(prev => ...) 拿到的 prev 是**上一次渲染**的参数，同一次 commit 里
  // 有两个 effect 各写一次 URL 时，后跑的那个会带着旧值把先跑的覆盖回去
  // （实测：本清理被紧随其后的 clearQualityScoreSort 顶掉，URL 纹丝不动）。
  // 派生值不碰 URL，就不存在这个竞态；代价是残留参数留在地址栏里空转——
  // 它不再影响任何展示，而且开关关掉后用户原来的选择还在，反倒更好。
  //
  // 单一真相源：这个派生只写这一处，App 直接消费返回的 effectiveFilterCategory，
  // 不要在调用方再算一遍。
  const effectiveFilterCategory = useMemo(
    () => (hideCategoryFilter ? EMPTY_FILTER : filterCategory),
    [hideCategoryFilter, filterCategory],
  );

  // 数据过滤和排序
  const processedData = useMemo(() => {
    // 多选过滤：空数组表示"全部"
    const providerSet = filterProvider.length > 0 ? new Set(filterProvider) : null;
    const serviceSet = filterService.length > 0 ? new Set(filterService) : null;
    const channelSet = filterChannel.length > 0 ? new Set(filterChannel) : null;
    const categorySet = effectiveFilterCategory.length > 0 ? new Set(effectiveFilterCategory) : null;
    const vendorSet = filterVendor.length > 0 ? new Set(filterVendor) : null;

    const filtered = rawData.filter((item) => {
      const matchService = serviceSet === null || serviceSet.has(item.serviceType.toLowerCase());
      const matchProvider = providerSet === null || providerSet.has(item.providerId);
      const matchChannel = channelSet === null || (item.channel && channelSet.has(item.channel));
      const matchCategory = categorySet === null || (item.category && categorySet.has(item.category));
      // 未声明厂商的通道在厂商筛选生效时一律排除（"未知"不属于任何一家）
      const matchVendor = vendorSet === null || (!!item.modelVendor && vendorSet.has(item.modelVendor));
      return matchService && matchProvider && matchChannel && matchCategory && matchVendor;
    });

    // 在排序前给每项注入 qualityScore（来自 rpdiag 三元组查表）。
    // rpdiag 功能关闭 / 未加载完成 / 查不到时为 null，sortMonitors 内 compareQualityScore 会让其沉底。
    const enriched = rpdiagEnabled && rpdiagScoresLoaded
      ? filtered.map((item) => ({
          ...item,
          qualityScore:
            lookupRpdiagScore(rpdiagScores, [item.providerName, item.providerId], item.serviceType, item.channelName || item.channel, item.channelId)
              ?.max_score ?? null,
        }))
      : filtered;

    // 使用带置顶逻辑的排序函数
    return sortMonitorsWithPinning(enriched, sortConfig, sponsorPinConfig, isInitialSort);
  }, [rawData, filterService, filterProvider, filterChannel, effectiveFilterCategory, filterVendor, sortConfig, sponsorPinConfig, isInitialSort, rpdiagScores, rpdiagScoresLoaded, rpdiagEnabled]);

  // 统计数据
  const stats = useMemo(() => {
    const total = processedData.length;
    const healthy = processedData.filter((i) => i.currentStatus === 'AVAILABLE').length;
    const issues = total - healthy;
    return { total, healthy, issues };
  }, [processedData]);

  return {
    loading,
    error,
    data: processedData,
    rawData,  // 未过滤的原始数据，供 App.tsx 计算 effectiveXxx 使用
    stats,
    channels,
    providers,
    slowLatencyMs,
    enableAnnotations,
    boardsEnabled,  // 板块功能开关
    boardsEnabledLoaded,  // 是否已从 API 获取板块开关状态
    boardCounts,    // 各板块通道数量
    allMonitorIds,  // 全量监控项 ID（用于清理无效收藏）
    allMonitorIdsSupported, // 后端是否支持 all_monitor_ids（用于区分"空列表"和"不支持"）
    hidePriceColumn, // runtime 价格列隐藏开关（meta.hide_price_column 优先，缺失时回落 build-time）
    hideCategoryFilter, // runtime 分类筛选器隐藏开关（meta.hide_category_filter；缺失即显示）
    effectiveFilterCategory, // 经开关裁决后的分类筛选值——调用方一律用它，别再自己判 hideCategoryFilter
    rpdiagEnabled, // rpdiag 质量功能总开关（meta.rpdiag_enabled；fail-open 默认 true）
    refetch: triggerRefetch,
  };
}
