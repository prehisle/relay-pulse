// @vitest-environment jsdom
//
// useBreakpointMatch 收拢了四处订阅样板（App / ProviderPage / StatusTable 用 tablet，
// Tooltip 用 mobile）。这里钉两件事：① 订阅的确实是**调用方要的那个断点**（写死成某个
// 断点会让 Tooltip 的底部 Sheet 在错误宽度触发，且没有任何类型错误）；② 卸载时退订。
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { BREAKPOINTS } from '../utils/mediaQuery';
import { useBreakpointMatch } from './useBreakpoint';

interface FakeQuery {
  media: string;
  matches: boolean;
  addEventListener: ReturnType<typeof vi.fn>;
  removeEventListener: ReturnType<typeof vi.fn>;
}

let queries: FakeQuery[];
let container: HTMLElement;
let root: Root;

function installMatchMedia(matchedMedia: string[]) {
  queries = [];
  window.matchMedia = ((media: string) => {
    const q: FakeQuery = {
      media,
      matches: matchedMedia.includes(media),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    };
    queries.push(q);
    return q as unknown as MediaQueryList;
  }) as typeof window.matchMedia;
}

function Probe({ breakpoint }: { breakpoint: keyof typeof BREAKPOINTS }) {
  return <span>{String(useBreakpointMatch(breakpoint))}</span>;
}

function render(breakpoint: keyof typeof BREAKPOINTS): boolean {
  act(() => {
    root.render(<Probe breakpoint={breakpoint} />);
  });
  return container.textContent === 'true';
}

beforeEach(() => {
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  container.remove();
});

describe('useBreakpointMatch', () => {
  it('订阅调用方指定的断点，不是写死的某一个', () => {
    installMatchMedia([]);
    render('mobile');
    expect(queries.map((q) => q.media)).toEqual([BREAKPOINTS.mobile]);

    act(() => root.unmount());
    container.remove();
    container = document.createElement('div');
    document.body.appendChild(container);
    root = createRoot(container);

    installMatchMedia([]);
    render('tablet');
    expect(queries.map((q) => q.media)).toEqual([BREAKPOINTS.tablet]);
    act(() => root.unmount());
  });

  it('命中断点时返回 true，未命中返回 false', () => {
    installMatchMedia([BREAKPOINTS.tablet]);
    expect(render('tablet')).toBe(true);
    act(() => root.unmount());

    container.remove();
    container = document.createElement('div');
    document.body.appendChild(container);
    root = createRoot(container);

    // 只有 tablet 命中时，问 mobile 应为 false（两个断点不是同一个查询）
    installMatchMedia([BREAKPOINTS.tablet]);
    expect(render('mobile')).toBe(false);
    act(() => root.unmount());
  });

  it('卸载时退订，避免重复挂载堆积监听器', () => {
    installMatchMedia([]);
    render('tablet');
    expect(queries[0].addEventListener).toHaveBeenCalledTimes(1);
    expect(queries[0].removeEventListener).not.toHaveBeenCalled();

    act(() => root.unmount());
    expect(queries[0].removeEventListener).toHaveBeenCalledTimes(1);
  });
});
