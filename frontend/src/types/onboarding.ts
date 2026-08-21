// 自助收录相关类型定义

/** 申请状态 */
export type SubmissionStatus = 'pending' | 'approved' | 'rejected' | 'published';

/** 赞助等级信息 */
export interface SponsorLevelInfo {
  value: string;
  label: string;
  description: string;
}

/** 通道类型信息 */
export interface ChannelTypeInfo {
  value: string;
  label: string;
}

/** 测试类型信息 */
export interface TestTypeInfo {
  id: string;
  name: string;
  description: string;
  default_variant: string;
  variants: { id: string; order: number }[];
}

/** 模型厂商受控词表条目（后端 internal/modelvendor 是唯一真相源） */
export interface ModelVendorInfo {
  code: string;
  label: string;
  icon_key: string;
}

/** 「选模型」下拉里的一个条目。用户看到的是厂商与模型名，模板是选中后随行提交的实现细节。 */
export interface ModelOption {
  /** 选项唯一键（同一模板可因不同种子模型出现多次） */
  key: string;
  label: string;
  /** 厂商 code；空表示模板未声明，由用户另选 */
  vendor: string;
  /** 提交时随行的 template_name */
  template: string;
  /** 提交时随行的行级模型 ID：仅第一方厂商条目非空 */
  model: string;
  /** 实际会请求的模型 ID，仅供展示 */
  request_model: string;
  /** 模型 ID 是否可编辑（第一方厂商条目为 true——同一模型在不同平台 ID 未必相同） */
  editable: boolean;
}

/** 自填模型 ID 时可选的「请求形态」（native 模板的人话名） */
export interface RequestShapeOption {
  template: string;
  label: string;
}

/** 通道来源选项（受控词表条目，按 service 划分下发） */
export interface ChannelSourceOption {
  value: string;
  label: string;
  category: string; // 仅用于前端分组展示（subscription/official/cloud/reverse/mixed）
}

/** 通道分组（channel_group）的同步校验规则 */
export interface ChannelGroupRule {
  pattern: string;
  default: string;
  max_length: number;
}

/** 申请表单元数据 */
export interface OnboardingMeta {
  service_types: string[];
  sponsor_levels: SponsorLevelInfo[];
  channel_types: ChannelTypeInfo[];
  channel_sources_by_service: Record<string, ChannelSourceOption[]>;
  /** 通道类型(O/R/M) → 允许的来源 category 列表，用于按已选类型过滤来源下拉 */
  channel_type_allowed_categories: Record<string, string[]>;
  channel_group_rule: ChannelGroupRule;
  test_types: TestTypeInfo[];
  contact_info: string;
  /** 模型厂商受控词表（自填模型时的厂商下拉） */
  model_vendors: ModelVendorInfo[];
  /** service → 可选模型目录 */
  models_by_service: Record<string, ModelOption[]>;
  /** service → 自填模型时可选的请求形态；没有 native 模板的 service 不出现（前端据此不渲染自填入口） */
  request_shapes_by_service: Record<string, RequestShapeOption[]>;
}

/** 用户端表单数据 */
export interface OnboardingFormData {
  // Step 1: 服务商信息
  providerName: string;
  websiteUrl: string;
  category: 'commercial' | 'public';
  serviceType: string;
  sponsorLevel: string;
  channelType: string;
  channelSource: string;
  channelGroup: string;
  channelName: string;
  agreementAccepted: boolean;

  // Step 2: 连通性测试
  baseUrl: string;
  apiKey: string;
  testType: string;
  /** 提交给后端的探针模板名（由所选模型 / 请求形态决定，界面上不出现） */
  testVariant: string;
  /** 所选模型条目的 key；CUSTOM_MODEL_KEY 表示「其他（自填模型 ID）」 */
  modelKey: string;
  /** 行级模型 ID：仅第一方厂商模型与自填时非空 */
  model: string;
  /** 行级模型厂商 code：同上，可留空（词表外厂商由管理员后补） */
  modelVendor: string;
}

/** 测试结果（内联探测响应） */
export interface OnboardingTestResult {
  probe_status?: number;
  sub_status?: string;
  http_code?: number;
  latency?: number;
  error_message?: string;
  response_snippet?: string;
  probe_id: string;
  /** 本次实际请求对应的可复制 curl 命令（默认脱敏，密钥用 $RP_API_KEY 占位）。仅 admin 测试下发。 */
  curl?: string;
  test_proof?: string;
  /** proof 绝对过期时间（Unix 秒），由后端按真实 proof_ttl 下发，供前端权威倒计时 */
  proof_expires_at?: number;
}

/** 提交申请请求 */
export interface SubmitOnboardingRequest {
  provider_name: string;
  website_url: string;
  category: string;
  service_type: string;
  template_name: string;
  /** 行级模型 ID（仅第一方厂商模型非空） */
  model: string;
  /** 行级模型厂商 code（可空） */
  model_vendor: string;
  sponsor_level: string;
  channel_type: string;
  channel_source: string;
  channel_group: string;
  channel_name: string;
  base_url: string;
  api_key: string;
  test_proof: string;
  test_job_id: string;
  test_type: string;
  test_api_url: string;
  test_latency: number;
  test_http_code: number;
  locale: string;
  /** 用户逐条确认《入驻须知与确认》全部要点；后端要求为 true 才受理 */
  agreement_accepted: boolean;
}

/** 提交申请响应 */
export interface SubmitOnboardingResponse {
  public_id: string;
  contact_info: string;
}

/** 管理员视角的完整申请 */
export interface AdminSubmission {
  id: number;
  public_id: string;
  status: SubmissionStatus;
  provider_name: string;
  website_url: string;
  category: string;
  service_type: string;
  template_name: string;
  model: string;
  model_vendor: string;
  sponsor_level: string;
  channel_type: string;
  channel_source: string;
  channel_group: string;
  channel_code: string;
  target_provider: string;
  target_service: string;
  target_channel: string;
  channel_name: string;
  listed_since: string;
  expires_at: string;
  price_min: number;
  price_max: number;
  base_url: string;
  api_key_encrypted: string;
  api_key_fingerprint: string;
  api_key_last4: string;
  test_job_id: string;
  test_passed_at: number;
  test_latency_ms: number;
  test_http_code: number;
  submitter_ip_hash: string;
  locale: string;
  admin_note: string;
  admin_config_json: string;
  reviewed_at: number | null;
  created_at: number;
  updated_at: number;
  agreement_accepted: boolean;
  agreement_accepted_at: number;
  agreement_version: string;
}

/** 管理员列表响应 */
export interface AdminListResponse {
  submissions: AdminSubmission[];
  total: number;
  limit: number;
  offset: number;
}

/** 管理员详情响应 */
export interface AdminDetailResponse {
  submission: AdminSubmission;
  api_key: string;
}
