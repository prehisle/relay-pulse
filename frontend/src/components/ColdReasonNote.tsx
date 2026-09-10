import { Snowflake } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { BoardValue } from '../types';

/**
 * 冷板原因常驻行：让冷板通道「为什么停探」不必悬浮就能看见。
 *
 * 两条刻意的边界：
 * - 判据是 `board === 'cold'` 而不是「当前在冷板 tab」——`board=all` 视图里冷板行与
 *   热板行混排，那里最需要这条说明。
 * - 原因为空就整块不渲染（生产上大半冷板通道没写原因），不占位、不出「原因未填」这类噪音。
 *
 * 文本截断由外部给宽度，完整内容走原生 title；桌面表格另有通道名 hover tooltip 里那份。
 */
export function ColdReasonNote({
  board,
  reason,
  className = '',
}: {
  board: BoardValue;
  reason?: string;
  className?: string;
}) {
  const { t } = useTranslation();
  const text = reason?.trim();
  if (board !== 'cold' || !text) return null;

  return (
    <span
      className={`inline-flex items-center gap-1 min-w-0 text-[10px] text-muted ${className}`}
      title={`${t('table.channelTooltip.coldReason', '冷板原因')}: ${text}`}
    >
      <Snowflake size={10} className="flex-shrink-0 text-info" aria-hidden="true" />
      <span className="truncate">{text}</span>
    </span>
  );
}
