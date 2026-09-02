import { useMemo } from 'react';
import { Filter, LayoutGrid, List, X, Clock, AlignStartVertical, Star } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { getTimeRanges } from '../constants';
import { MultiSelect } from './MultiSelect';
import { TimeFilterPicker } from './TimeFilterPicker';
import { SubscribeButton } from './SubscribeButton';
import { BoardSwitcher } from './BoardSwitcher';
import { RefreshButton } from './RefreshButton';
import type { MultiSelectOption } from './MultiSelect';
import type { ViewMode, ProviderOption, ChannelOption, BoardFilter, BoardCounts } from '../types';

interface ControlsProps {
  filterProvider: string[];  // 多选服务商，空数组表示"全部"
  filterService: string[];   // 多选服务，空数组表示"全部"
  filterChannel: string[];   // 多选通道，空数组表示"全部"
  filterCategory: string[];  // 多选分类，空数组表示"全部"
  filterVendor: string[];    // 多选模型厂商，空数组表示"全部"
  filterModel: string[];     // 多选模型（版本级 canonical key），空数组表示"全部"
  showFavoritesOnly: boolean; // 仅显示收藏
  favorites: Set<string>;     // 收藏项集合
  favoritesCount: number;     // 收藏数量
  timeRange: string;
  timeAlign: string;         // 时间对齐模式：空=动态窗口, "hour"=整点对齐
  timeFilter: string | null; // 每日时段过滤：null=全天, "09:00-17:00"=自定义
  board: BoardFilter;        // 当前板块：hot/secondary/cold/all
  boardsEnabled: boolean;    // 板块功能是否启用
  boardCounts?: BoardCounts; // 各板块通道数量（可选，兼容旧后端）
  viewMode: ViewMode;
  loading: boolean;
  channels: ChannelOption[];  // 通道选项列表
  providers: ProviderOption[];  // 服务商选项列表
  effectiveServices: string[];    // 动态服务选项（始终传递数组）
  effectiveCategories: string[];  // 动态分类选项（始终传递数组）
  /** 动态模型厂商选项（{value:code,label:本地化名}，已按 label 排序）。
   *  空数组 = 当前数据里没有任何通道声明了厂商 → 整个筛选器不渲染，
   *  而不是给一个永远打不开的空下拉。Phase 3 回填前生产恒为空。 */
  effectiveVendors: MultiSelectOption[];
  /** 动态模型选项（{value:canonical key, label:版本级展示名}，按家族分组、组内按名排序）。
   *  每项带 groupKey/groupLabel，MultiSelect 据此渲染可一键全选的家族分组。
   *  空数组 = 当前数据里一个模型都没有 → 整个筛选器不渲染。 */
  effectiveModels: MultiSelectOption[];
  showCategoryFilter?: boolean; // 是否显示分类筛选器，默认 true（用于服务商专属页面）
  showVendorFilter?: boolean;   // 是否显示厂商筛选器，默认 true（runtime 开关 hide_vendor_filter 的反面）
  refreshCooldown?: boolean; // 刷新冷却中，显示提示
  autoRefresh?: boolean; // 自动刷新开关
  isMobile?: boolean; // 是否为移动端，用于隐藏视图切换按钮
  embed?: boolean; // 嵌入模式（?embed=1）：隐藏收藏/订阅/板块切换等平台级控件，只留刷新+视图切换
  showFilterDrawer?: boolean; // 移动端筛选抽屉是否显示（由 App 层级控制）
  onFilterDrawerClose?: () => void; // 关闭筛选抽屉回调
  onProviderChange: (providers: string[]) => void;  // 多选回调
  onServiceChange: (services: string[]) => void;    // 多选回调
  onChannelChange: (channels: string[]) => void;    // 多选回调
  onCategoryChange: (categories: string[]) => void; // 多选回调
  onVendorChange: (vendors: string[]) => void;      // 多选回调
  onModelChange: (models: string[]) => void;        // 多选回调
  onShowFavoritesOnlyChange: (value: boolean) => void; // 收藏筛选回调
  onTimeRangeChange: (range: string) => void;
  onTimeAlignChange: (align: string) => void;       // 切换时间对齐模式
  onTimeFilterChange: (filter: string | null) => void; // 切换每日时段过滤
  onBoardChange: (board: BoardFilter) => void;      // 切换板块
  onViewModeChange: (mode: ViewMode) => void;
  onRefresh: () => void;
  onToggleAutoRefresh?: () => void; // 切换自动刷新开关
}

