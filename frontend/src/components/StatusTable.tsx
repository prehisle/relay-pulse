import { useState, useEffect, useMemo, useId, memo } from 'react';
import { List, type RowComponentProps } from 'react-window';
import { ArrowUpDown, ArrowUp, ArrowDown, Zap, Shield, Filter } from 'lucide-react';
import { useTranslation, Trans } from 'react-i18next';
import { StatusDot } from './StatusDot';
import { HeatmapBlock } from './HeatmapBlock';
import { LayeredHeatmapBlock } from './LayeredHeatmapBlock';
import { ChannelTypeIcon, parseChannelType } from './ChannelTypeIcon';
import { ExternalLink } from './ExternalLink';
import { HeaderInfoPopover } from './HeaderInfoPopover';
import { HoverTooltip } from './HoverTooltip';
import { AnnotationCell } from './annotations';
import { FavoriteButton } from './FavoriteButton';
import { getTimeRanges } from '../constants';
import { availabilityToColor, latencyToColor, sponsorLevelToBorderClass, sponsorLevelToCardBorderColor, sponsorLevelToPinnedBgClass } from '../utils/color';
import { trackEvent } from '../utils/analytics';
import { aggregateHeatmap } from '../utils/heatmapAggregator';
import { createMediaQueryEffect } from '../utils/mediaQuery';
import { shortenModelName } from '../utils/modelName';
import { hasAnyAnnotation, hasAnyAnnotationInList } from '../utils/annotationUtils';
import { formatPriceRatioStructured } from '../utils/format';
import { getServiceIconComponent } from './ServiceIcon';
import { VendorBadge } from './VendorBadge';
import { lookupRpdiagScore } from '../hooks/useRpdiagScores';
import type { ProcessedMonitorData, SortConfig } from '../types';
import type { RpdiagModelScore, RpdiagScore, RpdiagScoresResponse } from '../types/monitor';

type HistoryPoint = ProcessedMonitorData['history'][number];

// 虚拟滚动常量
const MOBILE_ROW_HEIGHT = 160;  // 移动端卡片高度（约 150px 内容 + 10px 间距）
const MOBILE_MAX_HEIGHT = 800;  // 移动端列表最大高度

// ServiceIcon 模块级缓存，避免重复调用 getServiceIconComponent
const serviceIconCache = new Map<string, ReturnType<typeof getServiceIconComponent>>();
const getCachedServiceIcon = (serviceType: string) => {
  if (!serviceIconCache.has(serviceType)) {
    serviceIconCache.set(serviceType, getServiceIconComponent(serviceType));
  }
  return serviceIconCache.get(serviceType);
};

// 通道单元格组件（带自定义 CSS tooltip，替代原生 title 属性）
interface ChannelCellProps {
  channel?: string;
  probeUrl?: string;
  templateName?: string;
  coldReason?: string;
  boardReason?: string;
  boardReasonModels?: string;
  className?: string;
}

function ChannelCell({ channel, probeUrl, templateName, coldReason, boardReason, boardReasonModels, className = '' }: ChannelCellProps) {
  const { t } = useTranslation();
  const channelType = parseChannelType(channel);
  const isQualityHardFail = boardReason === 'quality_hardfail';
  const hasTooltip = !!(channelType || probeUrl || templateName || coldReason || isQualityHardFail);

  const channelContent = (
    <>
      <ChannelTypeIcon channel={channel} />
      <span className="min-w-0 truncate">{channel || '-'}</span>
    </>
  );

  if (!hasTooltip) {
    return <span className={`inline-flex items-center gap-1 ${className}`}>{channelContent}</span>;
  }

  return (
    <HoverTooltip
      triggerClassName={`gap-1 cursor-help ${className}`}
      content={
        <span className="flex flex-col gap-1">
          {channelType && (
            <span className="flex flex-col">
              <span className="text-muted text-[10px]">{t('table.channelTooltip.channelType')}</span>
              <span className="text-primary text-[11px]">
                {t(`table.channelType.${channelType}`)} — {t(`table.channelType.${channelType}Desc`)}
              </span>
            </span>
          )}
          {probeUrl && (
            <span className="flex flex-col">
              <span className="text-muted text-[10px]">{t('table.channelTooltip.probeUrl')}</span>
              <span className="text-primary font-mono text-[11px] break-all">{probeUrl}</span>
            </span>
          )}
          {templateName && (
            <span className="flex flex-col">
              <span className="text-muted text-[10px]">{t('table.channelTooltip.template')}</span>
              <span className="text-primary font-mono text-[11px] break-all">{templateName}</span>
            </span>
          )}
          {coldReason && (
            <span className="flex flex-col">
              <span className="text-muted text-[10px]">{t('table.channelTooltip.coldReason', '冷板原因')}</span>
              <span className="text-warning text-[11px] break-all">{coldReason}</span>
            </span>
          )}
          {isQualityHardFail && (
            <span className="flex flex-col">
              <span className="text-muted text-[10px]">{t('table.channelTooltip.qualityHardFail.label', '质量移板')}</span>
              <span className="text-warning text-[11px] break-all">
                {boardReasonModels
                  ? t('table.channelTooltip.qualityHardFail.text', '{{models}} 近3次评测均未取得可评分响应，已暂移备用板', { models: boardReasonModels })
                  : t('table.channelTooltip.qualityHardFail.textNoModels', '近3次评测均未取得可评分响应，已暂移备用板')}
              </span>
            </span>
          )}
        </span>
      }
    >
      {channelContent}
    </HoverTooltip>
  );
}

// ─── 模型列辅助函数 ───────────────────────────────────────────

function getModelDisplayList(modelEntries?: ProcessedMonitorData['modelEntries']): string[] {
  if (!modelEntries || modelEntries.length === 0) return [];
  return modelEntries
    .map((entry) => shortenModelName(entry.requestModel) || entry.model || '-')
    .filter(Boolean);
}

