import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { Helmet } from 'react-helmet-async';
import { ArrowLeft, ArrowRight, Check, Loader2, Key, AlertCircle, Copy, Activity } from 'lucide-react';
import { LANGUAGE_PATH_MAP, type SupportedLanguage } from '../i18n';
import { useChangeRequest, changeRequiresTest, type ChangeStep } from '../hooks/useChangeRequest';
import type { AuthCandidate } from '../types/change';
import { inputClass, selectClass, labelClass, primaryButtonClass, secondaryButtonClass } from '../components/onboarding/controls';
import { isProviderNameValid, isChannelNameValid } from '../utils/displayName';

/** 步骤指示器 */
function StepIndicator({ current, requiresTest }: { current: ChangeStep; requiresTest: boolean }) {
  const { t } = useTranslation();
  const steps: { key: ChangeStep; label: string }[] = [
    { key: 'auth', label: t('changeRequest.steps.auth') },
    { key: 'edit', label: t('changeRequest.steps.edit') },
    ...(requiresTest ? [{ key: 'test' as ChangeStep, label: t('changeRequest.steps.test') }] : []),
    { key: 'review', label: t('changeRequest.steps.review') },
  ];

  const currentIdx = steps.findIndex(s => s.key === current);

  return (
    <div className="flex items-center justify-center gap-2 mb-8">
      {steps.map((s, i) => (
        <div key={s.key} className="flex items-center gap-2">
          <div
            className={`flex items-center justify-center w-8 h-8 rounded-full text-sm font-medium transition ${
              i <= currentIdx
                ? 'bg-accent text-white'
                : 'bg-muted text-muted'
            }`}
          >
            {i < currentIdx ? <Check size={14} /> : i + 1}
          </div>
          <span className={`text-sm hidden sm:inline ${i <= currentIdx ? 'text-primary' : 'text-muted'}`}>
            {s.label}
          </span>
          {i < steps.length - 1 && <div className="w-8 h-px bg-muted mx-1" />}
        </div>
      ))}
    </div>
  );
}

/** Auth 步骤 */
function AuthStep({
  apiKey,
  setApiKey,
  isAuthenticating,
  authenticate,
  error,
}: {
  apiKey: string;
  setApiKey: (v: string) => void;
  isAuthenticating: boolean;
  authenticate: () => void;
  error: string | null;
}) {
  const { t } = useTranslation();

  return (
    <div className="max-w-lg mx-auto">
      <h2 className="text-xl font-semibold text-primary mb-2">{t('changeRequest.auth.title')}</h2>
      <p className="text-secondary text-sm mb-6">{t('changeRequest.auth.description')}</p>

      <div className="space-y-4">
        <div>
          <label className={labelClass}>API Key</label>
          <div className="relative">
            <Key size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-muted" />
            <input
              type="password"
              value={apiKey}
              onChange={e => setApiKey(e.target.value)}
              onKeyDown={e => e.key === 'Enter' && authenticate()}
              placeholder={t('changeRequest.auth.placeholder')}
              className={`${inputClass()} pl-10`}
            />
          </div>
        </div>

        {error && (
          <div className="flex items-center gap-2 p-3 rounded-lg bg-danger/10 text-danger text-sm">
            <AlertCircle size={16} />
            <span>{error}</span>
          </div>
        )}

        <button
          onClick={authenticate}
          disabled={isAuthenticating || apiKey.length < 10}
          className={`w-full justify-center ${primaryButtonClass}`}
        >
          {isAuthenticating ? <Loader2 size={16} className="animate-spin" /> : null}
          {t('changeRequest.auth.submit')}
        </button>
      </div>
    </div>
  );
}

