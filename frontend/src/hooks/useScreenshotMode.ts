import { useEffect, useMemo } from 'react';

/** 截图模式下的派生状态（`?screenshot=1`）。 */
export interface ScreenshotMode {
  /** 是否处于截图模式。 */
  isScreenshotMode: boolean;
  /** 截图时间戳（组件挂载时记录，非截图模式为空串）。 */
  screenshotTimestamp: string;
  /** 截图标题（`?title=`，已清洗控制字符并截断，非截图模式为空串）。 */
  screenshotTitle: string;
}

/**
 * 截图模式（群聊分享用）：识别 `?screenshot=1`、强制暗色主题、派生时间戳与标题。
 *
 * @param search 当前 URL 的 query 串（`useLocation().search`）
 */
export function useScreenshotMode(search: string): ScreenshotMode {
  // 检测截图模式
  const isScreenshotMode = useMemo(() => {
    return new URLSearchParams(search).get('screenshot') === '1';
  }, [search]);

  // 截图模式下强制使用 default-dark 主题
  useEffect(() => {
    if (!isScreenshotMode) return;
    const root = document.documentElement;
    root.setAttribute('data-theme', 'default-dark');
    root.style.colorScheme = 'dark';
  }, [isScreenshotMode]);

  // 截图时间戳（组件挂载时记录）
  const screenshotTimestamp = useMemo(() => {
    if (!isScreenshotMode) return '';
    return new Date().toLocaleString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
      timeZone: 'Asia/Shanghai',
    });
  }, [isScreenshotMode]);

  // 截图标题（群名专属标识）
  const screenshotTitle = useMemo(() => {
    if (!isScreenshotMode) return '';
    const raw = new URLSearchParams(search).get('title') || '';
    // 清理控制字符，限制长度
    const cleaned = raw.replace(/[\r\n\t]+/g, ' ').trim();
    const chars = Array.from(cleaned);
    if (chars.length > 60) return chars.slice(0, 60).join('') + '…';
    return cleaned;
  }, [isScreenshotMode, search]);

  return { isScreenshotMode, screenshotTimestamp, screenshotTitle };
}
