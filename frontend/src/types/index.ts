// API 响应类型定义
export interface TimePoint {
  time: string;         // 格式化时间标签（如 "15:04" 或 "2006-01-02"）
  timestamp: number;    // Unix 时间戳（秒）
  status: number;       // 1=可用, 0=不可用, 2=波动, 3=未配置/认证失败, -1=缺失（bucket内最后一条）
  latency: number;      // 平均延迟(ms)
  availability: number; // 可用率百分比(0-100)，缺失时为 -1
  status_counts?: StatusCounts; // 各状态计数（可选，向后兼容）
}

export interface StatusCounts {
  available: number;   // 绿色（可用）次数
  degraded: number;    // 黄色（波动/降级）次数
  unavailable: number; // 红色（不可用）次数
  missing: number;     // 灰色（无数据/未配置）次数

  // 黄色波动细分
  slow_latency: number; // 响应慢次数
  rate_limit: number;   // 限流次数

  // 红色不可用细分
  server_error: number;     // 服务器错误次数（5xx）
  client_error: number;     // 客户端错误次数（4xx）
  auth_error: number;       // 认证失败次数（401/403）
  invalid_request: number;  // 请求参数错误次数（400）
  network_error: number;    // 连接失败次数
  response_timeout: number; // 响应超时次数（连接成功但读取响应体超时）
  content_mismatch: number; // 内容校验失败次数

  // HTTP 错误码细分统计
  // key: SubStatus 类型（如 "server_error", "client_error"）
  // value: 错误码 -> 出现次数 的映射
  http_code_breakdown?: Record<string, Record<number, number>>;
}

export interface CurrentStatus {
  status: number;
  latency: number;
  timestamp: number;
}

// 赞助等级类型（通道级）
export type SponsorLevel = 'public' | 'signal' | 'pulse' | 'beacon' | 'backbone' | 'core';

// 板块实际值（来自 API 响应的监测项/监测组字段）
// 注意：'all' 仅用于过滤参数，不应出现在实际数据中
export type BoardValue = 'hot' | 'secondary' | 'cold';

// 板块过滤参数（URL/UI/API 查询）
export type BoardFilter = BoardValue | 'active' | 'all';

/**
 * @deprecated 为保持向后兼容保留。新代码请使用：
 * - BoardValue：实际板块值（API 响应/数据模型）
 * - BoardFilter：过滤参数（URL/UI/API 查询）
 */
export type Board = BoardFilter;

// 板块配置（来自 API meta）
export interface BoardsConfig {
  enabled: boolean;
}

// 板块通道数量（来自 API meta）
export interface BoardCounts {
  hot: number;
  secondary: number;
  cold: number;
}

// 赞助通道置顶配置（来自 API meta）
export interface SponsorPinConfig {
  enabled: boolean;
  max_pinned: number;
  min_uptime: number;
  min_level: SponsorLevel;
}

// 注解 family 类型
export type AnnotationFamily = 'positive' | 'neutral' | 'negative';

// 注解（来自 API）
export interface Annotation {
  id: string;                    // 唯一标识（如 "public_service", "sponsor_beacon", "risk_flight"）
  family: AnnotationFamily;      // positive=正向, neutral=中性, negative=负向
  icon?: string;                 // 图标标识（对应 lucide 图标名）
  label: string;                 // 显示文本（后端直出）
  tooltip?: string;              // 提示文本（后端直出）
  href?: string;                 // 可选链接
  priority: number;              // 排序权重（正向加分，负向扣分）
  origin: string;                // system | rule | config
  metadata?: Record<string, unknown>; // 运行时元数据（如 interval_ms）
}

