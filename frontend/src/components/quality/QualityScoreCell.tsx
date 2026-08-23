import { useId } from 'react';
import { HoverTooltip } from '../HoverTooltip';
import { trackEvent } from '../../utils/analytics';
import { buildModelTooltipRow } from './modelTooltipRow';
import {
  UNAVAILABLE_COLOR,
  compareModelKeys,
  qualityScoreColor,
  qualityScoreYNorm,
} from './qualityScale';
import type { RpdiagScore } from '../../types/monitor';

// rpdiag 质量分单元格：所有 model 的 5 点 sparkline (30d 均 / 7d 均 / 最近 3
// 次单 sample 升序) 叠在同一个 SVG 画布上，**不分块**。每条折线颜色由
// 该 model 自己的真实质量 sample 决定（100=绿/60=黄/0=红 平滑渐变），dot
// 标记每个真实数据点；硬失败合成点改用灰色不可用 marker。80 / 100 两条点划
// 参考线作 Y 轴刻度。
// 无可见数字，hover tooltip 出 per-model 明细。
// recent_scores 在 ranking-export.v5.2 起暴露；v5.1 wire 则 fallback 到 3 点
// (30d / 7d / latest) 显示，最新分始终落在最右侧槽位。
//
// 视觉读法：
//   3 条线靠近 → "所有 model 表现接近"（共识）
//   分散      → "各 model 差异大"（需点进详情看哪个掉了）
//   缺点      → 不补 0；缺一个点的 model 只画 dot/短线
//   灰点/灰线 → rpdiag 判该 model 当前硬失败故障态；灰点贴底+中性灰表示「测不了 /
//                无质量数据」，区别于 qualityScoreColor 的红=测到响应但质量真差。
//                无任何历史的纯故障 model 画成 5 个灰点贴底的整条灰线；曾有真实分的
//                则彩色折线在末段渐变落到灰点。tooltip 出 availability_warning
export function QualityScoreCell({ score, compact = false }: { score?: RpdiagScore; compact?: boolean }) {
  // Unique base for the per-series SVG gradient ids. SVG <defs> ids are
  // document-global, so every cell needs its own namespace; `useId` must run
  // before the early return to satisfy the rules of hooks. Strip the colons
  // React emits so the id is safe inside a `url(#…)` reference.
  const gradientBaseId = useId().replace(/[^a-zA-Z0-9_-]/g, '');

  if (!score || !score.models || score.models.length === 0) {
    return <span className="text-muted text-xs">-</span>;
  }

  const ranked = [...score.models].sort(compareModelKeys);
  const tooltipRows = ranked.map(buildModelTooltipRow);

  const W = compact ? 36 : 44;
  const H = compact ? 14 : 36;
  // 5 槽位 sparkline：slot 0/1 是 30d / 7d 窗口均值，slot 2/3/4 是最近
  // 3 次单 sample 升序（旧→新）；缺值的槽位不画点，折线跨空槽直连
  // 反映样本稀少的自然间隔。槽位横向位置用「占列宽百分比」表达（非固定像素），
  // 让 sparkline 随列宽（verbose locale 的表头会把质量列撑宽）自适应铺满，
  // 同时圆点半径/线宽保持绝对像素、不随横向拉伸变形（保「填满列 + 圆点保圆」）。
  const NUM_SLOTS = 5;
  // 1.2px 内边距上下避免点贴边
  const PAD = 1.2;
  // 圆点半径/线粗随 H 等比放大，desktop H=36 时圆点更醒目
  const DOT_R = compact ? 1.4 : 2.4;
  const STROKE_W = compact ? 1.2 : 1.6;
  // Y 轴参考线：80 / 100 两档，让点的高度有"刻度感"。score=80 对应 norm 0.4、
  // score=100 对应 norm 1.0；与折线/圆点共用 qualityScoreYNorm 保证一致。
  const referenceLines = [80, 100].map((markerScore) => ({
    score: markerScore,
    y: H - PAD - qualityScoreYNorm(markerScore) * (H - 2 * PAD),
  }));

  // Y 轴分段非线性：高分段占 SVG 顶部 60% 像素，让 95 vs 100 等小差异有视觉空间。
  //   score 0-60   → SVG 底部 20%
  //   score 60-80  → 中间 20%
  //   score 80-100 → 顶部 60%（实际业务关心的"好通道"区域）
  // 跨 row 仍可比（同分数 → 同高度），但读 sparkline 时要意识到刻度不是匀速的。
  // 用 qualityScoreYNorm 计算。绝对分数由 dot 颜色 + tooltip 数字双重提供。
  type SparkNode = { xPct: number; y: number; color: string };
  type SparkStop = { offset: number; color: string };
  // 一个候选槽位点：slot=横向位置，value=分数（不可用点 value 无意义、取 0 贴底），
  // unavailable=true 表示该次 hard-fail，画中性灰而非 qualityScoreColor 的红。
  type SlotPoint = { slot: number; value: number; unavailable: boolean };

  // 把 (slot, 分数) 映射成一个着色节点：xPct 取槽位中心占列宽的百分比，y 走
  // qualityScoreYNorm 非线性轴（绝对像素），color 由调用方决定（真实分用
  // qualityScoreColor，不可用用灰）。
  const nodeAt = (slot: number, value: number, color: string): SparkNode => {
    const norm = qualityScoreYNorm(Math.max(0, Math.min(100, value)));
    return { xPct: ((slot + 0.5) / NUM_SLOTS) * 100, y: H - PAD - norm * (H - 2 * PAD), color };
  };

  const series = ranked
    .map((m) => {
      const t = m.trend;
      const failed = m.failed === true;
      // no_recent_attempts（近7天无终态评测）且非 hard-fail：整条 series 降饱和展示，
      // 保留真实历史色相「只发暗」，区别于 failed/unavailable 的清零灰。failed 优先，
      // 不降饱和（走既有灰逻辑）。
      const dim = m.no_recent_attempts === true && !failed;

      // 收集"有数据"的槽位点。slot 0/1 永远是 30d / 7d 窗口均值：有打分样本才画、
      // 无则留空——绝不涂灰（均值是分数的平均，没有分就没有均值，涂灰会把"无数据"
      // 和"不可用"混为一谈）。slot 2/3/4 是最近 3 次的结局，逻辑见下。
      const points: SlotPoint[] = [];
      if (typeof t?.avg_30d === 'number') points.push({ slot: 0, value: t.avg_30d, unavailable: false });
      if (typeof t?.avg_7d === 'number') points.push({ slot: 1, value: t.avg_7d, unavailable: false });

      if (Array.isArray(t?.recent_attempts)) {
        // v5.4 wire：slot 2/3/4 = 最近 ≤3 次质量相关 terminal attempt，右对齐升序。
        // number→按分着色；null→该次 hard-fail，画中性灰贴底（与"槽位无数据"区分——
        // 无数据不会进 points，灰点是实打实的一次失败探测）。
        const attempts = t.recent_attempts.slice(-3);
        const startSlot = NUM_SLOTS - attempts.length;
        attempts.forEach((v, i) => {
          const slot = startSlot + i;
          points.push(
            typeof v === 'number'
              ? { slot, value: v, unavailable: false }
              : { slot, value: 0, unavailable: true },
          );
        });
      } else {
        // 旧 wire fallback（pre-v5.4，无 recent_attempts）：沿用 recent_scores
        // （打分-only，右对齐升序）；无则用 latest 单点填最右槽位（v5.1 兼容）。
        const recentScores = Array.isArray(t?.recent_scores) ? t.recent_scores.slice(-3) : [];
        if (recentScores.length > 0) {
          const startSlot = NUM_SLOTS - recentScores.length;
          recentScores.forEach((v, i) => {
            points.push({ slot: startSlot + i, value: v, unavailable: false });
          });
        } else if (typeof t?.latest === 'number') {
          points.push({ slot: NUM_SLOTS - 1, value: t.latest, unavailable: false });
        }
        // client.go 的 normalizeHardFailTrend 在旧 wire 末尾塞了合成 0 表示
        // "当前不可用"，把最右点改判为灰，保持 v5.4 之前的渲染不变。
        if (failed && points.length > 0) {
          const last = points[points.length - 1];
          points[points.length - 1] = { ...last, value: 0, unavailable: true };
        }
      }

      // rpdiag quality_state="unavailable" 非硬失败（v5.10 stale/aged 行）：该 model
      // 当前「不可测」。语义上要画一个中性灰的「代表点/最新槽位」，但**不能覆盖**
      // recent_attempts / 30d·7d 的历史彩点（那些点仍按 qualityScoreColor 正常着色）——
      // 这也避免「只有一个数字历史点被标灰 → every-unavailable 把整条压成灰线」误吞历史。
      // 做法：仅当最右槽位（NUM_SLOTS-1）尚无点时，才追加一个贴底灰代表点；已被真实
      // 近况占用则不动（历史彩点保留，不可测状态由 tooltip「不可测」传达）。纯空历史的
      // unavailable model 也据此得到一个灰点 → 走下方 every-unavailable 整条灰线，绝不
      // 回退成 `-`。
      if (m.unavailable === true) {
        const hasLatestSlot = points.some((p) => p.slot === NUM_SLOTS - 1);
        // no_recent_attempts 展示优先于 unavailable（spec §③ 行109/152-153）：不追加灰
        // 代表点去盖住降饱和的真实历史；仅当该 model 毫无历史点时才兜底一个灰点，避免
        // 整格塌成 '-'。
        const suppressGreyForNoRecent = m.no_recent_attempts === true && points.length > 0;
        if (!hasLatestSlot && !suppressGreyForNoRecent) {
          points.push({ slot: NUM_SLOTS - 1, value: 0, unavailable: true });
        }
      }

      if (points.length === 0) return null;

      let nodes: SparkNode[];
      if (points.every((p) => p.unavailable)) {
        // 纯不可用：没有任何均值/打分锚点，近况全是 hard-fail。沿用 Request A 视觉，
        // 画一条贯穿 5 槽位、贴底的整条灰线，读成清晰的"什么都没测到"，而不是
        // 孤零零几个灰点。
        nodes = Array.from({ length: NUM_SLOTS }, (_, slot) =>
          nodeAt(slot, 0, UNAVAILABLE_COLOR),
        );
      } else {
        // 逐元素着色：真实分走 qualityScoreColor，失败点贴底走中性灰。连接线在彩↔灰
        // 之间渐变，把"刚掉到不可用"或"已恢复"的过渡如实画出来。
        nodes = points.map(({ slot, value, unavailable }) =>
          nodeAt(slot, value, unavailable ? UNAVAILABLE_COLOR : qualityScoreColor(value)),
        );
      }

      // 每个节点一个 gradient stop（offset = 其归一化 x）。相邻 stop 之间正好覆盖
      // 该段，于是线在每个顶点处=该顶点自身色、每段是其两端点的渐变——包含刚掉到
      // 不可用那条 model 的彩→灰末段。节点沿 x 单调递增保证 offset 单调。
      const x0 = nodes[0].xPct;
      const span = nodes.length > 1 ? nodes[nodes.length - 1].xPct - x0 || 1 : 1;
      const stops: SparkStop[] = nodes.map((n) => ({
        offset: (n.xPct - x0) / span,
        color: n.color,
      }));
      return { nodes, stops, dim };
    })
    .filter(
      (s): s is { nodes: SparkNode[]; stops: SparkStop[]; dim: boolean } => s !== null,
    );

  if (series.length === 0) {
    return <span className="text-muted text-xs">-</span>;
  }

  const sparkline = (
    <span className={compact ? 'inline-flex items-center' : 'flex w-full items-center'}>
      <svg
        width={compact ? W : '100%'}
        height={H}
        aria-hidden="true"
        className="flex-shrink-0"
      >
        <defs>
          {series.map((s, i) =>
            s.nodes.length > 1 ? (
              // Gradient axis runs horizontally from the first node's x to the
              // last node's x (both as % of column width); userSpaceOnUse keeps
              // the colour a pure function of x, independent of the line
              // segments' vertical zig-zag. One stop per node (offset = that
              // node's normalized x) makes each adjacent pair of stops
              // interpolate over exactly its segment — so every per-segment
              // <line> hits its endpoints' own colours (score colour, or grey
              // for an unavailable endpoint), matching the dots.
              <linearGradient
                key={i}
                id={`${gradientBaseId}-${i}`}
                gradientUnits="userSpaceOnUse"
                x1={`${s.nodes[0].xPct}%`}
                y1="0"
                x2={`${s.nodes[s.nodes.length - 1].xPct}%`}
                y2="0"
              >
                {s.stops.map((st, k) => (
                  <stop key={k} offset={st.offset} stopColor={st.color} />
                ))}
              </linearGradient>
            ) : null,
          )}
        </defs>
        {referenceLines.map((line) => (
          <line
            key={line.score}
            x1="0"
            y1={line.y}
            x2="100%"
            y2={line.y}
            stroke="hsl(0 0% 75% / 0.55)"
            strokeWidth="1"
            strokeDasharray="2 2"
          />
        ))}
        {series.map((s, i) => (
          <g
            key={i}
            // no_recent_attempts series 降饱和：保留真实历史色相只发暗。saturate/opacity
            // 经 playwright 在真实暗底 44×36 尺寸目测微调（此处是唯一调参点）——更低值
            // 会让小尺寸色相糊成近灰、读不出趋势。
            style={s.dim ? { filter: 'saturate(0.55)', opacity: 0.6 } : undefined}
          >
            {/* 折线拆成逐段 <line>（x 用百分比；polyline 的 points 不支持百分比单位）。
                每段共用本 series 的 userSpaceOnUse 渐变、按绝对 x 采样 → 颜色与单条
                polyline 时完全一致；round linecap + 顶点处的圆点盖住接缝。 */}
            {s.nodes.length > 1 &&
              s.nodes.slice(0, -1).map((n, j) => (
                <line
                  key={`seg-${j}`}
                  x1={`${n.xPct}%`}
                  y1={n.y.toFixed(1)}
                  x2={`${s.nodes[j + 1].xPct}%`}
                  y2={s.nodes[j + 1].y.toFixed(1)}
                  stroke={`url(#${gradientBaseId}-${i})`}
                  strokeWidth={STROKE_W}
                  strokeLinecap="round"
                  opacity="0.85"
                />
              ))}
            {s.nodes.map((n, j) => (
              <circle key={j} cx={`${n.xPct}%`} cy={n.y} r={DOT_R} fill={n.color} />
            ))}
          </g>
        ))}
      </svg>
    </span>
  );

  // per-model 明细浮层：用共享 HoverTooltip（与通道列同款样式），替代原生 title，
  // 让质量列 tip 与表内其它 tip 视觉一致。每个 model 一块：标识 + 30d/7d/近3次，
  // 硬失败 model 追加一行高亮可用性提示。
  const cell = (
    <HoverTooltip
      triggerClassName={compact ? '' : 'w-full'}
      widthClass="w-auto max-w-[22rem]"
      content={
        <span className="flex flex-col gap-1.5">
          {tooltipRows.map((row, i) => (
            <span key={i} className="flex flex-col">
              <span className="text-primary text-[11px] font-medium">{row.key}</span>
              <span className="text-muted text-[10px]">{row.detail}</span>
              {row.warning && (
                <span className="text-warning text-[10px]">⚠ {row.warning}</span>
              )}
            </span>
          ))}
        </span>
      }
    >
      {sparkline}
    </HoverTooltip>
  );

  if (!score.channel_url) return cell;

  // 裸 <a>：保留新窗 + noopener；不复用 ExternalLink 因为它强制带 ↗ 图标，
  // 在密集表格里这点宝贵宽度还是留给 sparkline。
  return (
    <a
      href={score.channel_url}
      target="_blank"
      rel="noopener noreferrer"
      className={
        compact
          ? 'inline-flex items-center hover:opacity-80 active:opacity-60'
          : 'flex w-full items-center hover:opacity-80 active:opacity-60'
      }
      onClick={() => trackEvent('click_external_link', { link_text: 'rpdiag quality score', link_url: score.channel_url, outbound: true })}
    >
      {cell}
    </a>
  );
}
