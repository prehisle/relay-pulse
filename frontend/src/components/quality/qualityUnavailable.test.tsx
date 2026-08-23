// @vitest-environment jsdom
//
// D3b unavailable model 守护：rpdiag quality_state="unavailable"（v5.10 stale/aged）
// 的 model 即使 recent_attempts=[]、无历史均值，质量列也要画中性灰 sparkline +
// tooltip 读「不可测」，而**不能**回退成 `-`（那会把"探过但当前不可测"误当"从未测"）。
// 用 react-dom/client 在 jsdom 中真实渲染（零新依赖，沿用 onboardingDisplay.test 形态）。
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { describe, it, expect } from 'vitest';
import {
  QualityScoreCell,
  isModelQualityUnusable,
  UNAVAILABLE_COLOR,
  buildModelTooltipRow,
} from './index';
import type { RpdiagScore, RpdiagModelScore } from '../../types/monitor';

function renderCell(score: RpdiagScore): HTMLElement {
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(<QualityScoreCell score={score} />);
  });
  return container;
}

// 纯 unavailable：无 30d/7d 均值、recent_attempts 空、unavailable=true。
const unavailableModel: RpdiagModelScore = {
  model: 'opus-4-8',
  unavailable: true,
  trend: {
    latest: null,
    avg_7d: null,
    avg_30d: null,
    recent_attempts: [],
    n_7d: 0,
    n_30d: 0,
  },
};

describe('unavailable model 质量列渲染', () => {
  it('recent_attempts=[] 的 unavailable model 仍画灰 sparkline，不回退 "-"', () => {
    const container = renderCell({ models: [unavailableModel], trend: unavailableModel.trend, channel_url: '' });
    const svg = container.querySelector('svg');
    // 不是纯 "-" 占位：SVG 存在
    expect(svg).not.toBeNull();
    // 至少一个圆点用中性灰（不可测色）着色
    const greyCircles = Array.from(container.querySelectorAll('circle')).filter(
      (c) => c.getAttribute('fill') === UNAVAILABLE_COLOR,
    );
    expect(greyCircles.length).toBeGreaterThan(0);
    // 文本内容不等于纯 "-"
    expect(container.textContent).not.toBe('-');
  });

  it('isModelQualityUnusable：unavailable 或 failed 均为 true', () => {
    expect(isModelQualityUnusable(unavailableModel)).toBe(true);
    expect(isModelQualityUnusable({ model: 'x', failed: true, trend: unavailableModel.trend })).toBe(true);
    expect(isModelQualityUnusable({ model: 'x', trend: unavailableModel.trend })).toBe(false);
  });

  it('tooltip 近况对 unavailable model 读「不可测」', () => {
    const row = buildModelTooltipRow(unavailableModel);
    expect(row.detail).toContain('不可测');
  });

  it('有历史彩点的 unavailable model：历史彩点保留，不被 every-unavailable 压成整条灰线', () => {
    // 单个数字历史点占住最新槽位 + unavailable：真实近况彩点必须保留（不被误吞成整条灰
    // 线）。此时不再另画灰代表点（不覆盖真实观测值），不可测状态由 tooltip 传达。
    const withHistory: RpdiagModelScore = {
      model: 'sonnet',
      unavailable: true,
      trend: { latest: 88, avg_7d: null, avg_30d: null, recent_attempts: [88], n_7d: 1, n_30d: 0 },
    };
    const container = renderCell({ models: [withHistory], trend: withHistory.trend, channel_url: '' });
    const circles = Array.from(container.querySelectorAll('circle'));
    const colouredCount = circles.filter((c) => c.getAttribute('fill') !== UNAVAILABLE_COLOR).length;
    // 历史彩点 88 保留（未被整条灰线吞掉）；且不是纯 "-"。
    expect(colouredCount).toBeGreaterThan(0);
    expect(container.querySelector('svg')).not.toBeNull();
    // tooltip 仍读到真实近况分（与彩点一致，不被强塞覆盖），但补「当前不可测」后缀，
    // 确保 unavailable 当前态在有历史分时也不消失。
    const row = buildModelTooltipRow(withHistory);
    expect(row.detail).toContain('88');
    expect(row.detail).toContain('当前不可测');
  });
});