export interface MonitorResult {
  provider: string;
  provider_name?: string;              // Provider 显示名称（可选）
  provider_slug: string;               // URL slug（用于生成专属页面链接）
  provider_url?: string;               // 服务商官网链接
  service: string;
  service_name?: string;               // Service 显示名称（可选）
  category: 'commercial' | 'public';  // 分类：commercial（商业站）或 public（公益站）
  sponsor: string;                     // 赞助者
  sponsor_url?: string;                // 赞助者链接
  sponsor_level?: SponsorLevel;        // 赞助等级：public/signal/pulse/beacon/backbone/core
  annotations?: Annotation[];          // 注解数组
  price_min?: number;                  // 参考倍率下限
  price_max?: number;                  // 参考倍率
  listed_days?: number;                // 收录天数
  channel: string;                     // 业务通道标识
  channel_id?: string;                 // 通道稳定 id（跨产品 join 锚，可选/旧后端缺失）
  channel_name?: string;               // Channel 显示名称（可选）
  model?: string;                      // 模型展示名（可选）
  request_model?: string;              // 实际请求模型 ID（可选）
  model_vendor?: string;               // 模型厂商受控 code（后端 internal/modelvendor 词表；未回填时缺失）
  board: BoardValue;                   // 板块：hot/secondary/cold
  cold_reason?: string;                // 冷板原因（仅 cold 有值）
  board_reason?: string;               // 移板机器码（如 "quality_hardfail"），前端本地化
  board_reason_models?: string;        // 触发质量移板的模型名（逗号连接）
  probe_url?: string;                  // 探测端点 URL（脱敏后）
  template_name?: string;              // 请求体模板名称（如有）
  interval_ms?: number;                // 监测间隔（毫秒，可选兼容旧版本）
  slow_latency_ms?: number;            // 慢请求阈值（毫秒，per-monitor）
  current_status: CurrentStatus | null;
  timeline: TimePoint[];
}

export interface ApiResponse {
  meta: {
    period: string;
    count: number;
    timeline_mode?: 'raw' | 'aggregated';  // 时间线模式：raw=原始记录，aggregated=聚合数据
    slow_latency_ms?: number;  // 慢延迟阈值（毫秒），用于延迟颜色渐变
    enable_annotations?: boolean;   // 注解系统总开关（默认 true）
    hide_price_column?: boolean;    // 运行时控制：是否隐藏价格列（默认 false / 旧后端缺失）
    hide_category_filter?: boolean; // 运行时控制：是否隐藏分类筛选器（默认 false / 旧后端缺失）
    hide_vendor_filter?: boolean;   // 运行时控制：是否隐藏厂商筛选器（默认 false / 旧后端缺失）
    rpdiag_enabled?: boolean;       // 质量列/diag 外链入口总开关；私有部署未接 rpdiag 时为 false（旧后端缺失→fail-open 默认显示）
    sponsor_pin?: SponsorPinConfig;  // 赞助商置顶配置
    boards?: BoardsConfig;     // 板块配置
    board_counts?: BoardCounts; // 各板块通道数量
    all_monitor_ids?: string[]; // 全量监控项 ID 列表（用于清理无效收藏）
  };
  data: MonitorResult[];
}

// 前端状态枚举
export type StatusKey = 'AVAILABLE' | 'DEGRADED' | 'UNAVAILABLE' | 'MISSING';

export interface StatusConfig {
  color: string;
  text: string;
  glow: string;
  label: string;
  weight: number;
}

export const STATUS_MAP: Record<number, StatusKey> = {
  1: 'AVAILABLE',
  2: 'DEGRADED',
  0: 'UNAVAILABLE',
  3: 'MISSING',   // 未配置/认证失败
  '-1': 'MISSING',  // 缺失数据
};

