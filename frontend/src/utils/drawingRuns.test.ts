import { describe, expect, it } from 'vitest';
import { pickRecentRuns, svgDataUri } from './drawingRuns';
import type { RpdiagScore } from '../types/monitor';

function score(models: Array<{ model: string; runs: Array<[string, string]> }>): RpdiagScore {
  return {
    channel_url: '',
    trend: { n_7d: 0, n_30d: 0 },
    models: models.map((m) => ({
      model: m.model,
      trend: { n_7d: 0, n_30d: 0, recent_runs: m.runs.map(([task_id, run_at]) => ({ task_id, run_at })) },
    })),
  };
}

describe('pickRecentRuns', () => {
  it('merges models, newest first, capped at three', () => {
    const runs = pickRecentRuns(score([
      { model: 'opus', runs: [['a1', '2026-11-06T01:00:00Z'], ['a2', '2026-11-06T05:00:00Z']] },
      { model: 'sonnet', runs: [['b1', '2026-11-06T03:00:00Z'], ['b2', '2026-11-06T04:00:00Z']] },
    ]));
    expect(runs.map((r) => r.taskId)).toEqual(['a2', 'b2', 'b1']);
    expect(runs.map((r) => r.model)).toEqual(['opus', 'sonnet', 'sonnet']);
  });

  it('drops a run listed twice (a channel merged across a rename)', () => {
    const runs = pickRecentRuns(score([
      { model: 'opus', runs: [['a1', '2026-11-06T01:00:00Z']] },
      { model: 'opus', runs: [['a1', '2026-11-06T01:00:00Z'], ['a0', '2026-11-05T01:00:00Z']] },
    ]));
    expect(runs.map((r) => r.taskId)).toEqual(['a1', 'a0']);
  });

  it('is empty without a score or without runs (pre-v5.18 wire)', () => {
    expect(pickRecentRuns(undefined)).toEqual([]);
    expect(pickRecentRuns({ channel_url: '', trend: { n_7d: 0, n_30d: 0 }, models: [{ trend: { n_7d: 0, n_30d: 0 } }] })).toEqual([]);
  });
});

describe('svgDataUri', () => {
  it('percent-encodes the markup so it can only be an image source', () => {
    const uri = svgDataUri('<svg><script>alert(1)</script></svg>');
    expect(uri.startsWith('data:image/svg+xml;charset=utf-8,')).toBe(true);
    expect(uri).not.toContain('<');
  });
});
