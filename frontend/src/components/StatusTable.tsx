import { ArrowUpDown, ArrowUp, ArrowDown, Zap, Shield } from 'lucide-react';
import { StatusDot } from './StatusDot';
import { HeatmapBlock } from './HeatmapBlock';
import { ExternalLink } from './ExternalLink';
import { STATUS, TIME_RANGES } from '../constants';
import { availabilityToColor } from '../utils/color';
import type { ProcessedMonitorData, SortConfig } from '../types';

type HistoryPoint = ProcessedMonitorData['history'][number];

interface StatusTableProps {
  data: ProcessedMonitorData[];
  sortConfig: SortConfig;
  timeRange: string;
  onSort: (key: string) => void;
  onBlockHover: (e: React.MouseEvent<HTMLDivElement>, point: HistoryPoint) => void;
  onBlockLeave: () => void;
}

export function StatusTable({
  data,
  sortConfig,
  timeRange,
  onSort,
  onBlockHover,
  onBlockLeave,
}: StatusTableProps) {
  const SortIcon = ({ columnKey }: { columnKey: string }) => {
    if (sortConfig.key !== columnKey)
      return <ArrowUpDown size={14} className="opacity-30 ml-1" />;
    return sortConfig.direction === 'asc' ? (
      <ArrowUp size={14} className="text-cyan-400 ml-1" />
    ) : (
      <ArrowDown size={14} className="text-cyan-400 ml-1" />
    );
  };

  const currentTimeRange = TIME_RANGES.find((r) => r.id === timeRange);

  return (
    <div className="overflow-x-auto rounded-2xl border border-slate-800/50 shadow-xl">
      <table className="w-full text-left border-collapse bg-slate-900/40 backdrop-blur-sm">
        <thead>
          <tr className="border-b border-slate-700/50 text-slate-400 text-xs uppercase tracking-wider">
            <th
              className="p-4 font-medium cursor-pointer hover:text-cyan-400 transition-colors"
              onClick={() => onSort('providerName')}
            >
              <div className="flex items-center">
                服务商 <SortIcon columnKey="providerName" />
              </div>
            </th>
            <th
              className="p-4 font-medium cursor-pointer hover:text-cyan-400 transition-colors"
              onClick={() => onSort('sponsor')}
            >
              <div className="flex items-center">
                赞助者 <SortIcon columnKey="sponsor" />
              </div>
            </th>
            <th
              className="p-4 font-medium cursor-pointer hover:text-cyan-400 transition-colors"
              onClick={() => onSort('serviceType')}
            >
              <div className="flex items-center">
                服务 <SortIcon columnKey="serviceType" />
              </div>
            </th>
            <th
              className="p-4 font-medium cursor-pointer hover:text-cyan-400 transition-colors"
              onClick={() => onSort('channel')}
            >
              <div className="flex items-center">
                通道 <SortIcon columnKey="channel" />
              </div>
            </th>
            <th
              className="p-4 font-medium cursor-pointer hover:text-cyan-400 transition-colors"
              onClick={() => onSort('currentStatus')}
            >
              <div className="flex items-center">
                当前状态 <SortIcon columnKey="currentStatus" />
              </div>
            </th>
            <th
              className="p-4 font-medium cursor-pointer hover:text-cyan-400 transition-colors"
              onClick={() => onSort('uptime')}
            >
              <div className="flex items-center">
                可用率 <SortIcon columnKey="uptime" />
              </div>
            </th>
            <th className="p-4 font-medium">最后检测</th>
            <th className="p-4 font-medium w-1/3 min-w-[200px]">
              <div className="flex items-center gap-2">
                质量趋势
                <span className="text-[10px] normal-case opacity-50 border border-slate-700 px-1 rounded">
                  {currentTimeRange?.label}
                </span>
              </div>
            </th>
          </tr>
        </thead>
        <tbody className="divide-y divide-slate-800/50 text-sm">
          {data.map((item) => (
            <tr
              key={item.id}
              className="group hover:bg-slate-800/40 transition-[background-color,color]"
            >
              <td className="p-4 font-medium text-slate-200">
                <div className="flex items-center gap-2">
                  <ExternalLink href={item.providerUrl}>{item.providerName}</ExternalLink>
                  <span
                    className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-semibold uppercase tracking-wide ${
                      item.category === 'commercial'
                        ? 'text-emerald-300 bg-emerald-500/10 border border-emerald-500/30'
                        : 'text-cyan-300 bg-cyan-500/10 border border-cyan-500/30'
                    }`}
                    title={item.category === 'commercial' ? '推广站' : '公益站'}
                  >
                    {item.category === 'commercial' ? '推' : '益'}
                  </span>
                </div>
              </td>
              <td className="p-4 text-slate-300 text-sm">
                <ExternalLink href={item.sponsorUrl}>{item.sponsor}</ExternalLink>
              </td>
              <td className="p-4">
                <span
                  className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-mono border ${
                    item.serviceType === 'cc'
                      ? 'border-purple-500/30 text-purple-300 bg-purple-500/10'
                      : 'border-blue-500/30 text-blue-300 bg-blue-500/10'
                  }`}
                >
                  {item.serviceType === 'cc' && <Zap size={10} className="mr-1" />}
                  {item.serviceType === 'cx' && <Shield size={10} className="mr-1" />}
                  {item.serviceType.toUpperCase()}
                </span>
              </td>
              <td className="p-4 text-slate-400 text-xs">
                {item.channel || '-'}
              </td>
              <td className="p-4">
                <div className="flex items-center gap-2">
                  <StatusDot status={item.currentStatus} size="sm" />
                  <span className={STATUS[item.currentStatus].text}>
                    {STATUS[item.currentStatus].label}
                  </span>
                </div>
              </td>
              <td className="p-4 font-mono font-bold">
                <span style={{ color: availabilityToColor(item.uptime) }}>
                  {item.uptime}%
                </span>
              </td>
              <td className="p-4">
                {item.lastCheckTimestamp ? (
                  <div className="text-xs text-slate-400 font-mono flex flex-col gap-0.5">
                    <span>{new Date(item.lastCheckTimestamp * 1000).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })}</span>
                    {item.lastCheckLatency !== undefined && (
                      <span className="text-slate-600 text-[10px]">{item.lastCheckLatency}ms</span>
                    )}
                  </div>
                ) : (
                  <span className="text-slate-600 text-xs">-</span>
                )}
              </td>
              <td className="p-4">
                <div className="flex gap-[2px] h-6 w-full max-w-xs">
                  {item.history.map((point, idx) => (
                    <HeatmapBlock
                      key={idx}
                      point={point}
                      width={`${100 / item.history.length}%`}
                      height="h-full"
                      onHover={onBlockHover}
                      onLeave={onBlockLeave}
                    />
                  ))}
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
