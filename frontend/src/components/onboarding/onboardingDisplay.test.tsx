// @vitest-environment jsdom
//
// 收录向导 step2/step3 的显示正确性守护。这两步在浏览器里受 step2 真探测闸
// 把守（无有效上游 key 时 playwright 到不了 step3），过去多次只能靠 tsc + 肉眼，
// 导致「服务类型 vs 请求模板」标签错配这类问题漏到生产才被发现。本测试用
// react-dom/client 在 jsdom 中真实渲染组件（零新依赖，不引 testing-library），
// 锁定三处显示契约：
//   #1 step2 探测失败渲染上游 response_snippet（HTTP 4xx/5xx 详情）
//   #2 step3 赞助等级附等级代码「脉冲链路（pulse）」
//   #3 step3 连接摘要那行标签是「请求模板」而非「服务类型」
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { I18nextProvider } from 'react-i18next';
import { MemoryRouter } from 'react-router-dom';
import { describe, it, expect, beforeAll } from 'vitest';
import type { ReactNode } from 'react';
import i18n from '../../i18n';
import { ConfirmStep } from './ConfirmStep';
import { ConnectionTestStep } from './ConnectionTestStep';
import { ProviderInfoStep } from './ProviderInfoStep';
import type { OnboardingFormData, OnboardingMeta, OnboardingTestResult } from '../../types/onboarding';

// React 19 的 act 需要此全局标记，否则会告警（无 testing-library 自动设置）
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

/** 在 jsdom 中真实挂载组件并返回其 innerHTML（含 i18n + Router 上下文）。 */
function renderHTML(node: ReactNode): string {
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <MemoryRouter>{node}</MemoryRouter>
      </I18nextProvider>,
    );
  });
  const html = container.innerHTML;
  act(() => root.unmount());
  container.remove();
  return html;
}

const noop = () => {};
const updateField = noop as <K extends keyof OnboardingFormData>(
  key: K,
  value: OnboardingFormData[K],
) => void;

const baseFormData: OnboardingFormData = {
  providerName: 'TestProbe',
  websiteUrl: 'https://example.com',
  category: 'commercial',
  serviceType: 'cc',
  sponsorLevel: '', // 自助收录恒为空 → 回退展示 pulse
  channelType: 'O',
  channelSource: 'max',
  channelGroup: 'main',
  channelName: '',
  agreementAccepted: false,
  baseUrl: 'https://api.example.com',
  apiKey: 'sk-test-1234567890abcd',
  testType: 'cc',
  testVariant: 'cc-haiku-arith',
  modelKey: '',
  model: '',
  modelVendor: '',
};

const meta: OnboardingMeta = {
  service_types: ['cc'],
  sponsor_levels: [],
  channel_types: [],
  channel_sources_by_service: { cc: [] },
  channel_type_allowed_categories: {
    O: ['subscription', 'official', 'cloud'],
    R: ['reverse'],
    M: ['mixed'],
  },
  channel_group_rule: { pattern: '^[a-z0-9]{1,8}$', default: 'main', max_length: 8 },
  test_types: [
    {
      id: 'cc',
      name: 'Claude Code (cc)',
      description: '',
      default_variant: 'cc-haiku-arith',
      variants: [
        { id: 'cc-haiku-arith', order: 1 },
        { id: 'cc-haiku-titlegen', order: 6 },
      ],
    },
  ],
  contact_info: 'QQ:18058344',
  model_vendors: [{ code: 'anthropic', label: 'Anthropic', icon_key: 'anthropic' }],
  models_by_service: {},
};

beforeAll(async () => {
  await i18n.changeLanguage('zh-CN');
});

describe('ConfirmStep 摘要显示（step3）', () => {
  const html = (meta: OnboardingMeta | null = null) =>
    renderHTML(
      <ConfirmStep
        formData={baseFormData}
        meta={meta}
        updateField={updateField}
        submitResult={null}
        isSubmitting={false}
        testPassedAt={null}
        checkedClauses={{}}
        onToggleClause={noop}
        onSubmit={noop}
        onBack={noop}
        onReset={noop}
      />,
    );

  it('#3 连接摘要展示所选模型的人话名，而不是模板名', () => {
    const metaWithCatalog: OnboardingMeta = {
      ...meta,
      models_by_service: {
        cc: [{
          key: 'cc-haiku-arith',
          label: 'Claude Haiku 4.5',
          vendor: 'anthropic',
          template: 'cc-haiku-arith',
          model: '',
          request_model: 'claude-haiku-4-5-20251001',
          editable: false,
        }],
      },
    };
    const out = renderHTML(
      <ConfirmStep
        formData={{ ...baseFormData, modelKey: 'cc-haiku-arith' }}
        meta={metaWithCatalog}
        updateField={updateField}
        submitResult={null}
        isSubmitting={false}
        testPassedAt={null}
        checkedClauses={{}}
        onToggleClause={noop}
        onSubmit={noop}
        onBack={noop}
        onReset={noop}
      />,
    );
    expect(out).toContain('监测的模型');
    expect(out).toContain('Claude Haiku 4.5');
    // 表单里不该再出现模板名
    expect(out).not.toContain('cc-haiku-arith');
  });

  it('#3b 目录取不到时回落展示模板名，不留空行', () => {
    // meta 尚未加载（首屏刷新回到第三步）时仍要说清测的是什么
    expect(html()).toContain('cc-haiku-arith');
  });

  it('填了通道显示名称时摘要出一行，留空不渲染该行', () => {
    const withName = renderHTML(
      <ConfirmStep
        formData={{ ...baseFormData, channelName: ' Max 高速线路 ' }}
        updateField={updateField}
        submitResult={null}
        isSubmitting={false}
        testPassedAt={null}
        checkedClauses={{}}
        onToggleClause={noop}
        onSubmit={noop}
        onBack={noop}
        onReset={noop}
      />,
    );
    expect(withName).toContain('通道显示名称');
    expect(withName).toContain('Max 高速线路');
    expect(html()).not.toContain('通道显示名称');
  });

  it('#2 赞助等级附等级代码「脉冲链路（pulse）」', () => {
    expect(html()).toContain('脉冲链路（pulse）');
  });

  it('入驻须知 6 条逐条勾选渲染', () => {
    const out = html();
    const checkboxCount = (out.match(/type="checkbox"/g) ?? []).length;
    expect(checkboxCount).toBeGreaterThanOrEqual(6);
  });

  it('第 6 条「禁止监测作弊」条款渲染出来', () => {
    expect(html()).toContain('针对平台的可用性监测或质量检测探针作弊');
  });

  it('反作弊条为必勾项：仅勾旧 5 条仍被门控，勾满 6 条才放行', () => {
    const AGREEMENT_KEYS = [
      'clausePaid', 'clauseApiKey', 'clauseQuality',
      'clauseNoCheat', 'clauseLegit', 'clauseNoEndorse',
    ] as const;
    const HINT = '请勾选全部要点后再提交';
    const renderWith = (checked: Record<string, boolean>) =>
      renderHTML(
        <ConfirmStep
          formData={baseFormData}
          updateField={updateField}
          submitResult={null}
          isSubmitting={false}
          testPassedAt={null}
          proofExpiresAt={null}
          checkedClauses={checked}
          onToggleClause={noop}
          onSubmit={noop}
          onBack={noop}
          onReset={noop}
        />,
      );
    const exceptNoCheat = Object.fromEntries(
      AGREEMENT_KEYS.filter((k) => k !== 'clauseNoCheat').map((k) => [k, true]),
    );
    const all = Object.fromEntries(AGREEMENT_KEYS.map((k) => [k, true]));
    expect(renderWith(exceptNoCheat)).toContain(HINT);
    expect(renderWith(all)).not.toContain(HINT);
  });
});

