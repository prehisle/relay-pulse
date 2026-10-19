import { describe, expect, it } from 'vitest';
import { sortMonitors, sortMonitorsWithPinning } from './sortMonitors';
import type { ProcessedMonitorData, SortConfig, SponsorPinConfig } from '../types';

// 创建测试用的 mock 数据
function createMockData(overrides: Partial<ProcessedMonitorData>): ProcessedMonitorData {
  return {
    id: 'test-id',
    providerId: 'test',
    providerSlug: 'test',
    providerName: 'Test',
    serviceType: 'cc',
    serviceName: 'cc',
    category: 'commercial',
    sponsor: 'Test Sponsor',
    board: 'hot',
    intervalMs: 60000,
    history: [],
    currentStatus: 'AVAILABLE',
    uptime: 99.5,
    lastCheckLatency: 100,
    isMultiModel: false,
    ...overrides,
  };
}

describe('sortMonitors', () => {
  describe('主排序', () => {
    it('按服务商名称升序排序', () => {
      const data = [
        createMockData({ id: '1', providerName: 'Charlie', lastCheckLatency: 100 }),
        createMockData({ id: '2', providerName: 'Alpha', lastCheckLatency: 200 }),
        createMockData({ id: '3', providerName: 'Bravo', lastCheckLatency: 150 }),
      ];
      const config: SortConfig = { key: 'providerName', direction: 'asc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.providerName)).toEqual(['Alpha', 'Bravo', 'Charlie']);
    });

    it('按服务商名称降序排序', () => {
      const data = [
        createMockData({ id: '1', providerName: 'Alpha' }),
        createMockData({ id: '2', providerName: 'Charlie' }),
        createMockData({ id: '3', providerName: 'Bravo' }),
      ];
      const config: SortConfig = { key: 'providerName', direction: 'desc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.providerName)).toEqual(['Charlie', 'Bravo', 'Alpha']);
    });

    it('按可用率降序排序', () => {
      const data = [
        createMockData({ id: '1', uptime: 80, lastCheckLatency: 100 }),
        createMockData({ id: '2', uptime: 99.9, lastCheckLatency: 200 }),
        createMockData({ id: '3', uptime: 95, lastCheckLatency: 150 }),
      ];
      const config: SortConfig = { key: 'uptime', direction: 'desc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.uptime)).toEqual([99.9, 95, 80]);
    });

    it('按可用率升序排序', () => {
      const data = [
        createMockData({ id: '1', uptime: 99.9 }),
        createMockData({ id: '2', uptime: 80 }),
        createMockData({ id: '3', uptime: 95 }),
      ];
      const config: SortConfig = { key: 'uptime', direction: 'asc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.uptime)).toEqual([80, 95, 99.9]);
    });

    it('按状态权重排序（AVAILABLE > DEGRADED > UNAVAILABLE）', () => {
      const data = [
        createMockData({ id: '1', currentStatus: 'DEGRADED', lastCheckLatency: 100 }),
        createMockData({ id: '2', currentStatus: 'AVAILABLE', lastCheckLatency: 200 }),
        createMockData({ id: '3', currentStatus: 'UNAVAILABLE', lastCheckLatency: 150 }),
      ];
      const config: SortConfig = { key: 'currentStatus', direction: 'desc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.currentStatus)).toEqual([
        'AVAILABLE',
        'DEGRADED',
        'UNAVAILABLE',
      ]);
    });
  });

  describe('uptime 特殊处理', () => {
    it('无数据（uptime < 0）始终排最后（降序）', () => {
      const data = [
        createMockData({ id: '1', uptime: -1, lastCheckLatency: 50 }),
        createMockData({ id: '2', uptime: 99, lastCheckLatency: 100 }),
        createMockData({ id: '3', uptime: 80, lastCheckLatency: 150 }),
      ];
      const config: SortConfig = { key: 'uptime', direction: 'desc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.uptime)).toEqual([99, 80, -1]);
    });

    it('无数据（uptime < 0）始终排最后（升序）', () => {
      const data = [
        createMockData({ id: '1', uptime: -1, lastCheckLatency: 50 }),
        createMockData({ id: '2', uptime: 99, lastCheckLatency: 100 }),
        createMockData({ id: '3', uptime: 80, lastCheckLatency: 150 }),
      ];
      const config: SortConfig = { key: 'uptime', direction: 'asc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.uptime)).toEqual([80, 99, -1]);
    });

    it('多个无数据记录保持相对顺序', () => {
      const data = [
        createMockData({ id: '1', uptime: -1, lastCheckLatency: 200 }),
        createMockData({ id: '2', uptime: 95, lastCheckLatency: 100 }),
        createMockData({ id: '3', uptime: -1, lastCheckLatency: 100 }),
      ];
      const config: SortConfig = { key: 'uptime', direction: 'desc' };

      const result = sortMonitors(data, config);

      // 95 排第一，两个 -1 按延迟二级排序
      expect(result.map((d) => d.id)).toEqual(['2', '3', '1']);
    });
  });

  describe('二级排序（延迟）', () => {
    it('可用率相等时，按延迟升序排序', () => {
      const data = [
        createMockData({ id: '1', uptime: 99, lastCheckLatency: 300 }),
        createMockData({ id: '2', uptime: 99, lastCheckLatency: 100 }),
        createMockData({ id: '3', uptime: 99, lastCheckLatency: 200 }),
      ];
      const config: SortConfig = { key: 'uptime', direction: 'desc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.lastCheckLatency)).toEqual([100, 200, 300]);
    });

    it('状态相等时，按延迟升序排序', () => {
      const data = [
        createMockData({ id: '1', currentStatus: 'AVAILABLE', lastCheckLatency: 500 }),
        createMockData({ id: '2', currentStatus: 'AVAILABLE', lastCheckLatency: 100 }),
        createMockData({ id: '3', currentStatus: 'AVAILABLE', lastCheckLatency: 250 }),
      ];
      const config: SortConfig = { key: 'currentStatus', direction: 'desc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.lastCheckLatency)).toEqual([100, 250, 500]);
    });

    it('延迟为 undefined 时排最后', () => {
      const data = [
        createMockData({ id: '1', uptime: 99, lastCheckLatency: undefined }),
        createMockData({ id: '2', uptime: 99, lastCheckLatency: 100 }),
        createMockData({ id: '3', uptime: 99, lastCheckLatency: 200 }),
      ];
      const config: SortConfig = { key: 'uptime', direction: 'desc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.id)).toEqual(['2', '3', '1']);
    });

    it('多个延迟为 undefined 时保持原顺序', () => {
      const data = [
        createMockData({ id: '1', uptime: 99, lastCheckLatency: undefined }),
        createMockData({ id: '2', uptime: 99, lastCheckLatency: undefined }),
        createMockData({ id: '3', uptime: 99, lastCheckLatency: 100 }),
      ];
      const config: SortConfig = { key: 'uptime', direction: 'desc' };

      const result = sortMonitors(data, config);

      // id=3 有延迟排第一，id=1 和 id=2 都无延迟，保持原顺序
      expect(result.map((d) => d.id)).toEqual(['3', '1', '2']);
    });
  });

  describe('不可变性', () => {
    it('不修改原数组', () => {
      const data = [
        createMockData({ id: '1', providerName: 'Charlie' }),
        createMockData({ id: '2', providerName: 'Alpha' }),
      ];
      const originalOrder = data.map((d) => d.id);
      const config: SortConfig = { key: 'providerName', direction: 'asc' };

      sortMonitors(data, config);

      expect(data.map((d) => d.id)).toEqual(originalOrder);
    });

    it('返回新数组', () => {
      const data = [createMockData({ id: '1' })];
      const config: SortConfig = { key: 'providerName', direction: 'asc' };

      const result = sortMonitors(data, config);

      expect(result).not.toBe(data);
    });
  });

  describe('边界情况', () => {
    it('空数组返回空数组', () => {
      const config: SortConfig = { key: 'uptime', direction: 'desc' };

      const result = sortMonitors([], config);

      expect(result).toEqual([]);
    });

    it('单元素数组直接返回', () => {
      const data = [createMockData({ id: '1' })];
      const config: SortConfig = { key: 'uptime', direction: 'desc' };

      const result = sortMonitors(data, config);

      expect(result).toHaveLength(1);
      expect(result[0].id).toBe('1');
    });

    it('空 key 时返回原数组副本', () => {
      const data = [
        createMockData({ id: '1' }),
        createMockData({ id: '2' }),
      ];
      const config: SortConfig = { key: '', direction: 'desc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.id)).toEqual(['1', '2']);
      expect(result).not.toBe(data);
    });
  });

  describe('priceRatio 排序 (使用 priceMin/priceMax)', () => {
    it('按倍率升序排序，null 值排最后', () => {
      const data = [
        createMockData({ id: '1', priceMin: 0.8, priceMax: 0.8, lastCheckLatency: 100 }),
        createMockData({ id: '2', priceMin: null, priceMax: null, lastCheckLatency: 100 }),
        createMockData({ id: '3', priceMin: 1.2, priceMax: 1.2, lastCheckLatency: 100 }),
      ];
      const config: SortConfig = { key: 'priceRatio', direction: 'asc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.id)).toEqual(['1', '3', '2']); // 0.8 < 1.2 < null
    });

    it('按倍率降序排序，null 值排最后', () => {
      const data = [
        createMockData({ id: '1', priceMin: 0.8, priceMax: 0.8, lastCheckLatency: 100 }),
        createMockData({ id: '2', priceMin: null, priceMax: null, lastCheckLatency: 100 }),
        createMockData({ id: '3', priceMin: 1.2, priceMax: 1.2, lastCheckLatency: 100 }),
      ];
      const config: SortConfig = { key: 'priceRatio', direction: 'desc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.id)).toEqual(['3', '1', '2']); // 1.2 > 0.8 > null
    });

    it('多个 null 值按延迟二级排序', () => {
      const data = [
        createMockData({ id: '1', priceMin: null, priceMax: null, lastCheckLatency: 200 }),
        createMockData({ id: '2', priceMin: 0.9, priceMax: 0.9, lastCheckLatency: 100 }),
        createMockData({ id: '3', priceMin: null, priceMax: null, lastCheckLatency: 100 }),
      ];
      const config: SortConfig = { key: 'priceRatio', direction: 'asc' };

      const result = sortMonitors(data, config);

      // 0.9 排第一，两个 null 按延迟排序
      expect(result.map((d) => d.id)).toEqual(['2', '3', '1']);
    });

    it('全部为 null 时按延迟排序', () => {
      const data = [
        createMockData({ id: '1', priceMin: null, priceMax: null, lastCheckLatency: 300 }),
        createMockData({ id: '2', priceMin: null, priceMax: null, lastCheckLatency: 100 }),
        createMockData({ id: '3', priceMin: null, priceMax: null, lastCheckLatency: 200 }),
      ];
      const config: SortConfig = { key: 'priceRatio', direction: 'desc' };

      const result = sortMonitors(data, config);

      // 全部 null，按延迟升序
      expect(result.map((d) => d.id)).toEqual(['2', '3', '1']);
    });

    it('按上限排序（保护用户，关注最坏情况）', () => {
      const data = [
        createMockData({ id: '1', priceMin: 0.01, priceMax: 0.5, lastCheckLatency: 100 }), // 上限 0.5
        createMockData({ id: '2', priceMin: 0.3, priceMax: 0.4, lastCheckLatency: 100 }),  // 上限 0.4
        createMockData({ id: '3', priceMin: 0.1, priceMax: 0.6, lastCheckLatency: 100 }),  // 上限 0.6
      ];
      const config: SortConfig = { key: 'priceRatio', direction: 'asc' };

      const result = sortMonitors(data, config);

      // 按上限升序：0.4 < 0.5 < 0.6（而非按中心值或下限）
      expect(result.map((d) => d.id)).toEqual(['2', '1', '3']);
    });
  });

  describe('qualityScore 排序', () => {
    it('按质量分降序，null 沉底', () => {
      const data = [
        createMockData({ id: '1', qualityScore: 80, lastCheckLatency: 100 }),
        createMockData({ id: '2', qualityScore: null, lastCheckLatency: 100 }),
        createMockData({ id: '3', qualityScore: 95, lastCheckLatency: 100 }),
      ];
      const config: SortConfig = { key: 'qualityScore', direction: 'desc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.id)).toEqual(['3', '1', '2']);
    });

    it('按质量分升序，null 仍沉底（不随 direction 翻转）', () => {
      const data = [
        createMockData({ id: '1', qualityScore: 80, lastCheckLatency: 100 }),
        createMockData({ id: '2', qualityScore: null, lastCheckLatency: 100 }),
        createMockData({ id: '3', qualityScore: 95, lastCheckLatency: 100 }),
      ];
      const config: SortConfig = { key: 'qualityScore', direction: 'asc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.id)).toEqual(['1', '3', '2']);
    });

    it('故障态 0 是有效分（非 null），降序时排在 null 之前', () => {
      const data = [
        createMockData({ id: '1', qualityScore: 0, lastCheckLatency: 100 }),
        createMockData({ id: '2', qualityScore: null, lastCheckLatency: 100 }),
        createMockData({ id: '3', qualityScore: 10, lastCheckLatency: 100 }),
      ];
      const config: SortConfig = { key: 'qualityScore', direction: 'desc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.id)).toEqual(['3', '1', '2']);
    });

    it('故障态 0 升序排最前，null 仍沉底', () => {
      const data = [
        createMockData({ id: '1', qualityScore: 0, lastCheckLatency: 100 }),
        createMockData({ id: '2', qualityScore: null, lastCheckLatency: 100 }),
        createMockData({ id: '3', qualityScore: 10, lastCheckLatency: 100 }),
      ];
      const config: SortConfig = { key: 'qualityScore', direction: 'asc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.id)).toEqual(['1', '3', '2']);
    });

    it('多个 null 时按延迟二级排序', () => {
      const data = [
        createMockData({ id: '1', qualityScore: null, lastCheckLatency: 200 }),
        createMockData({ id: '2', qualityScore: 90, lastCheckLatency: 100 }),
        createMockData({ id: '3', qualityScore: null, lastCheckLatency: 100 }),
      ];
      const config: SortConfig = { key: 'qualityScore', direction: 'desc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.id)).toEqual(['2', '3', '1']);
    });

    it('全部为 null（rpdiag 未启用 / 全 cx 通道）时退化为按延迟排序', () => {
      const data = [
        createMockData({ id: '1', qualityScore: null, lastCheckLatency: 300 }),
        createMockData({ id: '2', qualityScore: null, lastCheckLatency: 100 }),
        createMockData({ id: '3', qualityScore: null, lastCheckLatency: 200 }),
      ];
      const config: SortConfig = { key: 'qualityScore', direction: 'desc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.id)).toEqual(['2', '3', '1']);
    });

    it('undefined（未注入字段）等同 null', () => {
      const data = [
        createMockData({ id: '1', lastCheckLatency: 100 }), // qualityScore 字段缺失
        createMockData({ id: '2', qualityScore: 70, lastCheckLatency: 200 }),
      ];
      const config: SortConfig = { key: 'qualityScore', direction: 'desc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.id)).toEqual(['2', '1']);
    });
  });

  describe('延迟主排序', () => {
    it('按延迟升序排序（有效延迟）', () => {
      const data = [
        createMockData({ id: '1', currentStatus: 'AVAILABLE', lastCheckLatency: 300 }),
        createMockData({ id: '2', currentStatus: 'AVAILABLE', lastCheckLatency: 100 }),
        createMockData({ id: '3', currentStatus: 'DEGRADED', lastCheckLatency: 200 }),
      ];
      const config: SortConfig = { key: 'latency', direction: 'asc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.lastCheckLatency)).toEqual([100, 200, 300]);
    });

    it('按延迟降序排序', () => {
      const data = [
        createMockData({ id: '1', currentStatus: 'AVAILABLE', lastCheckLatency: 100 }),
        createMockData({ id: '2', currentStatus: 'DEGRADED', lastCheckLatency: 300 }),
        createMockData({ id: '3', currentStatus: 'AVAILABLE', lastCheckLatency: 200 }),
      ];
      const config: SortConfig = { key: 'latency', direction: 'desc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.lastCheckLatency)).toEqual([300, 200, 100]);
    });

    it('不可用状态的延迟排最后（无论延迟值大小）', () => {
      const data = [
        createMockData({ id: '1', currentStatus: 'UNAVAILABLE', lastCheckLatency: 50 }), // 虽然延迟最低，但不可用
        createMockData({ id: '2', currentStatus: 'AVAILABLE', lastCheckLatency: 200 }),
        createMockData({ id: '3', currentStatus: 'DEGRADED', lastCheckLatency: 300 }),
      ];
      const config: SortConfig = { key: 'latency', direction: 'asc' };

      const result = sortMonitors(data, config);

      // UNAVAILABLE 排最后，即使其延迟值最小
      expect(result.map((d) => d.id)).toEqual(['2', '3', '1']);
    });

    it('undefined 延迟排最后', () => {
      const data = [
        createMockData({ id: '1', currentStatus: 'AVAILABLE', lastCheckLatency: undefined }),
        createMockData({ id: '2', currentStatus: 'AVAILABLE', lastCheckLatency: 100 }),
        createMockData({ id: '3', currentStatus: 'DEGRADED', lastCheckLatency: 200 }),
      ];
      const config: SortConfig = { key: 'latency', direction: 'asc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.id)).toEqual(['2', '3', '1']);
    });

    it('UNAVAILABLE 始终排最后，即使可用状态无延迟', () => {
      const data = [
        createMockData({ id: '1', currentStatus: 'UNAVAILABLE', lastCheckLatency: 50 }),  // UNAVAILABLE 排最后
        createMockData({ id: '2', currentStatus: 'AVAILABLE', lastCheckLatency: 200 }),   // 有效延迟
        createMockData({ id: '3', currentStatus: 'AVAILABLE', lastCheckLatency: undefined }), // 可用但无延迟
        createMockData({ id: '4', currentStatus: 'UNAVAILABLE', lastCheckLatency: 100 }), // UNAVAILABLE 排最后
      ];
      const config: SortConfig = { key: 'latency', direction: 'asc' };

      const result = sortMonitors(data, config);

      // 优先级：有延迟的可用状态 > 无延迟的可用状态 > UNAVAILABLE
      // id=2 (200ms) → id=3 (undefined) → id=1 和 id=4 (UNAVAILABLE 保持原顺序)
      expect(result.map((d) => d.id)).toEqual(['2', '3', '1', '4']);
    });

    it('绿色和黄色状态同等对待', () => {
      const data = [
        createMockData({ id: '1', currentStatus: 'DEGRADED', lastCheckLatency: 100 }),
        createMockData({ id: '2', currentStatus: 'AVAILABLE', lastCheckLatency: 200 }),
        createMockData({ id: '3', currentStatus: 'DEGRADED', lastCheckLatency: 150 }),
      ];
      const config: SortConfig = { key: 'latency', direction: 'asc' };

      const result = sortMonitors(data, config);

      // 不区分状态，纯按延迟排序
      expect(result.map((d) => d.lastCheckLatency)).toEqual([100, 150, 200]);
    });

    it('降序排序时不可用状态仍排最后', () => {
      const data = [
        createMockData({ id: '1', currentStatus: 'UNAVAILABLE', lastCheckLatency: 1000 }), // 虽然延迟最高，但不可用
        createMockData({ id: '2', currentStatus: 'AVAILABLE', lastCheckLatency: 200 }),
        createMockData({ id: '3', currentStatus: 'DEGRADED', lastCheckLatency: 300 }),
      ];
      const config: SortConfig = { key: 'latency', direction: 'desc' };

      const result = sortMonitors(data, config);

      // 降序排序有效延迟，UNAVAILABLE 仍排最后
      expect(result.map((d) => d.id)).toEqual(['3', '2', '1']);
    });

    it('多个 UNAVAILABLE 保持原顺序（延迟不参与排序）', () => {
      const data = [
        createMockData({ id: '1', currentStatus: 'UNAVAILABLE', lastCheckLatency: 500 }),
        createMockData({ id: '2', currentStatus: 'UNAVAILABLE', lastCheckLatency: 100 }), // 延迟更低但不应排到前面
        createMockData({ id: '3', currentStatus: 'AVAILABLE', lastCheckLatency: 200 }),
      ];
      const config: SortConfig = { key: 'latency', direction: 'asc' };

      const result = sortMonitors(data, config);

      // id=3 排第一，id=1 和 id=2 保持原顺序（不按延迟排序）
      expect(result.map((d) => d.id)).toEqual(['3', '1', '2']);
    });
  });

  describe('lastCheck 组合排序（状态优先 + 延迟次级）', () => {
    it('降序：按状态权重排序 AVAILABLE > DEGRADED > UNAVAILABLE', () => {
      const data = [
        createMockData({ id: '1', currentStatus: 'UNAVAILABLE', lastCheckLatency: 50 }),
        createMockData({ id: '2', currentStatus: 'AVAILABLE', lastCheckLatency: 200 }),
        createMockData({ id: '3', currentStatus: 'DEGRADED', lastCheckLatency: 100 }),
      ];
      const config: SortConfig = { key: 'lastCheck', direction: 'desc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.currentStatus)).toEqual([
        'AVAILABLE',
        'DEGRADED',
        'UNAVAILABLE',
      ]);
    });

    it('升序：按状态权重排序 UNAVAILABLE > DEGRADED > AVAILABLE', () => {
      const data = [
        createMockData({ id: '1', currentStatus: 'AVAILABLE', lastCheckLatency: 200 }),
        createMockData({ id: '2', currentStatus: 'UNAVAILABLE', lastCheckLatency: 50 }),
        createMockData({ id: '3', currentStatus: 'DEGRADED', lastCheckLatency: 100 }),
      ];
      const config: SortConfig = { key: 'lastCheck', direction: 'asc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.currentStatus)).toEqual([
        'UNAVAILABLE',
        'DEGRADED',
        'AVAILABLE',
      ]);
    });

    it('状态相同时按延迟升序排序', () => {
      const data = [
        createMockData({ id: '1', currentStatus: 'AVAILABLE', lastCheckLatency: 500 }),
        createMockData({ id: '2', currentStatus: 'AVAILABLE', lastCheckLatency: 100 }),
        createMockData({ id: '3', currentStatus: 'AVAILABLE', lastCheckLatency: 250 }),
      ];
      const config: SortConfig = { key: 'lastCheck', direction: 'desc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.lastCheckLatency)).toEqual([100, 250, 500]);
    });

    it('MISSING 始终排最后（降序）', () => {
      const data = [
        createMockData({ id: '1', currentStatus: 'MISSING', lastCheckLatency: undefined }),
        createMockData({ id: '2', currentStatus: 'UNAVAILABLE', lastCheckLatency: 100 }),
        createMockData({ id: '3', currentStatus: 'AVAILABLE', lastCheckLatency: 200 }),
      ];
      const config: SortConfig = { key: 'lastCheck', direction: 'desc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.currentStatus)).toEqual([
        'AVAILABLE',
        'UNAVAILABLE',
        'MISSING',
      ]);
    });

    it('MISSING 始终排最后（升序）', () => {
      const data = [
        createMockData({ id: '1', currentStatus: 'MISSING', lastCheckLatency: undefined }),
        createMockData({ id: '2', currentStatus: 'AVAILABLE', lastCheckLatency: 200 }),
        createMockData({ id: '3', currentStatus: 'UNAVAILABLE', lastCheckLatency: 100 }),
      ];
      const config: SortConfig = { key: 'lastCheck', direction: 'asc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.currentStatus)).toEqual([
        'UNAVAILABLE',
        'AVAILABLE',
        'MISSING',
      ]);
    });

    it('undefined 延迟在同状态内排最后', () => {
      const data = [
        createMockData({ id: '1', currentStatus: 'AVAILABLE', lastCheckLatency: undefined }),
        createMockData({ id: '2', currentStatus: 'AVAILABLE', lastCheckLatency: 100 }),
        createMockData({ id: '3', currentStatus: 'AVAILABLE', lastCheckLatency: 200 }),
      ];
      const config: SortConfig = { key: 'lastCheck', direction: 'desc' };

      const result = sortMonitors(data, config);

      expect(result.map((d) => d.id)).toEqual(['2', '3', '1']);
    });

    it('混合状态和延迟的完整排序', () => {
      const data = [
        createMockData({ id: '1', currentStatus: 'DEGRADED', lastCheckLatency: 300 }),
        createMockData({ id: '2', currentStatus: 'AVAILABLE', lastCheckLatency: 500 }),
        createMockData({ id: '3', currentStatus: 'UNAVAILABLE', lastCheckLatency: 50 }),
        createMockData({ id: '4', currentStatus: 'AVAILABLE', lastCheckLatency: 100 }),
        createMockData({ id: '5', currentStatus: 'MISSING', lastCheckLatency: undefined }),
      ];
      const config: SortConfig = { key: 'lastCheck', direction: 'desc' };

      const result = sortMonitors(data, config);

      // AVAILABLE(100ms, 500ms) → DEGRADED(300ms) → UNAVAILABLE(50ms) → MISSING
      expect(result.map((d) => d.id)).toEqual(['4', '2', '1', '3', '5']);
    });
  });
});

describe('sortMonitorsWithPinning 质量移板排除', () => {
  it('携带负向质量移板注解的赞助通道不参与置顶', () => {
    const pinConfig: SponsorPinConfig = {
      enabled: true,
      max_pinned: 3,
      min_uptime: 90,
      min_level: 'pulse',
    };
    const clean = createPinCandidate('clean', []);
    const demoted = createPinCandidate('demoted', [
      {
        id: 'quality_hardfail',
        family: 'negative',
        label: '质量移板',
        priority: -1,
        origin: 'system',
      },
    ]);
    const sortConfig: SortConfig = { key: 'providerName', direction: 'asc' };

    const result = sortMonitorsWithPinning([clean, demoted], sortConfig, pinConfig, true);
    const cleanRow = result.find((d) => d.id === 'clean');
    const demotedRow = result.find((d) => d.id === 'demoted');

    // clean 满足全部置顶条件应被置顶；demoted 仅因携带负向注解被排除
    // （不是两者都恰好落空），从而证明 meetsPinCriteria 的负向注解排除确实生效
    expect(cleanRow?.pinned).toBe(true);
    expect(demotedRow?.pinned).toBe(false);
  });
});

/** 构造满足置顶其余条件（赞助级别/可用率）的候选项，annotations 单独可控 */
function createPinCandidate(
  id: string,
  annotations: ProcessedMonitorData['annotations']
): ProcessedMonitorData {
  return createMockData({
    id,
    providerId: id,
    sponsorLevel: 'beacon',
    uptime: 99,
    lastCheckLatency: 100,
    annotations,
  });
}
