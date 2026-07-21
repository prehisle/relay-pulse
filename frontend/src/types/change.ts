/** 变更请求状态 */
export type ChangeRequestStatus = 'pending' | 'approved' | 'rejected' | 'applied';

/** 测试 payload 变体 */
export interface TestVariant {
  id: string;
  order: number;
}

/** 认证候选通道 */
export interface AuthCandidate {
  provider: string;
  service: string;
  channel: string;
  monitor_key: string;
  apply_mode: 'auto' | 'manual';
  provider_name: string;
  provider_url: string;
  channel_name: string;
  category: string;
  sponsor_level: string;
  base_url: string;
  key_last4: string;
  test_type: string;
  test_type_name: string;
  default_test_variant?: string;
  test_variants?: TestVariant[];
}

/** 认证响应 */
export interface AuthResponse {
  candidates: AuthCandidate[];
}

/** 提交变更请求参数 */
export interface SubmitChangeRequest {
  api_key: string;
  target_key: string;
  proposed_changes: Record<string, string>;
  new_api_key?: string;
  test_proof?: string;
  test_job_id?: string;
  test_type?: string;
  test_variant?: string;
  test_api_url?: string;
  test_latency?: number;
  test_http_code?: number;
  locale?: string;
  agreement_accepted?: boolean;
}

/** 提交变更响应 */
export interface SubmitChangeResponse {
  public_id: string;
}

/** 管理端变更请求 */
export interface AdminChangeRequest {
  id: number;
  public_id: string;
  status: ChangeRequestStatus;
  target_provider: string;
  target_service: string;
  target_channel: string;
  target_key: string;
  apply_mode: string;
  auth_fingerprint: string;
  auth_last4: string;
  current_snapshot: string;
  proposed_changes: string;
  new_key_encrypted?: string;
  new_key_fingerprint?: string;
  new_key_last4?: string;
  requires_test: boolean;
  test_type?: string;
  test_variant?: string;
  test_job_id?: string;
  test_passed_at?: number;
  test_latency_ms?: number;
  test_http_code?: number;
  /** 通道实时当前值（仅 proposed 涉及字段；后端 AdminList 填充）。manual/已删/读失败时缺省。 */
  live_current?: Record<string, string>;
  live_current_source?: 'auto' | 'manual' | 'deleted' | 'error';
  admin_note?: string;
  reviewed_at?: number;
  applied_at?: number;
  submitter_ip_hash?: string;
  locale?: string;
  agreement_accepted?: boolean;
  agreement_accepted_at?: number;
  agreement_version?: string;
  created_at: number;
  updated_at: number;
}

/**
 * 允许用户自助变更的字段集合（API Key 轮换使用顶层 new_api_key 字段，不走 proposed_changes）。
 * 注意：类别（category）与赞助等级（sponsor_level）须经 QQ 人工对接，不属于自助变更范围（见 sponsorship.md §2.2）。
 */
export const EDITABLE_FIELDS = [
  'provider_name',
  'provider_url',
  'channel_name',
  'base_url',
] as const;

export type EditableField = (typeof EDITABLE_FIELDS)[number];

/** proposed_changes 中需要测试的字段（API Key 轮换由顶层 new_api_key 单独触发） */
export const FIELDS_REQUIRING_TEST: ReadonlySet<string> = new Set([
  'base_url',
]);