export function Controls({
  filterProvider,
  filterService,
  filterChannel,
  filterCategory,
  filterVendor,
  filterModel,
  showFavoritesOnly,
  favorites,
  favoritesCount,
  timeRange,
  timeAlign,
  timeFilter,
  board,
  boardsEnabled,
  boardCounts,
  viewMode,
  loading,
  channels,
  providers,
  effectiveServices,
  effectiveCategories,
  effectiveVendors,
  effectiveModels,
  showCategoryFilter = true,
  showVendorFilter = true,
  refreshCooldown = false,
  autoRefresh = true,
  isMobile = false,
  embed = false,
  showFilterDrawer = false,
  onFilterDrawerClose,
  onProviderChange,
  onServiceChange,
  onChannelChange,
  onCategoryChange,
  onVendorChange,
  onModelChange,
  onShowFavoritesOnlyChange,
  onTimeRangeChange,
  onTimeAlignChange,
  onTimeFilterChange,
  onBoardChange,
  onViewModeChange,
  onRefresh,
  onToggleAutoRefresh,
}: ControlsProps) {
  const { t } = useTranslation();

  // 服务选项（始终基于 effectiveServices 动态计算）
  const serviceOptions = useMemo<MultiSelectOption[]>(() => {
    const allOptions = [
      { value: 'cc', label: t('controls.services.cc') },
      { value: 'cx', label: t('controls.services.cx') },
      { value: 'gm', label: t('controls.services.gm') },
    ];
    // 空数组表示无数据，显示全部选项作为回退
    if (effectiveServices.length === 0) return allOptions;
    return allOptions.filter(opt => effectiveServices.includes(opt.value));
  }, [t, effectiveServices]);

  // 分类选项（始终基于 effectiveCategories 动态计算）
  const categoryOptions = useMemo<MultiSelectOption[]>(() => {
    const allOptions = [
      { value: 'public', label: t('controls.categories.charity') },
      { value: 'commercial', label: t('controls.categories.promoted') },
    ];
    // 空数组表示无数据，显示全部选项作为回退
    if (effectiveCategories.length === 0) return allOptions;
    return allOptions.filter(opt => effectiveCategories.includes(opt.value));
  }, [t, effectiveCategories]);

  // 通道选项（已经是 ChannelOption[] 格式，直接转换为 MultiSelectOption[]）
  const channelOptions = useMemo<MultiSelectOption[]>(() => channels, [channels]);

  // 厂商筛选器：runtime 开关关掉、或数据里没有任何厂商声明时都不渲染。
  // 两个条件缺一不可——开关是运营决定，空数据是「回填前恒不可见」的既有行为。
  const vendorFilterVisible = showVendorFilter && effectiveVendors.length > 0;

  // 模型筛选器：同款数据驱动，本屏一个模型都没有时不渲染空下拉
  const showModelFilter = effectiveModels.length > 0;

  // 统计激活的筛选器数量（仅计入可见的筛选器）
  const activeFiltersCount = [
    showFavoritesOnly,
    showCategoryFilter && filterCategory.length > 0,
    providers.length > 0 && filterProvider.length > 0,
    filterService.length > 0,
    filterChannel.length > 0,
    vendorFilterVisible && filterVendor.length > 0,
    showModelFilter && filterModel.length > 0,
  ].filter(Boolean).length;

  // 筛选器组件（桌面和移动端共用）
  const FilterSelects = () => (
    <>
      {/* Category 筛选器 - 可通过 showCategoryFilter 控制显示 */}
      {showCategoryFilter && (
        <MultiSelect
          value={filterCategory}
          options={categoryOptions}
          onChange={onCategoryChange}
          placeholder={t('controls.filters.category')}
          searchable={false}
        />
      )}

      {/* Provider 筛选器 - 当 providers 为空时隐藏（用于服务商专属页面） */}
      {providers.length > 0 && (
        <MultiSelect
          value={filterProvider}
          options={providers}
          onChange={onProviderChange}
          placeholder={t('controls.filters.provider')}
          searchable
        />
      )}

      {/* Service 筛选器 */}
      <MultiSelect
        value={filterService}
        options={serviceOptions}
        onChange={onServiceChange}
        placeholder={t('controls.filters.service')}
        searchable={false}
      />

      {/* Channel 筛选器 */}
      <MultiSelect
        value={filterChannel}
        options={channelOptions}
        onChange={onChannelChange}
        placeholder={t('controls.filters.channel')}
        searchable={channels.length > 5}
      />

      {/* 模型筛选器 - 按家族分组，组标题可一键全选整个家族（如所有 Opus 版本）。
          恒可搜索：版本级选项有二十来个，比其它维度都长。 */}
      {showModelFilter && (
        <MultiSelect
          value={filterModel}
          options={effectiveModels}
          onChange={onModelChange}
          placeholder={t('controls.filters.model')}
          searchable
        />
      )}

      {/* 模型厂商筛选器 - 数据里出现过厂商声明才渲染 */}
      {vendorFilterVisible && (
        <MultiSelect
          value={filterVendor}
          options={effectiveVendors}
          onChange={onVendorChange}
          placeholder={t('controls.filters.vendor')}
          searchable={effectiveVendors.length > 5}
        />
      )}
    </>
  );

  return (
    <>
      <div className="flex flex-wrap items-center gap-2 mb-2 lg:mb-3 overflow-visible">
        {/* 筛选器区块（移动端隐藏） */}
        <div className="hidden min-[960px]:flex items-center gap-1.5 bg-surface/60 p-1.5 rounded-2xl min-w-0 max-w-full overflow-visible">
          <div className="flex items-center gap-2 text-secondary text-sm font-medium px-1 flex-shrink-0">
            <Filter size={16} />
          </div>
          <div className="flex items-center gap-1.5 min-w-0 overflow-visible">
            {FilterSelects()}
          </div>
        </div>

        {/* 第二组：操作按钮 + 时间范围（作为整体参与换行，min-w-max 让宽度随内容自适应） */}
        <div className="flex flex-1 min-w-0 min-[960px]:min-w-max items-center gap-2">
          {/* 操作按钮组（移动端隐藏） */}
          <div className="hidden min-[960px]:flex min-[960px]:order-2 min-[960px]:ml-auto items-center gap-1.5 bg-surface/60 p-1.5 rounded-2xl overflow-visible flex-shrink-0">
          {/* 收藏 / 订阅 / 板块切换 —— 嵌入模式整组隐藏（中转商外站只保留刷新与视图切换） */}
          {!embed && (
            <>
          {/* 收藏 + 订阅按钮组 */}
          <div className="flex items-center h-8 bg-elevated/50 rounded-lg border border-default/50 overflow-hidden">
            {/* 收藏筛选按钮 */}
            <button
              type="button"
              onClick={() => onShowFavoritesOnlyChange(!showFavoritesOnly)}
              className={`
                flex items-center gap-1.5 px-3 h-full transition-all duration-200
                focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent/50 focus-visible:outline-none
                ${showFavoritesOnly
                  ? 'bg-accent/10 text-accent'
                  : 'text-secondary hover:text-primary hover:bg-muted/50'
                }
                ${!showFavoritesOnly && favoritesCount === 0 ? 'opacity-50 cursor-not-allowed' : ''}
              `}
              disabled={!showFavoritesOnly && favoritesCount === 0}
              title={showFavoritesOnly
                ? t('controls.favorites.exitMode')
                : (favoritesCount > 0
                  ? t('controls.favorites.showOnly')
                  : t('controls.favorites.noFavorites'))
              }
              aria-label={showFavoritesOnly
                ? t('controls.favorites.exitMode')
                : (favoritesCount > 0
                  ? t('controls.favorites.showOnly')
                  : t('controls.favorites.noFavorites'))
              }
              aria-pressed={showFavoritesOnly}
            >
              <Star
                size={14}
                className={showFavoritesOnly ? 'text-warning' : ''}
                fill={showFavoritesOnly ? 'currentColor' : 'none'}
                strokeWidth={showFavoritesOnly ? 0 : 2}
              />
              {favoritesCount > 0 && (
                <span className="text-xs font-medium">{favoritesCount}</span>
              )}
            </button>

            {/* 按钮组内分隔线 */}
            <div className="w-px h-5 bg-muted/50"></div>

            {/* 订阅通知按钮（图标模式，融入按钮组） */}
            <SubscribeButton favorites={favorites} iconOnly inGroup />
          </div>

          <div className="w-px h-5 bg-muted mx-1"></div>

          {/* 板块切换（下拉菜单） */}
          <BoardSwitcher board={board} onBoardChange={onBoardChange} enabled={boardsEnabled} boardCounts={boardCounts} />
            </>
          )}

          {/* 视图切换（仅桌面端显示） */}
          {!isMobile && (
            <div className="flex h-8 bg-surface rounded-lg p-0.5 border border-default/50 shadow-sm">
              <button
                type="button"
                onClick={() => onViewModeChange('table')}
                className={`p-1.5 rounded min-w-[32px] flex-1 flex items-center justify-center focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none ${
                  viewMode === 'table'
                    ? 'bg-muted text-accent shadow'
                    : 'text-secondary hover:text-primary'
                }`}
                title={t('controls.views.table')}
                aria-label={t('controls.views.switchToTable')}
              >
                <List size={16} />
              </button>
              <button
                type="button"
                onClick={() => onViewModeChange('grid')}
                className={`p-1.5 rounded min-w-[32px] flex-1 flex items-center justify-center focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none ${
                  viewMode === 'grid'
                    ? 'bg-muted text-accent shadow'
                    : 'text-secondary hover:text-primary'
                }`}
                title={t('controls.views.card')}
                aria-label={t('controls.views.switchToCard')}
              >
                <LayoutGrid size={16} />
              </button>
            </div>
          )}

          {/* 刷新按钮 */}
          <RefreshButton
            loading={loading}
            autoRefresh={autoRefresh}
            refreshCooldown={refreshCooldown}
            onRefresh={onRefresh}
            onToggleAutoRefresh={onToggleAutoRefresh}
            size="md"
          />
        </div>

        {/* 时间范围选择 */}
        <div className="flex-1 min-w-0 min-[960px]:order-1 relative z-20 bg-surface/40 p-2 rounded-2xl backdrop-blur-md">
          <div className="flex items-center gap-1">
            {/* 可滚动区域：时间范围按钮 */}
            <div className="flex items-center gap-1 overflow-x-auto scrollbar-hide min-w-0">
              {/* 时间范围按钮（24h 按钮集成时间对齐切换） */}
              {getTimeRanges(t).map((range) => {
                const isActive = timeRange === range.id;
                const is24h = range.id === '24h';

                return (
                  <button
                    type="button"
                    key={range.id}
                    onClick={() => {
                      if (is24h && isActive) {
                        // 已选中 24h 时，再次点击切换对齐模式
                        onTimeAlignChange(timeAlign === 'hour' ? '' : 'hour');
                      } else {
                        onTimeRangeChange(range.id);
                      }
                    }}
                    title={is24h && isActive
                      ? (timeAlign === 'hour' ? t('controls.timeAlign.hourTitle') : t('controls.timeAlign.dynamicTitle'))
                      : undefined}
                    className={`px-3 py-2 text-xs font-medium rounded-xl transition-all duration-200 whitespace-nowrap flex-shrink-0 focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none ${
                      isActive
                        ? 'bg-gradient-button text-inverse shadow-lg shadow-accent/25'
                        : 'text-secondary hover:text-primary hover:bg-elevated'
                    }`}
                  >
                    <span className="flex items-center gap-1">
                      {is24h && (
                        timeAlign === 'hour'
                          ? <AlignStartVertical size={12} />
                          : <Clock size={12} />
                      )}
                      {range.label}
                    </span>
                  </button>
                );
              })}

              {/* 时段筛选（仅 7d/30d 有效）- 现在在滚动容器内，Portal 确保下拉菜单不被裁剪 */}
              <div className="flex-shrink-0">
                <TimeFilterPicker
                  value={timeFilter}
                  disabled={timeRange === '24h' || timeRange === '90m'}
                  onChange={onTimeFilterChange}
                />
              </div>
            </div>
          </div>
        </div>
        </div>
      </div>

      {/* 移动端筛选抽屉（960px 以下显示） */}
      {showFilterDrawer && (
        <div
          className="fixed inset-0 z-50 bg-black/60 backdrop-blur-sm min-[960px]:hidden"
          onClick={() => onFilterDrawerClose?.()}
        >
          <div
            className="absolute bottom-0 left-0 right-0 bg-surface border-t border-default rounded-t-2xl p-6 max-h-[80vh] overflow-y-auto"
            onClick={(e) => e.stopPropagation()}
          >
            {/* 抽屉头部 */}
            <div className="flex justify-between items-center mb-6">
              <div className="flex items-center gap-2">
                <Filter size={20} className="text-accent" />
                <h3 className="text-lg font-semibold text-primary">{t('controls.mobile.filterTitle')}</h3>
                {activeFiltersCount > 0 && (
                  <span className="px-2 py-0.5 bg-accent text-inverse text-xs rounded-full">
                    {activeFiltersCount}
                  </span>
                )}
              </div>
              <button
                type="button"
                onClick={() => onFilterDrawerClose?.()}
                className="p-2 rounded-lg bg-elevated text-secondary hover:text-primary transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
                aria-label={t('controls.mobile.closeFilter')}
              >
                <X size={20} />
              </button>
            </div>

            {/* 筛选器列表 */}
            <div className="flex flex-col gap-4">
              {FilterSelects()}

              {/* 收藏 / 板块 / 订阅 —— 嵌入模式整组隐藏 */}
              {!embed && (
                <>
              {/* 收藏筛选按钮（移动端） */}
              <button
                type="button"
                onClick={() => onShowFavoritesOnlyChange(!showFavoritesOnly)}
                className={`
                  flex items-center justify-center gap-2 w-full py-3 rounded-lg transition-all duration-200
                  focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none
                  ${showFavoritesOnly
                    ? 'bg-accent/10 text-accent border border-accent/30'
                    : 'bg-elevated text-secondary hover:text-primary border border-transparent'
                  }
                  ${!showFavoritesOnly && favoritesCount === 0 ? 'opacity-50 cursor-not-allowed' : ''}
                `}
                disabled={!showFavoritesOnly && favoritesCount === 0}
              >
                <Star
                  size={16}
                  className={showFavoritesOnly ? 'text-warning' : ''}
                  fill={showFavoritesOnly ? 'currentColor' : 'none'}
                  strokeWidth={showFavoritesOnly ? 0 : 2}
                />
                <span className="font-medium">
                  {t('controls.favorites.showOnly')}
                  {favoritesCount > 0 && ` (${favoritesCount})`}
                </span>
              </button>

              {/* 板块切换（移动端按钮组） */}
              {boardsEnabled && (
                <div className="w-full">
                  <div className="flex items-center justify-between px-4 py-3 border-b border-default/20">
                    <span className="text-sm text-secondary">{t('controls.boards.selectBoard')}</span>
                  </div>
                  <div className="flex gap-2 p-4">
                    {(['hot', 'secondary', 'cold', 'all'] as BoardFilter[]).map((b) => {
                      const count = boardCounts
                        ? (b === 'all'
                          ? boardCounts.hot + boardCounts.secondary + boardCounts.cold
                          : boardCounts[b as keyof BoardCounts])
                        : undefined;
                      return (
                        <button
                          key={b}
                          onClick={() => {
                            onBoardChange(b);
                            onFilterDrawerClose?.();
                          }}
                          className={`
                            flex-1 h-12 flex flex-col items-center justify-center gap-0.5 rounded-lg
                            transition-all duration-200 focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none
                            ${board === b ? 'bg-accent/20 text-accent border border-accent' : 'bg-elevated/50 text-primary border border-default/50 hover:bg-muted/50'}
                          `}
                          aria-label={t(`controls.boards.${b}`)}
                          title={t(`controls.boards.${b}`)}
                        >
                          <div className="flex items-center gap-1">
                            <BoardSwitcher.Icon board={b} />
                            <span className="text-xs font-medium">{t(`controls.boards.${b}Short`)}</span>
                          </div>
                          {count !== undefined && (
                            <span className={`text-[10px] tabular-nums ${board === b ? 'text-accent/70' : 'text-secondary'}`}>
                              {count}
                            </span>
                          )}
                        </button>
                      );
                    })}
                  </div>
                </div>
              )}

              {/* 订阅通知按钮（移动端） */}
              <SubscribeButton favorites={favorites} className="w-full justify-center py-3" />
                </>
              )}

              {/* 清空按钮 - 只清空可见的筛选器 */}
              {activeFiltersCount > 0 && (
                <button
                  type="button"
                  onClick={() => {
                    onShowFavoritesOnlyChange(false);
                    if (showCategoryFilter) onCategoryChange([]);
                    if (providers.length > 0) onProviderChange([]);
                    onServiceChange([]);
                    onChannelChange([]);
                    if (vendorFilterVisible) onVendorChange([]);
                    if (showModelFilter) onModelChange([]);
                  }}
                  className="w-full py-3 bg-elevated text-secondary rounded-lg hover:bg-muted transition-colors font-medium focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
                >
                  {t('common.clear')}
                </button>
              )}

              {/* 应用按钮 */}
              <button
                type="button"
                onClick={() => onFilterDrawerClose?.()}
                className="w-full py-3 bg-gradient-button text-inverse rounded-lg font-medium shadow-lg shadow-accent/25 hover:shadow-accent/40 transition-all focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
              >
                {t('common.apply')}
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
