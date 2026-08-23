// rpdiag 质量分的**纯计算层**：待测判定、不可用语义、sparkline 的 Y 轴与配色、
// 模型家族排序。全部无 React、无 DOM，供 QualityScoreCell 与调用方（表格 / 移动卡片）共用。
//
// 这些数值映射是跨产品的视觉语言：rpdiag 站内的 ranking sparkline 也照搬同一套
// qualityScoreYNorm / qualityScoreColor，改动会同时影响两个产品的读图直觉。
import type { RpdiagModelScore } from '../../types/monitor';

// rpdiag 覆盖的可执行 service（gm 暂不采样）。
const RPDIAG_COVERED_SERVICES = new Set(['cc', 'cx']);

/** 「待测」占位判定：有监测但 rpdiag 无 match 的 active 板 cc/cx 通道。
 *  gm/cold 板/rpdiag 关闭一律不挂（否则永久假「待测」误导）。 */
export function shouldShowQualityPending(args: {
  rpdiagEnabled: boolean; hasScore: boolean; serviceType: string; board: string;
}): boolean {
  if (!args.rpdiagEnabled || args.hasScore) return false;
  if (!RPDIAG_COVERED_SERVICES.has((args.serviceType || '').toLowerCase())) return false;
  return (args.board || '').toLowerCase() !== 'cold';
}

// 灰=测不了/无质量数据，与 qualityScoreColor 的红=测到响应但质量真差区分开。
// 中性灰跨 4 套主题都可辨，沿用本组件硬编码 HSL 的既有风格。
export const UNAVAILABLE_COLOR = 'hsl(0 0% 55%)';

/** 该 model 当前「不可测」：failed（硬失败清零灰）或 unavailable（v5.10 stale/aged 灰）。
 *  代表点/最新槽位据此画灰、tooltip 近况读「不可测」。纯判定，供 sparkline 与测试复用。 */
export function isModelQualityUnusable(m: RpdiagModelScore): boolean {
  return m.failed === true || m.unavailable === true;
}

// 分数 → SVG 高度归一化值（0=底部，1=顶部）。Piecewise 把高分段 80-100
// 拉伸到 60% 像素带，让 95 vs 100 等小差异在视觉上看得见。
// 业务事实：rpdiag 80% 的通道分数集中在 80-100，原本线性映射 95-100 只占
// 顶 5% 像素，sparkline 全部贴顶看不出形状。
export function qualityScoreYNorm(score: number): number {
  const c = Math.max(0, Math.min(100, score));
  if (c <= 60) return (c / 60) * 0.2;                  // [0,60]   → [0, 0.2]
  if (c <= 80) return 0.2 + ((c - 60) / 20) * 0.2;     // [60,80]  → [0.2, 0.4]
  return 0.4 + ((c - 80) / 20) * 0.6;                  // [80,100] → [0.4, 1.0]
}

// 分数 → HSL 颜色：5 个色站段内线性插值，高分段（80-100）分辨率最高，
// 让 90 / 95 / 100 也有清晰可辨的色差。
//   0   → 红     (hue 0)
//   60  → 橙黄   (hue 40)
//   80  → 黄绿   (hue 75)
//   90  → 草绿   (hue 105)
//   100 → 翠绿   (hue 140)
// 不复用 `availabilityToColor`：可用率与质量分语义不同，未来 rpdiag 可能调整阈值。
export function qualityScoreColor(score: number): string {
  // [score, hue, saturation, lightness]
  const stops: Array<[number, number, number, number]> = [
    [0,   0,   78, 50],
    [60,  40,  82, 50],
    [80,  75,  72, 48],
    [90,  105, 70, 46],
    [100, 140, 78, 44],
  ];
  const c = Math.max(0, Math.min(100, score));
  for (let i = 1; i < stops.length; i++) {
    if (c <= stops[i][0]) {
      const [s0, h0, sat0, l0] = stops[i - 1];
      const [s1, h1, sat1, l1] = stops[i];
      const t = s1 === s0 ? 0 : (c - s0) / (s1 - s0);
      const h = h0 + t * (h1 - h0);
      const sat = sat0 + t * (sat1 - sat0);
      const l = l0 + t * (l1 - l0);
      return `hsl(${h.toFixed(0)} ${sat.toFixed(0)}% ${l.toFixed(0)}%)`;
    }
  }
  const last = stops[stops.length - 1];
  return `hsl(${last[1]} ${last[2]}% ${last[3]}%)`;
}

// 能力阶梯升序。fable 是 Claude 5 世代新增家族（2026-06-09 GA），定位在 opus 之上，
// 故排在最后；未列入的家族回落到 50，无家族标识回落到 99。
const _MODEL_FAMILY_ORDER: Record<string, number> = { haiku: 0, sonnet: 1, opus: 2, fable: 3 };

export function compareModelKeys(a: RpdiagModelScore, b: RpdiagModelScore): number {
  const ra = _modelFamilyRank(a.model_key || a.model);
  const rb = _modelFamilyRank(b.model_key || b.model);
  if (ra !== rb) return ra - rb;
  return (a.model_key || a.model || '').localeCompare(b.model_key || b.model || '');
}

function _modelFamilyRank(name: string | undefined): number {
  if (!name) return 99;
  const lower = name.toLowerCase();
  for (const [family, rank] of Object.entries(_MODEL_FAMILY_ORDER)) {
    if (lower.includes(family)) return rank;
  }
  return 50;
}
