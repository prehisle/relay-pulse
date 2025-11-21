import type { TooltipState } from '../types';
import { availabilityToColor } from '../utils/color';

interface TooltipProps {
  tooltip: TooltipState;
}

export function Tooltip({ tooltip }: TooltipProps) {
  if (!tooltip.show || !tooltip.data) return null;

  // 状态计数统计（向后兼容）
  const counts = tooltip.data.statusCounts ?? {
    available: 0,
    degraded: 0,
    unavailable: 0,
    missing: 0,
  };

  const statusSummary = [
    { key: 'available', emoji: '🟢', label: '可用', value: counts.available },
    { key: 'degraded', emoji: '🟡', label: '波动', value: counts.degraded },
    { key: 'unavailable', emoji: '🔴', label: '不可用', value: counts.unavailable },
    { key: 'missing', emoji: '⚪', label: '无数据', value: counts.missing },
  ];

  return (
    <div
      className="fixed z-50 pointer-events-none transition-opacity duration-200"
      style={{
        left: tooltip.x,
        top: tooltip.y,
        transform: 'translate(-50%, -100%)',
      }}
    >
      <div className="bg-slate-900/95 backdrop-blur-md text-slate-200 text-xs p-3 rounded-lg border border-slate-700 shadow-[0_10px_40px_-10px_rgba(0,0,0,0.8)] flex flex-col gap-2 min-w-[180px]">
        <div className="text-slate-400 text-center">
          {new Date(tooltip.data.timestampNum * 1000).toLocaleString('zh-CN')}
        </div>
        {tooltip.data.availability >= 0 && (
          <div
            className="font-medium text-center"
            style={{ color: availabilityToColor(tooltip.data.availability) }}
          >
            可用率: {tooltip.data.availability.toFixed(2)}%
          </div>
        )}
        {tooltip.data.latency > 0 && (
          <div className="text-slate-500 text-[10px] text-center">延迟: {tooltip.data.latency}ms</div>
        )}

        {/* 状态统计 */}
        <div className="flex flex-col gap-1 pt-2 border-t border-slate-700/50">
          {statusSummary.map((item) => (
            <div key={item.key} className="flex justify-between items-center gap-3 text-[11px]">
              <span className="text-slate-300">
                {item.emoji} {item.label}
              </span>
              <span className="text-slate-100 font-semibold tabular-nums">
                {item.value} 次
              </span>
            </div>
          ))}
        </div>

        {/* 小三角箭头 */}
        <div className="absolute -bottom-1.5 left-1/2 -translate-x-1/2 w-3 h-3 bg-slate-900 border-r border-b border-slate-700 transform rotate-45"></div>
      </div>
    </div>
  );
}