/** Edit 步骤 */
function EditStep({
  candidates,
  selectedCandidate,
  setSelectedCandidate,
  changes,
  updateChange,
  newApiKey,
  setNewApiKey,
  proceedFromEdit,
  goBack,
  error,
}: {
  candidates: AuthCandidate[];
  selectedCandidate: AuthCandidate | null;
  setSelectedCandidate: (c: AuthCandidate) => void;
  changes: Record<string, string>;
  updateChange: (field: string, value: string) => void;
  newApiKey: string;
  setNewApiKey: (v: string) => void;
  proceedFromEdit: () => void;
  goBack: () => void;
  error: string | null;
}) {
  const { t } = useTranslation();

  // 通道选择（如多命中）
  if (candidates.length > 1 && !selectedCandidate) {
    return (
      <div className="max-w-lg mx-auto">
        <h2 className="text-xl font-semibold text-primary mb-2">{t('changeRequest.edit.selectChannel')}</h2>
        <p className="text-secondary text-sm mb-4">{t('changeRequest.edit.multipleChannels')}</p>
        <div className="space-y-2">
          {candidates.map(c => (
            <button
              key={c.monitor_key}
              onClick={() => setSelectedCandidate(c)}
              className="w-full flex items-center justify-between p-4 rounded-lg bg-elevated border border-default hover:border-accent/40 transition text-left"
            >
              <div>
                <div className="font-medium text-primary">{c.provider_name}</div>
                <div className="text-sm text-secondary">{c.channel_name} · {c.service}</div>
              </div>
              <span className="text-xs text-muted px-2 py-1 rounded-md bg-muted/30">
                {c.apply_mode === 'auto' ? t('admin.changes.modeAuto') : t('admin.changes.modeManual')}
              </span>
            </button>
          ))}
        </div>
        <button onClick={goBack} className="mt-4 inline-flex items-center gap-1 text-sm text-muted hover:text-secondary transition">
          <ArrowLeft size={14} />{t('changeRequest.back')}
        </button>
      </div>
    );
  }

  if (!selectedCandidate) return null;

  // 类别（category）与赞助等级（sponsor_level）的调整须经 QQ 人工对接，不在自助变更范围内（见 sponsorship.md §2.2），故此处不开放编辑
  const fields = [
    { key: 'provider_name', label: t('changeRequest.fields.providerName'), current: selectedCandidate.provider_name },
    { key: 'provider_url', label: t('changeRequest.fields.providerUrl'), current: selectedCandidate.provider_url },
    { key: 'channel_name', label: t('changeRequest.fields.channelName'), current: selectedCandidate.channel_name },
    { key: 'base_url', label: t('changeRequest.fields.baseUrl'), current: selectedCandidate.base_url },
  ];

  // 展示名前端校验（与后端 displayname 对齐）：内部不可见字符 / 超长 → 禁用「下一步」+ 内联错误。
  // 权威校验在后端；这里只做 fail-fast UX。仅对已改动（出现在 changes 中）的字段判定。
  const providerNameInvalid =
    changes['provider_name'] !== undefined && !isProviderNameValid(changes['provider_name']);
  const channelNameInvalid =
    changes['channel_name'] !== undefined && !isChannelNameValid(changes['channel_name']);
  const displayNameInvalid = providerNameInvalid || channelNameInvalid;

  return (
    <div className="max-w-lg mx-auto">
      <h2 className="text-xl font-semibold text-primary mb-2">{t('changeRequest.edit.title')}</h2>
      <p className="text-secondary text-sm mb-6">
        {t('changeRequest.edit.description', { channel: selectedCandidate.channel_name })}
      </p>

      <div className="space-y-4">
        {fields.map(f => {
          const invalid =
            (f.key === 'provider_name' && providerNameInvalid) ||
            (f.key === 'channel_name' && channelNameInvalid);
          return (
            <div key={f.key}>
              <label className={labelClass}>
                {f.label}
                <span className="text-muted ml-2 font-normal">({t('changeRequest.edit.current')}: {f.current || '—'})</span>
              </label>
              <input
                type="text"
                value={changes[f.key] ?? ''}
                onChange={e => updateChange(f.key, e.target.value)}
                placeholder={t('changeRequest.edit.noChange')}
                aria-invalid={invalid || undefined}
                className={inputClass(invalid)}
              />
              {invalid && (
                <p className="text-danger text-xs mt-1" role="alert">
                  {t('changeRequest.edit.invalidDisplayName')}
                </p>
              )}
            </div>
          );
        })}

        {/* 新 API Key */}
        <div>
          <label className={labelClass}>
            {t('changeRequest.fields.newApiKey')}
            <span className="text-muted ml-2 font-normal">({t('changeRequest.edit.current')}: ...{selectedCandidate.key_last4})</span>
          </label>
          <input
            type="password"
            value={newApiKey}
            onChange={e => setNewApiKey(e.target.value)}
            placeholder={t('changeRequest.edit.newApiKeyPlaceholder')}
            className={inputClass()}
          />
        </div>

        {error && (
          <div className="flex items-center gap-2 p-3 rounded-lg bg-danger/10 text-danger text-sm">
            <AlertCircle size={16} />
            <span>{error}</span>
          </div>
        )}

        <div className="flex gap-3 pt-2">
          <button onClick={goBack} className={secondaryButtonClass}>
            <ArrowLeft size={14} />{t('changeRequest.back')}
          </button>
          <button
            onClick={proceedFromEdit}
            disabled={displayNameInvalid}
            className={`flex-1 justify-center ${primaryButtonClass}`}
          >
            {t('changeRequest.next')}<ArrowRight size={14} />
          </button>
        </div>
      </div>
    </div>
  );
}

