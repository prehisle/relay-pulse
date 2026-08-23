// @vitest-environment jsdom
//
// 平板/移动端卡片视图守护。这一屏此前零测试覆盖——桌面用例把 matchMedia 恒设成
// false，于是 StatusTable 的整个移动分支（虚拟列表 + 卡片 + 横向排序条）从未被渲染过，
// 把它抽成独立模块时没有任何东西能证明搬对了。
//
// 钉死三条性质：
//   ① 断点命中时渲染的是卡片列表、不是 <table>（"表格视图"在窄屏实际是卡片）；
//   ② 每张卡片带齐服务商 / 通道 / 模型 / 可用率这几项主信息；
//   ③ 质量与厂商两个开关同时门控**卡片内容与排序条选项**——开关关掉时两处都不出，
//      这是 [[feature_flag_gate_all_surfaces]] 类 bug 最容易漏的一侧。
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { describe, it, expect, beforeAll } from 'vitest';
import { I18nextProvider } from 'react-i18next';
import i18n from '../../i18n';
import { StatusTable } from '../StatusTable';
import type { ProcessedMonitorData } from '../../types';
import type { RpdiagScoresResponse } from '../../types/monitor';

function monitor(overrides: Partial<ProcessedMonitorData>): ProcessedMonitorData {
  return {
    id: 'acme-cc-vip',
    providerId: 'acme',
    providerSlug: 'acme',
    providerName: 'Acme',
    serviceType: 'cc',
    serviceName: 'cc',
    category: 'commercial',
    sponsor: '',
    board: 'hot',
    intervalMs: 60000,
    history: [],
    currentStatus: 'AVAILABLE',
    uptime: 99,
    lastCheckLatency: 100,
    isMultiModel: false,
    channel: 'O-Max',
    modelEntries: [{ model: 'GLM-4.7', requestModel: 'glm-4.7' }],
    ...overrides,
  };
}

function renderMobile(
  data: ProcessedMonitorData[],
  opts: { rpdiagEnabled?: boolean; showVendorColumn?: boolean; rpdiagScores?: RpdiagScoresResponse } = {},
): HTMLElement {
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <StatusTable
          data={data}
          sortConfig={{ key: 'uptime', direction: 'desc' }}
          timeRange="24h"
          slowLatencyMs={5000}
          isFavorite={() => false}
          onToggleFavorite={() => {}}
          onSort={() => {}}
          onBlockHover={() => {}}
          onBlockLeave={() => {}}
          rpdiagEnabled={opts.rpdiagEnabled ?? false}
          rpdiagScores={opts.rpdiagScores}
          rpdiagScoresLoaded={!!opts.rpdiagScores}
          showVendorColumn={opts.showVendorColumn ?? false}
        />
      </I18nextProvider>,
    );
  });
  return container;
}

beforeAll(async () => {
  // 断言的是中文文案，固定语言避免受运行环境语言检测影响
  await i18n.changeLanguage('zh-CN');
  // jsdom 没有 matchMedia；恒 true = tablet 断点命中，本用例断言的正是卡片视图
  window.matchMedia = ((query: string) => ({
    matches: true,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

describe('移动端卡片视图', () => {
  it('断点命中时渲染卡片列表而非表格', () => {
    const container = renderMobile([monitor({})]);

    expect(container.querySelector('table')).toBeNull();
    // 横向排序条：至少有"按…排序"标签与若干排序按钮
    expect(container.textContent).toContain('排序');
    expect(container.querySelectorAll('button').length).toBeGreaterThan(0);
  });

  it('卡片带齐服务商 / 通道 / 模型 / 可用率', () => {
    const container = renderMobile([
      monitor({ providerName: 'Acme', channel: 'O-Max', uptime: 99 }),
    ]);

    const text = container.textContent ?? '';
    expect(text).toContain('Acme');
    expect(text).toContain('O-Max');
    expect(text).toContain('glm-4.7');
    expect(text).toContain('99%');
  });

  it('rpdiag 关闭：卡片内不出待测占位，排序条也不给"质量"选项', () => {
    const container = renderMobile([monitor({})], { rpdiagEnabled: false });

    const text = container.textContent ?? '';
    expect(text).not.toContain('待测');
    const sortLabels = Array.from(container.querySelectorAll('button')).map((b) => b.textContent ?? '');
    expect(sortLabels.some((l) => l.includes('质量'))).toBe(false);
  });

  it('rpdiag 开启且该通道无分：卡片出待测占位，排序条出"质量"选项', () => {
    const container = renderMobile([monitor({})], { rpdiagEnabled: true });

    expect(container.textContent).toContain('待测');
    const sortLabels = Array.from(container.querySelectorAll('button')).map((b) => b.textContent ?? '');
    expect(sortLabels.some((l) => l.includes('质量'))).toBe(true);
  });

  it('rpdiag 有分：卡片画 sparkline，不再显示待测', () => {
    const scores: RpdiagScoresResponse = {
      'acme|cc|o-max': {
        models: [{ model: 'sonnet', model_key: 'sonnet', trend: { avg_30d: 90, avg_7d: 92, recent_attempts: [91, 93, 95] } }],
      },
    } as unknown as RpdiagScoresResponse;
    const container = renderMobile([monitor({})], { rpdiagEnabled: true, rpdiagScores: scores });

    expect(container.querySelector('svg circle')).not.toBeNull();
    expect(container.textContent).not.toContain('待测');
  });

  it('厂商开关同时门控卡片徽章与排序条选项', () => {
    const off = renderMobile([monitor({ modelVendor: 'zhipu' })], { showVendorColumn: false });
    expect(off.textContent).not.toContain('智谱');
    expect(Array.from(off.querySelectorAll('button')).some((b) => (b.textContent ?? '').includes('厂商'))).toBe(false);

    const on = renderMobile([monitor({ modelVendor: 'zhipu' })], { showVendorColumn: true });
    expect(on.querySelector('[aria-label*="智谱"], [title*="智谱"]')).not.toBeNull();
    expect(Array.from(on.querySelectorAll('button')).some((b) => (b.textContent ?? '').includes('厂商'))).toBe(true);
  });
});
