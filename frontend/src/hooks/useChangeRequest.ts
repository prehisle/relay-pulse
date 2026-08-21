import { useState, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { apiPost, ApiError } from '../utils/apiClient';
import { canonicalEndpointUrl } from '../utils/endpointUrl';
import { normalizeDisplayName } from '../utils/displayName';
import type {
  AuthCandidate,
  AuthResponse,
  SubmitChangeRequest,
  SubmitChangeResponse,
} from '../types/change';

export type ChangeStep = 'auth' | 'edit' | 'test' | 'review' | 'done';

/**
 * 变更是否需要探测测试（并触发反作弊条款重确认）：改动了被探测目标 base_url，或提供了新 API Key。
 * 纯函数（不依赖任何 hook 状态），供本 hook 与复核页 ReviewStep 共用，保证两端判据字节等价，
 * 避免二处独立表达式漂移。与后端 change.Submit 的门控判据保持一致。
 */
export function changeRequiresTest(changes: Record<string, string>, newApiKey: string): boolean {
  return 'base_url' in changes || newApiKey !== '';
}

interface InlineTestResult {
  probe_status?: number;
  sub_status?: string;
  latency?: number;
  http_code?: number;
  error_message?: string;
  response_snippet?: string;
  probe_id: string;
  test_proof?: string;
  proof_expires_at?: number;
}

export function useChangeRequest() {
  const { t, i18n } = useTranslation();

  // 步骤控制
  const [step, setStepRaw] = useState<ChangeStep>('auth');

  // Auth 步骤
  const [apiKey, setApiKey] = useState('');
  const [candidates, setCandidates] = useState<AuthCandidate[]>([]);
  const [selectedCandidate, setSelectedCandidateRaw] = useState<AuthCandidate | null>(null);
  const [selectedVariant, setSelectedVariant] = useState('');
  const [isAuthenticating, setIsAuthenticating] = useState(false);

  // Edit 步骤
  const [changes, setChanges] = useState<Record<string, string>>({});
  const [newApiKey, setNewApiKey] = useState('');

  // Test 步骤
  const [isTesting, setIsTesting] = useState(false);
  const [testJobId, setTestJobId] = useState('');
  const [testResult, setTestResult] = useState<InlineTestResult | null>(null);
  const [testProof, setTestProof] = useState('');
  // proof 绝对过期时间（ms）。来源是后端按真实 proof_ttl 下发的 proof_expires_at，
  // 避免前端硬编码 TTL 与后端漂移；提交前据此做本地预检，与 onboarding 流程对齐。
  const [proofExpiresAt, setProofExpiresAt] = useState<number | null>(null);

  // Submit
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [publicId, setPublicId] = useState('');

  // 通用
  const [error, setError] = useState<string | null>(null);

  // 重置测试状态（共享工具）
  const resetTestState = useCallback(() => {
    setIsTesting(false);
    setTestJobId('');
    setTestResult(null);
    setTestProof('');
    setProofExpiresAt(null);
  }, []);

  // 解析候选通道的默认变体（优先 default_test_variant，兜底首个变体）
  const resolveDefaultVariant = useCallback((c: AuthCandidate | null): string => {
    if (!c) return '';
    if (c.default_test_variant) return c.default_test_variant;
    const variants = c.test_variants ?? [];
    if (variants.length > 0) {
      const sorted = variants.slice().sort((a, b) => a.order - b.order);
      return sorted[0].id;
    }
    return '';
  }, []);

  // 切换候选通道时同步重置变体和测试状态
  const setSelectedCandidate = useCallback((c: AuthCandidate | null) => {
    setSelectedCandidateRaw(c);
    setSelectedVariant(resolveDefaultVariant(c));
    resetTestState();
  }, [resolveDefaultVariant, resetTestState]);

  // 变体切换时重置测试状态，防止提交与实际测试不匹配的 variant 审计值
  const handleSetSelectedVariant = useCallback((v: string) => {
    setSelectedVariant(v);
    resetTestState();
  }, [resetTestState]);

  // 切步骤时自动清理测试状态（离开 test 步骤）
  const setStep = useCallback((next: ChangeStep) => {
    setStepRaw(prev => {
      if (prev === 'test' && next !== 'test') {
        setIsTesting(false);
      }
      return next;
    });
  }, []);

  // 认证
  const authenticate = useCallback(async () => {
    if (!apiKey || apiKey.length < 10) {
      setError(t('changeRequest.auth.invalidKey'));
      return;
    }
    setIsAuthenticating(true);
    setError(null);
    // 清理上一次的编辑状态，防止换 Key 后沿用旧数据
    setSelectedCandidateRaw(null);
    setSelectedVariant('');
    setChanges({});
    setNewApiKey('');
    resetTestState();
    try {
      const resp = await apiPost<AuthResponse>('/api/change/auth', { api_key: apiKey });
      setCandidates(resp.candidates);
      if (resp.candidates.length === 1) {
        setSelectedCandidate(resp.candidates[0]);
      }
      setStep('edit');
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t('changeRequest.auth.authFailed'));
    } finally {
      setIsAuthenticating(false);
    }
  }, [apiKey, t, setStep, setSelectedCandidate, resetTestState]);

  // 更新变更字段
  const updateChange = useCallback((field: string, value: string) => {
    setChanges(prev => {
      const next = { ...prev };
      if (value === '') {
        delete next[field];
      } else {
        next[field] = value;
      }
      return next;
    });
    // base_url 是被探测的目标，一旦改动，已有 proof（绑定旧 URL）即失效，清掉强制重测。
    if (field === 'base_url') {
      resetTestState();
    }
  }, [resetTestState]);

  // 新 API Key 改动同样会让 proof（绑定 key 指纹）失效，清测试状态强制重测。
  const handleSetNewApiKey = useCallback((v: string) => {
    setNewApiKey(v);
    resetTestState();
  }, [resetTestState]);

  // 判断是否需要测试（base_url 变更或提供新 API Key 时需要通过探测测试）
  const requiresTest = changeRequiresTest(changes, newApiKey);

  // 进入测试/提交步骤
  const proceedFromEdit = useCallback(() => {
    // 规范化展示名（与后端 displayname 对齐；剥首尾不可见字符）——保证 Review 展示、提交 payload、
    // 后端落盘看到同一字符串；规范化后为空的展示名视为「未改动」丢弃。非法值已被 EditStep 禁用「下一步」挡下。
    const normalized: Record<string, string> = {};
    for (const [k, v] of Object.entries(changes)) {
      if (k === 'provider_name' || k === 'channel_name') {
        const norm = normalizeDisplayName(v);
        if (norm !== '') normalized[k] = norm;
      } else {
        normalized[k] = v;
      }
    }
    if (Object.keys(normalized).length === 0 && newApiKey === '') {
      setError(t('changeRequest.edit.noChanges'));
      return;
    }
    setChanges(normalized);
    setError(null);
    if (requiresTest) {
      setStep('test');
    } else {
      setStep('review');
    }
  }, [changes, newApiKey, requiresTest, t, setStep]);

  // 运行测试
  const runTest = useCallback(async () => {
    if (!selectedCandidate) return;
    setIsTesting(true);
    setError(null);
    setTestResult(null);
    setTestProof('');
    setProofExpiresAt(null);

    try {
      // 确定测试参数：使用变更后的值
      // 与提交时同一口径：后端按「同一接入点」比较 test_api_url 与将要落地的 base_url，
      // 且 proof 绑的就是这个串——两处各自处理必然对不上。
      const testBaseUrl = canonicalEndpointUrl(changes['base_url'] || selectedCandidate.base_url);
      const testKey = newApiKey || apiKey;

      // 调用变更请求专用内联探测 API（同步返回结果）。
      // 走 /api/change/test 而非 /api/onboarding/test：变更流程只依赖 change_requests
      // 开关，未启用 onboarding 时也能测通（两端共用探测编排，proof 同源可被 submit 验证）。
      // target_key 必传：轮换 key 时这里发的是**新** key，它还没进后端的 API Key 索引，
      // 服务端只能靠 target_key 定位这条通道、取它正在跑的模型来探测（同 submit 的用法）。
      const resp = await apiPost<InlineTestResult>('/api/change/test', {
        service_type: selectedCandidate.test_type || selectedCandidate.service,
        template_name: selectedVariant || '',
        base_url: testBaseUrl,
        api_key: testKey,
        target_key: selectedCandidate.monitor_key,
      });

      setTestJobId(resp.probe_id);
      setTestResult(resp);

      if (resp.probe_status === 1 && resp.test_proof) {
        setTestProof(resp.test_proof);
        // proof_expires_at 为 Unix 秒；缺省时（前后端同体部署理论上不会）置 null。
        setProofExpiresAt(resp.proof_expires_at ? resp.proof_expires_at * 1000 : null);
      }
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t('changeRequest.test.requestFailed'));
    } finally {
      setIsTesting(false);
    }
  }, [selectedCandidate, selectedVariant, changes, newApiKey, apiKey, t]);

  // 提交变更
  const submit = useCallback(async (agreementAccepted: boolean) => {
    if (!selectedCandidate) return;
    // 提交前本地预检 proof 是否过期（与 onboarding 流程对齐）。后端 Verify 仍是权威校验，
    // 这里只为在打服务器前给出清晰的「请重新测试」提示，避免一次无谓往返。
    // 必须 gate 在 requiresTest 上：不需要测试的变更没有 proof，残留的 proofExpiresAt 不应误拦。
    if (requiresTest && testProof && proofExpiresAt !== null && Date.now() >= proofExpiresAt) {
      setError(t('changeRequest.test.proofExpired'));
      return;
    }
    setIsSubmitting(true);
    setError(null);

    try {
      const testBaseUrl = canonicalEndpointUrl(changes['base_url'] || selectedCandidate.base_url);

      const req: SubmitChangeRequest = {
        api_key: apiKey,
        target_key: selectedCandidate.monitor_key,
        proposed_changes: changes,
        locale: i18n.language,
      };

      if (newApiKey) {
        req.new_api_key = newApiKey;
      }

      if (requiresTest && testProof) {
        req.test_proof = testProof;
        req.test_job_id = testJobId;
        req.test_type = selectedCandidate.test_type || selectedCandidate.service;
        req.test_variant = selectedVariant || undefined;
        req.test_api_url = testBaseUrl;
        req.test_latency = testResult?.latency;
        req.test_http_code = testResult?.http_code;
      }

      if (requiresTest) {
        req.agreement_accepted = agreementAccepted;
      }

      const resp = await apiPost<SubmitChangeResponse>('/api/change/submit', req);
      setPublicId(resp.public_id);
      setStep('done');
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t('changeRequest.review.submitFailed'));
    } finally {
      setIsSubmitting(false);
    }
  }, [selectedCandidate, selectedVariant, changes, newApiKey, apiKey, requiresTest, testProof, testJobId, testResult, proofExpiresAt, i18n.language, t, setStep]);

  // 重置
  const reset = useCallback(() => {
    setStepRaw('auth');
    setApiKey('');
    setCandidates([]);
    setSelectedCandidateRaw(null);
    setSelectedVariant('');
    setChanges({});
    setNewApiKey('');
    setIsTesting(false);
    setTestJobId('');
    setTestResult(null);
    setTestProof('');
    setProofExpiresAt(null);
    setIsSubmitting(false);
    setPublicId('');
    setError(null);
  }, []);

  return {
    // 步骤
    step,
    setStep,

    // Auth
    apiKey,
    setApiKey,
    candidates,
    selectedCandidate,
    setSelectedCandidate,
    selectedVariant,
    setSelectedVariant: handleSetSelectedVariant,
    isAuthenticating,
    authenticate,

    // Edit
    changes,
    updateChange,
    newApiKey,
    setNewApiKey: handleSetNewApiKey,
    requiresTest,
    proceedFromEdit,

    // Test
    isTesting,
    testResult,
    testProof,
    runTest,

    // Submit
    isSubmitting,
    publicId,
    submit,

    // Common
    error,
    setError,
    reset,
  };
}
