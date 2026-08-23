// @vitest-environment jsdom
//
// 桌面表格的列骨架守护：11 列里有 5 列是条件渲染（标注 / 服务商 / 厂商 / 价格 / 质量），
// 而每一列都要在 <colgroup>、<thead>、每个 <tbody> 行三处各写一遍。三处漏改一处
// 就是整表错位——这是新增或隐藏条件列最典型的 bug 类别，且错位在类型层完全看不出来。
//
// 本文件对 5 根条件轴做全组合（2^5 = 32 种）断言：
//   ① col 数 == th 数 == 每行 td 数；
//   ② 出现的表头按固定 canonical 顺序排列（厂商紧跟模型、质量紧邻趋势）；
//   ③ 某轴关闭时，它在三处同时消失（不是只在表头消失）。
//
// 与 modelVendorColumn.test.tsx 的分工：那份钉厂商列的**渲染语义**（图标/占位/ⓘ），
// 这份只钉**列骨架**，与具体列的内容无关。
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { describe, it, expect, beforeAll } from 'vitest';
import { I18nextProvider } from 'react-i18next';
import i18n from '../../i18n';
import { StatusTable } from '../StatusTable';
import type { ProcessedMonitorData } from '../../types';

function monitor(overrides: Partial<ProcessedMonitorData> = {}): ProcessedMonitorData {
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

interface Axes {
  annotations: boolean;
  provider: boolean;
  vendor: boolean;
  price: boolean;
  quality: boolean;
}

function renderTable(axes: Axes): HTMLElement {
  // 标注列的显隐是**数据驱动**的（没有任何一行带标注就整列不渲染），不是 prop，
  // 故用带/不带 annotations 的两份 fixture 来切这根轴。
  const data: ProcessedMonitorData[] = [
    monitor({
      id: 'a',
      annotations: axes.annotations
        ? [{ id: 'public_service', family: 'positive', label: '公益', priority: 10, origin: 'config' }]
        : undefined,
    }),
    monitor({ id: 'b', modelVendor: 'zhipu' }),
  ];

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
          showProvider={axes.provider}
          showVendorColumn={axes.vendor}
          hidePriceColumn={!axes.price}
          rpdiagEnabled={axes.quality}
        />
      </I18nextProvider>,
    );
  });
  return container;
}

// canonical 列顺序。每项 = [轴名（null=恒显示）, 表头文案前缀]。
// 表头文案取自 i18n 而非硬编码，改文案不会误伤本守卫。
function canonicalColumns(): Array<{ axis: keyof Axes | null; label: string }> {
  const t = i18n.t.bind(i18n);
  return [
    { axis: 'annotations', label: t('table.headers.annotation') },
    { axis: 'provider', label: t('table.headers.provider') },
    { axis: null, label: t('table.headers.service') },
    { axis: null, label: t('table.headers.channel') },
    { axis: null, label: t('table.headers.model') },
    { axis: 'vendor', label: t('table.headers.modelVendor') },
    { axis: 'price', label: t('table.headers.priceRatioLine1') },
    { axis: null, label: t('table.headers.listedDaysLine1') },
    { axis: null, label: t('table.headers.uptime') },
    { axis: null, label: t('table.headers.lastCheckLine1') },
    { axis: 'quality', label: t('table.headers.quality') },
    { axis: null, label: t('table.headers.trend') },
  ];
}

const ALL_AXES: Array<keyof Axes> = ['annotations', 'provider', 'vendor', 'price', 'quality'];

// 5 根轴的全组合
function allCombos(): Axes[] {
  const combos: Axes[] = [];
  for (let mask = 0; mask < 1 << ALL_AXES.length; mask++) {
    const axes = {} as Axes;
    ALL_AXES.forEach((axis, i) => {
      axes[axis] = (mask & (1 << i)) !== 0;
    });
    combos.push(axes);
  }
  return combos;
}

function describeAxes(axes: Axes): string {
  return ALL_AXES.filter((a) => axes[a]).join('+') || '全关';
}

beforeAll(async () => {
  await i18n.changeLanguage('zh-CN');
  // jsdom 没有 matchMedia；恒 false = 桌面断点，本文件断言的是桌面表格骨架
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

describe('桌面表格列骨架（colgroup / thead / tbody 三处并行）', () => {
  it.each(allCombos().map((axes) => [describeAxes(axes), axes] as const))(
    '%s：col 数 == th 数 == 每行 td 数，且顺序为 canonical 子序列',
    (_name, axes) => {
      const container = renderTable(axes);

      const cols = container.querySelectorAll('colgroup col');
      const ths = Array.from(container.querySelectorAll('thead th'));
      const rows = Array.from(container.querySelectorAll('tbody tr'));

      const expected = canonicalColumns().filter((c) => c.axis === null || axes[c.axis]);

      expect(ths).toHaveLength(expected.length);
      expect(cols).toHaveLength(expected.length);
      expect(rows.length).toBeGreaterThan(0);
      for (const row of rows) {
        expect(row.querySelectorAll('td')).toHaveLength(expected.length);
      }

      // ② 顺序：逐列比对表头文案前缀（两行表头的 textContent 是两行拼接，故用 startsWith）
      ths.forEach((th, i) => {
        expect((th.textContent ?? '').trim().startsWith(expected[i].label)).toBe(true);
      });
    },
  );

  it('每根轴关掉时，它在 colgroup / thead / tbody 三处同时消失', () => {
    const on: Axes = { annotations: true, provider: true, vendor: true, price: true, quality: true };
    const full = renderTable(on);
    const fullCount = full.querySelectorAll('thead th').length;

    for (const axis of ALL_AXES) {
      const container = renderTable({ ...on, [axis]: false });
      const ths = container.querySelectorAll('thead th').length;
      const cols = container.querySelectorAll('colgroup col').length;
      const tds = container.querySelector('tbody tr')?.querySelectorAll('td').length ?? -1;

      expect(ths, `关掉 ${axis} 后表头应少一列`).toBe(fullCount - 1);
      expect(cols, `关掉 ${axis} 后 colgroup 应少一列`).toBe(fullCount - 1);
      expect(tds, `关掉 ${axis} 后单元格应少一个`).toBe(fullCount - 1);
    }
  });
});
