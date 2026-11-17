import { useEffect, useRef, useState, type RefObject } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { Check, Code, Copy, X } from 'lucide-react';
import { getDrawing } from '../../utils/drawingFetcher';
import { svgDataUri, type DrawingRun } from '../../utils/drawingRuns';
import type { RpdiagDrawingSide } from '../../types/monitor';

/**
 * 画作大图弹窗：通道这一幅与同组官方那一幅并排放大，各自可展开 SVG 源码并复制。
 * 顶部可在最近几次测试之间切换。
 *
 * 显示规则与格子一致：画只经 `<img src="data:…">` 显示；源码作为文本放进 `<pre>`，
 * 由 React 转义，不会被当成标记解析。
 */
export function DrawingViewer({
  runs,
  initialIndex,
  formatTime,
  onClose,
  fallbackFocus,
}: {
  runs: DrawingRun[];
  initialIndex: number;
  formatTime: (iso: string) => string;
  onClose: () => void;
  /** 打开弹窗的按钮已不在页面上时（从悬停浮层打开、浮层随后关掉），关闭后焦点交给它。 */
  fallbackFocus?: RefObject<HTMLElement | null>;
}) {
  const { t } = useTranslation();
  // 按 taskId 记当前看的是哪幅：轮询带来新测试时 runs 会整体后移，按下标记会悄悄换图
  const [taskId, setTaskId] = useState(() => runs[initialIndex]?.taskId ?? runs[0]?.taskId);
  const found = runs.findIndex((r) => r.taskId === taskId);
  const index = found >= 0 ? found : 0;
  const run = runs[index];
  const drawing = run ? getDrawing(run.taskId) : undefined;
  const dialogRef = useRef<HTMLDivElement>(null);
  const closeRef = useRef<HTMLButtonElement>(null);

  // 打开时焦点移进弹窗、关闭时还给原来的触发按钮；Tab 只在弹窗内循环，免得键盘用户
  // 跑到背景里再打开第二个弹窗。
  useEffect(() => {
    const previouslyFocused = document.activeElement as HTMLElement | null;
    const fallbackEl = fallbackFocus?.current; // 格子缩略图按钮，弹窗打开期间一直在
    closeRef.current?.focus();
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = prevOverflow;
      const target = previouslyFocused?.isConnected ? previouslyFocused : fallbackEl;
      target?.focus?.();
    };
  }, [fallbackFocus]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
        return;
      }
      if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
        e.preventDefault();
        const list = runs;
        setTaskId((cur) => {
          const i = Math.max(0, list.findIndex((r) => r.taskId === cur));
          const next = e.key === 'ArrowLeft' ? Math.max(0, i - 1) : Math.min(list.length - 1, i + 1);
          return list[next]?.taskId ?? cur;
        });
        return;
      }
      if (e.key === 'Tab' && dialogRef.current) {
        const focusables = dialogRef.current.querySelectorAll<HTMLElement>('button:not([disabled]), [href], [tabindex]:not([tabindex="-1"])');
        if (focusables.length === 0) return;
        const first = focusables[0];
        const last = focusables[focusables.length - 1];
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault();
          first.focus();
        } else if (!dialogRef.current.contains(document.activeElement)) {
          e.preventDefault();
          first.focus();
        }
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose, runs]);

  if (!run) return null;

  return createPortal(
    <div
      className="fixed inset-0 z-[9999] flex items-center justify-center bg-black/70 p-4 backdrop-blur-sm"
      role="dialog"
      aria-modal="true"
      aria-label={t('table.drawing.viewerTitle')}
      // 弹窗挂在表格行的 React 树下，点击别冒泡到行上的处理器
      onClick={(e) => {
        e.stopPropagation();
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        ref={dialogRef}
        className="relative flex max-h-full w-full max-w-5xl flex-col overflow-hidden rounded-xl border border-default bg-elevated shadow-2xl"
      >
        <div className="flex flex-wrap items-center gap-2 border-b border-default px-4 py-3 pr-12">
          <h2 className="mr-2 text-sm font-semibold text-primary">{t('table.drawing.viewerTitle')}</h2>
          {runs.map((r, i) => (
            <button
              key={r.taskId}
              type="button"
              onClick={() => setTaskId(r.taskId)}
              className={`rounded-md border px-2 py-1 text-xs transition-colors ${
                i === index
                  ? 'border-accent bg-accent/15 text-primary'
                  : 'border-default text-secondary hover:text-primary'
              }`}
            >
              {formatTime(r.runAt)}
              {r.model && <span className="ml-1 text-muted">{r.model}</span>}
            </button>
          ))}
          <button
            ref={closeRef}
            type="button"
            onClick={onClose}
            className="absolute right-4 top-3 text-secondary transition-colors hover:text-primary"
            aria-label={t('common.close')}
          >
            <X size={20} />
          </button>
        </div>

        <div className="grid flex-1 grid-cols-1 gap-4 overflow-y-auto p-4 md:grid-cols-2">
          <DrawingPanel
            key={`c-${run.taskId}`}
            title={t('table.drawing.candidateTitle')}
            side={drawing === undefined ? undefined : drawing?.candidate ?? null}
            alt={t('table.drawing.candidateAlt', { time: formatTime(run.runAt) })}
          />
          <DrawingPanel
            key={`o-${run.taskId}`}
            title={t('table.drawing.officialAlt')}
            side={drawing === undefined ? undefined : drawing?.official ?? null}
            alt={t('table.drawing.officialAlt')}
          />
        </div>

        <p className="border-t border-default px-4 py-2 text-[11px] text-muted">{t('table.drawing.viewerNote')}</p>
      </div>
    </div>,
    document.body,
  );
}

function DrawingPanel({ title, side, alt }: { title: string; side: RpdiagDrawingSide | null | undefined; alt: string }) {
  const { t } = useTranslation();
  const [showSource, setShowSource] = useState(false);
  const [copied, setCopied] = useState(false);
  const svg = side?.svg;

  const copy = async () => {
    if (!svg) return;
    try {
      await navigator.clipboard.writeText(svg);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      // 剪贴板不可用（非安全上下文等）：展开源码让用户手动选
      setShowSource(true);
    }
  };

  const placeholder = side === undefined
    ? ''
    : side?.state === 'failed'
      ? t('table.drawing.failed')
      : t('table.drawing.none');

  return (
    <div className="flex min-w-0 flex-col gap-2">
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs font-medium text-secondary">{title}</span>
        {svg && (
          <span className="flex gap-1">
            <button
              type="button"
              onClick={() => setShowSource((v) => !v)}
              className="inline-flex items-center gap-1 rounded border border-default px-2 py-0.5 text-[11px] text-secondary hover:text-primary"
              aria-pressed={showSource}
            >
              <Code size={12} />
              {t(showSource ? 'table.drawing.hideSource' : 'table.drawing.showSource')}
            </button>
            <button
              type="button"
              onClick={copy}
              className="inline-flex items-center gap-1 rounded border border-default px-2 py-0.5 text-[11px] text-secondary hover:text-primary"
            >
              {copied ? <Check size={12} /> : <Copy size={12} />}
              {t(copied ? 'table.drawing.copied' : 'table.drawing.copySource')}
            </button>
          </span>
        )}
      </div>
      {svg ? (
        <div className="flex aspect-square w-full items-center justify-center overflow-hidden rounded-lg border border-default/60 bg-white">
          <img src={svgDataUri(svg)} alt={alt} className="h-full w-full object-contain" />
        </div>
      ) : (
        <div
          className={`flex aspect-square w-full items-center justify-center rounded-lg border border-default/60 bg-muted/30 text-xs text-muted ${
            side === undefined ? 'animate-pulse' : ''
          }`}
        >
          {placeholder}
        </div>
      )}
      {svg && showSource && (
        <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-all rounded-lg border border-default bg-muted/40 p-2 font-mono text-[11px] leading-snug text-primary select-text">
          {svg}
        </pre>
      )}
    </div>
  );
}
