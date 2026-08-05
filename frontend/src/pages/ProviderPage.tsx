import { useState, useCallback, useRef, useEffect, useMemo } from 'react';
import { useLocation, useParams, useSearchParams } from 'react-router-dom';
import { Helmet } from 'react-helmet-async';
import { useTranslation } from 'react-i18next';
import { Server } from 'lucide-react';
import { useMonitorData } from '../hooks/useMonitorData';
import { useRpdiagScores } from '../hooks/useRpdiagScores';
import { useFavorites } from '../hooks/useFavorites';
import { useSeoMeta } from '../hooks/useSeoMeta';
import { Header } from '../components/Header';
import { Controls } from '../components/Controls';
import { StatusTable } from '../components/StatusTable';
import { StatusCard } from '../components/StatusCard';
import { vendorLabel } from '../components/VendorBadge';
import { Tooltip } from '../components/Tooltip';
import { Footer } from '../components/Footer';
import { EmptyFavorites } from '../components/EmptyFavorites';
import { createMediaQueryEffect } from '../utils/mediaQuery';
import { canonicalize } from '../utils/monitorDataProcessor';
import type { MultiSelectOption } from '../components/MultiSelect';
import type { ViewMode, SortConfig, TooltipState, ProcessedMonitorData, ChannelOption, BoardFilter } from '../types';

// localStorage key for time align preference (shared with App.tsx)
const STORAGE_KEY_TIME_ALIGN = 'relay-pulse-time-align';

// 获取 ProviderPage 专用的快照 key（按 provider slug 隔离）
const getSnapshotKey = (slug: string) =>
  `relay-pulse:v1:list-state:p/${encodeURIComponent(slug)}`;

// ProviderPage 快照数据结构（简化版，无 provider/category）
interface ProviderPageSnapshot {
  version: 1;
  filterService: string[];
  filterChannel: string[];
  /** 2026-08 加入的厂商筛选。**故意留在 version 1**：旧会话快照没有这个键，
   *  升 version 会让那份快照整体作废。解析时缺失降级为空数组，见 parseProviderPageSnapshot。 */
  filterVendor: string[];
}

/** 解析 ProviderPage 收藏模式快照：结构不对返回 null；仅新增的 filterVendor 允许缺失。 */
function parseProviderPageSnapshot(raw: string | null): ProviderPageSnapshot | null {
  if (!raw) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  if (typeof parsed !== 'object' || parsed === null) return null;
  const candidate = parsed as Record<string, unknown>;
  if (candidate.version !== 1) return null;

  const asStringArray = (value: unknown): string[] | null =>
    Array.isArray(value) && value.every((v) => typeof v === 'string') ? value : null;

  const filterService = asStringArray(candidate.filterService);
  const filterChannel = asStringArray(candidate.filterChannel);
  if (!filterService || !filterChannel) return null;

  return {
    version: 1,
    filterService,
    filterChannel,
    filterVendor: asStringArray(candidate.filterVendor) ?? [],
  };
}

/**
 * 服务商专属页面
 * URL: /p/:provider
 * 支持嵌入模式: ?embed=1
 */
