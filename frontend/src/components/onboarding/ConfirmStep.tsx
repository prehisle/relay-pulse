import { useState, useCallback, useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { ChevronLeft, Copy, Check, RotateCcw, ExternalLink, AlertTriangle } from 'lucide-react';
import type { OnboardingFormData, OnboardingMeta, SubmitOnboardingResponse } from '../../types/onboarding';
import { primaryButtonClass, secondaryButtonClass } from './controls';
import { describeModelSelection } from './modelSelection';

interface ConfirmStepProps {
  formData: OnboardingFormData;
  updateField: <K extends keyof OnboardingFormData>(key: K, value: OnboardingFormData[K]) => void;
  /** 用于把所选模型翻译成人话名（摘要里不出现模板名） */
  meta: OnboardingMeta | null;
  submitResult: SubmitOnboardingResponse | null;
  isSubmitting: boolean;
  testPassedAt: number | null;
  /** proof 绝对过期时间（ms），由后端按真实 proof_ttl 下发；过期/预警据此计算，不再硬编码 */
  proofExpiresAt: number | null;
  checkedClauses: Record<string, boolean>;
  onToggleClause: (key: string) => void;
  onSubmit: () => void;
  onBack: () => void;
  onReset: () => void;
}

/** 《入驻须知与确认》核心要点 i18n key（顺序即展示顺序） */
const AGREEMENT_CLAUSE_KEYS = [
  'clausePaid',
  'clauseApiKey',
  'clauseQuality',
  'clauseNoCheat',
  'clauseLegit',
  'clauseNoEndorse',
] as const;

/** 完整《入驻须知与确认》文档地址 */
const AGREEMENT_DOC_URL =
  'https://github.com/prehisle/relay-pulse/blob/main/docs/user/sponsorship-agreement.md';

/** A single row in the summary table. */
function SummaryRow({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4 py-2">
      <span className="text-sm text-secondary flex-shrink-0">{label}</span>
      <span className="text-sm text-primary text-right break-all">{value}</span>
    </div>
  );
}

/** Copyable text with a feedback icon. */
function CopyableText({ text }: { text: string }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);

  const handleCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Fallback: select text in a temp input
      const input = document.createElement('input');
      input.value = text;
      document.body.appendChild(input);
      input.select();
      document.execCommand('copy');
      document.body.removeChild(input);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  }, [text]);

  return (
    <div className="flex items-center gap-2">
      <code className="px-3 py-1.5 bg-accent/10 border border-accent/30 rounded text-accent font-mono text-sm select-all">
        {text}
      </code>
      <button
        type="button"
        onClick={handleCopy}
        className="p-1.5 text-muted hover:text-accent transition-colors"
        aria-label={t('onboarding.confirm.copy')}
      >
        {copied ? <Check className="w-4 h-4 text-success" /> : <Copy className="w-4 h-4" />}
      </button>
    </div>
  );
}

