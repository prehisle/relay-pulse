import { useState, useEffect } from 'react';
import { Server } from 'lucide-react';
import { Header } from './components/Header';
import { Controls } from './components/Controls';
import { StatusTable } from './components/StatusTable';
import { StatusCard } from './components/StatusCard';
import { Tooltip } from './components/Tooltip';
import { Footer } from './components/Footer';
import { useMonitorData } from './hooks/useMonitorData';
import { trackPeriodChange, trackServiceFilter, trackEvent } from './utils/analytics';
import type { ViewMode, SortConfig, TooltipState, ProcessedMonitorData } from './types';

function App() {
  const [filterService, setFilterService] = useState('all');
  const [filterProvider, setFilterProvider] = useState('all');
  const [filterChannel, setFilterChannel] = useState('all');
  const [filterCategory, setFilterCategory] = useState('all');
  const [timeRange, setTimeRange] = useState('24h');
  const [viewMode, setViewMode] = useState<ViewMode>('table');
  const [sortConfig, setSortConfig] = useState<SortConfig>({ key: 'uptime', direction: 'desc' });
  const [tooltip, setTooltip] = useState<TooltipState>({
    show: false,
    x: 0,
    y: 0,
    data: null,
  });

  const { loading, error, data, stats, channels, providers, refetch } = useMonitorData({
    timeRange,
    filterService,
    filterProvider,
    filterChannel,
    filterCategory,
    sortConfig,
  });

  // 追踪时间范围变化
  useEffect(() => {
    trackPeriodChange(timeRange);
  }, [timeRange]);

  // 追踪服务筛选变化
  useEffect(() => {
    trackServiceFilter(
      filterProvider !== 'all' ? filterProvider : undefined,
      filterService !== 'all' ? filterService : undefined
    );
  }, [filterProvider, filterService]);

  // 追踪通道筛选变化
  useEffect(() => {
    if (filterChannel !== 'all') {
      trackEvent('filter_channel', { channel: filterChannel });
    }
  }, [filterChannel]);

  // 追踪分类筛选变化
  useEffect(() => {
    if (filterCategory !== 'all') {
      trackEvent('filter_category', { category: filterCategory });
    }
  }, [filterCategory]);

  // 追踪视图模式切换
  useEffect(() => {
    trackEvent('change_view_mode', { mode: viewMode });
  }, [viewMode]);

  const handleSort = (key: string) => {
    let direction: 'asc' | 'desc' = 'desc';
    if (sortConfig.key === key && sortConfig.direction === 'desc') {
      direction = 'asc';
    }
    setSortConfig({ key, direction });
  };

  const handleBlockHover = (
    e: React.MouseEvent<HTMLDivElement>,
    point: ProcessedMonitorData['history'][number]
  ) => {
    const rect = e.currentTarget.getBoundingClientRect();
    setTooltip({
      show: true,
      x: rect.left + rect.width / 2,
      y: rect.top - 10,
      data: point,
    });
  };

  const handleBlockLeave = () => {
    setTooltip((prev) => ({ ...prev, show: false }));
  };

  const handleRefresh = () => {
    trackEvent('manual_refresh');
    refetch();
  };

  return (
    <div className="min-h-screen bg-slate-950 text-slate-200 font-sans selection:bg-cyan-500 selection:text-white overflow-x-hidden">
      {/* 全局 Tooltip */}
      <Tooltip tooltip={tooltip} />

      {/* 背景装饰 */}
      <div className="fixed top-0 left-0 w-full h-full overflow-hidden pointer-events-none z-0">
        <div className="absolute top-[-10%] right-[-10%] w-[600px] h-[600px] bg-blue-600/10 rounded-full blur-[120px]" />
        <div className="absolute bottom-[-10%] left-[-10%] w-[600px] h-[600px] bg-cyan-600/10 rounded-full blur-[120px]" />
      </div>

      <div className="relative z-10 max-w-7xl mx-auto px-4 py-8 sm:px-6 lg:px-8">
        {/* 头部 */}
        <Header stats={stats} />

        {/* 控制栏 */}
        <Controls
          filterProvider={filterProvider}
          filterService={filterService}
          filterChannel={filterChannel}
          filterCategory={filterCategory}
          timeRange={timeRange}
          viewMode={viewMode}
          loading={loading}
          channels={channels}
          providers={providers}
          onProviderChange={setFilterProvider}
          onServiceChange={setFilterService}
          onChannelChange={setFilterChannel}
          onCategoryChange={setFilterCategory}
          onTimeRangeChange={setTimeRange}
          onViewModeChange={setViewMode}
          onRefresh={handleRefresh}
        />

        {/* 内容区域 */}
        {error ? (
          <div className="flex flex-col items-center justify-center py-20 text-rose-400">
            <Server size={64} className="mb-4 opacity-20" />
            <p className="text-lg">加载失败: {error}</p>
          </div>
        ) : loading && data.length === 0 ? (
          <div className="flex flex-col items-center justify-center h-64 text-slate-500 gap-4">
            <div className="w-12 h-12 border-4 border-cyan-500/20 border-t-cyan-500 rounded-full animate-spin" />
            <p className="animate-pulse">正在同步数据节点...</p>
          </div>
        ) : data.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-20 text-slate-600">
            <Server size={64} className="mb-4 opacity-20" />
            <p className="text-lg">未找到符合条件的服务节点</p>
          </div>
        ) : (
          <>
            {viewMode === 'table' && (
              <StatusTable
                data={data}
                sortConfig={sortConfig}
                timeRange={timeRange}
                onSort={handleSort}
                onBlockHover={handleBlockHover}
                onBlockLeave={handleBlockLeave}
              />
            )}

            {viewMode === 'grid' && (
              <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-6">
                {data.map((item) => (
                  <StatusCard
                    key={item.id}
                    item={item}
                    timeRange={timeRange}
                    onBlockHover={handleBlockHover}
                    onBlockLeave={handleBlockLeave}
                  />
                ))}
              </div>
            )}
          </>
        )}

        {/* 免责声明 */}
        <Footer />
      </div>
    </div>
  );
}

export default App;