export default function ProviderPage() {
  const { provider } = useParams<{ provider: string }>();
  const [searchParams, setSearchParams] = useSearchParams();
  const { t, i18n } = useTranslation();
  const location = useLocation();
  const seo = useSeoMeta({ pathname: location.pathname, language: i18n.language });

  // 嵌入模式检测
  const isEmbedMode = searchParams.get('embed') === '1';

  // 规范化 provider slug
  const normalizedProvider = canonicalize(provider);

  // 板块状态（从 URL 读取，支持 hot/secondary/cold/all）
  const rawBoard = searchParams.get('board');
  const board: BoardFilter = (rawBoard === 'hot' || rawBoard === 'secondary' || rawBoard === 'cold' || rawBoard === 'active' || rawBoard === 'all') ? rawBoard : 'hot';

  const setBoard = useCallback((value: BoardFilter) => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      if (value === 'hot') {
        next.delete('board');
      } else {
        next.set('board', value);
      }
      return next;
    }, { replace: true });
  }, [setSearchParams]);

  // 状态管理
  const [timeRange, setTimeRange] = useState('90m');
  const [timeFilter, setTimeFilter] = useState<string | null>(null);
  const [filterService, setFilterService] = useState<string[]>([]);
  const [filterChannel, setFilterChannel] = useState<string[]>([]);
  const [filterVendor, setFilterVendor] = useState<string[]>([]);
  // filterCategory 在 Provider 页面固定为空数组（全部），不需要状态
  const [viewMode, setViewMode] = useState<ViewMode>('table');
  const [sortConfig, setSortConfig] = useState<SortConfig>({
    key: 'uptime',
    direction: 'desc',
  });

  // 时间对齐模式（使用 localStorage 持久化）
  const [timeAlign, setTimeAlignState] = useState<string>(() => {
    if (typeof window === 'undefined') return '';
    return localStorage.getItem(STORAGE_KEY_TIME_ALIGN) ?? 'hour';
  });

  const setTimeAlign = useCallback((align: string) => {
    setTimeAlignState(align);
    if (typeof window !== 'undefined') {
      if (align) {
        localStorage.setItem(STORAGE_KEY_TIME_ALIGN, align);
      } else {
        localStorage.removeItem(STORAGE_KEY_TIME_ALIGN);
      }
    }
  }, []);

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

  // 移动端筛选抽屉状态
  const [showFilterDrawer, setShowFilterDrawer] = useState(false);

  // 收藏管理 Hook
  const { favorites, isFavorite, toggleFavorite, cleanupMissingFavorites, count: favoritesCount } = useFavorites();
  const [showFavoritesOnly, setShowFavoritesOnly] = useState(false);

  // 移动端检测（< 960px）
  const [isMobile, setIsMobile] = useState(false);
  useEffect(() => {
    const cleanup = createMediaQueryEffect('tablet', setIsMobile);
    return cleanup;
  }, []);

  // rpdiag 质量分（异步），用于 sort 前给项目注入 qualityScore，并驱动质量列排序可用性
  const { scores: rpdiagScores, loaded: rpdiagScoresLoaded } = useRpdiagScores();

  // 数据获取 - 先获取全部数据用于构建映射
  // Provider 页面不启用置顶功能（isInitialSort=false）
  const { data: allData, rawData, loading, error, stats, slowLatencyMs, enableAnnotations, boardsEnabled, boardsEnabledLoaded, allMonitorIds, allMonitorIdsSupported, hidePriceColumn, rpdiagEnabled, refetch } = useMonitorData({
    timeRange,
    timeAlign,
    timeFilter,
    board,
    filterService,
    filterProvider: [], // 空数组表示全部
    filterChannel,
    filterCategory: [], // Provider页面不筛选分类，空数组表示全部
    filterVendor,
    sortConfig,
    isInitialSort: false, // Provider页面禁用置顶
    // 冷板数据不更新，禁用自动刷新以节省资源
    autoRefresh: board !== 'cold',
    rpdiagScores,
    rpdiagScoresLoaded,
  });

  // 运行时隐藏价格列后，若本地 sortConfig 还停留在 priceRatio，
  // 则退回默认排序，避免 UI 上"按隐藏列排序"的语义错位（局部状态，无 URL 干扰）。
  // 渲染期条件 setState（非 effect）：纠偏后 sortConfig.key 变 uptime，下一轮条件即假、自然收敛，
  // 不会级联循环（React 官方"渲染期调整 state"配方，比 effect 少一次提交后重渲染）。
  if (hidePriceColumn && sortConfig.key === 'priceRatio') {
    setSortConfig({ key: 'uptime', direction: 'desc' });
  }

  // 同理：rpdiag 关闭（私有部署）后质量列消失，本地 sortConfig 若停在 qualityScore 则退回默认。
  if (!rpdiagEnabled && sortConfig.key === 'qualityScore') {
    setSortConfig({ key: 'uptime', direction: 'desc' });
  }

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

  // 构建 slug -> providerId 映射
  const slugToProviderId = new Map<string, string>();
  allData.forEach((item) => {
    slugToProviderId.set(item.providerSlug, item.providerId);
  });

  // 将 URL slug 映射回 providerId
  const realProviderId = slugToProviderId.get(normalizedProvider) || normalizedProvider;

  // 按 providerId 过滤数据
  const data = allData.filter((item) => item.providerId === realProviderId);

  // 统计激活的筛选器数量（用于移动端 Header 显示）
  // Provider 页面不显示 category 和 provider 筛选器
  const activeFiltersCount = [
    showFavoritesOnly,
    filterService.length > 0,
    filterChannel.length > 0,
    filterVendor.length > 0,
  ].filter(Boolean).length;

  // 基础数据：应用收藏筛选后的数据（如适用）
  const baseData = useMemo(() => {
    if (!showFavoritesOnly) return data;
    return data.filter(item => favorites.has(item.id));
  }, [data, showFavoritesOnly, favorites]);

  // 选项基础数据：本 provider 的**未经筛选**数据（rawData 未过任何筛选器），
  // 用于计算 effectiveXxx —— 与 App 的 optionsBaseData 同一范式。
  // 不能用 baseData：它来自 useMonitorData 已按 service/channel/vendor 筛过的结果，
  // 拿它算选项等于「选了 cc 之后下拉里就只剩 cc」，用户没法再多选一个 cx。
  const optionsBaseData = useMemo(() => {
    const mine = rawData.filter(item => item.providerId === realProviderId);
    return showFavoritesOnly ? mine.filter(item => favorites.has(item.id)) : mine;
  }, [rawData, realProviderId, showFavoritesOnly, favorites]);

  // 最终过滤后的数据（应用所有筛选器）
  const filteredData = useMemo(() => {
    let filtered = baseData;
    if (filterService.length > 0) {
      filtered = filtered.filter(item => filterService.includes(item.serviceType.toLowerCase()));
    }
    if (filterChannel.length > 0) {
      filtered = filtered.filter(item => item.channel && filterChannel.includes(item.channel));
    }
    if (filterVendor.length > 0) {
      // 未声明厂商的通道在厂商筛选生效时排除（与 useMonitorData 口径一致）
      filtered = filtered.filter(item => !!item.modelVendor && filterVendor.includes(item.modelVendor));
    }
    return filtered;
  }, [baseData, filterService, filterChannel, filterVendor]);

  // 动态 Service 选项：基于 channel 筛选后的数据
  const effectiveServices = useMemo(() => {
    let filtered = optionsBaseData;
    if (filterChannel.length > 0) {
      filtered = filtered.filter(item => item.channel && filterChannel.includes(item.channel));
    }
    if (filterVendor.length > 0) {
      filtered = filtered.filter(item => !!item.modelVendor && filterVendor.includes(item.modelVendor));
    }
    const set = new Set<string>();
    filtered.forEach(item => set.add(item.serviceType.toLowerCase()));
    return Array.from(set).sort();
  }, [optionsBaseData, filterChannel, filterVendor]);

  // 动态 Channel 选项：基于 service 筛选后的数据
  const effectiveChannels = useMemo<ChannelOption[]>(() => {
    let filtered = optionsBaseData;
    if (filterService.length > 0) {
      filtered = filtered.filter(item => filterService.includes(item.serviceType.toLowerCase()));
    }
    if (filterVendor.length > 0) {
      filtered = filtered.filter(item => !!item.modelVendor && filterVendor.includes(item.modelVendor));
    }
    // 收集 channel -> channelName 映射
    const map = new Map<string, string>();
    filtered.forEach(item => {
      if (item.channel && !map.has(item.channel)) {
        map.set(item.channel, item.channelName || item.channel);
      }
    });
    // 转换为 ChannelOption[]，按 label 排序
    return Array.from(map.entries())
      .sort((a, b) => a[1].localeCompare(b[1], 'zh-CN'))
      .map(([value, label]) => ({ value, label }));
  }, [optionsBaseData, filterService, filterVendor]);

  // 动态厂商选项：基于 service/channel 筛选后的数据（不含 vendor 自身），label 本地化后排序
  const effectiveVendors = useMemo<MultiSelectOption[]>(() => {
    let filtered = optionsBaseData;
    if (filterService.length > 0) {
      filtered = filtered.filter(item => filterService.includes(item.serviceType.toLowerCase()));
    }
    if (filterChannel.length > 0) {
      filtered = filtered.filter(item => item.channel && filterChannel.includes(item.channel));
    }
    const set = new Set<string>();
    filtered.forEach(item => { if (item.modelVendor) set.add(item.modelVendor); });
    // 已选项即使被其他筛选筛没了也保持可见，否则用户取消不掉自己选的条件
    filterVendor.forEach(vendor => set.add(vendor));
    return Array.from(set)
      .map(value => ({ value, label: vendorLabel(t, value) }))
      .sort((a, b) => a.label.localeCompare(b.label, 'zh-CN'));
  }, [optionsBaseData, filterService, filterChannel, filterVendor, t]);

  // 厂商列显隐：基于**本 provider** 的未筛数据。用全站 allData 会让「别家有厂商、
  // 本家没有」的服务商页凭空多出一整列 '-'；用已筛的 data 又会随筛选抖动。
  const showVendorColumn = useMemo(
    () => rawData.some(item => item.providerId === realProviderId && !!item.modelVendor),
    [rawData, realProviderId],
  );

  // 同 hidePriceColumn / rpdiagEnabled 的渲染期纠偏：厂商列不可见时本地 sortConfig
  // 若停在 modelVendor 就退回默认排序。等 rawData 到货再判，避免首屏空数据误清。
  if (rawData.length > 0 && !showVendorColumn && sortConfig.key === 'modelVendor') {
    setSortConfig({ key: 'uptime', direction: 'desc' });
  }

  // 收藏模式切换（ProviderPage 独立快照管理）
  const handleFavoritesModeChange = useCallback((enabled: boolean) => {
    if (enabled) {
      // 防止重复进入：已在收藏模式时不重复保存快照
      if (showFavoritesOnly) return;

      // 保存快照
      const snapshot: ProviderPageSnapshot = {
        version: 1,
        filterService,
        filterChannel,
        filterVendor,
      };
      try {
        sessionStorage.setItem(getSnapshotKey(normalizedProvider), JSON.stringify(snapshot));
      } catch {
        // sessionStorage 不可用时静默失败
      }
      // 清空筛选器并启用收藏模式
      setFilterService([]);
      setFilterChannel([]);
      setFilterVendor([]);
      setShowFavoritesOnly(true);
    } else {
      // 恢复快照
      let snapshot: ProviderPageSnapshot | null = null;
      try {
        snapshot = parseProviderPageSnapshot(sessionStorage.getItem(getSnapshotKey(normalizedProvider)));
      } catch {
        // sessionStorage 不可用时使用默认值
      }
      // 无论成功与否都清理快照
      try {
        sessionStorage.removeItem(getSnapshotKey(normalizedProvider));
      } catch {
        // 静默失败
      }

      // 恢复筛选器
      if (snapshot) {
        setFilterService(snapshot.filterService);
        setFilterChannel(snapshot.filterChannel);
        setFilterVendor(snapshot.filterVendor);
      } else {
        // 无快照时恢复为默认
        setFilterService([]);
        setFilterChannel([]);
        setFilterVendor([]);
      }
      setShowFavoritesOnly(false);
    }
  }, [normalizedProvider, filterService, filterChannel, filterVendor, showFavoritesOnly]);

  // 移动端强制使用 table 视图
  const effectiveViewMode = isMobile ? 'table' : viewMode;

  // 软 404 处理：只在 provider slug 真正不存在时返回 404
  // 避免网络错误或筛选条件导致的空数据被误判为 404
  const providerExists = allData.some((item) => item.providerSlug === normalizedProvider);

  if (!loading && !error && !providerExists) {
    return <ProviderNotFound providerSlug={provider || ''} isEmbedMode={isEmbedMode} />;
  }

  // 从数据中获取 provider 显示名称
  const providerDisplayName = data[0]?.providerName || provider || '';

  // Tooltip 处理
  const handleBlockHover = (
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
  };

  const handleBlockLeave = () => {
    setTooltip((prev) => ({ ...prev, show: false }));
  };

  // 排序处理
  const handleSort = (key: string) => {
    setSortConfig((prevConfig) => ({
      key,
      direction:
        prevConfig.key === key && prevConfig.direction === 'asc'
          ? 'desc'
          : 'asc',
    }));
  };

  // 刷新处理
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
    refetch(true); // 绕过浏览器缓存
  };

  return (
    <>
      <Helmet>
        <html lang={seo.htmlLang} />
        <title>{t('provider.pageTitle', { name: providerDisplayName })}</title>
        <meta name="description" content={t('provider.pageDescription', { name: providerDisplayName })} />
      </Helmet>

      <div className="min-h-screen bg-page text-primary font-sans selection-accent overflow-x-hidden">
        {/* 全局 Tooltip */}
        <Tooltip tooltip={tooltip} onClose={handleBlockLeave} slowLatencyMs={slowLatencyMs} timeRange={timeRange} />

        {/* 背景装饰 */}
        {!isEmbedMode && (
          <div className="fixed top-0 left-0 w-full h-full overflow-hidden pointer-events-none z-0">
            <div className="absolute top-[-10%] right-[-10%] w-[600px] h-[600px] bg-accent/10 rounded-full blur-[120px]" />
            <div className="absolute bottom-[-10%] left-[-10%] w-[600px] h-[600px] bg-accent/10 rounded-full blur-[120px]" />
          </div>
        )}

        <div className="relative z-10 max-w-7xl mx-auto px-4 py-4 sm:py-6 sm:px-6 lg:px-8">
        {/* 完整模式：显示 Header */}
        {!isEmbedMode && (
          <Header
            stats={stats}
            onFilterClick={() => setShowFilterDrawer(true)}
            onRefresh={handleRefresh}
            loading={loading}
            refreshCooldown={refreshCooldown}
            activeFiltersCount={activeFiltersCount}
          />
        )}

        {/* 控制面板 - 隐藏 provider 和 category 筛选器，只显示当前 provider 的通道 */}
        <Controls
          timeRange={timeRange}
          timeAlign={timeAlign}
          timeFilter={timeFilter}
          board={board}
          boardsEnabled={boardsEnabled}
          filterService={filterService}
          filterProvider={[]}
          filterChannel={filterChannel}
          filterCategory={[]}
          filterVendor={filterVendor}
          showFavoritesOnly={showFavoritesOnly}
          favorites={favorites}
          favoritesCount={effectiveFavoritesCount}
          viewMode={viewMode}
          loading={loading}
          providers={[]} // 空数组 → 隐藏 provider 筛选器
          channels={effectiveChannels} // 收藏筛选时只显示收藏项中的通道
          effectiveServices={effectiveServices}
          effectiveCategories={[]}  // ProviderPage 不显示分类筛选
          effectiveVendors={effectiveVendors}
          showCategoryFilter={false} // 隐藏分类筛选器
          isMobile={isMobile}
          embed={isEmbedMode} // 嵌入模式隐藏收藏/订阅/板块切换，只留刷新+视图切换
          showFilterDrawer={showFilterDrawer}
          onFilterDrawerClose={() => setShowFilterDrawer(false)}
          onTimeRangeChange={setTimeRange}
          onTimeAlignChange={setTimeAlign}
          onTimeFilterChange={setTimeFilter}
          onBoardChange={setBoard}
          onServiceChange={setFilterService}
          onProviderChange={() => {}} // 无操作
          onChannelChange={setFilterChannel}
          onCategoryChange={() => {}} // 无操作
          onVendorChange={setFilterVendor}
          onShowFavoritesOnlyChange={handleFavoritesModeChange}
          onViewModeChange={setViewMode}
          onRefresh={handleRefresh}
          refreshCooldown={refreshCooldown}
        />

        {/* 主内容区域 - 移除 py-6 以减小与控制面板的间距 */}
        <main>
          {/* 冷板提示条 */}
          {boardsEnabled && board === 'cold' && (
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
            <EmptyFavorites onClearFilter={() => handleFavoritesModeChange(false)} />
          ) : (
            <>
              {effectiveViewMode === 'table' && (
                <StatusTable
                  data={filteredData}
                  sortConfig={sortConfig}
                  timeRange={timeRange}
                  slowLatencyMs={slowLatencyMs}
                  enableAnnotations={isEmbedMode ? false : enableAnnotations}
                  showCategoryTag={false}
                  showProvider={!isEmbedMode}
                  showSponsor={false}
                  isFavorite={isFavorite}
                  onToggleFavorite={toggleFavorite}
                  onSort={handleSort}
                  onBlockHover={handleBlockHover}
                  onBlockLeave={handleBlockLeave}
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
                      enableAnnotations={isEmbedMode ? false : enableAnnotations}
                      showCategoryTag={false}
                      showProvider={!isEmbedMode}
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
        </main>

        {/* 完整模式：显示 Footer */}
        {!isEmbedMode && <Footer rpdiagEnabled={rpdiagEnabled} />}
        </div>
      </div>
    </>
  );
}

/**
 * 404 页面组件 - 服务商未找到
 */
interface ProviderNotFoundProps {
  providerSlug: string;
  isEmbedMode: boolean;
}

function ProviderNotFound({ providerSlug, isEmbedMode }: ProviderNotFoundProps) {
  const { t } = useTranslation();

  return (
    <>
      <Helmet>
        <title>{t('provider.notFoundTitle')}</title>
        <meta name="robots" content="noindex, nofollow" />
      </Helmet>

      <div className={`min-h-screen flex items-center justify-center ${isEmbedMode ? '' : 'bg-page'}`}>
        <div className="text-center px-4">
          <h1 className="text-6xl font-bold text-primary mb-4">404</h1>
          <p className="text-xl text-muted mb-8">
            {t('provider.notFoundMessage', { slug: providerSlug })}
          </p>
          {!isEmbedMode && (
            <a
              href="/"
              className="inline-block px-6 py-3 bg-elevated hover:bg-muted/50 text-primary rounded-lg transition-colors"
            >
              {t('provider.backToHome')}
            </a>
          )}
        </div>
      </div>
    </>
  );
}