/** Step 3: Review summary and submit. */
export function ConfirmStep({ formData, meta, updateField, submitResult, isSubmitting, testPassedAt, proofExpiresAt, checkedClauses, onToggleClause, onSubmit, onBack, onReset }: ConfirmStepProps) {
  const { t } = useTranslation();

  // 全勾同步到 formData.agreementAccepted
  const allClausesChecked = AGREEMENT_CLAUSE_KEYS.every((k) => checkedClauses[k]);
  useEffect(() => {
    if (formData.agreementAccepted !== allClausesChecked) {
      updateField('agreementAccepted', allClausesChecked);
    }
  }, [allClausesChecked, formData.agreementAccepted, updateField]);

  // Proof 过期/预警基于后端下发的绝对过期时间 proofExpiresAt。
  // 每 30s 采样一次 now（lazy 初始化，避免渲染期重复调用 Date.now()）。
  const [nowMs, setNowMs] = useState<number>(() => Date.now());
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  useEffect(() => {
    if (timerRef.current) clearInterval(timerRef.current);
    if (!proofExpiresAt) { timerRef.current = null; return; }
    // nowMs 已在 mount 时 lazy 初始化为当前时刻；后续每 30s 采样刷新过期/预警。
    timerRef.current = setInterval(() => setNowMs(Date.now()), 30_000);
    return () => { if (timerRef.current) clearInterval(timerRef.current); };
  }, [proofExpiresAt]);

  const proofExpired = proofExpiresAt !== null && nowMs >= proofExpiresAt;
  // 在生命周期最后 20%（至少 30s）内预警，自动适配服务端 proof_ttl，无需硬编码。
  const proofWarning = (() => {
    if (proofExpiresAt === null || testPassedAt === null || proofExpired) return false;
    const total = proofExpiresAt - testPassedAt;
    const warnAt = proofExpiresAt - Math.max(total * 0.2, 30_000);
    return nowMs >= warnAt;
  })();

  // 显示层大写（type-source），分组原样；与 ProviderInfoStep 预览一致，存储层由后端统一小写
  const channelCode = formData.channelType && formData.channelSource
    ? `${formData.channelType.toUpperCase()}-${formData.channelSource.toUpperCase()}-${formData.channelGroup.trim() || 'main'}`
    : '';

  const maskApiKey = (key: string): string => {
    if (key.length <= 8) return '****';
    return `${key.slice(0, 4)}${'*'.repeat(Math.min(key.length - 8, 20))}${key.slice(-4)}`;
  };

  // After successful submission
  if (submitResult) {
    return (
      <div className="bg-surface border border-muted rounded-lg p-6 space-y-6">
        <div className="text-center space-y-3">
          <div className="inline-flex items-center justify-center w-16 h-16 bg-success/10 rounded-full">
            <Check className="w-8 h-8 text-success" />
          </div>
          <h2 className="text-xl font-semibold text-primary">
            {t('onboarding.confirm.successTitle')}
          </h2>
          <p className="text-secondary">{t('onboarding.confirm.successDescription')}</p>
        </div>

        {/* Public ID */}
        <div className="bg-elevated rounded-lg p-4 space-y-3">
          <div>
            <p className="text-sm font-medium text-secondary mb-1">
              {t('onboarding.confirm.publicId')}
            </p>
            <CopyableText text={submitResult.public_id} />
          </div>
          <p className="text-xs text-muted">{t('onboarding.confirm.publicIdHint')}</p>
        </div>

        {/* Contact info with copy template */}
        {submitResult.contact_info && (
          <div className="bg-elevated rounded-lg p-4 space-y-3">
            <p className="text-sm font-medium text-secondary">
              {t('onboarding.confirm.contactLabel')}
            </p>
            <p className="text-sm text-primary">{submitResult.contact_info}</p>
            <div>
              <p className="text-xs text-muted mb-1">{t('onboarding.confirm.copyTemplateHint')}</p>
              <CopyableText
                text={t('onboarding.confirm.contactTemplate', {
                  id: submitResult.public_id,
                  provider: formData.providerName,
                })}
              />
            </div>
          </div>
        )}

        {/* Reset button */}
        <div className="flex flex-wrap justify-center gap-3 pt-2">
          <button
            type="button"
            onClick={onReset}
            className={secondaryButtonClass}
          >
            <RotateCcw className="w-4 h-4" />
            {t('onboarding.confirm.newSubmission')}
          </button>
        </div>
      </div>
    );
  }

  // Pre-submission: review summary
  return (
    <div className="bg-surface border border-muted rounded-lg p-6 space-y-6">
      <h2 className="text-xl font-semibold text-primary">
        {t('onboarding.confirm.title')}
      </h2>
      <p className="text-sm text-secondary">{t('onboarding.confirm.description')}</p>

      {/* Provider info summary */}
      <div className="bg-elevated rounded-lg p-4 space-y-1 divide-y divide-muted/20">
        <h3 className="text-sm font-semibold text-primary pb-2">
          {t('onboarding.confirm.sectionProvider')}
        </h3>
        <SummaryRow
          label={t('onboarding.providerInfo.providerName')}
          value={formData.providerName}
        />
        <SummaryRow
          label={t('onboarding.providerInfo.websiteUrl')}
          value={formData.websiteUrl}
        />
        <SummaryRow
          label={t('onboarding.providerInfo.category')}
          value={t(`onboarding.providerInfo.categories.${formData.category}`)}
        />
        <SummaryRow
          label={t('onboarding.providerInfo.serviceType')}
          value={t(`onboarding.providerInfo.serviceTypes.${formData.serviceType}`, { defaultValue: formData.serviceType.toUpperCase() })}
        />
        <SummaryRow
          label={t('onboarding.providerInfo.sponsorLevel')}
          /* 自助收录仅 pulse 等级（无选择器），sponsorLevel 恒为空，回退展示 pulse 避免出现空行。
             附等级代码（与来源下拉「名称（code）」惯例及《赞助权益体系》表格「脉冲链路 pulse」一致） */
          value={`${t(`onboarding.providerInfo.sponsorLevels.${formData.sponsorLevel || 'pulse'}`, { defaultValue: formData.sponsorLevel || 'pulse' })}（${formData.sponsorLevel || 'pulse'}）`}
        />
        {formData.channelName.trim() !== '' && (
          <SummaryRow
            label={t('onboarding.providerInfo.channelName', { defaultValue: '通道显示名称' })}
            value={formData.channelName.trim()}
          />
        )}
        <SummaryRow
          label={t('onboarding.providerInfo.channelCodePreview')}
          value={
            <code className="px-2 py-0.5 bg-accent/10 border border-accent/30 rounded text-accent font-mono font-bold">
              {channelCode}
            </code>
          }
        />
      </div>

      {/* Connection info summary */}
      <div className="bg-elevated rounded-lg p-4 space-y-1 divide-y divide-muted/20">
        <h3 className="text-sm font-semibold text-primary pb-2">
          {t('onboarding.confirm.sectionConnection')}
        </h3>
        <SummaryRow
          label={t('onboarding.connectionTest.baseUrl')}
          value={formData.baseUrl}
        />
        <SummaryRow
          label={t('onboarding.connectionTest.apiKey')}
          value={
            <span className="font-mono text-xs">{maskApiKey(formData.apiKey)}</span>
          }
        />
        <SummaryRow
          /* 展示所选模型的人话名（表单里已不出现模板名）；服务类型(cc) 已在上方单列不重复 */
          label={t('onboarding.connectionTest.model')}
          value={describeModelSelection(meta, formData.serviceType, formData.modelKey, formData.model)
            || formData.testVariant}
        />
      </div>

      {/* 入驻须知与确认：逐条勾选 */}
      <div className="bg-elevated rounded-lg p-4 space-y-3 border border-accent/20">
        <h3 className="text-sm font-semibold text-primary">
          {t('onboarding.confirm.agreement.title')}
        </h3>
        <p className="text-xs text-secondary">{t('onboarding.confirm.agreement.intro')}</p>
        <ul className="space-y-2.5">
          {AGREEMENT_CLAUSE_KEYS.map((key) => (
            <li key={key}>
              <label className="flex items-start gap-3 cursor-pointer py-1">
                <input
                  type="checkbox"
                  checked={!!checkedClauses[key]}
                  onChange={() => onToggleClause(key)}
                  className="mt-0.5 w-4 h-4 flex-shrink-0 rounded border-muted accent-accent"
                />
                <span className="text-sm text-secondary leading-relaxed">
                  {t(`onboarding.confirm.agreement.${key}`)}
                </span>
              </label>
            </li>
          ))}
        </ul>
        <p className="text-xs text-muted">
          {t('onboarding.confirm.agreement.fullText')}{' '}
          <a
            href={AGREEMENT_DOC_URL}
            target="_blank"
            rel="noopener noreferrer"
            className="text-accent hover:text-accent-strong underline inline-flex items-center gap-0.5"
          >
            {t('onboarding.confirm.agreement.fullLink')}
            <ExternalLink className="w-3 h-3" />
          </a>
        </p>
      </div>

      {/* Proof expiry warning */}
      {proofExpired && (
        <div className="flex items-center gap-2 p-3 bg-danger/10 border border-danger/20 rounded-lg text-sm text-danger" role="alert">
          <AlertTriangle className="w-4 h-4 flex-shrink-0" />
          <span>{t('onboarding.confirm.proofExpiredBanner')}</span>
        </div>
      )}
      {proofWarning && (
        <div className="flex items-center gap-2 p-3 bg-warning/10 border border-warning/20 rounded-lg text-sm text-warning" role="status" aria-live="polite">
          <AlertTriangle className="w-4 h-4 flex-shrink-0" />
          <span>{t('onboarding.confirm.proofExpiringSoon')}</span>
        </div>
      )}

      {/* Navigation buttons */}
      <div className="flex flex-col items-stretch gap-2 pt-2 sm:flex-row sm:items-center sm:justify-between">
        <button
          type="button"
          onClick={onBack}
          className={`${secondaryButtonClass} justify-center`}
        >
          <ChevronLeft className="w-4 h-4" />
          {t('onboarding.back')}
        </button>
        <div className="flex flex-col items-stretch gap-1 sm:items-end">
          <button
            type="button"
            onClick={onSubmit}
            disabled={isSubmitting || !allClausesChecked || proofExpired}
            title={
              proofExpired
                ? t('onboarding.confirm.proofExpiredBanner')
                : !allClausesChecked
                  ? t('onboarding.confirm.agreement.allRequiredHint')
                  : undefined
            }
            className={primaryButtonClass}
          >
            {isSubmitting ? t('onboarding.confirm.submitting') : t('onboarding.confirm.submit')}
          </button>
          {!allClausesChecked && (
            <span className="text-xs text-muted text-center sm:text-right">
              {t('onboarding.confirm.agreement.allRequiredHint')}
            </span>
          )}
        </div>
      </div>
    </div>
  );
}
