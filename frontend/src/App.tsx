import { useState, useEffect, useCallback, useMemo } from 'react';
import { useLocation } from 'react-router-dom';
import { Server } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Helmet } from 'react-helmet-async';
import { Header } from './components/Header';
import { Controls } from './components/Controls';
import { StatusTable } from './components/StatusTable';
import { StatusCard } from './components/StatusCard';
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
import { useScreenshotMode } from './hooks/useScreenshotMode';
import { useFilterOptions } from './hooks/useFilterOptions';
import { useEffectiveFavorites } from './hooks/useEffectiveFavorites';
import { useFilteredData } from './hooks/useFilteredData';
import { useTimeAlign } from './hooks/useTimeAlign';
import { useAutoRefreshPreference, useRefreshCooldown } from './hooks/useRefreshControl';
import { useBlockTooltip } from './hooks/useBlockTooltip';
import { useUrlStateReconcile } from './hooks/useUrlStateReconcile';
import { useHomeAnalytics } from './hooks/useHomeAnalytics';
import { createMediaQueryEffect } from './utils/mediaQuery';

function App() {
  const { t, i18n } = useTranslation();
  const location = useLocation();
  const seo = useSeoMeta({ pathname: location.pathname, language: i18n.language });

  const { isScreenshotMode, screenshotTimestamp, screenshotTitle } = useScreenshotMode(location.search);

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

  // 时间对齐模式（localStorage 持久化，不影响分享链接）
  const [timeAlign, setTimeAlign] = useTimeAlign();

  // 移动端检测（< 960px）
  const [isMobile, setIsMobile] = useState(false);
  useEffect(() => {
    const cleanup = createMediaQueryEffect('tablet', setIsMobile);
    return cleanup;
  }, []);

  // 移动端强制使用 table 视图，截图模式也强制 table
  const effectiveViewMode = isScreenshotMode ? 'table' : (isMobile ? 'table' : viewMode);

  // 热力图色块的全局 Tooltip
  const { tooltip, handleBlockHover, handleBlockLeave } = useBlockTooltip();

  // 自动刷新开关（持久化到 localStorage，默认开启）——必须在 useMonitorData 之前
  const { autoRefresh, handleToggleAutoRefresh } = useAutoRefreshPreference();

  const { scores: rpdiagScores, loaded: rpdiagScoresLoaded } = useRpdiagScores();

  const { loading, error, data, rawData, stats, providers, slowLatencyMs, enableAnnotations, boardsEnabled, boardsEnabledLoaded, boardCounts, allMonitorIds, allMonitorIdsSupported, hidePriceColumn, hideCategoryFilter, effectiveFilterCategory, rpdiagEnabled, refetch } = useMonitorData({
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

  // 手动刷新 + 冷却提示——需要 useMonitorData 的 refetch
  const { refreshCooldown, handleRefresh } = useRefreshCooldown(refetch);

  // 厂商列显隐：基于**未筛选**的全量数据判定，避免"筛一下列就冒出来/消失"的布局抖动。
  // Phase 3 回填厂商前恒 false —— 整列与筛选器对用户完全不存在。
  // ⚠️ 必须先于 useUrlStateReconcile 求值——那里的厂商排序清理要用它。
  const showVendorColumn = useMemo(() => rawData.some(item => !!item.modelVendor), [rawData]);

  // 运行时开关 ↔ URL 状态归一（隐藏列的残留排序、板块归一）
  useUrlStateReconcile({
    hidePriceColumn,
    rpdiagEnabled,
    boardsEnabledLoaded,
    boardsEnabled,
    board,
    setBoard,
    rawDataLength: rawData.length,
    showVendorColumn,
    clearPriceRatioSort,
    clearQualityScoreSort,
    clearModelVendorSort,
  });

  // 有效收藏计数（含无效收藏的静默清理）
  const effectiveFavoritesCount = useEffectiveFavorites({
    loading,
    error,
    favorites,
    favoritesCount,
    allMonitorIds,
    allMonitorIdsSupported,
    cleanupMissingFavorites,
  });

  // 统计激活的筛选器数量（用于移动端 Header 显示）
  const activeFiltersCount = [
    showFavoritesOnly,
    effectiveFilterCategory.length > 0,
    providers.length > 0 && filterProvider.length > 0,
    filterService.length > 0,
    filterChannel.length > 0,
    filterVendor.length > 0,
  ].filter(Boolean).length;

  // 数据筛选链：收藏筛选 → 全筛选器 → 状态统计
  const { optionsBaseData, filteredData, effectiveStats } = useFilteredData({
    data,
    rawData,
    showFavoritesOnly,
    favorites,
    filterProvider,
    filterService,
    filterChannel,
    effectiveFilterCategory,
    filterVendor,
    stats,
  });

  // 五个筛选器的动态选项（联动筛选 + 保留已选项，逐项口径见 hook 内注释）
  const {
    effectiveProviders,
    effectiveServices,
    effectiveChannels,
    effectiveCategories,
    effectiveVendors,
  } = useFilterOptions({
    optionsBaseData,
    filterProvider,
    filterService,
    filterChannel,
    effectiveFilterCategory,
    filterVendor,
    t,
  });

  // 收藏模式切换（使用事务性方法，保存/恢复筛选状态快照）
  const handleFavoritesModeChange = useCallback((enabled: boolean) => {
    if (enabled) {
      enterFavoritesMode();
    } else {
      exitFavoritesMode();
    }
  }, [enterFavoritesMode, exitFavoritesMode]);

  // 筛选/视图埋点（六条 effect 的顺序即上报顺序，见 hook 内注释）
  useHomeAnalytics({
    timeRange,
    filterProvider,
    filterService,
    filterChannel,
    filterVendor,
    effectiveFilterCategory,
    effectiveViewMode,
  });

  const handleSort = (key: string) => {
    let direction: 'asc' | 'desc' = 'desc';
    // 初始状态（置顶模式）下，首次点击任何排序都使用降序
    // 非初始状态下，点击同一字段切换升降序
    if (!isInitialSort && sortConfig.key === key && sortConfig.direction === 'desc') {
      direction = 'asc';
    }
    setSortConfig({ key, direction });
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
              filterCategory={effectiveFilterCategory}
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
              showCategoryFilter={!hideCategoryFilter}
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
