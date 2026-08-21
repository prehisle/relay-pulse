import { useState, useMemo, useEffect, useSyncExternalStore } from 'react';
import { useTranslation } from 'react-i18next';
import { ChevronLeft, ChevronRight, Eye, EyeOff, Play, Clock, Loader2, CheckCircle2, AlertTriangle, XCircle, CircleHelp } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import type { OnboardingFormData, OnboardingMeta, OnboardingTestResult } from '../../types/onboarding';
import { inputClass, selectClass, labelClass, hintClass, primaryButtonClass, secondaryButtonClass } from './controls';
import {
  CUSTOM_MODEL_KEY,
  isSelectionValid,
  modelOptionsFor,
  needsModelId,
  requestShapesFor,
  resolveDefaultSelection,
  selectionForCustom,
  selectionFromOption,
  type ModelSelection,
} from './modelSelection';

/**
 * Module-level countdown store for proof validity.
 * Driven by the absolute expiry (proof_expires_at) the backend issues from the
 * real proof_ttl — never a hardcoded duration — so UI and server never disagree.
 */
const proofCountdownStore = (() => {
  let expiresAt: number | null = null; // absolute ms
  let remaining: number | null = null; // seconds
  let timer: ReturnType<typeof setInterval> | null = null;
  const listeners = new Set<() => void>();

  function notify() { listeners.forEach((fn) => fn()); }

  function tick() {
    if (expiresAt == null) return;
    remaining = Math.max(0, Math.ceil((expiresAt - Date.now()) / 1000));
    notify();
    if (remaining <= 0 && timer) { clearInterval(timer); timer = null; }
  }

  function start(expiresAtMs: number) {
    stop();
    expiresAt = expiresAtMs;
    tick();
    timer = setInterval(tick, 1000);
  }

  function stop() {
    if (timer) { clearInterval(timer); timer = null; }
    expiresAt = null;
    remaining = null;
    notify();
  }

  return {
    start,
    stop,
    subscribe: (cb: () => void) => { listeners.add(cb); return () => { listeners.delete(cb); }; },
    getSnapshot: () => remaining,
  };
})();

function useProofCountdown(testProof: string | null, proofExpiresAt: number | null): number | null {
  useEffect(() => {
    if (testProof && proofExpiresAt) {
      proofCountdownStore.start(proofExpiresAt);
    } else {
      proofCountdownStore.stop();
    }
    return () => proofCountdownStore.stop();
  }, [testProof, proofExpiresAt]);

  return useSyncExternalStore(proofCountdownStore.subscribe, proofCountdownStore.getSnapshot);
}

interface ConnectionTestStepProps {
  formData: OnboardingFormData;
  updateField: <K extends keyof OnboardingFormData>(key: K, value: OnboardingFormData[K]) => void;
  /** 一次性应用模型选择（模型 / 厂商 / 探针模板），并作废已有测试证明 */
  applyModelSelection: (selection: ModelSelection) => void;
  meta: OnboardingMeta | null;
  testResult: OnboardingTestResult | null;
  testProof: string | null;
  proofExpiresAt: number | null;
  isTesting: boolean;
  onRunTest: () => void;
  onBack: () => void;
  onNext: () => void;
}

const probeStatusConfig: Record<number, { labelKey: string; colorClass: string; Icon: LucideIcon }> = {
  1: { labelKey: 'onboarding.test.statusAvailable', colorClass: 'text-success', Icon: CheckCircle2 },
  2: { labelKey: 'onboarding.test.statusDegraded', colorClass: 'text-warning', Icon: AlertTriangle },
  0: { labelKey: 'onboarding.test.statusUnavailable', colorClass: 'text-danger', Icon: XCircle },
};