function getModelTooltip(modelEntries?: ProcessedMonitorData['modelEntries']): string | undefined {
  if (!modelEntries || modelEntries.length === 0) return undefined;
  return modelEntries
    .map((entry) => entry.requestModel || entry.model || '-')
    .join('\n');
}

interface StatusTableProps {
  data: ProcessedMonitorData[];
  sortConfig: SortConfig;
  isInitialSort?: boolean;   // 是否为初始排序状态（控制高亮显示）
  timeRange: string;
  slowLatencyMs: number;
  enableAnnotations?: boolean;      // 注解系统总开关，默认 true
  showCategoryTag?: boolean; // 是否显示分类标签（推荐/公益），默认 true
  showProvider?: boolean;    // 是否显示服务商名称，默认 true
  showSponsor?: boolean;     // 是否显示赞助者信息，默认 true
  isFavorite: (id: string) => boolean;  // 检查是否已收藏
  onToggleFavorite: (id: string) => void; // 切换收藏状态
  onSort: (key: string) => void;
  onBlockHover: (e: React.MouseEvent<HTMLDivElement>, point: HistoryPoint) => void;
  onBlockLeave: () => void;
  onFilterProvider?: (providerId: string) => void; // 按服务商筛选
  /** rpdiag 质量分索引（按 "provider|service|channel" 键）。空对象表示功能未启用或上游不可达。 */
  rpdiagScores?: RpdiagScoresResponse;
  /** rpdiag 质量分是否已加载完成。false 时质量列排序按钮置灰（避免空数据触发的伪排序）。 */
  rpdiagScoresLoaded?: boolean;
  /** rpdiag 质量功能总开关（meta.rpdiag_enabled 派生）。false 时整列消失（私有部署未接 rpdiag）。默认 true（fail-open）。 */
  rpdiagEnabled?: boolean;
  /** runtime 价格列隐藏开关（meta.hide_price_column 派生）。默认 false（显示）。 */
  hidePriceColumn?: boolean;
  /** 是否显示「模型厂商」列。由调用方基于**未筛选**的全量数据判定（见 App/ProviderPage），
   *  刻意不在本组件内按已筛数据算——否则筛选一下列就会凭空出现/消失，表格布局抖动。
   *  厂商全站未回填（Phase 3 之前）时恒 false，对用户完全无感。 */
  showVendorColumn?: boolean;
}

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

