import { describe, expect, it } from 'vitest';
import { layeredHeatmapHeightPx } from './layeredHeatmapHeight';

describe('layeredHeatmapHeightPx', () => {
  it('4 层及以下保持表格行的 20px 基准高度', () => {
    for (const n of [1, 2, 3, 4]) {
      expect(layeredHeatmapHeightPx(n, 20)).toBe(20);
    }
  });

  it('超过 4 层时按每层 3.5px + 2px 间隙撑开', () => {
    expect(layeredHeatmapHeightPx(5, 20)).toBe(25.5);
    expect(layeredHeatmapHeightPx(6, 20)).toBe(31);
    expect(layeredHeatmapHeightPx(7, 20)).toBe(36.5);
  });

  it('基准更高时（卡片视图 40px）层数不多就不变', () => {
    expect(layeredHeatmapHeightPx(7, 40)).toBe(40);
    expect(layeredHeatmapHeightPx(11, 40)).toBe(58.5);
  });

  it('空层数回退到基准高度', () => {
    expect(layeredHeatmapHeightPx(0, 20)).toBe(20);
  });
});
