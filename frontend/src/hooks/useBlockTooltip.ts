import { useCallback, useState } from 'react';
import type React from 'react';
import type { ProcessedMonitorData, TooltipState } from '../types';

export interface BlockTooltip {
  tooltip: TooltipState;
  /** 热力图色块 hover：按色块位置定位浮层。 */
  handleBlockHover: (
    e: React.MouseEvent<HTMLDivElement>,
    point: ProcessedMonitorData['history'][number],
  ) => void;
  handleBlockLeave: () => void;
}

/** 热力图色块的全局 Tooltip 状态与两个交互回调（引用恒定）。 */
export function useBlockTooltip(): BlockTooltip {
  const [tooltip, setTooltip] = useState<TooltipState>({
    show: false,
    x: 0,
    y: 0,
    data: null,
  });

  const handleBlockHover = useCallback((
    e: React.MouseEvent<HTMLDivElement>,
    point: ProcessedMonitorData['history'][number]
  ) => {
    const rect = e.currentTarget.getBoundingClientRect();
    setTooltip({
      show: true,
      x: rect.left + rect.width / 2,
      y: rect.top - 10,
      blockBottom: rect.bottom + 10,
      data: point,
    });
  }, []);

  const handleBlockLeave = useCallback(() => {
    setTooltip((prev) => ({ ...prev, show: false }));
  }, []);

  return { tooltip, handleBlockHover, handleBlockLeave };
}