// 处理后的数据类型
export interface ProcessedMonitorData {
  id: string;
  providerId: string;
  providerSlug: string;                // URL slug（用于生成专属页面链接）
  providerName: string;
  providerUrl?: string | null;         // 服务商官网链接
  serviceType: string;
  serviceName: string;                 // Service 显示名称
  category: 'commercial' | 'public';  // 分类
  sponsor: string;                     // 赞助者
  sponsorUrl?: string | null;          // 赞助者链接
  sponsorLevel?: SponsorLevel;         // 赞助商等级
  annotations?: Annotation[];          // 注解数组
  priceMin?: number | null;            // 参考倍率下限
  priceMax?: number | null;            // 参考倍率
  listedDays?: number | null;          // 收录天数
  channel?: string;                    // 业务通道标识
  channelName?: string;                // Channel 显示名称
  channelId?: string;                  // 通道稳定 id（跨产品 join 锚，Plan A 经 /api/status 暴露；旧后端/inline 缺失）
  /** 通道级模型厂商 code（modelvendor 词表）。仅当该通道**所有** layer 都声明了同一厂商时才有值，
   *  半填/冲突/未回填一律 undefined —— 见 monitorDataProcessor.deriveChannelVendor。 */
  modelVendor?: string;
  board: BoardValue;                   // 板块：hot/secondary/cold
  coldReason?: string;                 // 冷板原因（仅 cold 有值）
  boardReason?: string;                // 移板机器码（如 "quality_hardfail"），前端本地化
  boardReasonModels?: string;          // 触发质量移板的模型名（逗号连接）
  probeUrl?: string;                   // 探测端点 URL（脱敏后）
  templateName?: string;               // 请求体模板名称（如有）
  intervalMs?: number;                 // 监测间隔（毫秒，可选）
  slowLatencyMs?: number;              // 慢请求阈值（毫秒，per-monitor）
  pinned?: boolean;                    // 是否为置顶项（由排序逻辑标记）
  qualityScore?: number | null;        // rpdiag 通道质量排名分（max_score=活跃 model 排名贡献均值，fresh 取 30d 均值），sort 前由 useMonitorData 注入；null 表示未上报或 rpdiag 未启用
  isMultiModel: boolean;               // 是否为多模型监测组
  layers?: MonitorLayer[];             // 原始分层数据（仅多模型组有值）
  modelEntries?: Array<{ model: string; requestModel: string }>; // 模型展示名与实际请求模型映射
  history: Array<{
    index: number;
    status: StatusKey;
    timestamp: string;
    timestampNum: number;     // Unix 时间戳（秒）
    latency: number;
    availability: number;     // 可用率百分比(0-100)，缺失时为 -1
    statusCounts: StatusCounts; // 各状态计数
    slowLatencyMs?: number;   // 慢请求阈值（毫秒，per-monitor，用于 tooltip 显示）
    model?: string;           // 模型名称（可选，仅多模型时有值）
    requestModel?: string;    // 实际请求模型 ID（可选）
    layerOrder?: number;      // 层序号（可选，仅多模型时有值）
  }>;
  currentStatus: StatusKey;
  uptime: number;             // 可用率百分比
  lastCheckTimestamp?: number; // 最后监测时间（Unix 时间戳，秒）
  lastCheckLatency?: number;   // 最后监测延迟（毫秒）
}

// 时间范围配置
export interface TimeRange {
  id: string;
  label: string;
  points: number;
  unit: 'minute' | 'hour' | 'day';
}

// 服务商配置
export interface Provider {
  id: string;
  name: string;
  services: string[];
}

// 排序配置
export interface SortConfig {
  key: string;
  direction: 'asc' | 'desc';
}

// Tooltip 状态
export interface TooltipState {
  show: boolean;
  x: number;
  y: number;
  blockBottom?: number;
  data: {
    index: number;
    status: StatusKey;
    timestamp: string;
    timestampNum: number;  // Unix 时间戳（秒）
    latency: number;
    availability: number;  // 可用率百分比(0-100)，缺失时为 -1
    statusCounts: StatusCounts; // 各状态计数
    slowLatencyMs?: number;     // 慢请求阈值（毫秒，per-monitor）
    model?: string;             // 模型名称（可选，仅多模型时有值）
    requestModel?: string;      // 实际请求模型 ID（可选）
    layerOrder?: number;        // 层序号（可选，仅多模型时有值）
  } | null;
}

