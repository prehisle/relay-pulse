import { Filter, RefreshCw, LayoutGrid, List } from 'lucide-react';
import { TIME_RANGES } from '../constants';
import type { ViewMode } from '../types';

interface ControlsProps {
  filterProvider: string;
  filterService: string;
  filterChannel: string;
  filterCategory: string;
  timeRange: string;
  viewMode: ViewMode;
  loading: boolean;
  channels: string[];
  providers: string[];
  onProviderChange: (provider: string) => void;
  onServiceChange: (service: string) => void;
  onChannelChange: (channel: string) => void;
  onCategoryChange: (category: string) => void;
  onTimeRangeChange: (range: string) => void;
  onViewModeChange: (mode: ViewMode) => void;
  onRefresh: () => void;
}

export function Controls({
  filterProvider,
  filterService,
  filterChannel,
  filterCategory,
  timeRange,
  viewMode,
  loading,
  channels,
  providers,
  onProviderChange,
  onServiceChange,
  onChannelChange,
  onCategoryChange,
  onTimeRangeChange,
  onViewModeChange,
  onRefresh,
}: ControlsProps) {
  return (
    <div className="flex flex-col lg:flex-row gap-4 mb-8">
      {/* 筛选和视图控制 */}
      <div className="flex-1 flex flex-wrap gap-4 items-center bg-slate-900/40 p-3 rounded-2xl border border-slate-800/50 backdrop-blur-md">
        <div className="flex items-center gap-2 text-slate-400 text-sm font-medium px-2">
          <Filter size={16} />
        </div>

        <select
          value={filterCategory}
          onChange={(e) => onCategoryChange(e.target.value)}
          className="bg-slate-800 text-slate-200 text-sm rounded-lg border border-slate-700 focus:ring-2 focus:ring-cyan-500 focus:border-transparent p-2 outline-none transition-all hover:bg-slate-750"
        >
          <option value="all">所有分类</option>
          <option value="public">公益站</option>
          <option value="commercial">推广站</option>
        </select>

        <select
          value={filterProvider}
          onChange={(e) => onProviderChange(e.target.value)}
          className="bg-slate-800 text-slate-200 text-sm rounded-lg border border-slate-700 focus:ring-2 focus:ring-cyan-500 focus:border-transparent p-2 outline-none transition-all hover:bg-slate-750"
        >
          <option value="all">所有服务商</option>
          {providers.map((provider) => (
            <option key={provider} value={provider}>
              {provider}
            </option>
          ))}
        </select>

        <select
          value={filterService}
          onChange={(e) => onServiceChange(e.target.value)}
          className="bg-slate-800 text-slate-200 text-sm rounded-lg border border-slate-700 focus:ring-2 focus:ring-cyan-500 focus:border-transparent p-2 outline-none transition-all hover:bg-slate-750"
        >
          <option value="all">所有服务</option>
          <option value="cc">Claude Code (cc)</option>
          <option value="cx">Codex (cx)</option>
        </select>

        <select
          value={filterChannel}
          onChange={(e) => onChannelChange(e.target.value)}
          className="bg-slate-800 text-slate-200 text-sm rounded-lg border border-slate-700 focus:ring-2 focus:ring-cyan-500 focus:border-transparent p-2 outline-none transition-all hover:bg-slate-750"
        >
          <option value="all">所有通道</option>
          {channels.map((channel) => (
            <option key={channel} value={channel}>
              {channel}
            </option>
          ))}
        </select>

        <div className="w-px h-8 bg-slate-700 mx-2 hidden sm:block"></div>

        {/* 视图切换 */}
        <div className="flex bg-slate-800 rounded-lg p-1 border border-slate-700">
          <button
            onClick={() => onViewModeChange('table')}
            className={`p-1.5 rounded ${
              viewMode === 'table'
                ? 'bg-slate-700 text-cyan-400 shadow'
                : 'text-slate-400 hover:text-slate-200'
            }`}
            title="表格视图"
          >
            <List size={18} />
          </button>
          <button
            onClick={() => onViewModeChange('grid')}
            className={`p-1.5 rounded ${
              viewMode === 'grid'
                ? 'bg-slate-700 text-cyan-400 shadow'
                : 'text-slate-400 hover:text-slate-200'
            }`}
            title="卡片视图"
          >
            <LayoutGrid size={18} />
          </button>
        </div>

        {/* 刷新按钮 */}
        <button
          onClick={onRefresh}
          className="ml-auto p-2 rounded-lg bg-cyan-500/10 text-cyan-400 hover:bg-cyan-500/20 transition-colors border border-cyan-500/20 group"
          title="刷新数据"
        >
          <RefreshCw
            size={18}
            className={`transition-transform ${loading ? 'animate-spin' : 'group-hover:rotate-180'}`}
          />
        </button>
      </div>

      {/* 时间范围选择 */}
      <div className="bg-slate-900/40 p-2 rounded-2xl border border-slate-800/50 backdrop-blur-md flex items-center gap-1">
        {TIME_RANGES.map((range) => (
          <button
            key={range.id}
            onClick={() => onTimeRangeChange(range.id)}
            className={`px-3 py-2 text-xs font-medium rounded-xl transition-all duration-200 whitespace-nowrap ${
              timeRange === range.id
                ? 'bg-gradient-to-br from-cyan-500 to-blue-600 text-white shadow-lg shadow-cyan-500/25'
                : 'text-slate-400 hover:text-slate-200 hover:bg-slate-800'
            }`}
          >
            {range.label}
          </button>
        ))}
      </div>
    </div>
  );
}
