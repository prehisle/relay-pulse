import { describe, it, expect, vi, beforeEach } from 'vitest';
import { aggregateHeatmap, getAggregationFactor, resetMediaQueryCache } from './heatmapAggregator';
import type { ProcessedMonitorData } from '../types';

type HistoryPoint = ProcessedMonitorData['history'][number];

// Mock mediaQuery to avoid window dependency in node
vi.mock('./mediaQuery', () => ({
  BREAKPOINTS: { tablet: '(max-width: 960px)', mobile: '(max-width: 768px)' },
  addMediaQueryListener: vi.fn(() => vi.fn()),
}));

beforeEach(() => {
  resetMediaQueryCache();
});

function makePoint(index: number, timestampNum: number, overrides: Partial<HistoryPoint> = {}): HistoryPoint {
  return {
    index,
    status: 'AVAILABLE' as HistoryPoint['status'],
    timestamp: String(timestampNum),
    timestampNum,
    latency: 100,
    availability: 99.5,
    statusCounts: {
      available: 1,
      degraded: 0,
      unavailable: 0,
      missing: 0,
      slow_latency: 0,
      rate_limit: 0,
      server_error: 0,
      client_error: 0,
      auth_error: 0,
      invalid_request: 0,
      network_error: 0,
      content_mismatch: 0,
    },
    ...overrides,
  };
}

describe('aggregateHeatmap', () => {
  it('returns original points for 90m window (span <= 5400s)', () => {
    const now = Math.floor(Date.now() / 1000);
    const points = [
      makePoint(0, now - 3600),
      makePoint(1, now - 1800),
      makePoint(2, now),
    ];
    // 90m window: span = 3600s <= 5400s → no aggregation
    const result = aggregateHeatmap(points);
    expect(result).toBe(points); // same reference
  });

  it('returns original points when desktop (no matchMedia match)', () => {
    // In node env, getIsTablet() returns false (no window), so no aggregation
    const now = Math.floor(Date.now() / 1000);
    const points: HistoryPoint[] = [];
    for (let i = 0; i < 100; i++) {
      points.push(makePoint(i, now - (86400 - i * 864))); // spans ~24h
    }
    const result = aggregateHeatmap(points);
    expect(result).toBe(points); // no aggregation on desktop
  });

  it('returns original points when fewer than maxBlocks', () => {
    const now = Math.floor(Date.now() / 1000);
    const points = [
      makePoint(0, now - 86400),
      makePoint(1, now),
    ];
    const result = aggregateHeatmap(points, 50);
    expect(result).toBe(points);
  });

  it('handles empty array', () => {
    const result = aggregateHeatmap([]);
    expect(result).toEqual([]);
  });

  it('handles single point', () => {
    const point = makePoint(0, 1000);
    const result = aggregateHeatmap([point]);
    expect(result).toEqual([point]);
  });

  it('聚合块的可用率按探测次数加权（与可用率列同口径）', () => {
    // node 环境默认没有 window，手动伪造 matchMedia 让 getIsTablet() 命中平板分支
    const originalWindow = (globalThis as unknown as { window?: unknown }).window;
    (globalThis as unknown as { window: unknown }).window = {
      matchMedia: () => ({ matches: true }),
    };
    resetMediaQueryCache();

    try {
      const now = Math.floor(Date.now() / 1000);
      // 两个跑满的全绿块（各 100 次探测）+ 一个只跑了 1 次的全红块
      const points = [
        makePoint(0, now - 86400, { availability: 100, statusCounts: { ...makePoint(0, 0).statusCounts!, available: 100 } }),
        makePoint(1, now - 43200, { availability: 100, statusCounts: { ...makePoint(0, 0).statusCounts!, available: 100 } }),
        makePoint(2, now, {
          availability: 0,
          status: 'UNAVAILABLE' as HistoryPoint['status'],
          statusCounts: { ...makePoint(0, 0).statusCounts!, available: 0, unavailable: 1 },
        }),
      ];

      const result = aggregateHeatmap(points, 1);

      expect(result).toHaveLength(1);
      // 加权：(100*100 + 100*100 + 0*1) / 201 = 99.5；等权平均会是 66.67
      expect(result[0].availability).toBeCloseTo(99.5, 2);
    } finally {
      if (originalWindow === undefined) {
        delete (globalThis as unknown as { window?: unknown }).window;
      } else {
        (globalThis as unknown as { window: unknown }).window = originalWindow;
      }
      resetMediaQueryCache();
    }
  });
});

describe('getAggregationFactor', () => {
  it('returns 1 on desktop (no matchMedia)', () => {
    // In node environment, getIsTablet() returns false
    expect(getAggregationFactor('24h')).toBe(1);
    expect(getAggregationFactor('7d')).toBe(1);
    expect(getAggregationFactor('30d')).toBe(1);
    expect(getAggregationFactor('90m')).toBe(1);
  });
});
