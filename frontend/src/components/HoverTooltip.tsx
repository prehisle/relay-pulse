import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';

interface HoverTooltipProps {
  /** 浮层内容（结构化 ReactNode，由调用方组织 label/value 层次）。 */
  content: ReactNode;
  /** 触发器内容（鼠标悬停在其上时展开浮层）。 */
  children: ReactNode;
  /** 触发器额外 class（间距/光标等，如 `gap-1 cursor-help`）。 */
  triggerClassName?: string;
  /** 浮层尺寸 class，默认与通道列 tooltip 同款宽。 */
  widthClass?: string;
}

/**
 * 表格单元格悬浮提示（替代原生 `title` 属性）。
 *
 * 单一真相源：表内所有富 tooltip（通道列、质量列等）共用这套外观与行为，避免
 * 各列分叉成不同视觉风格（早期质量列用原生 `title`、与通道列自定义浮层不一致）。
 *
 * 关键设计：
 * - 浮层经 createPortal 渲染到 document.body 且 position:fixed，**逃出表格
 *   overflow-x:auto 滚动容器 + backdrop-filter 造成的 containing block**，
 *   既不被裁切、也不向表下方探出撑大 scrollHeight 而凭空生成竖直滚动条。
 * - mouseleave 留 100ms 关闭延迟做 hover 桥，让指针能从触发器移到浮层内选择
 *   文本 / 点击内链。
 * - 打开期间监听 scroll/resize 重算坐标，浮层跟随触发器。
 * - 浮层渲染后量一次尺寸：右边放不下就改为贴视口右边，下面放不下且上面放得下就翻到
 *   触发器上方（画作列的三幅大图浮层最容易越界）。直接改 DOM 样式，不走 state。
 */
export function HoverTooltip({
  content,
  children,
  triggerClassName = '',
  widthClass = 'md:min-w-[20rem] max-w-[90vw] md:max-w-2xl',
}: HoverTooltipProps) {
  const triggerRef = useRef<HTMLSpanElement>(null);
  const floatRef = useRef<HTMLSpanElement>(null);
  const leaveTimer = useRef<number>(0);
  const [hover, setHover] = useState(false);
  const [pos, setPos] = useState<{ x: number; y: number; top: number } | null>(null);

  const updatePosition = useCallback(() => {
    if (!triggerRef.current) return;
    const rect = triggerRef.current.getBoundingClientRect();
    setPos({ x: rect.left, y: rect.bottom, top: rect.top });
  }, []);

  useLayoutEffect(() => {
    const el = floatRef.current;
    if (!hover || !pos || !el) return;
    const margin = 8;
    // clientWidth/Height 不含滚动条。越界时改用 right/bottom 锚定，浮层之后再长大也不会出界。
    const { clientWidth, clientHeight } = document.documentElement;
    const { width, height } = el.getBoundingClientRect();
    const overflowsRight = pos.x + width > clientWidth - margin;
    el.style.left = overflowsRight ? 'auto' : `${pos.x}px`;
    el.style.right = overflowsRight ? `${margin}px` : 'auto';
    const flipUp = pos.y + height > clientHeight - margin && pos.top - height >= margin;
    el.style.top = flipUp ? 'auto' : `${pos.y}px`;
    el.style.bottom = flipUp ? `${clientHeight - pos.top}px` : 'auto';
  }, [hover, pos, content]);

  const handleEnter = useCallback(() => {
    clearTimeout(leaveTimer.current);
    updatePosition();
    setHover(true);
  }, [updatePosition]);

  const handleLeave = useCallback(() => {
    clearTimeout(leaveTimer.current);
    leaveTimer.current = window.setTimeout(() => setHover(false), 100);
  }, []);

  // 卸载时清理定时器
  useEffect(() => () => { clearTimeout(leaveTimer.current); }, []);

  // 打开时跟随滚动/resize 更新位置
  useEffect(() => {
    if (!hover) return;
    updatePosition();
    window.addEventListener('resize', updatePosition);
    window.addEventListener('scroll', updatePosition, true);
    return () => {
      window.removeEventListener('resize', updatePosition);
      window.removeEventListener('scroll', updatePosition, true);
    };
  }, [hover, updatePosition]);

  return (
    <span
      ref={triggerRef}
      className={`inline-flex items-center ${triggerClassName}`}
      onMouseEnter={handleEnter}
      onMouseLeave={handleLeave}
    >
      {children}
      {hover && pos && createPortal(
        <span
          ref={floatRef}
          className={`fixed px-2 py-1.5 bg-elevated border border-default text-xs rounded-lg shadow-lg z-50 select-text cursor-text ${widthClass}`}
          style={{ left: pos.x, top: pos.y }}
          onMouseEnter={handleEnter}
          onMouseLeave={handleLeave}
        >
          {content}
        </span>,
        document.body,
      )}
    </span>
  );
}
