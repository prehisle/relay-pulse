// API 响应类型定义
export interface TimePoint {
  time: string;
  status: number; // 1=可用, 0=不可用, 2=波动
  latency: number; // 延迟(ms)
}

export interface CurrentStatus {
  status: number;
  latency: number;
  timestamp: number;
}

export interface MonitorResult {
  provider: string;
  service: string;
  current_status: CurrentStatus | null;
  timeline: TimePoint[];
}

export interface ApiResponse {
  meta: {
    period: string;
    count: number;
  };
  data: MonitorResult[];
}

// 前端状态枚举
export type StatusKey = 'AVAILABLE' | 'DEGRADED' | 'UNAVAILABLE';

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
};

// 处理后的数据类型
export interface ProcessedMonitorData {
  id: string;
  providerId: string;
  providerName: string;
  serviceType: string;
  history: Array<{
    index: number;
    status: StatusKey;
    timestamp: string;
    latency: number;
  }>;
  currentStatus: StatusKey;
  uptime: number; // 可用率百分比
}

// 时间范围配置
export interface TimeRange {
  id: string;
  label: string;
  points: number;
  unit: 'hour' | 'day';
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
  data: {
    index: number;
    status: StatusKey;
    timestamp: string;
    latency: number;
  } | null;
}

// 视图模式
export type ViewMode = 'table' | 'grid';
