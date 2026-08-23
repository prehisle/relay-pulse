// @vitest-environment jsdom
//
// 质量分查表的**两个调用点**守护（桌面单元格 + 移动端卡片）。
//
// lookupRpdiagScore 的 provider 参数收的是候选数组「展示名优先、slug 兜底」——
// 这是当年 WorldBase.ai / YunWu 两家质量列整列空白的修复点：rpdiag 侧按展示名建索引，
// 前端却按 slug 查，两者相等的 88 家没事，不等的两家落空。
//
// 那条规则本身在 useRpdiagScores.test.ts 有单测；这里补的是**调用点**：拆分表格时，
// 只要有人把某一处「顺手归一化」成单参数 item.providerId，那一屏就会静默丢分，
// 而 hook 单测照样全绿。故两屏必须各渲染一次、各断言一次。
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { describe, it, expect, beforeAll, afterEach } from 'vitest';
import { I18nextProvider } from 'react-i18next';
import i18n from '../../i18n';
import { StatusTable } from '../StatusTable';
import type { ProcessedMonitorData } from '../../types';
import type { RpdiagScoresResponse } from '../../types/monitor';

// slug ≠ 展示名：rpdiag 只按展示名建了索引，按 providerId 查必然落空
const SLUG = 'worldbase';
const DISPLAY_NAME = 'WorldBase.ai';

const scores = {
  [`${DISPLAY_NAME.toLowerCase()}|cc|o-max`]: {
    models: [
      { model: 'sonnet', model_key: 'sonnet', trend: { avg_30d: 90, avg_7d: 92, recent_attempts: [91, 93, 95] } },
    ],
  },
} as unknown as RpdiagScoresResponse;

const item: ProcessedMonitorData = {
  id: 'wb-cc-omax',
  providerId: SLUG,
  providerSlug: SLUG,
  providerName: DISPLAY_NAME,
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
  modelEntries: [{ model: 'Sonnet', requestModel: 'claude-sonnet-4-5' }],
} as ProcessedMonitorData;

function setViewport(isMobile: boolean) {
  window.matchMedia = ((query: string) => ({
    matches: isMobile,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
}

function render(): HTMLElement {
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <StatusTable
          data={[item]}
          sortConfig={{ key: 'uptime', direction: 'desc' }}
          timeRange="24h"
          slowLatencyMs={5000}
          isFavorite={() => false}
          onToggleFavorite={() => {}}
          onSort={() => {}}
          onBlockHover={() => {}}
          onBlockLeave={() => {}}
          rpdiagEnabled
          rpdiagScoresLoaded
          rpdiagScores={scores}
        />
      </I18nextProvider>,
    );
  });
  return container;
}

beforeAll(async () => {
  await i18n.changeLanguage('zh-CN');
});

afterEach(() => {
  setViewport(false);
});

describe('质量分查表：slug≠展示名时两屏都必须命中', () => {
  it('桌面表格单元格画出 sparkline，而不是「待测」', () => {
    setViewport(false);
    const container = render();

    expect(container.querySelector('tbody svg circle')).not.toBeNull();
    expect(container.querySelector('tbody')?.textContent).not.toContain('待测');
  });

  it('移动端卡片画出 sparkline，而不是「待测」', () => {
    setViewport(true);
    const container = render();

    expect(container.querySelector('table')).toBeNull(); // 确认真的在卡片视图
    expect(container.querySelector('svg circle')).not.toBeNull();
    expect(container.textContent).not.toContain('待测');
  });
});