/** Step 2: Connection test with API key and base URL. */
export function ConnectionTestStep({
  formData, updateField, applyModelSelection, meta, testResult, testProof, proofExpiresAt,
  isTesting, onRunTest, onBack, onNext,
}: ConnectionTestStepProps) {
  const { t } = useTranslation();
  const [showApiKey, setShowApiKey] = useState(false);
  const proofRemaining = useProofCountdown(testProof, proofExpiresAt);

  const filteredTestTypes = useMemo(() => {
    if (!meta) return [];
    if (!formData.serviceType) return meta.test_types;
    return meta.test_types.filter((tt) => tt.id === formData.serviceType);
  }, [meta, formData.serviceType]);

  const selectedTestType = useMemo(() => {
    if (filteredTestTypes.length === 0) return null;
    return filteredTestTypes.find((tt) => tt.id === formData.testType) ?? filteredTestTypes[0];
  }, [filteredTestTypes, formData.testType]);

  const modelOptions = useMemo(
    () => modelOptionsFor(meta, formData.serviceType),
    [meta, formData.serviceType],
  );
  const requestShapes = useMemo(
    () => requestShapesFor(meta, formData.serviceType),
    [meta, formData.serviceType],
  );

  /** 按厂商分组，组内保持后端给的顺序（后端已按受控词表排好，同厂商条目连续） */
  const groupedModels = useMemo(() => {
    const groups: { vendor: string; options: typeof modelOptions }[] = [];
    for (const option of modelOptions) {
      const last = groups[groups.length - 1];
      if (last && last.vendor === option.vendor) last.options.push(option);
      else groups.push({ vendor: option.vendor, options: [option] });
    }
    return groups;
  }, [modelOptions]);

  const vendorLabel = (code: string) => {
    if (!code) return t('onboarding.connectionTest.modelVendorUnknown');
    const fallback = meta?.model_vendors?.find((v) => v.code === code)?.label ?? code;
    return t(`vendors.${code}`, { defaultValue: fallback });
  };

  const showModelIdInput = needsModelId(modelOptions, formData.modelKey);
  const selectedOption = modelOptions.find((o) => o.key === formData.modelKey);

  const canRunTest = useMemo(() => {
    return (
      formData.baseUrl.trim().length > 0 &&
      formData.apiKey.trim().length > 0 &&
      filteredTestTypes.length > 0 &&
      formData.testVariant.length > 0 &&
      // 自填/可改条目必须真的填了模型 ID——后端对 native 模板漏填模型是硬拒
      (!showModelIdInput || formData.model.trim().length > 0) &&
      !isTesting
    );
  }, [formData.baseUrl, formData.apiKey, formData.testVariant, formData.model,
      filteredTestTypes.length, showModelIdInput, isTesting]);

  const testPassed = useMemo(() => {
    return testResult?.probe_status === 1 && !!testProof;
  }, [testResult, testProof]);

  const proofExpired = proofRemaining !== null && proofRemaining <= 0;
  const canProceed = testPassed && !proofExpired;

  /** test_type 跟随所选服务类型 */
  useEffect(() => {
    if (filteredTestTypes.length === 0) return;
    const matched = filteredTestTypes.find((tt) => tt.id === formData.serviceType) ?? filteredTestTypes[0];
    if (formData.testType !== matched.id) {
      updateField('testType', matched.id);
    }
  }, [filteredTestTypes, formData.serviceType, formData.testType, updateField]);

  /**
   * 模型选择失效时回落到默认。
   *
   * 会失效的情形：换了服务类型（模型目录按 service 分组）、旧草稿里存着一个已下线的模型、
   * 后端把某个模型标成了不开放自助。留着一个不存在的选择，用户会带着空模板去测。
   */
  useEffect(() => {
    if (!meta) return;
    if (isSelectionValid(meta, formData.serviceType, formData.modelKey)) return;
    const fallback = resolveDefaultSelection(meta, formData.serviceType);
    if (fallback) applyModelSelection(fallback);
  }, [meta, formData.serviceType, formData.modelKey, applyModelSelection]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (canProceed) onNext();
  };

  const formatCountdown = (seconds: number): string => {
    const m = Math.floor(seconds / 60);
    const s = seconds % 60;
    return `${m}:${s.toString().padStart(2, '0')}`;
  };

  if (!meta) {
    return (
      <div className="bg-surface border border-muted rounded-lg p-8 text-center">
        <p className="text-secondary">{t('onboarding.loading')}</p>
      </div>
    );
  }

  return (
    <form onSubmit={handleSubmit} className="bg-surface border border-muted rounded-lg p-6 space-y-6">
      <h2 className="text-xl font-semibold text-primary">
        {t('onboarding.connectionTest.title')}
      </h2>
      <p className="text-sm text-secondary">{t('onboarding.connectionTest.description')}</p>

      {/* Test type info + variant selector */}
      {selectedTestType && (
        <div className="space-y-3">
          <div className="p-3 rounded-lg bg-elevated border border-muted">
            <div className="text-xs text-muted mb-0.5">
              {t('onboarding.connectionTest.testType', { defaultValue: '服务类型' })}
            </div>
            <div className="text-sm text-primary font-medium">
              {selectedTestType.name || selectedTestType.id}
            </div>
          </div>
          {/* 选模型：按厂商分组；「其他」进入自填模型 ID 分支 */}
          {modelOptions.length > 0 && (
            <div>
              <label htmlFor="ob-model" className={labelClass}>
                {t('onboarding.connectionTest.model')}
                <span className="text-danger ml-0.5">*</span>
              </label>
              <select
                id="ob-model"
                value={formData.modelKey}
                onChange={(e) => {
                  const key = e.target.value;
                  if (key === CUSTOM_MODEL_KEY) {
                    applyModelSelection(selectionForCustom(requestShapes));
                    return;
                  }
                  const option = modelOptions.find((o) => o.key === key);
                  if (option) applyModelSelection(selectionFromOption(option));
                }}
                disabled={isTesting}
                className={selectClass}
              >
                {groupedModels.map((group) => (
                  <optgroup key={group.vendor || 'unknown'} label={vendorLabel(group.vendor)}>
                    {group.options.map((option) => (
                      <option key={option.key} value={option.key}>{option.label}</option>
                    ))}
                  </optgroup>
                ))}
                {requestShapes.length > 0 && (
                  <option value={CUSTOM_MODEL_KEY}>
                    {t('onboarding.connectionTest.modelCustom')}
                  </option>
                )}
              </select>
              <p className={hintClass}>{t('onboarding.connectionTest.modelHint')}</p>
            </div>
          )}

          {/* 目录条目：亮出实际会请求的模型 ID，免得用户以为选的是别的 */}
          {!showModelIdInput && selectedOption && (
            <div className="text-xs text-muted">
              {t('onboarding.connectionTest.requestModel')}：
              <span className="font-mono text-secondary ml-1">{selectedOption.request_model}</span>
            </div>
          )}

          {/* 自填 / 第一方厂商模型：模型 ID + 厂商 + 请求形态 */}
          {showModelIdInput && (
            <div className="space-y-3 p-3 rounded-lg bg-elevated border border-muted">
              <div>
                <label htmlFor="ob-model-id" className={labelClass}>
                  {t('onboarding.connectionTest.modelId')}
                  <span className="text-danger ml-0.5">*</span>
                </label>
                <input
                  id="ob-model-id"
                  type="text"
                  value={formData.model}
                  onChange={(e) => updateField('model', e.target.value)}
                  placeholder="glm-5.2"
                  disabled={isTesting}
                  className={`${inputClass()} font-mono`}
                />
                <p className={hintClass}>{t('onboarding.connectionTest.modelIdHint')}</p>
              </div>

              <div>
                <label htmlFor="ob-model-vendor" className={labelClass}>
                  {t('onboarding.connectionTest.modelVendor')}
                </label>
                <select
                  id="ob-model-vendor"
                  value={formData.modelVendor}
                  onChange={(e) => updateField('modelVendor', e.target.value)}
                  disabled={isTesting}
                  className={selectClass}
                >
                  <option value="">{t('onboarding.connectionTest.modelVendorUnknown')}</option>
                  {(meta.model_vendors ?? []).map((vendor) => (
                    <option key={vendor.code} value={vendor.code}>{vendorLabel(vendor.code)}</option>
                  ))}
                </select>
                <p className={hintClass}>{t('onboarding.connectionTest.modelVendorHint')}</p>
              </div>

              {requestShapes.length > 1 && (
                <div>
                  <label htmlFor="ob-request-shape" className={labelClass}>
                    {t('onboarding.connectionTest.requestShape')}
                  </label>
                  <select
                    id="ob-request-shape"
                    value={formData.testVariant}
                    onChange={(e) => updateField('testVariant', e.target.value)}
                    disabled={isTesting}
                    className={selectClass}
                  >
                    {requestShapes.map((shape) => (
                      <option key={shape.template} value={shape.template}>{shape.label}</option>
                    ))}
                  </select>
                  <p className={hintClass}>{t('onboarding.connectionTest.requestShapeHint')}</p>
                </div>
              )}
            </div>
          )}
        </div>
      )}

      {/* Base URL */}
      <div>
        <label htmlFor="ob-base-url" className={labelClass}>
          {t('onboarding.connectionTest.baseUrl')}
          <span className="text-danger ml-0.5">*</span>
        </label>
        <input
          id="ob-base-url"
          type="url"
          required
          value={formData.baseUrl}
          onChange={(e) => updateField('baseUrl', e.target.value)}
          placeholder="https://api.example.com"
          disabled={isTesting}
          className={inputClass()}
        />
        <p className={hintClass}>{t('onboarding.connectionTest.baseUrlHint')}</p>
      </div>

      {/* API Key with show/hide toggle */}
      <div>
        <label htmlFor="ob-api-key" className={labelClass}>
          {t('onboarding.connectionTest.apiKey')}
          <span className="text-danger ml-0.5">*</span>
        </label>
        <div className="relative">
          <input
            id="ob-api-key"
            type={showApiKey ? 'text' : 'password'}
            required
            value={formData.apiKey}
            onChange={(e) => updateField('apiKey', e.target.value)}
            placeholder={t('onboarding.connectionTest.apiKeyPlaceholder')}
            disabled={isTesting}
            className={`${inputClass()} pr-12`}
          />
          <button
            type="button"
            onClick={() => setShowApiKey((prev) => !prev)}
            className="absolute right-3 top-1/2 -translate-y-1/2 text-muted hover:text-secondary transition-colors"
            aria-label={showApiKey ? t('onboarding.connectionTest.hideApiKey') : t('onboarding.connectionTest.showApiKey')}
          >
            {showApiKey ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
          </button>
        </div>
        <p className={hintClass}>{t('onboarding.connectionTest.apiKeyHint')}</p>
      </div>

      {/* Run Test button */}
      <button
        type="button"
        onClick={onRunTest}
        disabled={!canRunTest}
        className="flex items-center justify-center gap-2 w-full px-6 py-3 bg-accent/10 border border-accent/40 text-accent rounded-lg font-medium hover:bg-accent/20 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
      >
        {isTesting ? (
          <>
            <Loader2 className="w-4 h-4 animate-spin" />
            {t('onboarding.connectionTest.testing')}
          </>
        ) : (
          <>
            <Play className="w-4 h-4" />
            {t(testResult ? 'onboarding.connectionTest.rerunTest' : 'onboarding.connectionTest.runTest')}
          </>
        )}
      </button>

      {/* Test result panel */}
      {testResult && (
        <div className="bg-elevated border border-muted rounded-lg p-5 space-y-3">
          <h3 className="text-sm font-semibold text-primary">
            {t('onboarding.connectionTest.resultTitle')}
          </h3>

          <div className="space-y-2">
            {/* Probe status */}
            {testResult.probe_status !== undefined && (
              <div className="flex items-center justify-between">
                <span className="text-sm text-secondary">{t('onboarding.connectionTest.probeStatus')}</span>
                {(() => {
                  const cfg = probeStatusConfig[testResult.probe_status];
                  const StatusIcon = cfg?.Icon ?? CircleHelp;
                  return (
                    <span className={`flex items-center gap-1.5 text-sm font-medium ${cfg?.colorClass ?? 'text-muted'}`}>
                      <StatusIcon className="w-4 h-4 flex-shrink-0" aria-hidden="true" />
                      {t(cfg?.labelKey ?? 'onboarding.test.statusUnknown')}
                    </span>
                  );
                })()}
              </div>
            )}

            {/* Latency */}
            {testResult.latency != null && testResult.latency > 0 && (
              <div className="flex items-center justify-between">
                <span className="text-sm text-secondary">{t('onboarding.connectionTest.latency')}</span>
                <span className="text-sm text-primary font-mono">{testResult.latency} ms</span>
              </div>
            )}

            {/* HTTP code */}
            {testResult.http_code != null && testResult.http_code > 0 && (
              <div className="flex items-center justify-between">
                <span className="text-sm text-secondary">{t('onboarding.connectionTest.httpCode')}</span>
                <span className="text-sm text-primary font-mono">{testResult.http_code}</span>
              </div>
            )}

            {/* Sub status */}
            {testResult.sub_status && (
              <div className="flex items-center justify-between">
                <span className="text-sm text-secondary">{t('onboarding.connectionTest.subStatus')}</span>
                {/* 走全局 subStatus 词表翻译；未知码回退原始字符串，避免裸码泄漏或缺键报错 */}
                <span className="text-sm text-primary">{t(`subStatus.${testResult.sub_status}`, testResult.sub_status)}</span>
              </div>
            )}

            {/* Error message（网络层错误等；HTTP 4xx/5xx 时通常为空，详情走 response_snippet） */}
            {testResult.error_message && (
              <div className="mt-2 p-3 bg-danger/10 border border-danger/20 rounded">
                <p className="text-sm font-medium text-danger mb-1">{t('onboarding.connectionTest.error')}</p>
                <p className="text-xs text-secondary font-mono break-all">{testResult.error_message}</p>
              </div>
            )}

            {/* 上游响应详情：失败时由后端截取前 512 字节，含 HTTP 4xx/5xx 的具体报错体；
                与 error_message 相同则不重复展示（网络错误时两者同源） */}
            {testResult.response_snippet && testResult.response_snippet !== testResult.error_message && (
              <div className="mt-2 p-3 bg-danger/10 border border-danger/20 rounded">
                <p className="text-sm font-medium text-danger mb-1">
                  {t('onboarding.connectionTest.responseDetail', { defaultValue: '响应详情' })}
                </p>
                <p className="text-xs text-secondary font-mono break-all whitespace-pre-wrap">{testResult.response_snippet}</p>
              </div>
            )}
          </div>
        </div>
      )}

      {/* Test proof countdown */}
      {testProof && proofRemaining !== null && (
        <div className={`flex items-center gap-2 p-3 rounded-lg text-sm ${
          proofExpired
            ? 'bg-danger/10 border border-danger/20 text-danger'
            : proofRemaining <= 60
              ? 'bg-warning/10 border border-warning/20 text-warning'
              : 'bg-success/10 border border-success/20 text-success'
        }`}>
          <Clock className="w-4 h-4 flex-shrink-0" />
          {proofExpired ? (
            <span>{t('onboarding.connectionTest.proofExpired')}</span>
          ) : (
            <span>
              {t('onboarding.connectionTest.proofValid', { time: formatCountdown(proofRemaining) })}
            </span>
          )}
        </div>
      )}

      {/* Navigation buttons */}
      <div className="flex justify-between pt-2">
        <button
          type="button"
          onClick={onBack}
          className={secondaryButtonClass}
        >
          <ChevronLeft className="w-4 h-4" />
          {t('onboarding.back')}
        </button>
        <button
          type="submit"
          disabled={!canProceed}
          className={primaryButtonClass}
        >
          {t('onboarding.next')}
          <ChevronRight className="w-4 h-4" />
        </button>
      </div>
    </form>
  );
}