describe('ConnectionTestStep 探测结果显示（step2）', () => {
  const renderResult = (testResult: OnboardingTestResult) =>
    renderHTML(
      <ConnectionTestStep
        formData={baseFormData}
        updateField={updateField}
        meta={meta}
        testResult={testResult}
        testProof={null}
        isTesting={false}
        onRunTest={noop}
        onBack={noop}
        onNext={noop}
      />,
    );

  it('#1 失败探测渲染上游 response_snippet 详情', () => {
    const out = renderResult({
      probe_status: 0,
      sub_status: 'invalid_request',
      http_code: 400,
      latency: 424,
      error_message: '',
      response_snippet: '{"error":{"message":"invalid model name"}}',
      probe_id: 'probe-test',
    });
    expect(out).toContain('响应详情');
    expect(out).toContain('invalid model name'); // snippet 正文（引号会被 HTML 转义，正文不含特殊字符）
    expect(out).toContain('400');
    // sub_status 现走 subStatus 词表翻译展示（不再裸码）；invalid_request → 「请求参数错误」
    expect(out).toContain('请求参数错误');
  });

  it('response_snippet 与 error_message 相同时不重复展示详情', () => {
    const out = renderResult({
      probe_status: 0,
      sub_status: 'network_error',
      http_code: 0,
      latency: 0,
      error_message: 'dial tcp: connection refused',
      response_snippet: 'dial tcp: connection refused',
      probe_id: 'probe-test',
    });
    // error_message 块仍在，但「响应详情」块因去重不出现
    expect(out).toContain('dial tcp: connection refused');
    expect(out).not.toContain('响应详情');
  });
});

describe('ProviderInfoStep 通道命名字段（step1）', () => {
  const html = (overrides: Partial<OnboardingFormData> = {}) =>
    renderHTML(
      <ProviderInfoStep
        formData={{ ...baseFormData, ...overrides }}
        updateField={updateField}
        meta={meta}
        onNext={noop}
      />,
    );

  it('渲染可选的「通道显示名称」输入框，且分组字段文案为「通道分组代号」', () => {
    const out = html();
    expect(out).toContain('id="ob-channel-name"');
    expect(out).toContain('通道显示名称');
    expect(out).toContain('通道分组代号');
    expect(out).toContain('不作为展示名称');
  });

  it('中文显示名合法不出错误提示；含零宽字符时给出 alert 错误', () => {
    const ok = html({ channelName: 'Max 高速线路' });
    expect(ok).not.toContain('不能包含控制字符');
    const bad = html({ channelName: 'a\u200bb' });
    expect(bad).toContain('不能包含控制字符或零宽字符');
    expect(bad).toContain('role="alert"');
  });

  /** 从渲染出的 innerHTML 中取出「下一步」提交按钮片段，判断其是否带 disabled 属性。 */
  const nextButtonDisabled = (out: string) => {
    const match = out.match(/<button type="submit"[\s\S]*?<\/button>/);
    if (!match) throw new Error('未找到「下一步」提交按钮');
    return match[0].includes('disabled=""');
  };

  it('服务商名称放开为 Unicode 展示名：中文名不再被拒绝，下一步按钮可点击', () => {
    const out = html({ providerName: '赛博AI' });
    expect(nextButtonDisabled(out)).toBe(false);
  });

  it('服务商名称含零宽字符（不可见格式字符）时下一步按钮禁用', () => {
    const out = html({ providerName: 'Sai\u200bAI' });
    expect(nextButtonDisabled(out)).toBe(true);
  });

  it('服务商名称超过 100 个 Unicode 码位时下一步按钮禁用', () => {
    const out = html({ providerName: '赛'.repeat(101) });
    expect(nextButtonDisabled(out)).toBe(true);
  });
});
