import { STATUS } from '../constants';
import type { TooltipState } from '../types';

interface TooltipProps {
  tooltip: TooltipState;
}

export function Tooltip({ tooltip }: TooltipProps) {
  if (!tooltip.show || !tooltip.data) return null;

  return (
    <div
      className="fixed z-50 pointer-events-none transition-opacity duration-200"
      style={{
        left: tooltip.x,
        top: tooltip.y,
        transform: 'translate(-50%, -100%)',
      }}
    >
      <div className="bg-slate-900/95 backdrop-blur-md text-slate-200 text-xs p-3 rounded-lg border border-slate-700 shadow-[0_10px_40px_-10px_rgba(0,0,0,0.8)] whitespace-nowrap flex flex-col items-center gap-1">
        <div className="text-slate-400">{tooltip.data.timestamp}</div>
        <div className={`font-bold text-sm ${STATUS[tooltip.data.status].text}`}>
          {STATUS[tooltip.data.status].label}
        </div>
        <div className="text-slate-400 text-[10px]">延迟: {tooltip.data.latency}ms</div>
        {/* 小三角箭头 */}
        <div className="absolute -bottom-1.5 left-1/2 -translate-x-1/2 w-3 h-3 bg-slate-900 border-r border-b border-slate-700 transform rotate-45"></div>
      </div>
    </div>
  );
}
