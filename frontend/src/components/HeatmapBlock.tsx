import { STATUS } from '../constants';
import type { StatusKey } from '../types';

interface HeatmapPoint {
  index: number;
  status: StatusKey;
  timestamp: string;
  latency: number;
}

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
