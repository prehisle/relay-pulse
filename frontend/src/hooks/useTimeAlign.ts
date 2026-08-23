import { useCallback, useState } from 'react';
import { trackEvent } from '../utils/analytics';

// localStorage key for time align preference
const STORAGE_KEY_TIME_ALIGN = 'relay-pulse-time-align';

/**
 * 时间对齐模式（使用 localStorage 持久化，不影响分享链接）。
 *
 * @returns `[timeAlign, setTimeAlign]`，setter 同步写 localStorage 并打点
 */
export function useTimeAlign(): [string, (align: string) => void] {
  const [timeAlign, setTimeAlignState] = useState<string>(() => {
    if (typeof window === 'undefined') return '';
    return localStorage.getItem(STORAGE_KEY_TIME_ALIGN) ?? 'hour';
  });

  // 包装 setter 以同步到 localStorage
  const setTimeAlign = useCallback((align: string) => {
    setTimeAlignState(align);
    if (typeof window !== 'undefined') {
      if (align) {
        localStorage.setItem(STORAGE_KEY_TIME_ALIGN, align);
      } else {
        localStorage.removeItem(STORAGE_KEY_TIME_ALIGN);
      }
    }
    // 追踪时间对齐模式变化
    trackEvent('change_time_align', { align: align || 'dynamic' });
  }, []);

  return [timeAlign, setTimeAlign];
}
