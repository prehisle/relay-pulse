// 质量列 hover 浮层的 per-model 明细行文案。抽成独立模块有两个理由：组件文件保持
// 「只导出组件」（react-refresh 才能整文件热替换），以及这段文案是已登记的 i18n 债
// （'不可测' / '近3次' / '近7天无评测记录' 目前四语言都出中文），将来补 t() 时改这一个文件。
import { isModelQualityUnusable } from './qualityScale';
import type { RpdiagModelScore } from '../../types/monitor';

interface ModelTooltipRow {
  /** 模型标识（展示名/model_key）。 */
  key: string;
  /** 三组指标拼成一行：30d 均 / 7d 均 / 近 3 次。 */
  detail: string;
  /** 硬失败可用性提示（存在时单独一行高亮）。 */
  warning?: string;
}

export function buildModelTooltipRow(m: RpdiagModelScore): ModelTooltipRow {
  const fmt = (v: number | null | undefined) => (typeof v === 'number' ? v.toFixed(1) : '—');
  const key = m.model_key || m.model || '?';
  const t = m.trend;
  // failed=硬失败清零灰；unavailable=v5.10 stale/aged「不可测」灰。二者的近况都读作
  // 「不可测」（沿用旧 wire 的整行降级逻辑，让代表分那一格显示不可测而非旧分数）。
  // 但 no_recent_attempts（近 7 天无终态评测、非 hard-fail）是展示主态，优先级高于
  // unavailable（spec §② Failed > NoRecentAttempts > Unavailable）：此时**保留真实历史
  // recent_scores**、不把近况槽位抹成「不可测」，好让主注「…以下为历史」名副其实。
  // failed 与 no_recent 在数据层互斥（hard-fail 必有近 7 天尝试），故 failed 行仍照抹灰。
  const unusable = isModelQualityUnusable(m) && m.no_recent_attempts !== true;
  // 近 3 次：与 sparkline 的 slot 2/3/4 同源，让 tooltip 把 5 个槽位读全
  // （30d / 7d / 近 3 次）。优先用 v5.4 的 recent_attempts（逐次 terminal attempt
  // 结局，null=hard-fail→"不可测"）；旧 wire 回退到 recent_scores + 整行 failed。
  // 30d / 7d 仍是真实历史均值。
  let recentStr: string;
  if (Array.isArray(t?.recent_attempts)) {
    const attempts = t.recent_attempts.slice(-3);
    // 空 recent_attempts（近 7 天无探测）但整行 unusable → 读「不可测」而非「—」，
    // 与 sparkline 的纯灰线一致（v5.10 stale/aged unavailable 行走此路）。
    recentStr = attempts.length > 0
      ? attempts.map((v) => (typeof v === 'number' ? fmt(v) : '不可测')).join(', ')
      : (unusable ? '不可测' : '—');
  } else {
    const recent = Array.isArray(t?.recent_scores) ? t.recent_scores.slice(-3) : [];
    if (recent.length > 0) {
      recentStr = recent
        .map((v, i) => (unusable && i === recent.length - 1 ? '不可测' : fmt(v)))
        .join(', ');
    } else {
      // ranking-export.v5.1 wire 没有 recent_scores 时回退到单个 latest。
      recentStr = unusable ? '不可测' : fmt(t?.latest);
    }
  }
  // unavailable（v5.10 stale/aged）但近况仍有真实分时：不覆盖真实历史分（会误导），
  // 而是补一个「当前不可测」后缀，确保"当前不可测"这一状态在 tooltip 里不会消失
  // （sparkline 侧若最右槽位被真实点占用同样不另画灰点，此后缀是唯一的当前态信号）。
  const detail = `30d=${fmt(t?.avg_30d)}  7d=${fmt(t?.avg_7d)}  近3次=${recentStr}`;
  // no_recent_attempts 主态下不叠加「当前不可测」次级后缀：主注「以下为历史」已表明所示
  // 分数非当前，再缀「当前不可测」既冗余又会打乱 no_recent 领先的语序（spec §②）。
  const detailWithState =
    m.unavailable === true && m.no_recent_attempts !== true && !recentStr.includes('不可测')
      ? `${detail}（当前不可测）`
      : detail;
  // no_recent_attempts（rpdiag attempts_7d==0 且非 hard-fail）：注「近7天无评测记录」但
  // KEEP 历史分可见——区别于 failed/unavailable 的「不可测」，只是提示历史已陈旧。
  const detailFinal =
    m.no_recent_attempts === true
      ? `${detailWithState}（近7天无评测记录·以下为历史）`
      : detailWithState;
  return {
    key,
    detail: detailFinal,
    warning: m.availability_warning || undefined,
  };
}