// 分数 → SVG 高度归一化值（0=底部，1=顶部）。Piecewise 把高分段 80-100
// 拉伸到 60% 像素带，让 95 vs 100 等小差异在视觉上看得见。
// 业务事实：rpdiag 80% 的通道分数集中在 80-100，原本线性映射 95-100 只占
// 顶 5% 像素，sparkline 全部贴顶看不出形状。
function qualityScoreYNorm(score: number): number {
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
function qualityScoreColor(score: number): string {
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

function compareModelKeys(a: RpdiagModelScore, b: RpdiagModelScore): number {
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

interface ModelTooltipRow {
  /** 模型标识（展示名/model_key）。 */
  key: string;
  /** 三组指标拼成一行：30d 均 / 7d 均 / 近 3 次。 */
  detail: string;
  /** 硬失败可用性提示（存在时单独一行高亮）。 */
  warning?: string;
}

export function buildModelTooltipRow(m: RpdiagModelScore): ModelTooltipRow {
  const fmt = (v: number | null | undefined) => (typeof v === 'number' ? v.toFixed(1) : '—');
  const key = m.model_key || m.model || '?';
  const t = m.trend;
  // failed=硬失败清零灰；unavailable=v5.10 stale/aged「不可测」灰。二者的近况都读作
  // 「不可测」（沿用旧 wire 的整行降级逻辑，让代表分那一格显示不可测而非旧分数）。
  // 但 no_recent_attempts（近 7 天无终态评测、非 hard-fail）是展示主态，优先级高于
  // unavailable（spec §② Failed > NoRecentAttempts > Unavailable）：此时**保留真实历史
  // recent_scores**、不把近况槽位抹成「不可测」，好让主注「…以下为历史」名副其实。
  // failed 与 no_recent 在数据层互斥（hard-fail 必有近 7 天尝试），故 failed 行仍照抹灰。
  const unusable = isModelQualityUnusable(m) && m.no_recent_attempts !== true;
  // 近 3 次：与 sparkline 的 slot 2/3/4 同源，让 tooltip 把 5 个槽位读全
  // （30d / 7d / 近 3 次）。优先用 v5.4 的 recent_attempts（逐次 terminal attempt
  // 结局，null=hard-fail→"不可测"）；旧 wire 回退到 recent_scores + 整行 failed。
  // 30d / 7d 仍是真实历史均值。
  let recentStr: string;
  if (Array.isArray(t?.recent_attempts)) {
    const attempts = t.recent_attempts.slice(-3);
    // 空 recent_attempts（近 7 天无探测）但整行 unusable → 读「不可测」而非「—」，
    // 与 sparkline 的纯灰线一致（v5.10 stale/aged unavailable 行走此路）。
    recentStr = attempts.length > 0
      ? attempts.map((v) => (typeof v === 'number' ? fmt(v) : '不可测')).join(', ')
      : (unusable ? '不可测' : '—');
  } else {
    const recent = Array.isArray(t?.recent_scores) ? t.recent_scores.slice(-3) : [];
    if (recent.length > 0) {
      recentStr = recent
        .map((v, i) => (unusable && i === recent.length - 1 ? '不可测' : fmt(v)))
        .join(', ');
    } else {
      // ranking-export.v5.1 wire 没有 recent_scores 时回退到单个 latest。
      recentStr = unusable ? '不可测' : fmt(t?.latest);
    }
  }
  // unavailable（v5.10 stale/aged）但近况仍有真实分时：不覆盖真实历史分（会误导），
  // 而是补一个「当前不可测」后缀，确保"当前不可测"这一状态在 tooltip 里不会消失
  // （sparkline 侧若最右槽位被真实点占用同样不另画灰点，此后缀是唯一的当前态信号）。
  const detail = `30d=${fmt(t?.avg_30d)}  7d=${fmt(t?.avg_7d)}  近3次=${recentStr}`;
  // no_recent_attempts 主态下不叠加「当前不可测」次级后缀：主注「以下为历史」已表明所示
  // 分数非当前，再缀「当前不可测」既冗余又会打乱 no_recent 领先的语序（spec §②）。
  const detailWithState =
    m.unavailable === true && m.no_recent_attempts !== true && !recentStr.includes('不可测')
      ? `${detail}（当前不可测）`
      : detail;
  // no_recent_attempts（rpdiag attempts_7d==0 且非 hard-fail）：注「近7天无评测记录」但
  // KEEP 历史分可见——区别于 failed/unavailable 的「不可测」，只是提示历史已陈旧。
  const detailFinal =
    m.no_recent_attempts === true
      ? `${detailWithState}（近7天无评测记录·以下为历史）`
      : detailWithState;
  return {
    key,
    detail: detailFinal,
    warning: m.availability_warning || undefined,
  };
}

// react-window v2 虚拟列表行组件（rowComponent 接口）
interface MobileRowProps {
  data: ProcessedMonitorData[];
  slowLatencyMs: number;
  enableAnnotations: boolean;
  showProvider: boolean;
  showSponsor: boolean;
  useLatencyGradient: boolean;
  isFavorite: (id: string) => boolean;
  onToggleFavorite: (id: string) => void;
  onBlockHover: (e: React.MouseEvent<HTMLDivElement>, point: HistoryPoint) => void;
  onBlockLeave: () => void;
  rpdiagScores?: RpdiagScoresResponse;
  rpdiagEnabled: boolean;
  showVendorColumn: boolean;
}

function MobileRow({ index, style, data, slowLatencyMs, enableAnnotations, showProvider, showSponsor, useLatencyGradient, isFavorite, onToggleFavorite, onBlockHover, onBlockLeave, rpdiagScores, rpdiagEnabled, showVendorColumn }: RowComponentProps<MobileRowProps>) {
  const item = data[index];
  return (
    <div style={style}>
      <div style={{ marginBottom: 8 }}>
        <MobileListItem
          item={item}
          slowLatencyMs={slowLatencyMs}
          enableAnnotations={enableAnnotations}
          showProvider={showProvider}
          showSponsor={showSponsor}
          useLatencyGradient={useLatencyGradient}
          isFavorite={isFavorite(item.id)}
          onToggleFavorite={() => onToggleFavorite(item.id)}
          onBlockHover={onBlockHover}
          onBlockLeave={onBlockLeave}
          rpdiagScore={rpdiagEnabled ? lookupRpdiagScore(rpdiagScores, [item.providerName, item.providerId], item.serviceType, item.channelName || item.channel, item.channelId) : undefined}
          rpdiagEnabled={rpdiagEnabled}
          showVendorColumn={showVendorColumn}
        />
      </div>
    </div>
  );
}

// 移动端卡片列表项组件
function MobileListItem({
  item,
  slowLatencyMs,
  enableAnnotations = true,
  showProvider = true,
  showSponsor = true,
  useLatencyGradient = false,
  isFavorite,
  onToggleFavorite,
  onBlockHover,
  onBlockLeave,
  rpdiagScore,
  rpdiagEnabled = false,
  showVendorColumn = false,
}: {
  item: ProcessedMonitorData;
  slowLatencyMs: number;
  enableAnnotations?: boolean;
  showProvider?: boolean;
  showSponsor?: boolean;
  useLatencyGradient?: boolean;
  isFavorite: boolean;
  onToggleFavorite: () => void;
  onBlockHover: (e: React.MouseEvent<HTMLDivElement>, point: HistoryPoint) => void;
  onBlockLeave: () => void;
  rpdiagScore?: RpdiagScore;
  rpdiagEnabled?: boolean;
  showVendorColumn?: boolean;
}) {
  const { t, i18n } = useTranslation();
  const ServiceIcon = getCachedServiceIcon(item.serviceType);

  // 聚合热力图数据
  const aggregatedHistory = useMemo(
    () => aggregateHeatmap(item.history, 30),
    [item.history]
  );

  // 检查是否有注解需要显示
  const hasItemAnnotations = hasAnyAnnotation(item, { enableAnnotations });

  // 卡片左边框颜色（仅基于赞助级别，置顶改用背景色）
  const borderColor = sponsorLevelToCardBorderColor(item.sponsorLevel);

  // 是否显示左边框（仅基于赞助级别）
  const hasLeftBorder = !!item.sponsorLevel;

  // 置顶项使用对应注解颜色的极淡背景色
  const pinnedBgClass = item.pinned ? sponsorLevelToPinnedBgClass(item.sponsorLevel) : '';
  const baseBgClass = pinnedBgClass || 'bg-surface/60';

  // 卡片最小高度 = 行高(160) - 行间距(8) = 152px
  // 确保所有卡片高度一致，避免虚拟列表中间距不均
  const cardMinHeight = 152;

  return (
    <div
      className={`${baseBgClass} border border-default rounded-r-xl ${hasLeftBorder ? 'rounded-l-sm border-l-2' : 'rounded-l-xl'} p-3 space-y-2`}
      style={{
        ...(borderColor ? { borderLeftColor: borderColor } : {}),
        minHeight: cardMinHeight,
      }}
    >
      {/* 注解行 - 仅在有注解时显示 */}
      {hasItemAnnotations && (
        <AnnotationCell annotations={item.annotations} />
      )}

      {/* 主要信息行 */}
      <div className="flex items-start justify-between gap-2">
        <div className="flex items-center gap-2 min-w-0 flex-1">
          {/* 服务图标 */}
          <div className="w-8 h-8 flex-shrink-0 rounded-lg bg-elevated flex items-center justify-center border border-default text-primary">
            {ServiceIcon ? (
              <ServiceIcon className="w-4 h-4" />
            ) : item.serviceType === 'cc' ? (
              <Zap className="text-service-cc" size={14} />
            ) : (
              <Shield className="text-service-cx" size={14} />
            )}
          </div>

          {/* 服务商名称 + 收藏按钮 */}
          <div className="min-w-0 flex-1">
            {showProvider && (
              <div className="flex items-center gap-1.5">
                <span className="font-semibold text-primary truncate text-sm leading-tight">
                  <ExternalLink href={item.providerUrl} compact requireConfirm>{item.providerName}</ExternalLink>
                </span>
                <FavoriteButton
                  isFavorite={isFavorite}
                  onToggle={onToggleFavorite}
                  size={12}
                  inline
                />
              </div>
            )}
            <div className="flex items-center gap-2 mt-0.5 text-xs text-secondary">
              {/* 赞助者（放在服务类型前） */}
              {showSponsor && item.sponsor && (
                <span className="text-[10px] text-muted truncate max-w-[80px]">
                  <ExternalLink href={item.sponsorUrl} compact>{item.sponsor}</ExternalLink>
                </span>
              )}
              <span
                className={`px-1.5 py-0.5 rounded text-[10px] font-mono border flex-shrink-0 ${
                  item.serviceType === 'cc'
                    ? 'border-service-cc text-service-cc bg-service-cc'
                    : item.serviceType === 'gm'
                    ? 'border-service-gm text-service-gm bg-service-gm'
                    : 'border-service-cx text-service-cx bg-service-cx'
                }`}
              >
                {item.serviceName.toUpperCase()}
              </span>
              {item.channel && (
                <ChannelCell
                  channel={item.channelName || item.channel}
                  probeUrl={item.probeUrl}
                  templateName={item.templateName}
                  coldReason={item.coldReason}
                  boardReason={item.boardReason}
                  boardReasonModels={item.boardReasonModels}
                  className="text-muted truncate"
                />
              )}
              {item.modelEntries && item.modelEntries.length > 0 && (() => {
                const models = getModelDisplayList(item.modelEntries);
                if (models.length === 0) return null;
                return (
                  <span
                    className="text-[10px] text-muted truncate max-w-[120px]"
                    title={getModelTooltip(item.modelEntries)}
                  >
                    {models.length === 1 ? models[0] : `${models[0]} +${models.length - 1}`}
                  </span>
                );
              })()}
              {/* 厂商：紧跟模型名。移动端只出图标——这一行还挤着通道名/模型名/收录天数，
                  加一段厂商名会把前两者一起挤成省略号（实测），全名走 title。 */}
              {showVendorColumn && (
                <VendorBadge vendor={item.modelVendor} compact iconOnly className="text-[10px] text-muted max-w-[70px]" />
              )}
              {/* 收录时间 */}
              {item.listedDays != null && (
                <span className="text-[10px] text-muted font-mono flex-shrink-0">
                  {item.listedDays}d
                </span>
              )}
            </div>
          </div>
        </div>

        {/* 状态、可用率、时间和延迟 */}
        <div className="flex flex-col items-end gap-1 flex-shrink-0">
          <div className="flex items-center p-1.5 rounded-full bg-elevated border border-default">
            <StatusDot status={item.currentStatus} size="sm" />
          </div>
          <span
            className="text-sm font-mono font-bold"
            style={{ color: availabilityToColor(item.uptime) }}
          >
            {item.uptime >= 0 ? `${item.uptime}%` : '--'}
          </span>
          {rpdiagScore && rpdiagScore.models && rpdiagScore.models.length > 0 ? (
            <QualityScoreCell score={rpdiagScore} compact />
          ) : shouldShowQualityPending({ rpdiagEnabled, hasScore: !!rpdiagScore, serviceType: item.serviceType, board: item.board }) ? (
            // 移动端：有分/待测两态显示；无分且非待测维持原「不渲染」以最小化视觉变化。
            <span className="text-muted text-xs" title={t('table.qualityPendingTooltip')}>
              {t('table.qualityPending')}
            </span>
          ) : null}
          {/* 时间和延迟（总是显示） */}
          <div className="flex items-center gap-2 text-[10px] text-muted font-mono">
            {item.lastCheckTimestamp && (
              <span>
                {new Date(item.lastCheckTimestamp * 1000).toLocaleString(i18n.language, {
                  hour: '2-digit',
                  minute: '2-digit',
                })}
              </span>
            )}
            {item.lastCheckLatency !== undefined && (
              <span style={{ color: item.currentStatus === 'UNAVAILABLE' ? 'hsl(var(--text-muted))' : latencyToColor(item.lastCheckLatency, item.slowLatencyMs ?? slowLatencyMs) }}>
                {item.lastCheckLatency}ms
              </span>
            )}
          </div>
        </div>
      </div>

      {/* 热力图 */}
      <div className="flex items-center gap-[2px] h-5 w-full overflow-hidden rounded-sm">
        {aggregatedHistory.map((point, idx) => (
          <HeatmapBlock
            key={idx}
            point={point}
            width={`${100 / aggregatedHistory.length}%`}
            height="h-full"
            onHover={onBlockHover}
            onLeave={onBlockLeave}
            isMobile
            useLatencyGradient={useLatencyGradient}
          />
        ))}
      </div>
    </div>
  );
}

// 移动端排序菜单
function MobileSortMenu({
  sortConfig,
  isInitialSort,
  onSort,
  hidePriceColumn,
  rpdiagScoresLoaded,
  rpdiagEnabled,
  showVendorColumn,
}: {
  sortConfig: SortConfig;
  isInitialSort?: boolean;
  onSort: (key: string) => void;
  hidePriceColumn: boolean;
  rpdiagScoresLoaded: boolean;
  rpdiagEnabled: boolean;
  showVendorColumn: boolean;
}) {
  const { t } = useTranslation();

  const sortOptions: Array<{ key: string; label: string; disabled?: boolean }> = [
    { key: 'providerName', label: t('table.sorting.provider') },
    { key: 'uptime', label: t('table.sorting.uptime') },
    { key: 'lastCheck', label: t('table.sorting.lastCheck') },
    { key: 'serviceType', label: t('table.sorting.service') },
    // 厂商未回填时移动端不提供"按厂商排序"（与桌面端隐藏厂商列一致）
    ...(showVendorColumn ? [{ key: 'modelVendor', label: t('table.sorting.modelVendor') }] : []),
    ...(hidePriceColumn ? [] : [{ key: 'priceRatio', label: t('table.sorting.priceRatio') }]),
    { key: 'listedDays', label: t('table.sorting.listedDays') },
    // rpdiag 关闭时移动端不提供"按质量排序"（与桌面端隐藏质量列一致）
    ...(rpdiagEnabled ? [{ key: 'qualityScore', label: t('table.sorting.quality'), disabled: !rpdiagScoresLoaded }] : []),
  ];

  return (
    <div className="flex items-center gap-2 mb-2 overflow-x-auto pb-2">
      <span className="text-xs text-muted flex-shrink-0">{t('controls.sortBy')}</span>
      {sortOptions.map((option) => {
        // rpdiag 未加载完成时质量按钮置灰、不响应点击；其他按钮无变化
        const isDisabled = option.disabled === true;
        // 初始状态下不高亮任何排序按钮
        const isActive = !isDisabled && !isInitialSort && sortConfig.key === option.key;
        return (
          <button
            key={option.key}
            onClick={() => !isDisabled && onSort(option.key)}
            disabled={isDisabled}
            className={`flex items-center gap-1 px-2.5 py-1.5 rounded-lg text-xs font-medium transition-colors flex-shrink-0 focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none ${
              isActive
                ? 'bg-accent/20 text-accent border border-accent/30'
                : isDisabled
                ? 'bg-elevated text-muted border border-default opacity-60 cursor-not-allowed'
                : 'bg-elevated text-secondary border border-default hover:text-primary'
            }`}
          >
            {option.label}
            {isActive && (
              sortConfig.direction === 'asc' ? (
                <ArrowUp size={12} />
              ) : (
                <ArrowDown size={12} />
              )
            )}
          </button>
        );
      })}
    </div>
  );
}

function StatusTableComponent({
  data,
  sortConfig,
  isInitialSort = false,
  timeRange,
  slowLatencyMs,
  enableAnnotations = true,
  showProvider = true,
  showSponsor = true,
  isFavorite,
  onToggleFavorite,
  onSort,
  onBlockHover,
  onBlockLeave,
  onFilterProvider,
  rpdiagScores,
  rpdiagScoresLoaded = false,
  rpdiagEnabled = true,
  hidePriceColumn = false,
  showVendorColumn = false,
}: StatusTableProps) {
  const { t, i18n } = useTranslation();
  const [isMobile, setIsMobile] = useState(false);

  // 检测是否为平板/移动端（tablet 断点 = max-width:1023px，见 utils/mediaQuery.ts BREAKPOINTS；兼容 Safari ≤13）
  useEffect(() => {
    const cleanup = createMediaQueryEffect('tablet', setIsMobile);
    return cleanup;
  }, []);

  // 排序图标：初始状态下不显示高亮
  const SortIcon = ({ columnKey }: { columnKey: string }) => {
    // 初始状态下所有排序图标都不高亮
    if (isInitialSort || sortConfig.key !== columnKey) {
      return <ArrowUpDown size={14} className="opacity-30 ml-1" />;
    }
    return sortConfig.direction === 'asc' ? (
      <ArrowUp size={14} className="text-accent ml-1" />
    ) : (
      <ArrowDown size={14} className="text-accent ml-1" />
    );
  };

  const currentTimeRange = getTimeRanges(t).find((r) => r.id === timeRange);
  const useLatencyGradient = timeRange === '90m';
  // 质量列总开关：rpdiag 未启用（私有部署）时整列（表头 + 格子 + colgroup + 移动端排序项）消失
  const showQualityColumn = rpdiagEnabled;

  // 移动端：虚拟滚动卡片列表视图
  if (isMobile) {
    // 计算虚拟列表高度（最大 MOBILE_MAX_HEIGHT，最小为所有项目高度）
    const mobileListHeight = Math.min(
      data.length * MOBILE_ROW_HEIGHT,
      MOBILE_MAX_HEIGHT
    );

    return (
      <div>
        <MobileSortMenu
          sortConfig={sortConfig}
          isInitialSort={isInitialSort}
          onSort={onSort}
          hidePriceColumn={hidePriceColumn}
          rpdiagScoresLoaded={rpdiagScoresLoaded}
          rpdiagEnabled={showQualityColumn}
          showVendorColumn={showVendorColumn}
        />
        <List
          style={{ height: mobileListHeight, width: '100%' }}
          rowCount={data.length}
          rowHeight={MOBILE_ROW_HEIGHT}
          overscanCount={3}
          rowComponent={MobileRow}
          rowProps={{ data, slowLatencyMs, enableAnnotations, showProvider, showSponsor, useLatencyGradient, isFavorite, onToggleFavorite, onBlockHover, onBlockLeave, rpdiagScores, rpdiagEnabled: showQualityColumn, showVendorColumn }}
        />
      </div>
    );
  }

  // 检查是否有任何注解需要显示
  const hasAnnotations = hasAnyAnnotationInList(data, { enableAnnotations });

  // 桌面端：表格视图
  return (
    <div className="overflow-x-auto rounded-2xl border border-default/50 shadow-xl bg-surface/40 backdrop-blur-sm">
      <table className="w-full text-left border-collapse bg-transparent">
        <colgroup>
          {hasAnnotations && <col className="w-px" />}
          {showProvider && <col className="w-px" />}
          <col className="w-px" /> {/* service */}
          <col className="w-px" /> {/* channel */}
          <col className="w-px" /> {/* model */}
          {showVendorColumn && <col className="w-px" />} {/* modelVendor */}
          {!hidePriceColumn && <col className="w-px" />} {/* priceRatio */}
          <col className="w-px" /> {/* listedDays */}
          <col className="w-px" /> {/* uptime */}
          <col className="w-px" /> {/* lastCheck */}
          {showQualityColumn && <col className="w-px" />} {/* quality */}
          <col className="w-full" /> {/* trend */}
        </colgroup>
        <thead>
          <tr className="border-b border-default/50 text-secondary text-[11px] uppercase">
            {/* 注解列 - 仅在有注解时显示 */}
            {hasAnnotations && (
              <th className="px-1 py-3 font-medium whitespace-nowrap">
                {t('table.headers.annotation')}
              </th>
            )}
            {/* 服务商列（合并赞助者） */}
            {showProvider && (
              <th
                className="px-1.5 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
                onClick={() => onSort('providerName')}
                onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('providerName'))}
                tabIndex={0}
                role="button"
              >
                <div className="flex items-center">
                  {t('table.headers.provider')} <SortIcon columnKey="providerName" />
                </div>
              </th>
            )}
            <th
              className="px-1.5 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
              onClick={() => onSort('serviceType')}
              onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('serviceType'))}
              tabIndex={0}
              role="button"
            >
              <div className="flex items-center">
                {t('table.headers.service')}
                {/* 服务列的语义是**接入协议族**（用哪套 API / 哪个客户端），不是「模型是谁家的」。
                    第一方厂商开放兼容端点后二者解耦（用 Claude 协议跑智谱模型），故在此点明，
                    并把「谁家的模型」指向厂商列。ⓘ 与价格/质量列同款组件。
                    与厂商列同生共死：厂商列不在时这段文案会指向一个看不见的列，反而更费解。 */}
                {showVendorColumn && (
                  <HeaderInfoPopover className="ml-1" align="center" widthClass="w-64">
                    {t('table.headers.serviceTooltip')}
                  </HeaderInfoPopover>
                )}
                <SortIcon columnKey="serviceType" />
              </div>
            </th>
            <th
              className="px-1.5 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
              onClick={() => onSort('channel')}
              onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('channel'))}
              tabIndex={0}
              role="button"
            >
              <div className="flex items-center">
                {t('table.headers.channel')} <SortIcon columnKey="channel" />
              </div>
            </th>
            <th className="px-1.5 py-3 font-medium whitespace-nowrap">
              {t('table.headers.model')}
            </th>
            {/* 模型厂商列：紧邻模型列——两者一起回答「跑的是谁家的什么模型」，
                与左边「服务=接入协议族」正交（服务列表头 ⓘ 解释了这层关系）。 */}
            {showVendorColumn && (
              <th
                className="px-1.5 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
                onClick={() => onSort('modelVendor')}
                onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('modelVendor'))}
                tabIndex={0}
                role="button"
              >
                <div className="flex items-center">
                  {t('table.headers.modelVendor')} <SortIcon columnKey="modelVendor" />
                </div>
              </th>
            )}
            {!hidePriceColumn && (
              <th
                className="px-1.5 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
                onClick={() => onSort('priceRatio')}
                onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('priceRatio'))}
                tabIndex={0}
                role="button"
              >
                <div className="flex items-center">
                  <div className="flex flex-col leading-tight">
                    <span>{t('table.headers.priceRatioLine1')}</span>
                    <span className="text-[10px] opacity-50 font-normal">{t('table.headers.priceRatioLine2')}</span>
                  </div>
                  <HeaderInfoPopover className="ml-1" align="center" widthClass="w-48">
                    {t('table.headers.priceRatioTooltip')}
                  </HeaderInfoPopover>
                  <SortIcon columnKey="priceRatio" />
                </div>
              </th>
            )}
            <th
              className="px-1.5 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
              onClick={() => onSort('listedDays')}
              onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('listedDays'))}
              tabIndex={0}
              role="button"
            >
              <div className="flex items-center">
                <div className="flex flex-col leading-tight">
                  <span>{t('table.headers.listedDaysLine1')}</span>
                  <span className="text-[10px] opacity-50 font-normal">{t('table.headers.listedDaysLine2')}</span>
                </div>
                <SortIcon columnKey="listedDays" />
              </div>
            </th>
            <th
              className="px-1.5 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
              onClick={() => onSort('uptime')}
              onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('uptime'))}
              tabIndex={0}
              role="button"
            >
              <div className="flex items-center">
                {t('table.headers.uptime')} <SortIcon columnKey="uptime" />
              </div>
            </th>
            <th
              className="px-1.5 py-3 font-medium whitespace-nowrap cursor-pointer hover:text-accent transition-colors focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
              onClick={() => onSort('lastCheck')}
              onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onSort('lastCheck'))}
              tabIndex={0}
              role="button"
            >
              <div className="flex items-center">
                <div className="flex flex-col leading-tight">
                  <span>{t('table.headers.lastCheckLine1')}</span>
                  <span className="text-[10px] opacity-50 font-normal">{t('table.headers.lastCheckLine2')}</span>
                </div>
                <SortIcon columnKey="lastCheck" />
              </div>
            </th>
            {/* 质量列表头：rpdiag 关闭时整列隐藏；启用时未加载完成前置灰不响应排序，避免空数据触发的伪排序 */}
            {showQualityColumn && (
            <th
              className={`px-1.5 py-3 font-medium whitespace-nowrap focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none ${
                rpdiagScoresLoaded
                  ? 'cursor-pointer hover:text-accent transition-colors'
                  : 'text-muted cursor-not-allowed opacity-60'
              }`}
              onClick={() => rpdiagScoresLoaded && onSort('qualityScore')}
              onKeyDown={(e) => {
                if (!rpdiagScoresLoaded) return;
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault();
                  onSort('qualityScore');
                }
              }}
              tabIndex={rpdiagScoresLoaded ? 0 : -1}
              role="button"
              aria-disabled={rpdiagScoresLoaded ? undefined : true}
            >
              <div className="flex items-center gap-1">
                {t('table.headers.quality', '质量')}
                <HeaderInfoPopover align="right" widthClass="w-56">
                  {/* 文案内 <diag> 占位 → diag.relaypulse.top 外链（新标签打开 rpdiag 站点本身）。 */}
                  <Trans
                    i18nKey="table.headers.qualityTooltip"
                    components={{
                      diag: (
                        <a
                          href="https://diag.relaypulse.top"
                          target="_blank"
                          rel="noopener noreferrer"
                          className="text-accent hover:underline"
                          onClick={(e) => e.stopPropagation()}
                        />
                      ),
                    }}
                  />
                  {/* 了解评分方法 → rpdiag 站点（本站旧 /detect 专题页已下线，内容由 diag 承载） */}
                  <a
                    href="https://diag.relaypulse.top"
                    target="_blank"
                    rel="noopener noreferrer"
                    onClick={(e) => e.stopPropagation()}
                    className="mt-1.5 block text-accent hover:underline font-medium"
                  >
                    {t('table.qualityHintLink')} →
                  </a>
                </HeaderInfoPopover>
                {rpdiagScoresLoaded && <SortIcon columnKey="qualityScore" />}
              </div>
            </th>
            )}
            <th className="pl-1.5 pr-2 py-3 font-medium min-w-[224px]">
              <div className="flex items-center gap-2">
                {t('table.headers.trend')}
                <span className="text-[10px] normal-case opacity-50 border border-default px-1 rounded">
                  {currentTimeRange?.label}
                </span>
              </div>
            </th>
          </tr>
        </thead>
        <tbody className="divide-y divide-default/50 text-sm">
          {data.map((item, rowIndex) => {
            const ServiceIcon = getCachedServiceIcon(item.serviceType);
            const hasItemAnnotations = hasAnyAnnotation(item, { enableAnnotations });
            const pinnedBg = item.pinned ? sponsorLevelToPinnedBgClass(item.sponsorLevel) : '';
            return (
            <tr
              key={item.id}
              className={`group hover:bg-elevated/40 transition-[background-color,color] ${pinnedBg} ${sponsorLevelToBorderClass(item.sponsorLevel)}`}
            >
              {/* 注解列 */}
              {hasAnnotations && (
                <td className="px-1 py-1 whitespace-nowrap">
                  {hasItemAnnotations ? (
                    <AnnotationCell
                      annotations={item.annotations}
                      tooltipPlacement={rowIndex === 0 ? 'bottom' : 'top'}
                    />
                  ) : null}
                </td>
              )}
              {/* 服务商列（两行紧贴，整体垂直居中） */}
              {showProvider && (
                <td className="px-1.5 py-1.5">
                  <div className="flex items-center h-8 group/provider">
                    <div className="flex flex-col gap-0 flex-1 min-w-0 max-w-[13rem]">
                      <div className="flex items-center gap-1.5">
                        <span className="font-medium text-primary text-sm leading-tight truncate">
                          <ExternalLink href={item.providerUrl} inline requireConfirm>{item.providerName}</ExternalLink>
                        </span>
                        {/* 收藏按钮：始终显示，未收藏时弱化 */}
                        <div className="flex-shrink-0">
                          <FavoriteButton
                            isFavorite={isFavorite(item.id)}
                            onToggle={() => onToggleFavorite(item.id)}
                            size={12}
                            inline
                          />
                        </div>
                        {/* 过滤按钮：悬浮时显示 */}
                        {onFilterProvider && (
                          <button
                            type="button"
                            onClick={(e) => {
                              e.stopPropagation();
                              onFilterProvider(item.providerId);
                            }}
                            className="flex-shrink-0 p-0.5 rounded opacity-0 group-hover/provider:opacity-60 hover:!opacity-100 hover:text-accent transition-opacity cursor-pointer"
                            title={t('table.filterByProvider')}
                          >
                            <Filter size={10} />
                          </button>
                        )}
                      </div>
                      {showSponsor && item.sponsor && (
                        <span className="text-[9px] text-muted leading-none">
                          <ExternalLink href={item.sponsorUrl} inline>{item.sponsor}</ExternalLink>
                        </span>
                      )}
                    </div>
                  </div>
                </td>
              )}
              <td className="px-1.5 py-1">
                <span
                  className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-mono border ${
                    item.serviceType === 'cc'
                      ? 'border-service-cc text-service-cc bg-service-cc'
                      : item.serviceType === 'gm'
                      ? 'border-service-gm text-service-gm bg-service-gm'
                      : 'border-service-cx text-service-cx bg-service-cx'
                  }`}
                >
                  {ServiceIcon ? (
                    <ServiceIcon className="w-3.5 h-3.5 mr-1 text-primary" />
                  ) : (
                    <>
                      {item.serviceType === 'cc' && <Zap size={10} className="mr-1 text-primary" />}
                      {item.serviceType === 'cx' && <Shield size={10} className="mr-1 text-primary" />}
                    </>
                  )}
                  {item.serviceName.toUpperCase()}
                </span>
              </td>
              <td className="px-1.5 py-1 text-secondary text-xs">
                <ChannelCell
                  channel={item.channelName || item.channel}
                  probeUrl={item.probeUrl}
                  templateName={item.templateName}
                  coldReason={item.coldReason}
                  boardReason={item.boardReason}
                  boardReasonModels={item.boardReasonModels}
                  className="max-w-[10rem]"
                />
              </td>
              <td className="px-1.5 py-1 text-secondary text-xs max-w-[14rem]">
                {(() => {
                  const models = getModelDisplayList(item.modelEntries);
                  if (models.length === 0) return <span className="text-muted">-</span>;
                  if (models.length === 1) {
                    return (
                      <span className="block truncate" title={getModelTooltip(item.modelEntries)}>
                        {models[0]}
                      </span>
                    );
                  }
                  return (
                    <div className="flex flex-col gap-0.5" title={getModelTooltip(item.modelEntries)}>
                      {models.map((m, i) => (
                        <span key={i} className="block truncate">{m}</span>
                      ))}
                    </div>
                  );
                })()}
              </td>
              {showVendorColumn && (
                <td className="px-1.5 py-1 text-secondary text-xs max-w-[9rem]">
                  {item.modelVendor
                    ? <VendorBadge vendor={item.modelVendor} />
                    : <span className="text-muted">-</span>}
                </td>
              )}
              {!hidePriceColumn && (
                <td className="px-1.5 py-1 font-mono text-xs whitespace-nowrap">
                  {(() => {
                    const priceData = formatPriceRatioStructured(item.priceMin, item.priceMax);
                    if (!priceData) return <span className="text-muted">-</span>;
                    return (
                      <div className="flex flex-col leading-tight">
                        <span className="text-secondary">{priceData.base}</span>
                        {priceData.sub && (
                          <span className="text-[10px] text-muted">{priceData.sub}</span>
                        )}
                      </div>
                    );
                  })()}
                </td>
              )}
              <td className="px-1.5 py-1 font-mono text-xs text-secondary whitespace-nowrap">
                {item.listedDays != null ? `${item.listedDays}d` : '-'}
              </td>
              <td className="px-1.5 py-1 font-mono font-bold whitespace-nowrap">
                <span style={{ color: availabilityToColor(item.uptime) }}>
                  {item.uptime >= 0 ? `${item.uptime}%` : '--'}
                </span>
              </td>
              <td className="px-1.5 py-1">
                <div className="flex items-center gap-1.5">
                  <StatusDot status={item.currentStatus} size="sm" />
                  {item.lastCheckTimestamp ? (
                    <div className="text-xs text-secondary font-mono flex flex-col gap-0.5">
                      {item.lastCheckLatency !== undefined && (
                        <span
                          className="text-[10px] font-mono"
                          style={{ color: item.currentStatus === 'UNAVAILABLE' ? 'hsl(var(--text-muted))' : latencyToColor(item.lastCheckLatency, item.slowLatencyMs ?? slowLatencyMs) }}
                        >
                          {item.lastCheckLatency}ms
                        </span>
                      )}
                      <span className="text-[10px] text-muted">{new Date(item.lastCheckTimestamp * 1000).toLocaleString(i18n.language, { hour: '2-digit', minute: '2-digit' })}</span>
                    </div>
                  ) : (
                    <span className="text-muted text-xs">-</span>
                  )}
                </div>
              </td>
              {showQualityColumn && (
              <td className="px-1.5 py-1 whitespace-nowrap">
                {(() => {
                  const rpScore = lookupRpdiagScore(rpdiagScores, [item.providerName, item.providerId], item.serviceType, item.channelName || item.channel, item.channelId);
                  if (rpScore && rpScore.models && rpScore.models.length > 0) {
                    return <QualityScoreCell score={rpScore} />;
                  }
                  if (shouldShowQualityPending({ rpdiagEnabled: showQualityColumn, hasScore: !!rpScore, serviceType: item.serviceType, board: item.board })) {
                    return (
                      <span className="text-muted text-xs" title={t('table.qualityPendingTooltip')}>
                        {t('table.qualityPending')}
                      </span>
                    );
                  }
                  return <span className="text-muted text-xs">-</span>;
                })()}
              </td>
              )}
              <td className="pl-1.5 pr-2 py-1.5 align-middle">
                <div className="flex items-center gap-[2px] h-5 w-full overflow-hidden rounded-sm">
                  {/* 热力图：多层 vs 单层 */}
                  {item.isMultiModel && item.layers ? (
                    // Phase B: 多层垂直堆叠热力图
                    item.history.map((_, idx) => (
                      <LayeredHeatmapBlock
                        key={idx}
                        layers={item.layers!}
                        timeIndex={idx}
                        width={`${100 / item.history.length}%`}
                        height="h-full"
                        onHover={onBlockHover}
                        onLeave={onBlockLeave}
                        isMobile={false}
                        slowLatencyMs={item.slowLatencyMs ?? slowLatencyMs}
                        useLatencyGradient={useLatencyGradient}
                      />
                    ))
                  ) : (
                    // Phase A: 单层传统热力图
                    item.history.map((point, idx) => (
                      <HeatmapBlock
                        key={idx}
                        point={point}
                        width={`${100 / item.history.length}%`}
                        height="h-full"
                        onHover={onBlockHover}
                        onLeave={onBlockLeave}
                        isMobile={false}
                        useLatencyGradient={useLatencyGradient}
                      />
                    ))
                  )}
                </div>
              </td>
            </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

export const StatusTable = memo(StatusTableComponent);