// 视图模式
export type ViewMode = 'table' | 'grid';

// 服务商选项（用于筛选器）
export interface ProviderOption {
  value: string;  // 规范化的键（小写），用于筛选
  label: string;  // 显示标签（保留原始大小写）
}

// 通道选项（用于筛选器）
export interface ChannelOption {
  value: string;  // 通道标识符，用于筛选
  label: string;  // 显示名称（channelName 或 channel）
}

// 时段筛选预设
export interface TimeFilterPreset {
  id: string;           // 预设 ID（如 'all', 'work', 'morning'）
  labelKey: string;     // i18n 翻译 key
  value: string | null; // 时段值：null=全天, "09:00-17:00"=自定义
}

// 社群平台类型
export type CommunityPlatform = 'qq' | 'wechat' | 'telegram' | 'discord';

// 社群配置项
export interface CommunityItem {
  id: string;                    // 唯一标识
  platform: CommunityPlatform;   // 平台类型
  nameKey: string;               // i18n 翻译 key（群名）
  groupNumber?: string;          // 群号（可选，用于展示）
  qrImageSrc?: string;           // 二维码图片路径
  joinUrl?: string;              // 加入链接
}

// ============= 多模型/父子通道类型定义 =============

// 状态点（用于 layer 的当前状态）
export interface StatusPoint {
  status: number;      // 1=可用, 0=不可用, 2=波动, -1=缺失
  latency: number;     // 延迟(ms)
  timestamp: number;   // Unix 时间戳（秒）
}

// 监测层（单个 model 的探测结果）
export interface MonitorLayer {
  model: string;                  // 模型名称
  request_model?: string;         // 实际请求模型 ID（可选）
  model_vendor?: string;          // 模型厂商受控 code（后端 internal/modelvendor 词表；未回填时缺失）
  layer_order: number;            // 层序号：0=父，1+=子
  current_status: StatusPoint;    // 当前状态点
  timeline: TimePoint[];          // 时间线数据
}

// 监测组（父子/多模型结构的聚合单元）
export interface MonitorGroup {
  provider: string;
  provider_name?: string;
  provider_slug: string;
  provider_url?: string;
  service: string;
  service_name?: string;
  category: 'commercial' | 'public';
  sponsor: string;
  sponsor_url?: string;
  sponsor_level?: SponsorLevel;
  annotations?: Annotation[];
  price_min?: number;
  price_max?: number;
  listed_days?: number;
  channel: string;
  channel_id?: string;            // 通道稳定 id（跨产品 join 锚，多模型组级；旧后端缺失）
  channel_name?: string;
  board: BoardValue;
  cold_reason?: string;
  board_reason?: string;
  board_reason_models?: string;
  probe_url?: string;
  template_name?: string;
  interval_ms?: number;
  slow_latency_ms?: number;

  current_status: number;         // 组级最差状态：0>2>1>-1
  layers: MonitorLayer[];         // 分层列表（父在前，子在后）
}

// 扩展 ApiResponse 以包含 groups 字段
export interface ApiResponseWithGroups extends ApiResponse {
  groups?: MonitorGroup[];  // 可选，向后兼容
}

// 扩展 TooltipState.data 以支持 layer 信息
export interface TooltipDataWithLayer {
  index: number;
  status: StatusKey;
  timestamp: string;
  timestampNum: number;  // Unix 时间戳（秒）
  latency: number;
  availability: number;  // 可用率百分比(0-100)，缺失时为 -1
  statusCounts: StatusCounts; // 各状态计数
  slowLatencyMs?: number;     // 慢请求阈值（毫秒，per-monitor）
  model?: string;             // 模型名称（可选，仅多模型时有值）
  requestModel?: string;      // 实际请求模型 ID（可选）
  layerOrder?: number;        // 层序号（可选，仅多模型时有值）
}
