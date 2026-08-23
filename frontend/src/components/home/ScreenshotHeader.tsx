interface ScreenshotHeaderProps {
  /** 群专属标题（`?title=`，空则不渲染标题行）。 */
  title: string;
  /** 截图时间戳。 */
  timestamp: string;
  /** 当前展示的服务条数。 */
  count: number;
  timeRange: string;
}

/** 截图模式顶部信息条（截图分享到群里时的落款）。 */
export function ScreenshotHeader({ title, timestamp, count, timeRange }: ScreenshotHeaderProps) {
  return (
    <div className="mb-3 px-3 py-2 bg-elevated border border-default rounded-lg text-xs text-secondary">
      {/* 群专属标题行 - 仅当有 title 时显示 */}
      {title && (
        <div className="text-sm text-primary font-medium mb-1 truncate">
          {title}
        </div>
      )}
      {/* 时间和服务信息行 */}
      <div className="flex items-center justify-between">
        <span className="font-mono">{timestamp}</span>
        <span>
          {count} 个服务 | {timeRange}
        </span>
      </div>
    </div>
  );
}
