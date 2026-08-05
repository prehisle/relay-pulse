import { afterEach, describe, expect, it, vi } from 'vitest';
import type {
  MonitorGroup,
  MonitorLayer,
  MonitorResult,
  StatusCounts,
  TimePoint,
} from '../types';
import {
  calculateUptime,
  canonicalize,
  convertGroupToProcessedData,
  convertLegacyDataToProcessedData,
  deriveChannelVendor,
} from './monitorDataProcessor';

// ─── 测试工具 ───────────────────────────────────────────────

const ZERO_COUNTS: StatusCounts = {
  available: 0,
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
  response_timeout: 0,
  content_mismatch: 0,
};

function counts(overrides: Partial<StatusCounts> = {}): StatusCounts {
  return { ...ZERO_COUNTS, ...overrides };
}

function tp(overrides: Partial<TimePoint> = {}): TimePoint {
  return {
    time: '10:00',
    timestamp: 1000,
    status: 1,
    latency: 100,
    availability: 100,
    ...overrides,
  };
}

function layer(overrides: Partial<MonitorLayer> = {}): MonitorLayer {
  return {
    model: 'gpt-4o',
    layer_order: 0,
    current_status: { status: 1, latency: 100, timestamp: 1000 },
    timeline: [tp({ status_counts: counts({ available: 1 }) })],
    ...overrides,
  };
}

function legacyItem(overrides: Partial<MonitorResult> = {}): MonitorResult {
  return {
    provider: 'openai',
    provider_slug: 'openai',
    service: 'chat',
    category: 'commercial',
    sponsor: 'Test',
    channel: 'main',
    board: 'hot',
    current_status: { status: 1, latency: 120, timestamp: 1000 },
    timeline: [tp({ status_counts: counts({ available: 1 }) })],
    ...overrides,
  } as MonitorResult;
}

function group(overrides: Partial<MonitorGroup> = {}): MonitorGroup {
  return {
    provider: 'openai',
    provider_slug: 'openai',
    service: 'chat',
    category: 'commercial',
    sponsor: 'Test',
    channel: 'main',
    board: 'hot',
    current_status: 1,
    layers: [layer()],
    ...overrides,
  } as MonitorGroup;
}

afterEach(() => {
  vi.restoreAllMocks();
});

// ─── 测试 ───────────────────────────────────────────────────

