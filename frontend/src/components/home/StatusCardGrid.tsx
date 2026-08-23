import type { ComponentProps } from 'react';
import { StatusCard } from '../StatusCard';
import type { ProcessedMonitorData } from '../../types';

/** 卡片视图：逐项渲染 StatusCard，除 item 外的 prop 原样透传（含必填的显隐开关）。 */
export interface StatusCardGridProps extends Omit<ComponentProps<typeof StatusCard>, 'item'> {
  data: ProcessedMonitorData[];
}

export function StatusCardGrid({ data, ...cardProps }: StatusCardGridProps) {
  return (
    <div data-heatmap-container className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-6">
      {data.map((item) => (
        <StatusCard
          key={item.id}
          item={item}
          {...cardProps}
        />
      ))}
    </div>
  );
}