// no_recent_attempts（rpdiag attempts_7d==0 且非 hard-fail）：展示层降饱和 + tooltip
// 注「近7天无评测记录」，但 KEEP 历史分可见——区别于 failed/unavailable 的「不可测」。
describe('NoRecentAttempts tooltip', () => {
  // 纯 no_recent（非 unavailable）：真实历史分必须原样保留、绝不抹「不可测」。
  const staleHealthy: RpdiagModelScore = {
    model: 'sonnet',
    model_key: 'sonnet',
    no_recent_attempts: true,
    trend: { avg_30d: 91, avg_7d: 90, recent_scores: [85, 88], n_7d: 0, n_30d: 8 },
  };

  it('marks 近7天无评测记录 while keeping historical scores', () => {
    const row = buildModelTooltipRow(staleHealthy);
    expect(row.detail).toContain('近7天无评测记录');
    // 88 是真实近况分，走 unavailable 路径本会被抹成「不可测」——它仍在，才真正证明历史保真。
    expect(row.detail).toContain('88');
    // 纯 no_recent 不是「不可测」态：整串不得出现「不可测」。
    expect(row.detail).not.toContain('不可测');
  });

  it('does NOT treat no_recent_attempts as unusable', () => {
    expect(isModelQualityUnusable(staleHealthy)).toBe(false);
  });

  // 回归护栏（spec §② 优先级）：同一行同时 unavailable + no_recent 时 no_recent 展示优先——
  // 保留真实历史分（不被 unavailable 抹成「不可测」），主注「近7天无评测记录」。
  it('no_recent takes display precedence over unavailable (keeps real history)', () => {
    const bothFlags: RpdiagModelScore = {
      model: 'sonnet',
      model_key: 'sonnet',
      unavailable: true,
      no_recent_attempts: true,
      trend: { avg_30d: 91, avg_7d: 90, recent_scores: [85, 88], n_7d: 0, n_30d: 8 },
    };
    const row = buildModelTooltipRow(bothFlags);
    expect(row.detail).toContain('近7天无评测记录');
    expect(row.detail).toContain('88'); // unavailable 未抹掉历史
  });
});

