import { useState, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { Lock } from 'lucide-react';
import type { AdminSubmission, OnboardingTestResult } from '../../types/onboarding';
import { FormField, SelectField, ReadOnlyField } from './FormControls';
import { buildVendorOptions, useModelVendors } from '../../hooks/useModelVendors';
import { CurlCommandBlock } from './CurlCommandBlock';
import { isValidPscSlug, suggestSlugFromName, suggestSlugFromUrl } from '../../utils/pscSlug';

/** 可编辑字段列表 — 用于本地 draft 初始化和脏检测 */
const EDITABLE_FIELDS = [
  'provider_name', 'website_url', 'category', 'service_type',
  'template_name', 'model', 'model_vendor',
  'sponsor_level', 'channel_type', 'channel_source', 'channel_group',
  'target_provider', 'target_service', 'target_channel',
  'channel_name', 'listed_since', 'expires_at',
  'price_min', 'price_max', 'base_url', 'admin_note',
] as const;
type EditableKey = (typeof EDITABLE_FIELDS)[number];
type Draft = Record<EditableKey, string>;

function pickDraft(sub: AdminSubmission): Draft {
  const d = {} as Draft;
  for (const k of EDITABLE_FIELDS) d[k] = (sub[k] as string | number)?.toString() ?? '';
  return d;
}

function hasDraftChanged(draft: Draft, sub: AdminSubmission): boolean {
  return EDITABLE_FIELDS.some((k) => draft[k] !== ((sub[k] as string | number)?.toString() ?? ''));
}

interface SubmissionDetailProps {
  submission: AdminSubmission;
  apiKey: string;
  showApiKey: boolean;
  setShowApiKey: (show: boolean) => void;
  onSave: (fields: Partial<AdminSubmission>) => void;
  onTest: () => Promise<OnboardingTestResult | null>;
  /** 按服务类型拉取可用模板。失败应抛错，由本组件降级为禁用控件 + 错误提示。 */
  fetchTemplates: (serviceType?: string) => Promise<string[]>;
  onReject: (note: string) => void;
  onDelete: () => void;
  onPublish: (board: string) => void;
  suggestedChannel?: string;
  onBack: () => void;
}

/** Format a unix timestamp to a readable date string. */
function formatTimestamp(ts: number | null): string {
  if (!ts) return '--';
  return new Date(ts * 1000).toLocaleString(undefined, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
}

/** Mask an API key, showing only the last 4 characters. */
function maskApiKey(last4: string): string {
  return `${'*'.repeat(20)}${last4}`;
}

/**
 * 组装模板下拉选项。
 *
 * - 始终首项为占位（emptyLabel 由调用方按加载/错误/空状态等情况下文化）。
 * - current 是当前 submission.template_name：即使后端不再返回（被删/重命名），
 *   也保留为可选项，避免管理员保存时悄悄把 template_name 清空。
 */
function buildTemplateOptions(
  templates: string[],
  current: string,
  emptyLabel: string,
): { value: string; label: string }[] {
  const seen = new Set<string>();
  const options: { value: string; label: string }[] = [{ value: '', label: emptyLabel }];
  for (const name of templates) {
    if (!name || seen.has(name)) continue;
    seen.add(name);
    options.push({ value: name, label: name });
  }
  if (current && !seen.has(current)) {
    options.push({ value: current, label: current });
  }
  return options;
}

export const SubmissionDetail: React.FC<SubmissionDetailProps> = ({
  submission,
  apiKey,
  showApiKey,
  setShowApiKey,
  onSave,
  onTest,
  fetchTemplates,
  onReject,
  onDelete,
  onPublish,
  suggestedChannel,
  onBack,
}) => {
  const { t } = useTranslation();
  const vendors = useModelVendors();

  // 本地编辑 draft — submission 变化时重置（含保存后用持久化值回填、清 dirty）。
  // 渲染期 identity 守卫替代 effect：复刻原 [submission] 引用触发语义（保存后 AdminPage
  // 传入同 public_id 的新对象 → 引用变 → 重置），少一次首渲染无谓 reset，且规避
  // set-state-in-effect 的级联渲染（React 官方"渲染期调整 state"配方）。
  const [draft, setDraft] = useState<Draft>(() => pickDraft(submission));
  const [syncedFrom, setSyncedFrom] = useState(submission);
  if (syncedFrom !== submission) {
    setSyncedFrom(submission);
    setDraft(pickDraft(submission));
  }

  const dirty = hasDraftChanged(draft, submission);
  const [isSaving, setIsSaving] = useState(false);

  // 三个 PSC 覆盖值是英文机器代号（monitors.d/ 文件名分段 + 公开 URL slug），不是展示名。
  // 后端保存与上架都会拒非法值，这里只是把提示提前到输入的那一刻——不然管理员容易把它当成
  // 「服务商名称的覆盖」填中文，直到点上架才发现。
  const overrideErrorText = t('admin.detail.pscOverrideInvalid', {
    defaultValue: '只能填小写字母、数字、短横线，且不能以短横线开头/结尾或出现连续短横线',
  });
  const overrideErrors: Partial<Record<EditableKey, string>> = {};
  for (const k of ['target_provider', 'target_service', 'target_channel'] as const) {
    const v = draft[k].trim();
    if (v && !isValidPscSlug(v)) overrideErrors[k] = overrideErrorText;
  }
  const hasOverrideError = Object.keys(overrideErrors).length > 0;

  // provider 代号建议：优先按官网域名（`api.yintu.cc` → `yintu`），退回展示名（中文名推不出，
  // 则不给建议——宁可不给，也不给一个管理员一采纳就被后端拒的值）。
  const providerSlugSuggestion =
    suggestSlugFromUrl(draft.website_url) || suggestSlugFromName(draft.provider_name);
  const showProviderSuggestion =
    !!providerSlugSuggestion && providerSlugSuggestion !== draft.target_provider.trim();

  // 模板下拉：按 draft.service_type 动态拉取，避免硬编码列表与 templates/ 目录漂移。
  // 选 service_type 才有意义——templates/ 目录按 cc-/cx-/gm- 前缀划分。
  const [templates, setTemplates] = useState<string[]>([]);
  const [isTemplatesLoading, setIsTemplatesLoading] = useState(false);
  const [templatesError, setTemplatesError] = useState<string | null>(null);
  const serviceType = draft.service_type.trim();

  useEffect(() => {
    if (!serviceType) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- 无 service_type 时同步重置模板态为有意（非派生 state）
      setTemplates([]);
      setTemplatesError(null);
      setIsTemplatesLoading(false);
      return;
    }

    // 防止快速切换 service_type 时旧请求覆盖新结果。
    let active = true;
    setIsTemplatesLoading(true);
    setTemplatesError(null);

    fetchTemplates(serviceType)
      .then((items) => { if (active) setTemplates(items); })
      .catch((e) => {
        if (!active) return;
        setTemplates([]);
        setTemplatesError(e instanceof Error ? e.message : String(e));
      })
      .finally(() => { if (active) setIsTemplatesLoading(false); });

    return () => { active = false; };
  }, [fetchTemplates, serviceType]);

  const [rejectNote, setRejectNote] = useState('');
  const [showRejectInput, setShowRejectInput] = useState(false);
  const [isTesting, setIsTesting] = useState(false);
  const [testResult, setTestResult] = useState<OnboardingTestResult | null>(null);

  const [publishBoard, setPublishBoard] = useState('hot');
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);

  const canPublish = submission.status === 'pending' || submission.status === 'approved';
  const canReject = submission.status === 'pending' || submission.status === 'approved';
  const canDelete = submission.status !== 'published';

  // 占位文案与禁用条件共享同一组状态。空 service_type 时禁用，避免管理员先选模板再选服务、
  // 选到跨服务模板后保存时才报错。
  const templatePlaceholder = !serviceType
    ? t('admin.detail.templateSelectServiceType')
    : isTemplatesLoading
      ? t('admin.detail.templateLoading')
      : templatesError
        ? t('admin.detail.templateLoadFailed', { message: templatesError })
        : templates.length === 0
          ? t('admin.detail.templateEmpty')
          : t('admin.detail.templateUnset');
  const templateOptions = buildTemplateOptions(templates, draft.template_name, templatePlaceholder);
  const modelVendorOptions = buildVendorOptions(
    vendors,
    draft.model_vendor,
    t('admin.monitors.sponsorLevels.none', { defaultValue: '(空)' }),
  );
  const templateSelectDisabled =
    !serviceType || isTemplatesLoading || !!templatesError ||
    (templates.length === 0 && !draft.template_name);

  const updateField = (field: EditableKey, value: string) => {
    setDraft((prev) => ({ ...prev, [field]: value }));
  };

  const handleSave = async () => {
    if (!dirty || hasOverrideError) return;
    setIsSaving(true);
    try {
      // 只发送有变化的字段
      const changes: Partial<AdminSubmission> = {};
      for (const k of EDITABLE_FIELDS) {
        if (draft[k] !== ((submission[k] as string | number)?.toString() ?? '')) {
          if (k === 'price_min' || k === 'price_max') {
            (changes as Record<string, number>)[k] = parseFloat(draft[k]) || 0;
          } else {
            (changes as Record<string, string>)[k] = draft[k];
          }
        }
      }
      onSave(changes);
    } finally {
      setIsSaving(false);
    }
  };

  const handleRejectConfirm = () => {
    onReject(rejectNote);
    setShowRejectInput(false);
    setRejectNote('');
  };

  const handleTest = async () => {
    setIsTesting(true);
    setTestResult(null);
    try {
      const resp = await onTest();
      if (resp) setTestResult(resp);
    } finally {
      setIsTesting(false);
    }
  };

  const dirtyTitle = dirty ? '请先保存修改' : undefined;
  const statusBadgeClass: Record<string, string> = {
    pending: 'bg-warning/15 text-warning',
    approved: 'bg-accent/15 text-accent',
    rejected: 'bg-danger/15 text-danger',
    published: 'bg-success/15 text-success',
  };

  const actionButtons = (
    <>
      {dirty && (
        <button
          onClick={handleSave}
          disabled={isSaving || hasOverrideError}
          title={hasOverrideError ? overrideErrorText : undefined}
          className="px-4 py-2 text-sm font-medium rounded-md border
                     bg-accent/10 border-accent/40 text-accent
                     hover:bg-accent/20 transition-colors
                     disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {isSaving ? t('admin.detail.saving') : t('admin.detail.save')}
        </button>
      )}

      <button
        onClick={handleTest}
        disabled={isTesting || dirty}
        title={dirtyTitle}
        className="px-4 py-2 text-sm font-medium rounded-md border
                   border-default text-secondary
                   hover:bg-elevated transition-colors
                   disabled:opacity-50 disabled:cursor-not-allowed"
      >
        {isTesting ? t('admin.detail.testing') : t('admin.detail.test')}
      </button>

      {canPublish && (
        <>
          <SelectField
            label="发布版块"
            value={publishBoard}
            onChange={setPublishBoard}
            options={[
              { value: 'hot', label: '主板' },
              { value: 'secondary', label: '备板' },
              { value: 'cold', label: '冷板' },
            ]}
          />
          <button
            onClick={() => onPublish(publishBoard)}
            disabled={dirty}
            title={dirtyTitle}
            className="px-4 py-2 text-sm font-medium rounded-md border
                       bg-success/10 border-success/30 text-success
                       hover:bg-success/20 transition-colors
                       disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {t('admin.detail.publish')}
          </button>
        </>
      )}

      {canReject && (
        !showRejectInput ? (
          <button
            onClick={() => setShowRejectInput(true)}
            className="px-4 py-2 text-sm font-medium rounded-md border
                       bg-danger/10 border-danger/30 text-danger
                       hover:bg-danger/20 transition-colors"
          >
            {t('admin.detail.reject')}
          </button>
        ) : (
          <div className="flex items-center gap-2">
            <input
              type="text"
              value={rejectNote}
              onChange={(e) => setRejectNote(e.target.value)}
              placeholder={t('admin.detail.rejectNotePlaceholder')}
              className="px-3 py-2 bg-elevated border border-default rounded-md
                         text-primary placeholder:text-muted text-sm w-48
                         focus:outline-none focus:border-danger focus:ring-1 focus:ring-danger
                         transition-colors"
              autoFocus
            />
            <button
              onClick={handleRejectConfirm}
              className="px-3 py-2 text-sm font-medium rounded-md border
                         bg-danger/10 border-danger/30 text-danger
                         hover:bg-danger/20 transition-colors"
            >
              {t('admin.detail.confirmReject')}
            </button>
            <button
              onClick={() => { setShowRejectInput(false); setRejectNote(''); }}
              className="px-3 py-2 text-sm rounded-md border
                         border-default text-muted hover:text-secondary
                         hover:bg-elevated transition-colors"
            >
              {t('admin.detail.cancel')}
            </button>
          </div>
        )
      )}

      {canDelete && (
        !showDeleteConfirm ? (
          <button
            onClick={() => setShowDeleteConfirm(true)}
            className="px-4 py-2 text-sm font-medium rounded-md border
                       border-default text-muted
                       hover:bg-danger/10 hover:text-danger hover:border-danger/30
                       transition-colors"
          >
            {t('admin.detail.delete')}
          </button>
        ) : (
          <div className="flex items-center gap-2">
            <button
              onClick={() => { onDelete(); setShowDeleteConfirm(false); }}
              className="px-3 py-2 text-sm font-medium rounded-md border
                         bg-danger/10 border-danger/30 text-danger
                         hover:bg-danger/20 transition-colors"
            >
              {t('admin.detail.confirmDelete')}
            </button>
            <button
              onClick={() => setShowDeleteConfirm(false)}
              className="px-3 py-2 text-sm rounded-md border
                         border-default text-muted hover:text-secondary
                         hover:bg-elevated transition-colors"
            >
              {t('admin.detail.cancel')}
            </button>
          </div>
        )
      )}
    </>
  );

  return (
    <div className="space-y-6">
      {/* Header with back button：窄屏允许换行，标题整词不断行，长 UUID 折到次行 */}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <button
          onClick={onBack}
          className="shrink-0 px-3 py-1.5 text-sm rounded-md border
                     border-default text-secondary hover:bg-elevated transition-colors"
        >
          {t('admin.detail.back')}
        </button>
        <h2 className="text-lg font-semibold text-primary whitespace-nowrap">
          {t('admin.detail.title')}
        </h2>
        <span className="text-xs text-muted font-mono break-all">{submission.public_id}</span>
      </div>

      {/* Sticky action bar */}
      <div className="sticky top-4 z-10 bg-surface border border-default rounded-lg px-4 py-3 shadow-sm">
        <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
          <div className="flex flex-wrap items-center gap-2 min-w-0">
            <code className="text-xs text-muted font-mono">{submission.public_id}</code>
            <span className={`px-2 py-0.5 rounded text-xs font-medium ${statusBadgeClass[submission.status] ?? 'bg-muted/15 text-muted'}`}>
              {t(`admin.status.${submission.status}`)}
            </span>
            {dirty && (
              <span className="flex items-center gap-1 text-xs text-warning" role="status" aria-live="polite">
                <Lock className="w-3 h-3 flex-shrink-0" />
                有未保存修改，测试/发布已锁定
              </span>
            )}
          </div>
          <div className="flex flex-wrap items-center gap-2 lg:justify-end">
            {actionButtons}
          </div>
        </div>
      </div>

      {/* Main content card */}
      <div className="bg-surface border border-default rounded-lg p-6 space-y-6">
        {/* Metadata row */}
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
          <ReadOnlyField
            label={t('admin.detail.status')}
            value={t(`admin.status.${submission.status}`)}
          />
          <ReadOnlyField
            label={t('admin.detail.createdAt')}
            value={formatTimestamp(submission.created_at)}
          />
          <ReadOnlyField
            label={t('admin.detail.reviewedAt')}
            value={formatTimestamp(submission.reviewed_at)}
          />
          <ReadOnlyField
            label={t('admin.detail.channelCode')}
            value={submission.channel_code}
          />
        </div>

        <hr className="border-default" />

        {/* Editable fields */}
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <FormField
            label={t('admin.detail.providerName')}
            value={draft.provider_name}
            onChange={(v) => updateField('provider_name', v)}
          />
          <FormField
            label={t('admin.detail.websiteUrl')}
            value={draft.website_url}
            onChange={(v) => updateField('website_url', v)}
            type="url"
          />
          <SelectField
            label={t('admin.detail.category')}
            value={draft.category}
            onChange={(v) => updateField('category', v)}
            options={[
              { value: 'commercial', label: t('onboarding.providerInfo.categories.commercial') },
              { value: 'public', label: t('onboarding.providerInfo.categories.public') },
            ]}
          />
          <SelectField
            label={t('admin.detail.serviceType')}
            value={draft.service_type}
            onChange={(v) => updateField('service_type', v)}
            options={[
              { value: 'cc', label: 'CC (Claude Code)' },
              { value: 'cx', label: 'CX (Codex)' },
              { value: 'gm', label: 'GM (Gemini)' },
            ]}
          />
          <div>
            <SelectField
              label={t('admin.detail.templateName')}
              value={draft.template_name}
              onChange={(v) => updateField('template_name', v)}
              options={templateOptions}
              disabled={templateSelectDisabled}
            />
            {templatesError && (
              <p className="mt-1 text-xs text-danger">
                {t('admin.detail.templateLoadFailed', { message: templatesError })}
              </p>
            )}
          </div>
          {/* 行级模型：只有第一方厂商通用模板（*-native-*）需要填，其余模板由模板自身声明。
              后端 ValidateModelSelection 对这组组合是硬校验（填错即拒），故这里保持自由文本，
              让管理员既能补填、也能在改回普通模板时清空。 */}
          <FormField
            label={t('admin.detail.model')}
            value={draft.model}
            onChange={(v) => updateField('model', v)}
            placeholder="glm-5.2"
          />
          <SelectField
            label={t('admin.detail.modelVendor')}
            value={draft.model_vendor}
            onChange={(v) => updateField('model_vendor', v)}
            options={modelVendorOptions}
          />
          <SelectField
            label={t('admin.detail.sponsorLevel')}
            value={draft.sponsor_level}
            onChange={(v) => updateField('sponsor_level', v)}
            options={[
              { value: '', label: t('admin.monitors.sponsorLevels.none', { defaultValue: '(空)' }) },
              { value: 'public', label: 'Public' },
              { value: 'signal', label: 'Signal' },
              { value: 'pulse', label: 'Pulse' },
              { value: 'beacon', label: 'Beacon' },
              { value: 'backbone', label: 'Backbone' },
              { value: 'core', label: 'Core' },
            ]}
          />
          <SelectField
            label={t('admin.detail.channelType')}
            value={draft.channel_type}
            onChange={(v) => updateField('channel_type', v)}
            options={[
              { value: 'O', label: 'O - 官方直连' },
              { value: 'R', label: 'R - 逆向' },
              { value: 'M', label: 'M - 混合' },
            ]}
          />
          <FormField
            label={t('admin.detail.channelSource')}
            value={draft.channel_source}
            onChange={(v) => updateField('channel_source', v)}
            placeholder="max, api, aws, kiro..."
          />
          <FormField
            label={t('admin.detail.channelGroup', { defaultValue: '通道分组' })}
            value={draft.channel_group}
            onChange={(v) => updateField('channel_group', v)}
            placeholder="main, us, v2..."
          />
          <FormField
            label={t('admin.detail.channelName')}
            value={draft.channel_name}
            onChange={(v) => updateField('channel_name', v)}
          />
          <FormField
            label={t('admin.detail.listedSince')}
            value={draft.listed_since}
            onChange={(v) => updateField('listed_since', v)}
            type="date"
          />
          <FormField
            label={t('admin.detail.expiresAt', { defaultValue: '到期日期' })}
            value={draft.expires_at}
            onChange={(v) => updateField('expires_at', v)}
            type="date"
          />
          <div>
            <FormField
              label={t('admin.detail.targetProvider', { defaultValue: 'Provider 覆盖' })}
              value={draft.target_provider}
              onChange={(v) => updateField('target_provider', v)}
              placeholder={t('admin.detail.targetProviderHint', {
                defaultValue: '英文代号，留空则由服务商名称派生',
              })}
              error={overrideErrors.target_provider}
            />
            {showProviderSuggestion && (
              <button
                type="button"
                className="mt-1 text-xs px-2 py-1 rounded bg-accent/10 border border-accent/30 text-accent hover:bg-accent/20 transition-colors"
                onClick={() => updateField('target_provider', providerSlugSuggestion)}
              >
                {t('admin.detail.suggestedChannel', { defaultValue: '建议' })}: {providerSlugSuggestion}
              </button>
            )}
          </div>
          <FormField
            label={t('admin.detail.targetService', { defaultValue: 'Service 覆盖' })}
            value={draft.target_service}
            onChange={(v) => updateField('target_service', v)}
            placeholder={t('admin.detail.targetServiceHint', { defaultValue: '留空使用派生值' })}
            error={overrideErrors.target_service}
          />
          <div>
            <FormField
              label={t('admin.detail.targetChannel', { defaultValue: 'Channel 覆盖' })}
              value={draft.target_channel}
              onChange={(v) => updateField('target_channel', v)}
              placeholder={t('admin.detail.targetChannelHint', { defaultValue: '留空使用派生值' })}
              error={overrideErrors.target_channel}
            />
            {suggestedChannel && (
              <button
                type="button"
                className="mt-1 text-xs px-2 py-1 rounded bg-warning/15 border border-warning/30 text-warning hover:bg-warning/25 transition-colors"
                onClick={() => updateField('target_channel', suggestedChannel)}
              >
                {t('admin.detail.suggestedChannel', { defaultValue: '建议' })}: {suggestedChannel}
              </button>
            )}
          </div>
          <FormField
            label={t('admin.detail.priceMin')}
            value={draft.price_min}
            onChange={(v) => updateField('price_min', v)}
            type="number"
            placeholder="0"
          />
          <FormField
            label={t('admin.detail.priceMax')}
            value={draft.price_max}
            onChange={(v) => updateField('price_max', v)}
            type="number"
            placeholder="0"
          />
          <FormField
            label={t('admin.detail.baseUrl')}
            value={draft.base_url}
            onChange={(v) => updateField('base_url', v)}
            type="url"
          />
        </div>

        {/* API Key section */}
        <div>
          <label className="block text-xs font-medium text-muted mb-1">
            {t('admin.detail.apiKey')}
          </label>
          <div className="flex items-center gap-2">
            <div className="flex-1 px-3 py-2 bg-elevated border border-default rounded-md
                            text-sm font-mono text-secondary overflow-hidden text-ellipsis">
              {showApiKey
                ? apiKey || t('admin.detail.apiKeyNotLoaded')
                : maskApiKey(submission.api_key_last4)}
            </div>
            <button
              onClick={() => setShowApiKey(!showApiKey)}
              className="px-3 py-2 text-xs rounded-md border
                         bg-accent/10 border-accent/40 text-accent
                         hover:bg-accent/20 transition-colors whitespace-nowrap"
            >
              {showApiKey ? t('admin.detail.hideKey') : t('admin.detail.showKey')}
            </button>
          </div>
          <p className="mt-1 text-xs text-muted">
            {t('admin.detail.apiKeyFingerprint')}: {submission.api_key_fingerprint}
          </p>
        </div>

        {/* Test info */}
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
          <ReadOnlyField
            label={t('admin.detail.testLatency')}
            value={submission.test_latency_ms ? `${submission.test_latency_ms}ms` : '--'}
          />
          <ReadOnlyField
            label={t('admin.detail.testHttpCode')}
            value={submission.test_http_code ? String(submission.test_http_code) : '--'}
          />
          <ReadOnlyField
            label={t('admin.detail.testPassedAt')}
            value={formatTimestamp(submission.test_passed_at)}
          />
          <ReadOnlyField
            label={t('admin.detail.locale')}
            value={submission.locale}
          />
        </div>

        {/* Admin note */}
        <FormField
          label={t('admin.detail.adminNote')}
          value={draft.admin_note}
          onChange={(v) => updateField('admin_note', v)}
          placeholder={t('admin.detail.adminNotePlaceholder')}
          multiline
        />

      </div>

      {/* Test result */}
      {testResult && (
        <div className="bg-surface border border-default rounded-lg p-4 space-y-2">
          <h3 className="text-sm font-medium text-primary">{t('admin.detail.testResult')}</h3>
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 text-sm">
            <div>
              <span className="text-muted">{t('admin.detail.testHttpCode')}: </span>
              <span className="text-primary font-mono">{testResult.http_code ?? '--'}</span>
            </div>
            <div>
              <span className="text-muted">{t('admin.detail.testLatency')}: </span>
              <span className="text-primary font-mono">{testResult.latency ? `${testResult.latency}ms` : '--'}</span>
            </div>
            <div>
              <span className="text-muted">{t('admin.detail.status')}: </span>
              <span className={`font-medium ${
                testResult.probe_status === 1 ? 'text-success' :
                testResult.probe_status === 2 ? 'text-warning' : 'text-danger'
              }`}>
                {testResult.probe_status === 1 ? t('status.available', { defaultValue: '可用' }) :
                 testResult.probe_status === 2 ? t('status.degraded', { defaultValue: '降级' }) :
                 t('status.unavailable', { defaultValue: '不可用' })}
              </span>
            </div>
            {testResult.sub_status && testResult.sub_status !== 'none' && (
              <div>
                <span className="text-muted">{t('admin.detail.subStatus')}: </span>
                <span className="text-secondary">{testResult.sub_status}</span>
              </div>
            )}
          </div>
          {testResult.error_message && (
            <p className="text-xs text-danger">{testResult.error_message}</p>
          )}
          {testResult.curl && <CurlCommandBlock curl={testResult.curl} apiKey={apiKey} />}
        </div>
      )}
    </div>
  );
};
