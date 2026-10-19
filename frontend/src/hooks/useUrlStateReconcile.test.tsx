// @vitest-environment jsdom
// 旧厂商排序无条件清理；板块归一仍等待运行时配置。
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { useUrlStateReconcile, type UrlStateReconcileParams } from './useUrlStateReconcile';

interface Calls {
  clearPriceRatioSort: ReturnType<typeof vi.fn>;
  clearQualityScoreSort: ReturnType<typeof vi.fn>;
  clearModelVendorSort: ReturnType<typeof vi.fn>;
  setBoard: ReturnType<typeof vi.fn>;
}

function baseParams(calls: Calls): UrlStateReconcileParams {
  return {
    hidePriceColumn: false,
    rpdiagEnabled: true,
    boardsEnabledLoaded: true,
    boardsEnabled: true,
    board: 'hot',
    setBoard: calls.setBoard,
    clearPriceRatioSort: calls.clearPriceRatioSort,
    clearQualityScoreSort: calls.clearQualityScoreSort,
    clearModelVendorSort: calls.clearModelVendorSort,
  };
}

function Probe({ params }: { params: UrlStateReconcileParams }) {
  useUrlStateReconcile(params);
  return null;
}

let container: HTMLElement;
let root: Root;

function render(overrides: Partial<UrlStateReconcileParams>, calls: Calls) {
  act(() => {
    root.render(<Probe params={{ ...baseParams(calls), ...overrides }} />);
  });
}

function makeCalls(): Calls {
  return {
    clearPriceRatioSort: vi.fn(),
    clearQualityScoreSort: vi.fn(),
    clearModelVendorSort: vi.fn(),
    setBoard: vi.fn(),
  };
}

beforeEach(() => {
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

describe('useUrlStateReconcile', () => {
  it('正常开关只执行旧厂商排序清理', () => {
    const calls = makeCalls();
    render({}, calls);

    expect(calls.clearPriceRatioSort).not.toHaveBeenCalled();
    expect(calls.clearQualityScoreSort).not.toHaveBeenCalled();
    expect(calls.clearModelVendorSort).toHaveBeenCalledTimes(1);
    expect(calls.setBoard).not.toHaveBeenCalled();
  });

  it('价格列被运行时隐藏后清掉 priceRatio 排序', () => {
    const calls = makeCalls();
    render({ hidePriceColumn: true }, calls);

    expect(calls.clearPriceRatioSort).toHaveBeenCalledTimes(1);
    expect(calls.clearQualityScoreSort).not.toHaveBeenCalled();
    expect(calls.clearModelVendorSort).toHaveBeenCalledTimes(1);
  });

  it('rpdiag 关闭后清掉 qualityScore 排序', () => {
    const calls = makeCalls();
    render({ rpdiagEnabled: false }, calls);

    expect(calls.clearQualityScoreSort).toHaveBeenCalledTimes(1);
    expect(calls.clearPriceRatioSort).not.toHaveBeenCalled();
  });

  it('厂商排序清理不依赖数据或运行时开关', () => {
    const calls = makeCalls();
    render({ boardsEnabledLoaded: false }, calls);
    expect(calls.clearModelVendorSort).toHaveBeenCalledTimes(1);
    expect(calls.setBoard).not.toHaveBeenCalled();
  });

  it('板块功能禁用时把 board 归一到 hot', () => {
    const calls = makeCalls();
    render({ boardsEnabled: false, board: 'cold' }, calls);

    expect(calls.setBoard).toHaveBeenCalledExactlyOnceWith('hot');
  });

  it('⚠️ 加载闸：板块配置未返回前不改写 URL 的 ?board=', () => {
    const calls = makeCalls();
    render({ boardsEnabledLoaded: false, boardsEnabled: false, board: 'cold' }, calls);

    expect(calls.setBoard).not.toHaveBeenCalled();
  });

  it('board 已是 hot 时不重复归一（避免无谓的 URL 写入）', () => {
    const calls = makeCalls();
    render({ boardsEnabled: false, board: 'hot' }, calls);

    expect(calls.setBoard).not.toHaveBeenCalled();
  });
});
