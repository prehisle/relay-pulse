import { useState, useCallback, useEffect } from 'react';
import { apiGet, apiPost, ApiError } from '../utils/apiClient';
import { normalizeDisplayName } from '../utils/displayName';
import { canonicalEndpointUrl } from '../utils/endpointUrl';
import type { ModelSelection } from '../components/onboarding/modelSelection';
import type {
  OnboardingMeta,
  OnboardingFormData,
  OnboardingTestResult,
  SubmitOnboardingRequest,
  SubmitOnboardingResponse,
} from '../types/onboarding';

const DRAFT_KEY = 'relay-pulse-onboarding-draft';

/** 加载保存的草稿，剔除已废弃字段残留 */
function loadDraft(): Partial<OnboardingFormData> {
  try {
    const raw = localStorage.getItem(DRAFT_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as Record<string, unknown>;
      delete parsed.apiKey;
      delete parsed.contactInfo;
      delete parsed.identity;
      // 自定义通道类型 X 已下线：剔除残留字段并归一化旧草稿，避免提交被后端拒绝
      delete parsed.channelTypeCustom;
      if (parsed.channelType === 'X') parsed.channelType = 'O';
      // 通道来源已收敛为 2-5 位小写受控词表；旧草稿里的长词级联值（subscription/bedrock…）一律丢弃，强制重选
      if (typeof parsed.channelSource !== 'string' || !/^[a-z0-9]{2,5}$/.test(parsed.channelSource)) {
        delete parsed.channelSource;
      }
      // 表单从「选模板」改成「选厂商→选模型」后，旧草稿里的 testVariant 可能指向一个已不
      // 对外开放的模板；模型选择状态也不存在。一律丢弃，让第二步按目录重新解析默认值。
      delete parsed.testVariant;
      return parsed as Partial<OnboardingFormData>;
    }
  } catch { /* ignore */ }
  return {};
}

/** 保存草稿（排除敏感字段） */
function saveDraft(data: Partial<OnboardingFormData>) {
  try {
    const safe = { ...data } as Record<string, unknown>;
    delete safe.apiKey;
    localStorage.setItem(DRAFT_KEY, JSON.stringify(safe));
  } catch { /* ignore */ }
}

/** 清除草稿 */
function clearDraft() {
  try { localStorage.removeItem(DRAFT_KEY); } catch { /* ignore */ }
}

const defaultForm: OnboardingFormData = {
  providerName: '',
  websiteUrl: '',
  category: 'commercial',
  serviceType: 'cc',
  sponsorLevel: '',
  channelType: 'O',
  channelSource: '',
  channelGroup: 'main',
  // 旧版 localStorage 草稿无此字段时由 defaultForm 铺底为空串，合并草稿即自然兼容
  channelName: '',
  agreementAccepted: false,
  baseUrl: '',
  apiKey: '',
  testType: '',
  testVariant: '',
  modelKey: '',
};

