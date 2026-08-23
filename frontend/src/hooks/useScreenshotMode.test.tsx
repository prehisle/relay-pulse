// @vitest-environment jsdom
//
// 截图模式（`?screenshot=1`，用于把状态面板截图发群）的三条约定：
// 只认 `screenshot=1`、强制暗色主题、标题清洗与 60 字截断。
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { useScreenshotMode, type ScreenshotMode } from './useScreenshotMode';

let container: HTMLElement;
let root: Root;

/** 把 hook 结果渲染成 JSON 读回来——避免在组件里写外层变量（react-hooks/globals）。 */
function Probe({ search }: { search: string }) {
  return <span data-testid="out">{JSON.stringify(useScreenshotMode(search))}</span>;
}

function render(search: string): ScreenshotMode {
  act(() => {
    root.render(<Probe search={search} />);
  });
  return JSON.parse(container.textContent || '{}') as ScreenshotMode;
}

beforeEach(() => {
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
  document.documentElement.removeAttribute('data-theme');
  document.documentElement.style.colorScheme = '';
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  document.documentElement.removeAttribute('data-theme');
  document.documentElement.style.colorScheme = '';
});

describe('useScreenshotMode', () => {
  it('无 screenshot 参数时全部为空且不碰主题', () => {
    const result = render('?period=24h');

    expect(result.isScreenshotMode).toBe(false);
    expect(result.screenshotTimestamp).toBe('');
    expect(result.screenshotTitle).toBe('');
    expect(document.documentElement.getAttribute('data-theme')).toBeNull();
  });

  it('只认 screenshot=1，其它取值不进截图模式', () => {
    expect(render('?screenshot=true').isScreenshotMode).toBe(false);
    expect(render('?screenshot=0').isScreenshotMode).toBe(false);
    expect(render('?screenshot=1').isScreenshotMode).toBe(true);
  });

  it('进入截图模式时强制 default-dark 主题', () => {
    render('?screenshot=1');

    expect(document.documentElement.getAttribute('data-theme')).toBe('default-dark');
    expect(document.documentElement.style.colorScheme).toBe('dark');
  });

  it('时间戳非空且是可读的本地时间', () => {
    const result = render('?screenshot=1');

    expect(result.screenshotTimestamp).not.toBe('');
    // zh-CN + Asia/Shanghai 的 24 小时制形如「2026/08/23 12:00」
    expect(result.screenshotTimestamp).toMatch(/\d{4}\/\d{2}\/\d{2}/);
  });

  it('标题清洗掉换行与制表符并去首尾空白', () => {
    expect(render('?screenshot=1&title=%20%E7%BE%A4%0A%0AA%09B%20').screenshotTitle).toBe('群 A B');
  });

  it('标题超过 60 字符按字素截断并加省略号', () => {
    const long = '甲'.repeat(70);
    const result = render(`?screenshot=1&title=${encodeURIComponent(long)}`);

    expect(Array.from(result.screenshotTitle)).toHaveLength(61); // 60 + 省略号
    expect(result.screenshotTitle.endsWith('…')).toBe(true);

    // 恰好 60 字符不截断
    const exact = '乙'.repeat(60);
    expect(render(`?screenshot=1&title=${encodeURIComponent(exact)}`).screenshotTitle).toBe(exact);
  });

  it('非截图模式下即使带 title 也不产出标题', () => {
    expect(render('?title=%E7%BE%A4A').screenshotTitle).toBe('');
  });
});
