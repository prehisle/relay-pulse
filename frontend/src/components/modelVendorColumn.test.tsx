// @vitest-environment jsdom
//
// 模型厂商列（model_vendor 正交轴 Phase 2）守护。钉死三条性质：
//   ① showVendorColumn=false 时整列（表头 + 单元格 + 服务列 ⓘ）完全不存在
//      —— Phase 3 回填厂商之前生产就是这个态，「对用户零变化」必须可证；
//   ② showVendorColumn=true 时表头出现、有厂商的行出图标+名字、没厂商的行是 "-"
//      （不是空白，否则读不出"这条没声明"）；
//   ③ 表体单元格数与表头列数一致 —— colgroup/th/td 三处漏改一处就会整表错位，
//      这是新增条件列最典型的 bug 类别。
// 用 react-dom/client 在 jsdom 真实渲染，沿用 qualityUnavailable.test 形态、零新依赖。
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { describe, it, expect, beforeAll } from 'vitest';
import { I18nextProvider } from 'react-i18next';
import i18n from '../i18n';
import { StatusTable } from './StatusTable';
import { VendorBadge } from './VendorBadge';
import type { ProcessedMonitorData } from '../types';

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

function renderTable(data: ProcessedMonitorData[], showVendorColumn: boolean): HTMLElement {
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
          rpdiagEnabled={false}
          showVendorColumn={showVendorColumn}
        />
      </I18nextProvider>,
    );
  });
  return container;
}

beforeAll(async () => {
  // 断言的是中文文案，固定语言避免受运行环境语言检测影响
  await i18n.changeLanguage('zh-CN');
  // jsdom 没有 matchMedia；恒 false = 桌面端断点，本用例断言的正是桌面表格结构
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

describe('模型厂商列', () => {
  it('showVendorColumn=false：表头、单元格、服务列 ⓘ 全部不渲染', () => {
    const container = renderTable([monitor({ modelVendor: 'zhipu' })], false);

    const headers = Array.from(container.querySelectorAll('thead th')).map((th) => th.textContent ?? '');
    expect(headers.some((h) => h.includes('厂商'))).toBe(false);
    // 即使该行有 vendor，列关掉就一个字都不出
    expect(container.textContent).not.toContain('智谱');
    // 服务列 ⓘ 浮层触发器（文案指向厂商列）同样不渲染
    expect(container.querySelector('thead button[aria-label], thead [data-info-popover]')).toBeNull();
  });

  it('showVendorColumn=true：表头出现且紧邻模型列', () => {
    const container = renderTable([monitor({ modelVendor: 'zhipu' })], true);

    const headers = Array.from(container.querySelectorAll('thead th')).map((th) => th.textContent ?? '');
    const modelIdx = headers.findIndex((h) => h.includes('模型'));
    const vendorIdx = headers.findIndex((h) => h.includes('厂商'));
    expect(modelIdx).toBeGreaterThanOrEqual(0);
    expect(vendorIdx).toBe(modelIdx + 1);
  });

  it('有厂商的行渲染本地化厂商名 + 图标；无厂商的行渲染 "-"', () => {
    const container = renderTable(
      [monitor({ id: 'a', modelVendor: 'zhipu' }), monitor({ id: 'b' })],
      true,
    );

    const rows = Array.from(container.querySelectorAll('tbody tr'));
    const headers = Array.from(container.querySelectorAll('thead th')).map((th) => th.textContent ?? '');
    const vendorIdx = headers.findIndex((h) => h.includes('厂商'));

    const withVendor = rows[0].querySelectorAll('td')[vendorIdx];
    expect(withVendor.textContent).toContain('智谱');
    expect(withVendor.querySelector('svg')).not.toBeNull();

    const withoutVendor = rows[1].querySelectorAll('td')[vendorIdx];
    expect(withoutVendor.textContent?.trim()).toBe('-');
  });

  it('词表外的 code 原样显示 code，不猜名字也不崩', () => {
    const container = renderTable([monitor({ modelVendor: 'brandnew' })], true);
    expect(container.textContent).toContain('brandnew');
  });

  it('表头列数与每行单元格数一致（colgroup/th/td 三处同步）', () => {
    for (const show of [false, true]) {
      const container = renderTable([monitor({ modelVendor: 'zhipu' })], show);
      const thCount = container.querySelectorAll('thead th').length;
      const colCount = container.querySelectorAll('colgroup col').length;
      const tdCount = container.querySelectorAll('tbody tr')[0].querySelectorAll('td').length;
      expect({ show, thCount, tdCount }).toEqual({ show, thCount, tdCount: thCount });
      expect({ show, colCount }).toEqual({ show, colCount: thCount });
    }
  });
});

// iconOnly（移动端卡片）：只出图标不出文字，全名走 title/aria-label；
// 未收录 code 没图标，必须退回文字，否则那条通道会彻底没有厂商信号。
describe('VendorBadge iconOnly', () => {
  function renderBadge(vendor: string) {
    const container = document.createElement('div');
    document.body.appendChild(container);
    const root = createRoot(container);
    act(() => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <VendorBadge vendor={vendor} compact iconOnly />
        </I18nextProvider>,
      );
    });
    return container;
  }

  it('已收录厂商：出图标、不出文字、aria-label 带全名', () => {
    const container = renderBadge('zhipu');
    expect(container.querySelector('svg')).not.toBeNull();
    expect(container.textContent).toBe('');
    expect(container.querySelector('[aria-label]')?.getAttribute('aria-label')).toBe('智谱');
  });

  it('未收录 code：无图标时退回文字', () => {
    const container = renderBadge('brandnew');
    expect(container.querySelector('svg')).toBeNull();
    expect(container.textContent).toBe('brandnew');
  });
});
