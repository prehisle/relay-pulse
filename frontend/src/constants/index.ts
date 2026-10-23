import type { Provider, TimeRange, StatusConfig, TimeFilterPreset, CommunityItem } from '../types';
import type { TFunction } from 'i18next';

// 服务商列表
export const PROVIDERS: Provider[] = [
  { id: '88code', name: '88code', services: ['cc', 'cx'] },
  { id: 'xychatai', name: 'xychatai', services: ['cx'] },
  { id: 'duckcoding', name: 'duckcoding', services: ['cc', 'cx'] },
  { id: 'www.right.codes', name: 'www.right.codes', services: ['cx'] },
];

// 时间范围结构定义（仅 id/points/unit，供 mockMonitor 等非 i18n 场景使用）
export const TIME_RANGES: TimeRange[] = [
  { id: '90m', label: '近90分钟', points: 90, unit: 'minute' },
  { id: '24h', label: '近24小时', points: 24, unit: 'hour' },
  { id: '7d', label: '近7天', points: 7, unit: 'day' },
  { id: '30d', label: '近30天', points: 30, unit: 'day' },
];

// 时间范围配置工厂函数（i18n 版本）
export const getTimeRanges = (t: TFunction): TimeRange[] => [
  { id: '90m', label: t('controls.timeRanges.90m'), points: 90, unit: 'minute' },
  { id: '24h', label: t('controls.timeRanges.24h'), points: 24, unit: 'hour' },
  { id: '7d', label: t('controls.timeRanges.7d'), points: 7, unit: 'day' },
  { id: '30d', label: t('controls.timeRanges.30d'), points: 30, unit: 'day' },
];

// 状态配置工厂函数（i18n 版本）
// 使用语义化 CSS 类名，支持主题切换
export const getStatusConfig = (t: TFunction): Record<string, StatusConfig> => ({
  AVAILABLE: {
    color: 'bg-success',
    text: 'text-success',
    glow: 'glow-success',
    label: t('status.available'),
    weight: 3,
  },
  DEGRADED: {
    color: 'bg-warning',
    text: 'text-warning',
    glow: 'glow-warning',
    label: t('status.degraded'),
    weight: 2,
  },
  MISSING: {
    color: 'bg-secondary',
    text: 'text-secondary',
    glow: 'glow-muted',
    label: t('status.missing'),
    weight: 1,  // 排序权重与 UNAVAILABLE 相同
  },
  UNAVAILABLE: {
    color: 'bg-danger',
    text: 'text-danger',
    glow: 'glow-danger',
    label: t('status.unavailable'),
    weight: 1,
  },
});

// API 基础 URL（使用相对路径，自动适配当前域名）
export const API_BASE_URL = import.meta.env.VITE_API_BASE_URL || '';

// 是否使用模拟数据
export const USE_MOCK_DATA =
  (import.meta.env.VITE_USE_MOCK_DATA || '').toLowerCase() === 'true';

// 是否隐藏公开列表/卡片中的"价格 ¥/$"列（自报倍率，无法验证、随时可变）
// 构建期部署策略：值为 'true' 时隐藏，默认显示；admin 录入端不受影响
export const HIDE_PRICE_COLUMN =
  (import.meta.env.VITE_HIDE_PRICE_COLUMN || '').toLowerCase() === 'true';

// 反馈链接配置
// 向后兼容：优先使用新的环境变量，回退到旧的 VITE_FEEDBACK_URL
const legacyFeedbackUrl = import.meta.env.VITE_FEEDBACK_URL;
export const FEEDBACK_URLS = {
  // 申请收录（指向站内自助收录页）
  PROVIDER_SUGGESTION:
    import.meta.env.VITE_FEEDBACK_PROVIDER_URL ||
    legacyFeedbackUrl ||
    '/contact/apply',
  // 问题反馈
  BUG_REPORT:
    import.meta.env.VITE_FEEDBACK_BUG_URL ||
    legacyFeedbackUrl ||
    'https://github.com/prehisle/relay-pulse/issues/new?template=2-bug-report.yml',
} as const;

// 时段筛选预设配置
// 注意：value 代表用户本地时间，组件会在发送给后端时转换为 UTC
export const TIME_FILTER_PRESETS: TimeFilterPreset[] = [
  { id: 'all', labelKey: 'timeFilter.presets.all', value: null },
  { id: 'work', labelKey: 'timeFilter.presets.work', value: '09:00-17:00' },      // 本地工作时间
  { id: 'morning', labelKey: 'timeFilter.presets.morning', value: '06:00-12:00' }, // 本地上午
  { id: 'afternoon', labelKey: 'timeFilter.presets.afternoon', value: '12:00-18:00' }, // 本地下午
  { id: 'evening', labelKey: 'timeFilter.presets.evening', value: '18:00-24:00' }, // 本地晚上
];

// 时段筛选预设工厂函数（i18n 版本）
export const getTimeFilterPresets = (t: TFunction): Array<TimeFilterPreset & { label: string }> =>
  TIME_FILTER_PRESETS.map((preset) => ({
    ...preset,
    label: t(preset.labelKey),
  }));

// 生成 30 分钟粒度的时间选项（00:00 到 24:00）
export const TIME_OPTIONS: string[] = (() => {
  const options: string[] = [];
  for (let h = 0; h < 24; h++) {
    for (const m of [0, 30]) {
      options.push(`${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`);
    }
  }
  options.push('24:00'); // 24:00 表示午夜（结束时间专用）
  return options;
})();

// 时段筛选的开始时间选项（00:00 到 23:30，不包括 24:00）
export const TIME_START_OPTIONS = TIME_OPTIONS.slice(0, -1);

// 时段筛选的结束时间选项（00:30 到 24:00，不包括 00:00）
export const TIME_END_OPTIONS = TIME_OPTIONS.slice(1);

// 社群列表配置
export const COMMUNITY_LIST: CommunityItem[] = [
  {
    id: 'rp-qq-group',
    platform: 'qq',
    nameKey: 'community.items.qqGroup',
    groupNumber: '784246786',
    qrImageSrc: '/qqun.jpg',
    joinUrl: 'https://qm.qq.com/q/oPN0J85hIs',
  },
  {
    id: 'rp-telegram-group',
    platform: 'telegram',
    nameKey: 'community.items.telegramGroup',
    joinUrl: 'https://t.me/relaypulse_top',
  },
];
