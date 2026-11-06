import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { HoverTooltip } from '../HoverTooltip';
import { getDrawing, requestDrawings, subscribeDrawings } from '../../utils/drawingFetcher';
import { pickRecentRuns, svgDataUri, type DrawingRun } from '../../utils/drawingRuns';
import type { RpdiagDrawing, RpdiagScore } from '../../types/monitor';

/**
 * 画作列：rpdiag 最近几次测试里那幅鹈鹕骑自行车的 SVG。格子里放最新一幅，悬停展开
 * 最近三幅，每幅右下角叠一张同组官方的画作对照——官方自己也偶尔画出别的风格，
 * 单幅看不出什么，所以浮层明写「仅供参考」。
 *
 * 画是模型输出，只经 `<img src="data:image/svg+xml,…">` 显示（图片上下文里 SVG 的脚本
 * 不会执行），绝不插进 DOM。格子滚进视口才取数。
 */
const STALE_AFTER_MS = 7 * 24 * 60 * 60 * 1000;

export function DrawingCell({ score }: { score: RpdiagScore | undefined }) {
  const { t, i18n } = useTranslation();
  const runs = pickRecentRuns(score);
  const idsKey = runs.map((r) => r.taskId).join(',');
  const ref = useRef<HTMLSpanElement>(null);
  const [, setVersion] = useState(0);
  // 7 天的门槛不需要逐秒精确，挂载时的时间足够
  const [mountedAt] = useState(() => Date.now());

  useEffect(() => subscribeDrawings(() => setVersion((v) => v + 1)), []);

  // 格子进入视口（含上下 200px 预取）后才登记要取的画
  const visibleRef = useRef(false);
  useEffect(() => {
    const node = ref.current;
    if (!node || visibleRef.current) return;
    if (typeof IntersectionObserver === 'undefined') {
      visibleRef.current = true;
      return;
    }
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          visibleRef.current = true;
          requestDrawings(idsKey.split(','));
          observer.disconnect();
        }
      },
      { rootMargin: '200px 0px' },
    );
    observer.observe(node);
    return () => observer.disconnect();
  }, [idsKey]);

  // 可见后每次重渲染（质量分轮询、别的格子取到画）都再登记一次：已缓存且定局的 id
  // 由 requestDrawings 自己跳过，没取到的与还没定局的到期后才会真的再问。
  useEffect(() => {
    if (visibleRef.current && idsKey) requestDrawings(idsKey.split(','));
  });

  if (runs.length === 0) {
    return <span className="text-muted text-xs">-</span>;
  }

  const latest = runs[0];
  const latestDrawing = getDrawing(latest.taskId);
  // 最新一幅已超过 7 天：多半是近期测试都没出分（失败或停采），格子淡化以免读成现状
  const stale = mountedAt - Date.parse(latest.runAt) > STALE_AFTER_MS;
  const fmtTime = (iso: string) => {
    const d = new Date(iso);
    return Number.isNaN(d.getTime())
      ? iso
      : d.toLocaleString(i18n.language, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });
  };

  const content = (
    <span className="flex flex-col gap-2 p-1">
      <span className="flex gap-2">
        {runs.map((run) => (
          <DrawingCard key={run.taskId} run={run} drawing={getDrawing(run.taskId)} time={fmtTime(run.runAt)} />
        ))}
      </span>
      <span className="text-muted text-[10px] leading-snug">
        {t('table.drawing.tooltipNote')}
        {score?.channel_url && (
          <>
            {' '}
            <a
              href={score.channel_url}
              target="_blank"
              rel="noopener noreferrer"
              className="text-accent hover:underline"
            >
              {t('table.drawing.moreLink')} →
            </a>
          </>
        )}
      </span>
    </span>
  );

  return (
    <span ref={ref} className="inline-flex">
      <HoverTooltip content={content} widthClass="w-auto max-w-[90vw]">
        <span className={stale ? 'opacity-40' : undefined}>
          <DrawingThumb
            drawing={latestDrawing}
            size={36}
            label={t(stale ? 'table.drawing.staleAlt' : 'table.drawing.latestAlt', { time: fmtTime(latest.runAt) })}
          />
        </span>
      </HoverTooltip>
    </span>
  );
}

function DrawingCard({ run, drawing, time }: { run: DrawingRun; drawing: RpdiagDrawing | null | undefined; time: string }) {
  const { t } = useTranslation();
  const officialSvg = drawing?.official?.svg;
  return (
    <span className="flex flex-col items-center gap-1">
      <span className="relative">
        <DrawingThumb drawing={drawing} size={132} label={t('table.drawing.candidateAlt', { time })} />
        {officialSvg && (
          <span className="absolute -right-1 -bottom-1 rounded border border-accent/60 bg-white shadow">
            <img
              src={svgDataUri(officialSvg)}
              alt={t('table.drawing.officialAlt')}
              title={t('table.drawing.officialAlt')}
              width={44}
              height={44}
              className="block h-11 w-11 object-contain"
            />
          </span>
        )}
      </span>
      <span className="text-[10px] text-secondary">{time}</span>
      {run.model && <span className="max-w-[132px] truncate text-[10px] text-muted">{run.model}</span>}
    </span>
  );
}

function DrawingThumb({ drawing, size, label }: { drawing: RpdiagDrawing | null | undefined; size: number; label: string }) {
  const { t } = useTranslation();
  const box = 'flex items-center justify-center rounded border border-default/60 overflow-hidden';
  const style = { width: size, height: size };
  const svg = drawing?.candidate.svg;

  if (svg) {
    return (
      <span className={`${box} bg-white`} style={style}>
        <img src={svgDataUri(svg)} alt={label} title={label} className="h-full w-full object-contain" loading="lazy" />
      </span>
    );
  }
  // undefined：还在取；null 或没有 svg：这次测试没有可显示的画（失败、拒答、未揭晓…）。
  const text = drawing === undefined
    ? ''
    : drawing?.candidate.state === 'failed'
      ? t('table.drawing.failed')
      : t('table.drawing.none');
  return (
    <span
      className={`${box} bg-muted/30 text-[9px] text-muted ${drawing === undefined ? 'animate-pulse' : ''}`}
      style={style}
      title={text || undefined}
    >
      {size > 40 ? text : ''}
    </span>
  );
}