describe('monitorDataProcessor', () => {
  describe('canonicalize', () => {
    it('undefined 时返回空字符串', () => {
      expect(canonicalize()).toBe('');
    });

    it('空字符串返回空字符串', () => {
      expect(canonicalize('')).toBe('');
    });

    it('去空格 + 转小写', () => {
      expect(canonicalize('  OpEnAI  ')).toBe('openai');
    });

    it('纯空白字符返回空字符串', () => {
      expect(canonicalize('  \t\n  ')).toBe('');
    });
  });

  describe('calculateUptime', () => {
    it('全部有效点返回平均值（两位小数）', () => {
      expect(calculateUptime([
        { availability: 100 },
        { availability: 66.666 },
      ])).toBe(83.33);
    });

    it('跳过无效点（availability < 0）', () => {
      expect(calculateUptime([
        { availability: 100 },
        { availability: -1 },
        { availability: 50 },
      ])).toBe(75);
    });

    it('全部无效点返回 -1', () => {
      expect(calculateUptime([
        { availability: -1 },
        { availability: -1 },
      ])).toBe(-1);
    });

    it('空数组返回 -1', () => {
      expect(calculateUptime([])).toBe(-1);
    });
  });

  describe('convertLegacyDataToProcessedData', () => {
    it('carries channel_id into channelId (cross-product join anchor)', () => {
      const result = convertLegacyDataToProcessedData(legacyItem({ channel_id: 'ch_legacy' }), 5000);
      expect(result.channelId).toBe('ch_legacy');
    });

    it('正确映射基础字段并规范化 provider 名称', () => {
      const result = convertLegacyDataToProcessedData(
        legacyItem({
          provider: ' OpenAI ',
          provider_slug: 'openai-slug',
          service_name: '对话服务',
          current_status: { status: 2, latency: 321, timestamp: 2000 },
          timeline: [
            tp({ status: 1, availability: 100, status_counts: counts({ available: 1 }) }),
            tp({ time: '10:01', timestamp: 1060, status: 2, latency: 260, availability: 80,
              status_counts: counts({ degraded: 1, slow_latency: 1 }) }),
          ],
        }),
        5000,
      );

      expect(result).toMatchObject({
        id: 'openai-chat-main',
        providerId: 'openai',
        providerSlug: 'openai-slug',
        providerName: 'OpenAI',
        serviceName: '对话服务',
        currentStatus: 'DEGRADED',
        uptime: 90,
        lastCheckTimestamp: 2000,
        lastCheckLatency: 321,
        isMultiModel: false,
      });
    });

    it('使用 monitor 级 slow_latency_ms 覆盖全局值', () => {
      const result = convertLegacyDataToProcessedData(
        legacyItem({ slow_latency_ms: 2500 }),
        5000,
      );

      expect(result.slowLatencyMs).toBe(2500);
      expect(result.history[0].slowLatencyMs).toBe(2500);
    });

    it('旧版赞助等级 basic → pulse', () => {
      const item = legacyItem();
      (item as Record<string, unknown>).sponsor_level = ' BASIC ';
      const result = convertLegacyDataToProcessedData(item, 5000);
      expect(result.sponsorLevel).toBe('pulse');
    });

    it('保留有效 URL 并识别已支持的 sponsor_level', () => {
      const result = convertLegacyDataToProcessedData(
        legacyItem({
          provider_url: 'https://provider.example',
          sponsor_url: 'https://sponsor.example',
          sponsor_level: 'core',
        }),
        5000,
      );

      expect(result.providerUrl).toBe('https://provider.example');
      expect(result.sponsorUrl).toBe('https://sponsor.example');
      expect(result.sponsorLevel).toBe('core');
    });

    it('无效 URL 置为 null', () => {
      vi.spyOn(console, 'warn').mockImplementation(() => {});
      const result = convertLegacyDataToProcessedData(
        legacyItem({ provider_url: 'not-a-url', sponsor_url: 'bad' }),
        5000,
      );
      expect(result.providerUrl).toBeNull();
      expect(result.sponsorUrl).toBeNull();
    });

    it('current_status 为 null 时状态为 MISSING', () => {
      const result = convertLegacyDataToProcessedData(
        legacyItem({ current_status: null }),
        5000,
      );
      expect(result.currentStatus).toBe('MISSING');
    });

    it('缺失 status_counts 会回填默认零值', () => {
      const result = convertLegacyDataToProcessedData(
        legacyItem({ timeline: [tp()] }),
        5000,
      );
      expect(result.history[0].statusCounts).toEqual(ZERO_COUNTS);
    });
  });

  describe('convertGroupToProcessedData', () => {
    it('carries channel_id into channelId (cross-product join anchor)', () => {
      const result = convertGroupToProcessedData(group({ channel_id: 'ch_group' }), 5000);
      expect(result.channelId).toBe('ch_group');
    });

    it('多层 group 按最差状态合成时间线', () => {
      const parentLayer = layer({
        model: 'parent',
        layer_order: 0,
        current_status: { status: 1, latency: 111, timestamp: 1000 },
        timeline: [
          tp({ time: '10:00', timestamp: 1000, status: 1, latency: 100, availability: 98,
            status_counts: counts({ available: 1 }) }),
          tp({ time: '10:01', timestamp: 1060, status: 1, latency: 120, availability: 100,
            status_counts: counts({ available: 1 }) }),
        ],
      });
      const childLayer = layer({
        model: 'child',
        layer_order: 1,
        current_status: { status: 0, latency: 222, timestamp: 2000 },
        timeline: [
          // 故意乱序，验证排序逻辑
          tp({ time: '10:01', timestamp: 1062, status: 0, latency: 300, availability: 60,
            status_counts: counts({ unavailable: 1, server_error: 1 }) }),
          tp({ time: '10:00', timestamp: 1005, status: 2, latency: 200, availability: 70,
            status_counts: counts({ degraded: 1, slow_latency: 1 }) }),
        ],
      });

      const result = convertGroupToProcessedData(
        group({ current_status: 0, layers: [parentLayer, childLayer] }),
        4000,
      );

      expect(result.isMultiModel).toBe(true);
      expect(result.currentStatus).toBe('UNAVAILABLE');
      // uptime = min(parent avg, child avg)
      // parent: (98+100)/2 = 99, child: (70+60)/2 = 65 → min = 65
      expect(result.uptime).toBe(65);
      // 父层优先 → lastCheck 来自父层
      expect(result.lastCheckTimestamp).toBe(1000);
      expect(result.lastCheckLatency).toBe(111);

      expect(result.history).toHaveLength(2);
      // 第 1 个时间点：parent=1(绿) + child=2(黄) → worst=2(DEGRADED)
      expect(result.history[0]).toMatchObject({
        status: 'DEGRADED',
        latency: 200,         // max(100, 200)
        availability: 70,     // min(98, 70)
      });
      // 第 2 个时间点：parent=1(绿) + child=0(红) → worst=0(UNAVAILABLE)
      expect(result.history[1]).toMatchObject({
        status: 'UNAVAILABLE',
        latency: 300,         // max(120, 300)
        availability: 60,     // min(100, 60)
      });
    });

    it('超出时间容差的层点不参与合成', () => {
      // 子层 timestamp=1200 远离基准点 1000/1060（容差=30s），不参与合成
      const result = convertGroupToProcessedData(
        group({
          current_status: 0,
          layers: [
            layer({
              layer_order: 0,
              current_status: { status: 1, latency: 100, timestamp: 1000 },
              timeline: [
                tp({ time: '10:00', timestamp: 1000, status: 1, latency: 100, availability: 100,
                  status_counts: counts({ available: 1 }) }),
                tp({ time: '10:01', timestamp: 1060, status: 1, latency: 110, availability: 100,
                  status_counts: counts({ available: 1 }) }),
              ],
            }),
            layer({
              layer_order: 1,
              current_status: { status: 0, latency: 500, timestamp: 1200 },
              timeline: [tp({
                time: '10:03', timestamp: 1200, status: 0, latency: 500, availability: 50,
                status_counts: counts({ unavailable: 1, server_error: 1 }),
              })],
            }),
          ],
        }),
        5000,
      );

      expect(result.history).toHaveLength(2);
      // 子层点超出容差 → 合成结果仅反映父层
      expect(result.history[0]).toMatchObject({ status: 'AVAILABLE', latency: 100, availability: 100 });
      expect(result.history[1]).toMatchObject({ status: 'AVAILABLE', latency: 110, availability: 100 });
      // 但 uptime 取各层最小值（子层独立计算 = 50）
      expect(result.uptime).toBe(50);
    });

    it('单层 group 标记为非多模型', () => {
      const result = convertGroupToProcessedData(
        group({
          provider: ' Claude ',
          provider_slug: 'claude',
          current_status: 1,
          layers: [layer({
            current_status: { status: 1, latency: 88, timestamp: 3000 },
            timeline: [tp({ availability: 88 })],
          })],
        }),
        5000,
      );

      expect(result.isMultiModel).toBe(false);
      expect(result.providerName).toBe('Claude');
      expect(result.currentStatus).toBe('AVAILABLE');
      expect(result.uptime).toBe(88);
    });

    it('仅有 request_model 时也保留模型映射', () => {
      const result = convertGroupToProcessedData(
        group({
          current_status: 1,
          layers: [layer({
            model: '',
            request_model: 'claude-haiku-4-5-20251001',
            current_status: { status: 1, latency: 88, timestamp: 3000 },
            timeline: [tp({ availability: 100 })],
          })],
        }),
        5000,
      );

      expect(result.modelEntries).toEqual([
        { model: '', requestModel: 'claude-haiku-4-5-20251001' },
      ]);
    });

    it('无父层时选择时间戳最新的层', () => {
      const result = convertGroupToProcessedData(
        group({
          current_status: 2,
          layers: [
            layer({
              layer_order: 1,
              current_status: { status: 1, latency: 10, timestamp: 1000 },
              timeline: [],
            }),
            layer({
              layer_order: 2,
              current_status: { status: 2, latency: 20, timestamp: 2000 },
              timeline: [],
            }),
          ],
        }),
        4000,
      );

      expect(result.lastCheckTimestamp).toBe(2000);
      expect(result.lastCheckLatency).toBe(20);
    });

    it('空层返回空 history 和 -1 uptime', () => {
      const result = convertGroupToProcessedData(
        group({
          current_status: -1,
          layers: [layer({ timeline: [] })],
        }),
        5000,
      );

      expect(result.history).toHaveLength(0);
      expect(result.uptime).toBe(-1);
    });

    it('合并 http_code_breakdown', () => {
      const l1 = layer({
        layer_order: 0,
        timeline: [tp({
          timestamp: 1000,
          status: 0,
          availability: 50,
          status_counts: counts({
            unavailable: 1,
            server_error: 1,
            http_code_breakdown: { server_error: { 500: 2 } },
          }),
        })],
      });
      const l2 = layer({
        layer_order: 1,
        timeline: [tp({
          timestamp: 1005,
          status: 0,
          availability: 40,
          status_counts: counts({
            unavailable: 1,
            server_error: 1,
            http_code_breakdown: { server_error: { 500: 1, 502: 3 } },
          }),
        })],
      });

      const result = convertGroupToProcessedData(
        group({ current_status: 0, layers: [l1, l2] }),
        5000,
      );

      expect(result.history[0].statusCounts.http_code_breakdown).toEqual({
        server_error: { 500: 3, 502: 3 },
      });
    });
  });
});

