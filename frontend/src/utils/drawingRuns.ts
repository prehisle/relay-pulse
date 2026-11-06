import type { RpdiagScore } from '../types/monitor';

/** 画作列一格要展示的一次测试。 */
export interface DrawingRun {
  taskId: string;
  runAt: string;
  /** 这次测的是哪个模型（同一通道可挂多个模型，浮层里逐幅标出）。 */
  model: string;
}

export const DRAWING_RUNS_PER_CELL = 3;

/**
 * 通道最近几次出了分的测试，新的在前：各模型的 trend.recent_runs 合并、按 task id
 * 去重（同一通道改名前后两半会被 cid 合并，同一次测试可能出现两遍）、按时间倒序取前 N。
 */
export function pickRecentRuns(score: RpdiagScore | undefined, limit = DRAWING_RUNS_PER_CELL): DrawingRun[] {
  if (!score) return [];
  const byId = new Map<string, DrawingRun>();
  for (const m of score.models ?? []) {
    for (const run of m.trend?.recent_runs ?? []) {
      if (!run?.task_id || byId.has(run.task_id)) continue;
      byId.set(run.task_id, { taskId: run.task_id, runAt: run.run_at, model: m.model ?? '' });
    }
  }
  return [...byId.values()]
    .sort((a, b) => Date.parse(b.runAt) - Date.parse(a.runAt) || b.taskId.localeCompare(a.taskId))
    .slice(0, limit);
}

/** SVG 源码转 `<img>` 可用的 data URI。模型输出只走这条路显示，绝不插进 DOM。 */
export function svgDataUri(svg: string): string {
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
}