export function useOnboarding() {
  const [step, setStep] = useState(1);
  const [meta, setMeta] = useState<OnboardingMeta | null>(null);
  const [formData, setFormData] = useState<OnboardingFormData>(() => ({
    ...defaultForm,
    ...loadDraft(),
  }));

  // Test state
  const [testJobId, setTestJobId] = useState<string | null>(null);
  const [testResult, setTestResult] = useState<OnboardingTestResult | null>(null);
  const [testProof, setTestProof] = useState<string | null>(null);
  const [testPassedAt, setTestPassedAt] = useState<number | null>(null);
  // proof 绝对过期时间（ms）。来源是后端按真实 proof_ttl 下发的 proof_expires_at，
  // 取代前端硬编码 TTL，避免与后端 proof_ttl 漂移导致「前端显示有效、后端已过期」。
  const [proofExpiresAt, setProofExpiresAt] = useState<number | null>(null);
  const [isTesting, setIsTesting] = useState(false);

  // Agreement clauses — lifted here so they survive step back-navigation
  const [checkedClauses, setCheckedClauses] = useState<Record<string, boolean>>({});

  // Submit state
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitResult, setSubmitResult] = useState<SubmitOnboardingResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [metaError, setMetaError] = useState<string | null>(null);

  // Load meta on mount
  useEffect(() => {
    apiGet<OnboardingMeta>('/api/onboarding/meta')
      .then(setMeta)
      .catch((e) => {
        const msg = e instanceof ApiError ? e.message : '加载表单配置失败';
        setMetaError(msg);
      });
  }, []);

  // Save draft on form change
  useEffect(() => { saveDraft(formData); }, [formData]);

  const updateField = useCallback(<K extends keyof OnboardingFormData>(key: K, value: OnboardingFormData[K]) => {
    const resetTestState = key === 'serviceType' && formData.serviceType !== value;
    // proof 绑定了 apiKey 指纹、base_url 与**探针模板**（后端 apikey.ProofClaims），任一项变了
    // 旧 proof 就不再对应这次要提交的东西，必须清掉重测——否则用户会带着一个必然验签失败的
    // proof 走到最后一步才被拒。模型不单列：它由所选模板唯一决定，换模型即换模板。
    const invalidateProof =
      key === 'baseUrl' || key === 'apiKey' || key === 'testVariant' || key === 'modelKey';

    setFormData(prev => {
      const next = { ...prev, [key]: value } as OnboardingFormData;
      if (resetTestState) {
        next.testType = '';
        next.testVariant = '';
        // 模型目录按 service 分组，换了 service 旧选择必然失效
        next.modelKey = '';
      }
      return next;
    });

    if (resetTestState) {
      setTestJobId(null);
      setTestResult(null);
      setTestProof(null);
      setTestPassedAt(null);
      setProofExpiresAt(null);
      setIsTesting(false);
    } else if (invalidateProof && testProof) {
      setTestJobId(null);
      setTestResult(null);
      setTestProof(null);
      setTestPassedAt(null);
      setProofExpiresAt(null);
    }

    setError(null);
  }, [formData.serviceType, testProof]);

  /**
   * 应用一次模型选择（选项键与探针模板一次改完）。
   *
   * 不逐字段调 updateField：那会连发多次状态更新，且每次都各自判一遍要不要清测试证明，
   * 中间态（新模型 + 旧模板）也会短暂存在。模型选择是一个原子动作，proof 必然作废。
   */
  const applyModelSelection = useCallback((selection: ModelSelection) => {
    setFormData(prev => ({ ...prev, ...selection }));
    setTestJobId(null);
    setTestResult(null);
    setTestProof(null);
    setTestPassedAt(null);
    setProofExpiresAt(null);
    setError(null);
  }, []);

  const goToStep = useCallback((s: number) => {
    setStep(s);
    setError(null);
  }, []);

  /** 运行连通性测试（内联探测，同步返回） */
  const runTest = useCallback(async () => {
    setIsTesting(true);
    setError(null);
    setTestJobId(null);
    setTestResult(null);
    setTestProof(null);
    setTestPassedAt(null);
    setProofExpiresAt(null);

    try {
      const resp = await apiPost<OnboardingTestResult>('/api/onboarding/test', {
        service_type: formData.serviceType,
        template_name: formData.testVariant || formData.testType,
        // 与提交时同一口径：测试打的地址就是将来落地的 base_url，且 proof 绑的正是这个串。
        base_url: canonicalEndpointUrl(formData.baseUrl),
        api_key: formData.apiKey,
      });

      setTestResult(resp);
      setTestJobId(resp.probe_id);
      if (resp.test_proof) {
        setTestProof(resp.test_proof);
        setTestPassedAt(Date.now());
        // proof_expires_at 为 Unix 秒；缺省时（理论上不会，前后端同体部署）置 null
        setProofExpiresAt(resp.proof_expires_at ? resp.proof_expires_at * 1000 : null);
      }
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '测试请求失败');
    } finally {
      setIsTesting(false);
    }
  }, [formData.apiKey, formData.baseUrl, formData.serviceType, formData.testType, formData.testVariant]);

  /** 提交申请 */
  const submit = useCallback(async () => {
    if (!testProof || !testJobId || !testResult) {
      setError('请先通过连通性测试');
      return;
    }
    if (proofExpiresAt && Date.now() >= proofExpiresAt) {
      setError('测试证明已过期，请返回上一步重新测试');
      return;
    }

    setIsSubmitting(true);
    setError(null);

    // base_url 与 test_api_url 必须是同一个串：后端按「同一接入点」比较，且 proof 绑定的
    // 就是测试时用的那个地址。两处各自处理必然对不上（历史上正是这个 bug）。
    const baseUrl = canonicalEndpointUrl(formData.baseUrl);

    try {
      const req: SubmitOnboardingRequest = {
        provider_name: normalizeDisplayName(formData.providerName),
        website_url: canonicalEndpointUrl(formData.websiteUrl),
        category: formData.category,
        service_type: formData.serviceType,
        template_name: formData.testVariant || formData.testType,
        sponsor_level: formData.sponsorLevel,
        channel_type: formData.channelType,
        channel_source: formData.channelSource,
        channel_group: formData.channelGroup.trim() || 'main',
        channel_name: normalizeDisplayName(formData.channelName),
        base_url: baseUrl,
        api_key: formData.apiKey,
        test_proof: testProof,
        test_job_id: testJobId,
        test_type: formData.testType,
        test_api_url: baseUrl,
        test_latency: testResult.latency ?? 0,
        test_http_code: testResult.http_code ?? 0,
        locale: navigator.language || 'zh-CN',
        agreement_accepted: formData.agreementAccepted,
      };

      const resp = await apiPost<SubmitOnboardingResponse>('/api/onboarding/submit', req);
      setSubmitResult(resp);
      clearDraft();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '提交失败');
    } finally {
      setIsSubmitting(false);
    }
  }, [formData, testProof, testJobId, testResult, proofExpiresAt]);

  const toggleClause = useCallback((key: string) => {
    setCheckedClauses(prev => ({ ...prev, [key]: !prev[key] }));
  }, []);

  const reset = useCallback(() => {
    setStep(1);
    setFormData(defaultForm);
    setTestJobId(null);
    setTestResult(null);
    setTestProof(null);
    setTestPassedAt(null);
    setProofExpiresAt(null);
    setCheckedClauses({});
    setSubmitResult(null);
    setError(null);
    clearDraft();
  }, []);

  return {
    step,
    meta,
    metaError,
    formData,
    testJobId,
    testResult,
    testProof,
    testPassedAt,
    proofExpiresAt,
    isTesting,
    isSubmitting,
    submitResult,
    error,
    checkedClauses,
    toggleClause,
    updateField,
    applyModelSelection,
    goToStep,
    runTest,
    submit,
    reset,
  };
}
