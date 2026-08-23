import { useEffect, useState } from 'react';
import { createMediaQueryEffect, type BREAKPOINTS } from '../utils/mediaQuery';

/**
 * 订阅某个响应式断点，返回当前是否命中。
 *
 * 断点定义与 Safari ≤13 兼容回退都在 `utils/mediaQuery.ts`；这里只是把
 * 「useState + useEffect 订阅 + 返回 cleanup」这段四处重复的样板收成一处。
 *
 * ⚠️ 每个调用点各自订阅一次（与收拢前逐字一致）。若日后要收成全局单例，
 * 改这一个文件即可——但注意 `viewMode` 在 tablet 断点下被上层强制成 table、
 * 而表格组件内部又把 table 渲染成卡片，那是另一件事，别混在一起改。
 */
export function useBreakpointMatch(breakpoint: keyof typeof BREAKPOINTS): boolean {
  const [matches, setMatches] = useState(false);

  useEffect(() => {
    const cleanup = createMediaQueryEffect(breakpoint, setMatches);
    return cleanup;
  }, [breakpoint]);

  return matches;
}