// ─── 模型厂商（model_vendor 正交轴） ─────────────────────────

describe('deriveChannelVendor', () => {
  it('所有 layer 同一厂商 → 提升为通道级厂商', () => {
    expect(deriveChannelVendor([
      { model_vendor: 'zhipu' },
      { model_vendor: 'zhipu' },
    ])).toBe('zhipu');
  });

  it('单 layer 有厂商 → 直接采用', () => {
    expect(deriveChannelVendor([{ model_vendor: 'moonshot' }])).toBe('moonshot');
  });

  it('大小写/空白差异归一后视为同一厂商', () => {
    expect(deriveChannelVendor([
      { model_vendor: ' Zhipu ' },
      { model_vendor: 'zhipu' },
    ])).toBe('zhipu');
  });

  // 这条是厂商列的核心安全性质：配置侧只校验非空值一致，半填是合法加载态，
  // 展示层绝不能拿一半的证据给出十成的确定性。
  it('半填（一行有一行空）→ undefined，不显示任何厂商', () => {
    expect(deriveChannelVendor([
      { model_vendor: 'zhipu' },
      { model_vendor: '' },
    ])).toBeUndefined();
    expect(deriveChannelVendor([
      { model_vendor: 'zhipu' },
      {},
    ])).toBeUndefined();
  });

  it('冲突（两家不同厂商）→ undefined', () => {
    expect(deriveChannelVendor([
      { model_vendor: 'zhipu' },
      { model_vendor: 'moonshot' },
    ])).toBeUndefined();
  });

  it('全空 / 空数组 / undefined → undefined', () => {
    expect(deriveChannelVendor([{ model_vendor: '' }, {}])).toBeUndefined();
    expect(deriveChannelVendor([])).toBeUndefined();
    expect(deriveChannelVendor(undefined)).toBeUndefined();
  });
});

describe('convert*ToProcessedData 的 modelVendor', () => {
  it('分组路径：全 layer 一致时带上厂商', () => {
    const result = convertGroupToProcessedData(
      group({ layers: [layer({ model_vendor: 'zhipu' }), layer({ layer_order: 1, model_vendor: 'zhipu' })] }),
      5000,
    );
    expect(result.modelVendor).toBe('zhipu');
  });

  it('分组路径：存量数据（无 vendor 键）→ undefined', () => {
    const result = convertGroupToProcessedData(group({ layers: [layer()] }), 5000);
    expect(result.modelVendor).toBeUndefined();
  });

  it('legacy 扁平路径：直接读 model_vendor 并归一', () => {
    expect(convertLegacyDataToProcessedData(legacyItem({ model_vendor: ' QWEN ' }), 5000).modelVendor).toBe('qwen');
    expect(convertLegacyDataToProcessedData(legacyItem({}), 5000).modelVendor).toBeUndefined();
  });
});