/** Test 步骤 */
function TestStep({
  selectedCandidate,
  selectedVariant,
  setSelectedVariant,
  isTesting,
  testResult,
  testProof,
  runTest,
  goBack,
  goNext,
  error,
}: {
  selectedCandidate: AuthCandidate;
  selectedVariant: string;
  setSelectedVariant: (v: string) => void;
  isTesting: boolean;
  testResult: { probe_status?: number; sub_status?: string; latency?: number; http_code?: number; error_message?: string } | null;
  testProof: string;
  runTest: () => void;
  goBack: () => void;
  goNext: () => void;
  error: string | null;
}) {
  const { t } = useTranslation();
  const passed = testResult?.probe_status === 1 && testProof !== '';
  const variants = (selectedCandidate.test_variants ?? []).slice().sort((a, b) => a.order - b.order);
  const showVariantSelect = variants.length > 1;

  return (
    <div className="max-w-lg mx-auto">
      <h2 className="text-xl font-semibold text-primary mb-2">{t('changeRequest.test.title')}</h2>
      <p className="text-secondary text-sm mb-6">{t('changeRequest.test.description')}</p>

      <div className="space-y-4">
        {/* Test type info */}
        <div className="p-3 rounded-lg bg-elevated border border-default">
          <div className="text-xs text-muted mb-0.5">{t('changeRequest.test.testType', { defaultValue: '服务类型' })}</div>
          <div className="text-sm text-primary font-medium">
            {selectedCandidate.test_type_name || selectedCandidate.test_type || selectedCandidate.service}
          </div>
        </div>

        {/* Variant selector */}
        {showVariantSelect && (
          <div>
            <label className={labelClass}>
              {t('changeRequest.test.variant', { defaultValue: '请求模板' })}
            </label>
            <select
              value={selectedVariant}
              onChange={e => setSelectedVariant(e.target.value)}
              disabled={isTesting}
              className={selectClass}
            >
              {variants.map(v => (
                <option key={v.id} value={v.id}>{v.id}</option>
              ))}
            </select>
          </div>
        )}

        {!testResult && !isTesting && (
          <button
            onClick={runTest}
            className={`w-full justify-center ${primaryButtonClass}`}
          >
            {t('changeRequest.test.run')}
          </button>
        )}

        {isTesting && (
          <div className="flex items-center justify-center gap-2 p-4 text-accent">
            <Loader2 size={20} className="animate-spin" />
            <span>{t('changeRequest.test.running')}</span>
          </div>
        )}

        {testResult && !isTesting && (
          <div className={`p-4 rounded-lg border ${passed ? 'border-success/40 bg-success/5' : 'border-danger/40 bg-danger/5'}`}>
            <div className={`font-medium ${passed ? 'text-success' : 'text-danger'}`}>
              {passed ? t('changeRequest.test.passed') : t('changeRequest.test.failed')}
            </div>
            {testResult.latency && (
              <div className="text-sm text-secondary mt-1">{t('changeRequest.test.latency')}: {testResult.latency}ms</div>
            )}
            {testResult.error_message && (
              <div className="text-sm text-danger mt-1">{testResult.error_message}</div>
            )}
          </div>
        )}

        {!passed && testResult && !isTesting && (
          <button
            onClick={runTest}
            className={`w-full justify-center ${secondaryButtonClass}`}
          >
            {t('changeRequest.test.retry')}
          </button>
        )}

        {error && (
          <div className="flex items-center gap-2 p-3 rounded-lg bg-danger/10 text-danger text-sm">
            <AlertCircle size={16} />
            <span>{error}</span>
          </div>
        )}

        <div className="flex gap-3 pt-2">
          <button onClick={goBack} className={secondaryButtonClass}>
            <ArrowLeft size={14} />{t('changeRequest.back')}
          </button>
          {passed && (
            <button
              onClick={goNext}
              className={`flex-1 justify-center ${primaryButtonClass}`}
            >
              {t('changeRequest.next')}<ArrowRight size={14} />
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

/** Review 步骤 */
export function ReviewStep({
  selectedCandidate,
  changes,
  newApiKey,
  isSubmitting,
  submit,
  goBack,
  error,
}: {
  selectedCandidate: AuthCandidate;
  changes: Record<string, string>;
  newApiKey: string;
  isSubmitting: boolean;
  submit: (agreementAccepted: boolean) => void;
  goBack: () => void;
  error: string | null;
}) {
  const { t } = useTranslation();

  // 改动触及 base_url 或提供新 API Key 时，后端要求重新确认「禁止监测作弊」条款。
  // 与 useChangeRequest 共用同一纯函数，判据字节等价、不会漂移。
  const requiresTest = changeRequiresTest(changes, newApiKey);
  const [agreementChecked, setAgreementChecked] = useState(false);

  // 改动字段的展示标签：与编辑页保持一致，避免在复核页暴露 snake_case 原始键名
  const fieldLabels: Record<string, string> = {
    provider_name: t('changeRequest.fields.providerName'),
    provider_url: t('changeRequest.fields.providerUrl'),
    channel_name: t('changeRequest.fields.channelName'),
    base_url: t('changeRequest.fields.baseUrl'),
  };

  return (
    <div className="max-w-lg mx-auto">
      <h2 className="text-xl font-semibold text-primary mb-2">{t('changeRequest.review.title')}</h2>
      <p className="text-secondary text-sm mb-6">{t('changeRequest.review.description')}</p>

      <div className="space-y-3 mb-6">
        <div className="p-4 rounded-lg bg-elevated border border-default">
          <div className="text-sm font-medium text-muted mb-2">{t('changeRequest.review.target')}</div>
          <div className="text-primary font-medium">{selectedCandidate.provider_name}</div>
          <div className="text-sm text-secondary">{selectedCandidate.channel_name} · {selectedCandidate.monitor_key}</div>
        </div>

        {Object.entries(changes).map(([field, value]) => (
          <div key={field} className="flex items-center gap-3 p-3 rounded-lg bg-elevated border border-default">
            <div className="flex-1">
              <div className="text-xs text-muted">{fieldLabels[field] ?? field}</div>
              <div className="text-sm text-secondary line-through">
                {(selectedCandidate as unknown as Record<string, string>)[field] || '—'}
              </div>
              <div className="text-sm text-primary font-medium">{value}</div>
            </div>
          </div>
        ))}

        {newApiKey && (
          <div className="p-3 rounded-lg bg-elevated border border-default">
            <div className="text-xs text-muted">{t('changeRequest.fields.newApiKey')}</div>
            <div className="text-sm text-secondary line-through">...{selectedCandidate.key_last4}</div>
            <div className="text-sm text-primary font-medium">...{newApiKey.slice(-4)}</div>
          </div>
        )}
      </div>

      {requiresTest && (
        <div className="p-4 rounded-lg bg-elevated border border-accent/20 mb-4 space-y-2">
          <p className="text-sm text-secondary leading-relaxed">
            {t('onboarding.confirm.agreement.clauseNoCheat')}
          </p>
          <label className="flex items-start gap-3 cursor-pointer">
            <input
              type="checkbox"
              checked={agreementChecked}
              onChange={() => setAgreementChecked((v) => !v)}
              className="mt-0.5 w-4 h-4 flex-shrink-0 rounded border-muted accent-accent"
            />
            <span className="text-sm text-primary">{t('changeRequest.review.noCheatConfirm')}</span>
          </label>
          {!agreementChecked && (
            <p className="text-xs text-muted">{t('changeRequest.review.noCheatRequired')}</p>
          )}
        </div>
      )}

      {error && (
        <div className="flex items-center gap-2 p-3 rounded-lg bg-danger/10 text-danger text-sm mb-4">
          <AlertCircle size={16} />
          <span>{error}</span>
        </div>
      )}

      <div className="flex gap-3">
        <button onClick={goBack} className={secondaryButtonClass}>
          <ArrowLeft size={14} />{t('changeRequest.back')}
        </button>
        <button
          onClick={() => submit(agreementChecked)}
          disabled={isSubmitting || (requiresTest && !agreementChecked)}
          className={`flex-1 justify-center ${primaryButtonClass}`}
        >
          {isSubmitting ? <Loader2 size={16} className="animate-spin" /> : <Check size={16} />}
          {t('changeRequest.review.submit')}
        </button>
      </div>
    </div>
  );
}

/** Done 步骤 */
function DoneStep({ publicId, reset }: { publicId: string; reset: () => void }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);

  const copyId = () => {
    navigator.clipboard.writeText(publicId);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="max-w-lg mx-auto text-center">
      <div className="w-16 h-16 rounded-full bg-success/10 text-success flex items-center justify-center mx-auto mb-4">
        <Check size={32} />
      </div>
      <h2 className="text-xl font-semibold text-primary mb-2">{t('changeRequest.done.title')}</h2>
      <p className="text-secondary text-sm mb-6">{t('changeRequest.done.description')}</p>

      <div className="flex items-center justify-center gap-2 p-3 rounded-lg bg-elevated border border-default mb-6">
        <code className="text-sm text-primary font-mono">{publicId}</code>
        <button onClick={copyId} className="text-muted hover:text-accent transition" title={t('onboarding.confirm.copy')}>
          <Copy size={14} />
        </button>
        {copied && <span className="text-xs text-success">✓</span>}
      </div>

      <div className="flex flex-wrap justify-center gap-3">
        <button
          onClick={reset}
          className={secondaryButtonClass}
        >
          {t('changeRequest.done.newRequest')}
        </button>
      </div>
    </div>
  );
}

export default function ChangeRequestPage() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const cr = useChangeRequest();
  const langPrefix = LANGUAGE_PATH_MAP[i18n.language as SupportedLanguage];
  const homePath = langPrefix ? `/${langPrefix}` : '/';

  return (
    <>
      <Helmet>
        <title>{t('changeRequest.meta.title')}</title>
        <meta name="description" content={t('changeRequest.meta.description')} />
      </Helmet>

      <div className="min-h-screen bg-page flex flex-col">
        <header className="px-4 py-4 border-b border-default/50">
          <div className="max-w-4xl mx-auto flex items-center gap-3">
            <button
              onClick={() => navigate(homePath)}
              className="p-1.5 bg-accent/10 rounded-lg border border-accent/20 flex-shrink-0"
            >
              <Activity className="w-5 h-5 text-accent" />
            </button>
            <span className="text-lg font-bold text-gradient-hero">RelayPulse</span>
          </div>
        </header>

        <main className="flex-1 max-w-4xl mx-auto w-full px-4 py-8 sm:py-12">
          <div className="text-center mb-8">
            <h1 className="text-2xl sm:text-3xl font-bold text-primary mb-2">
              {t('changeRequest.title')}
            </h1>
            <p className="text-secondary max-w-xl mx-auto">
              {t('changeRequest.subtitle')}
            </p>
          </div>

          {cr.step !== 'done' && (
            <StepIndicator current={cr.step} requiresTest={cr.requiresTest} />
          )}

          {cr.step === 'auth' && (
            <AuthStep
              apiKey={cr.apiKey}
              setApiKey={cr.setApiKey}
              isAuthenticating={cr.isAuthenticating}
              authenticate={cr.authenticate}
              error={cr.error}
            />
          )}

          {cr.step === 'edit' && (
            <EditStep
              candidates={cr.candidates}
              selectedCandidate={cr.selectedCandidate}
              setSelectedCandidate={cr.setSelectedCandidate}
              changes={cr.changes}
              updateChange={cr.updateChange}
              newApiKey={cr.newApiKey}
              setNewApiKey={cr.setNewApiKey}
              proceedFromEdit={cr.proceedFromEdit}
              goBack={() => cr.setStep('auth')}
              error={cr.error}
            />
          )}

          {cr.step === 'test' && cr.selectedCandidate && (
            <TestStep
              selectedCandidate={cr.selectedCandidate}
              selectedVariant={cr.selectedVariant}
              setSelectedVariant={cr.setSelectedVariant}
              isTesting={cr.isTesting}
              testResult={cr.testResult}
              testProof={cr.testProof}
              runTest={cr.runTest}
              goBack={() => cr.setStep('edit')}
              goNext={() => cr.setStep('review')}
              error={cr.error}
            />
          )}

          {cr.step === 'review' && cr.selectedCandidate && (
            <ReviewStep
              selectedCandidate={cr.selectedCandidate}
              changes={cr.changes}
              newApiKey={cr.newApiKey}
              isSubmitting={cr.isSubmitting}
              submit={cr.submit}
              goBack={() => cr.setStep(cr.requiresTest ? 'test' : 'edit')}
              error={cr.error}
            />
          )}

          {cr.step === 'done' && (
            <DoneStep
              publicId={cr.publicId}
              reset={cr.reset}
            />
          )}
        </main>
      </div>
    </>
  );
}
