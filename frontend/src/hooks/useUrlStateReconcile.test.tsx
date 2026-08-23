// @vitest-environment jsdom
//
// 钉死「运行时开关变了就归一 URL 状态」的四条 effect，重点是两道容易被后人顺手删掉的闸：
//   ① 厂商排序清理必须等 rawData 到货——首屏 rawData 为空时 showVendorColumn 恒 false，
//      少了这道闸，用户带 ?sort=modelVendor_asc 的分享链接会在数据到达前就被清掉；
//   ② 板块归一必须等 boardsEnabledLoaded——否则初始加载会覆盖 URL 里的 ?board=。
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
    rawDataLength: 3,
    showVendorColumn: true,
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
  it('全开关正常时四条都不动 URL', () => {
    const calls = makeCalls();
    render({}, calls);

    expect(calls.clearPriceRatioSort).not.toHaveBeenCalled();
    expect(calls.clearQualityScoreSort).not.toHaveBeenCalled();
    expect(calls.clearModelVendorSort).not.toHaveBeenCalled();
    expect(calls.setBoard).not.toHaveBeenCalled();
  });

  it('价格列被运行时隐藏后清掉 priceRatio 排序', () => {
    const calls = makeCalls();
    render({ hidePriceColumn: true }, calls);

    expect(calls.clearPriceRatioSort).toHaveBeenCalledTimes(1);
    expect(calls.clearQualityScoreSort).not.toHaveBeenCalled();
    expect(calls.clearModelVendorSort).not.toHaveBeenCalled();
  });

  it('rpdiag 关闭后清掉 qualityScore 排序', () => {
    const calls = makeCalls();
    render({ rpdiagEnabled: false }, calls);

    expect(calls.clearQualityScoreSort).toHaveBeenCalledTimes(1);
    expect(calls.clearPriceRatioSort).not.toHaveBeenCalled();
  });

  it('厂商列不可见且数据已到货时清掉 modelVendor 排序', () => {
    const calls = makeCalls();
    render({ showVendorColumn: false, rawDataLength: 3 }, calls);

    expect(calls.clearModelVendorSort).toHaveBeenCalledTimes(1);
  });

  it('⚠️ 首屏闸：rawData 未到货时绝不清 modelVendor 排序（保住分享链接）', () => {
    const calls = makeCalls();
    render({ showVendorColumn: false, rawDataLength: 0 }, calls);

    expect(calls.clearModelVendorSort).not.toHaveBeenCalled();

    // 数据到货后（仍然没有任何通道声明厂商）才允许清
    render({ showVendorColumn: false, rawDataLength: 5 }, calls);
    expect(calls.clearModelVendorSort).toHaveBeenCalledTimes(1);
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
