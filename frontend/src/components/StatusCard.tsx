import { Activity, Clock, Zap, Shield } from 'lucide-react';
import { StatusDot } from './StatusDot';
import { HeatmapBlock } from './HeatmapBlock';
import { STATUS } from '../constants';
import type { ProcessedMonitorData } from '../types';

type HistoryPoint = ProcessedMonitorData['history'][number];

interface StatusCardProps {
  item: ProcessedMonitorData;
  timeRange: string;
  onBlockHover: (e: React.MouseEvent<HTMLDivElement>, point: HistoryPoint) => void;
  onBlockLeave: () => void;
}

export function StatusCard({ item, timeRange, onBlockHover, onBlockLeave }: StatusCardProps) {
  return (
    <div className="group relative bg-slate-900/60 border border-slate-800 hover:border-cyan-500/30 rounded-2xl p-6 transition-all duration-300 hover:shadow-[0_0_30px_rgba(6,182,212,0.1)] backdrop-blur-sm overflow-hidden">
      {/* 顶部状态条 */}
      <div className={`absolute top-0 left-0 w-full h-1 ${STATUS[item.currentStatus].color}`} />

      <div className="flex justify-between items-start mb-6">
        <div className="flex gap-4 items-center">
          <div className="w-12 h-12 rounded-xl bg-slate-800 flex items-center justify-center border border-slate-700 group-hover:border-slate-600 transition-colors">
            {item.serviceType === 'cc' ? (
              <Zap className="text-purple-400" size={24} />
            ) : (
              <Shield className="text-blue-400" size={24} />
            )}
          </div>
          <div>
            <div className="flex items-center gap-2">
              <h3 className="text-lg font-bold text-slate-100">{item.providerName}</h3>
              <span
                className={`px-2 py-0.5 rounded text-[10px] font-mono border ${
                  item.serviceType === 'cc'
                    ? 'border-purple-500/30 text-purple-300 bg-purple-500/10'
                    : 'border-blue-500/30 text-blue-300 bg-blue-500/10'
                }`}
              >
                {item.serviceType.toUpperCase()}
              </span>
            </div>
            <div className="flex items-center gap-2 mt-1 text-xs text-slate-400 font-mono">
              <Activity size={12} />
              <span>可用率: {item.uptime}%</span>
            </div>
          </div>
        </div>
        <div className="flex flex-col items-end gap-1.5">
          <div className="flex items-center gap-2 px-3 py-1 rounded-full bg-slate-800 border border-slate-700">
            <StatusDot status={item.currentStatus} />
            <span className={`text-xs font-bold ${STATUS[item.currentStatus].text}`}>
              {STATUS[item.currentStatus].label}
            </span>
          </div>
          {item.lastCheckTimestamp && (
            <div className="text-[10px] text-slate-500 font-mono flex flex-col items-end gap-0.5">
              <span>{new Date(item.lastCheckTimestamp * 1000).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })}</span>
              {item.lastCheckLatency !== undefined && (
                <span className="text-slate-600">{item.lastCheckLatency}ms</span>
              )}
            </div>
          )}
        </div>
      </div>

      {/* 热力图 */}
      <div>
        <div className="flex justify-between text-xs text-slate-500 mb-2">
          <span className="flex items-center gap-1">
            <Clock size={12} /> {timeRange === '24h' ? '24h' : `${parseInt(timeRange)}d`}
          </span>
          <span>Now</span>
        </div>
        <div className="flex gap-[3px] h-10 w-full">
          {item.history.map((point, idx) => (
            <HeatmapBlock
              key={idx}
              point={point}
              width={`${100 / item.history.length}%`}
              onHover={onBlockHover}
              onLeave={onBlockLeave}
            />
          ))}
        </div>
      </div>
    </div>
  );
}
