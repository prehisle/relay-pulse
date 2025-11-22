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
    slow_latency: 0,
    rate_limit: 0,
    server_error: 0,
    client_error: 0,
    auth_error: 0,
    invalid_request: 0,
    network_error: 0,
    content_mismatch: 0,
  };

  // 状态统计（不再显示"无数据"，因为运行时不会产生 status=3）
  const statusSummary = [
    { key: 'available', emoji: '🟢', label: '可用', value: counts.available },
    { key: 'degraded', emoji: '🟡', label: '波动', value: counts.degraded },
    { key: 'unavailable', emoji: '🔴', label: '不可用', value: counts.unavailable },
  ];

  // 黄色波动细分
  const degradedSubstatus = [
    { key: 'slow_latency', label: '响应慢', value: counts.slow_latency },
    { key: 'rate_limit', label: '限流', value: counts.rate_limit },
  ].filter(item => item.value > 0);

  // 红色不可用细分
  const unavailableSubstatus = [
    { key: 'server_error', label: '服务器错误', value: counts.server_error },
    { key: 'client_error', label: '客户端错误', value: counts.client_error },
    { key: 'auth_error', label: '认证失败', value: counts.auth_error },
    { key: 'invalid_request', label: '请求参数错误', value: counts.invalid_request },
    { key: 'network_error', label: '连接失败', value: counts.network_error },
    { key: 'content_mismatch', label: '内容校验失败', value: counts.content_mismatch },
  ].filter(item => item.value > 0);

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

        {/* 黄色波动细分 */}
        {degradedSubstatus.length > 0 && (
          <div className="flex flex-col gap-1 pt-2 border-t border-slate-700/50">
            <div className="text-[10px] text-slate-400 mb-0.5">🟡 波动细分</div>
            {degradedSubstatus.map((item) => (
              <div key={item.key} className="flex justify-between items-center gap-3 text-[10px] pl-2">
                <span className="text-slate-400">• {item.label}</span>
                <span className="text-slate-200 tabular-nums">{item.value}</span>
              </div>
            ))}
          </div>
        )}

        {/* 红色不可用细分 */}
        {unavailableSubstatus.length > 0 && (
          <div className="flex flex-col gap-1 pt-2 border-t border-slate-700/50">
            <div className="text-[10px] text-slate-400 mb-0.5">🔴 不可用细分</div>
            {unavailableSubstatus.map((item) => (
              <div key={item.key} className="flex justify-between items-center gap-3 text-[10px] pl-2">
                <span className="text-slate-400">• {item.label}</span>
                <span className="text-slate-200 tabular-nums">{item.value}</span>
              </div>
            ))}
          </div>
        )}

        {/* 小三角箭头 */}
        <div className="absolute -bottom-1.5 left-1/2 -translate-x-1/2 w-3 h-3 bg-slate-900 border-r border-b border-slate-700 transform rotate-45"></div>
      </div>
    </div>
  );
}
