import { STATUS } from '../constants';
import type { ProcessedMonitorData } from '../types';

// 直接使用 ProcessedMonitorData 中的 history 类型，确保字段完整性
type HeatmapPoint = ProcessedMonitorData['history'][number];

interface HeatmapBlockProps {
  point: HeatmapPoint;
  width: string;
  height?: string;
  onHover: (e: React.MouseEvent<HTMLDivElement>, point: HeatmapPoint) => void;
  onLeave: () => void;
}

export function HeatmapBlock({
  point,
  width,
  height = 'h-8',
  onHover,
  onLeave,
}: HeatmapBlockProps) {
  return (
    <div
      className={`${height} rounded-sm transition-all duration-200 hover:scale-110 hover:z-10 cursor-pointer ${STATUS[point.status].color} opacity-80 hover:opacity-100`}
      style={{ width }}
      onMouseEnter={(e) => onHover(e, point)}
      onMouseLeave={onLeave}
    />
  );
}
