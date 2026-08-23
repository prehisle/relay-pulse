import { useRef, useState } from 'react';
import { trackEvent } from '../utils/analytics';

// 自动刷新开关的 localStorage key
const AUTO_REFRESH_KEY = 'relay-pulse-auto-refresh';

// 刷新冷却窗口（窗口内重复点击只提示、不发请求）
const REFRESH_COOLDOWN_MS = 5000;

export interface AutoRefreshPreference {
  autoRefresh: boolean;
  handleToggleAutoRefresh: () => void;
}

/**
 * 自动刷新开关（持久化到 localStorage，默认开启）。
 *
 * ⚠️ 必须在 `useMonitorData` **之前**调用——`autoRefresh` 是它的入参。
 */
export function useAutoRefreshPreference(): AutoRefreshPreference {
  const [autoRefresh, setAutoRefresh] = useState(() => {
    try {
      const stored = localStorage.getItem(AUTO_REFRESH_KEY);
      if (stored === null) return true; // 无值时默认开启
      return stored === 'true'; // 有值则尊重用户选择
    } catch {
      return true; // 异常也默认开启
    }
  });

  // 切换自动刷新并持久化
  const handleToggleAutoRefresh = () => {
    setAutoRefresh(prev => {
      const next = !prev;
      try {
        localStorage.setItem(AUTO_REFRESH_KEY, String(next));
      } catch {
        // ignore
      }
      return next;
    });
  };

  return { autoRefresh, handleToggleAutoRefresh };
}

export interface RefreshCooldown {
  /** 冷却提示是否显示（提示 2 秒后自动消失）。 */
  refreshCooldown: boolean;
  handleRefresh: () => void;
}

/**
 * 手动刷新 + 冷却提示。
 *
 * ⚠️ 必须在 `useMonitorData` **之后**调用——需要它返回的 `refetch`。
 * `handleRefresh` 刻意不包 useCallback：与拆分前逐字一致（消费方 Header /
 * Controls 都没有 memo，稳定引用换不来任何东西）。
 */
export function useRefreshCooldown(refetch: (skipCache?: boolean) => void): RefreshCooldown {
  const lastRefreshRef = useRef<number>(0);
  const [refreshCooldown, setRefreshCooldown] = useState(false);

  const handleRefresh = () => {
    const now = Date.now();
    const elapsed = now - lastRefreshRef.current;

    if (elapsed < REFRESH_COOLDOWN_MS) {
      // 冷却中，显示提示
      setRefreshCooldown(true);
      setTimeout(() => setRefreshCooldown(false), 2000); // 提示显示 2 秒
      return;
    }

    lastRefreshRef.current = now;
    trackEvent('manual_refresh');
    refetch(true); // 绕过浏览器缓存
  };

  return { refreshCooldown, handleRefresh };
}