// no_recent_attempts 的视觉部分：series <g> 降饱和（filter:saturate + opacity），保留真实
// 历史色相只发暗；failed/正常 model 不降饱和；同时 no_recent+unavailable 共存有历史时不追加
// 灰代表点覆盖降饱和历史（spec §③）。
describe('NoRecentAttempts sparkline desaturation', () => {
  // 降饱和 <g>：React 只在 s.dim 时挂 style，故 svg g[style] 恰为被降饱和的 series。
  const dimmedGroups = (container: HTMLElement): SVGGElement[] =>
    Array.from(container.querySelectorAll<SVGGElement>('svg g[style]')).filter((g) =>
      (g.getAttribute('style') ?? '').includes('saturate'),
    );

  it('dims the no_recent series（保留彩色历史，只发暗）', () => {
    // 真实历史（30d/7d 均值 + 近3次打分）→ 彩色 series，非灰。
    const staleColored: RpdiagModelScore = {
      model: 'sonnet',
      model_key: 'sonnet',
      no_recent_attempts: true,
      trend: { avg_30d: 91, avg_7d: 90, recent_scores: [85, 88], n_7d: 0, n_30d: 8 },
    };
    const container = renderCell({ models: [staleColored], trend: staleColored.trend, channel_url: '' });
    const dimmed = dimmedGroups(container);
    expect(dimmed).toHaveLength(1);
    const style = dimmed[0].getAttribute('style') ?? '';
    expect(style).toContain('saturate');
    expect(style).toContain('opacity');
    // 历史真彩点仍在（非灰、非空）：证明是「发暗」而非「清零灰」。
    const colouredCircles = Array.from(dimmed[0].querySelectorAll('circle')).filter(
      (c) => c.getAttribute('fill') !== UNAVAILABLE_COLOR,
    );
    expect(colouredCircles.length).toBeGreaterThan(0);
  });

  it('does NOT dim a failed model（failed 优先于 no_recent，走既有灰逻辑）', () => {
    // 同时置 no_recent_attempts + failed：dim = no_recent && !failed 必须判 false，
    // 真正验证 `&& !failed` 闸（去掉它此用例即变红）。
    const failedModel: RpdiagModelScore = {
      model: 'sonnet',
      model_key: 'sonnet',
      no_recent_attempts: true,
      failed: true,
      trend: { avg_30d: 80, avg_7d: 80, recent_attempts: [null], n_7d: 1, n_30d: 5 },
    };
    const container = renderCell({ models: [failedModel], trend: failedModel.trend, channel_url: '' });
    expect(dimmedGroups(container)).toHaveLength(0);
  });

  it('does NOT dim a normal healthy model', () => {
    const healthy: RpdiagModelScore = {
      model: 'sonnet',
      model_key: 'sonnet',
      trend: { avg_30d: 95, avg_7d: 96, recent_scores: [94, 97], n_7d: 3, n_30d: 12 },
    };
    const container = renderCell({ models: [healthy], trend: healthy.trend, channel_url: '' });
    expect(dimmedGroups(container)).toHaveLength(0);
  });

  it('unavailable + no_recent + 有历史 → 不追加灰代表点覆盖降饱和历史（spec §③）', () => {
    // 历史只占 30d/7d 槽位（slot 0/1），**不**占最新槽位（slot 4）——正是原逻辑会补一个
    // 贴底灰代表点到 slot 4 的场景。no_recent 抑制后：series 只保留 2 个真实彩点、无灰点。
    const bothWithHistory: RpdiagModelScore = {
      model: 'sonnet',
      model_key: 'sonnet',
      unavailable: true,
      no_recent_attempts: true,
      trend: { avg_30d: 91, avg_7d: 90, recent_attempts: [], n_7d: 0, n_30d: 8 },
    };
    const container = renderCell({ models: [bothWithHistory], trend: bothWithHistory.trend, channel_url: '' });
    const dimmed = dimmedGroups(container);
    expect(dimmed).toHaveLength(1); // series 被降饱和
    const circles = Array.from(dimmed[0].querySelectorAll('circle'));
    // 恰好 2 个真实历史点（30d/7d），未新增灰代表点。
    expect(circles).toHaveLength(2);
    const greyCircles = circles.filter((c) => c.getAttribute('fill') === UNAVAILABLE_COLOR);
    expect(greyCircles).toHaveLength(0);
  });

  it('multi-model cell dims only the flagged series', () => {
    const normal: RpdiagModelScore = {
      model: 'haiku',
      model_key: 'haiku',
      trend: { avg_30d: 95, avg_7d: 96, recent_scores: [94, 97], n_7d: 3, n_30d: 12 },
    };
    const stale: RpdiagModelScore = {
      model: 'sonnet',
      model_key: 'sonnet',
      no_recent_attempts: true,
      trend: { avg_30d: 91, avg_7d: 90, recent_scores: [85, 88], n_7d: 0, n_30d: 8 },
    };
    const container = renderCell({ models: [normal, stale], trend: normal.trend, channel_url: '' });
    // 两条 series 都渲染，但只有 no_recent 那条降饱和。
    expect(container.querySelectorAll('svg > g').length).toBeGreaterThanOrEqual(2);
    expect(dimmedGroups(container)).toHaveLength(1);
  });
});
