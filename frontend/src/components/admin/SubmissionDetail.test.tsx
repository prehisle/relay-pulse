// @vitest-environment jsdom
//
// SubmissionDetail 的 PSC 覆盖值校验守护测试。锁定三条契约：
//   #1 管理员**本次输入**的非法覆盖值 → 字段级报错 + 锁住「保存修改」
//   #2 库里**存量**非法覆盖值 → 仍然报错提示，但不锁保存（后端也只校验请求带来的字段，
//      否则管理员连备注都改不了）
//   #3 合法覆盖值不误伤
import { act } from 'react';
import type { ComponentProps } from 'react';
import { createRoot } from 'react-dom/client';
import { I18nextProvider } from 'react-i18next';
import { describe, it, expect, afterEach, vi } from 'vitest';
import i18n from '../../i18n';
import { SubmissionDetail } from './SubmissionDetail';
import type { AdminSubmission } from '../../types/onboarding';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const baseSubmission: AdminSubmission = {
  id: 1,
  public_id: 'sub-1234567890',
  status: 'pending',
  provider_name: '银兔',
  website_url: 'https://api.yintu.cc',
  category: 'commercial',
  service_type: 'cc',
  template_name: 'cc-haiku-arith',
  model: '',
  model_vendor: '',
  sponsor_level: 'pulse',
  channel_type: 'O',
  channel_source: 'api',
  channel_group: 'main',
  channel_code: 'o-api-main',
  target_provider: '',
  target_service: '',
  target_channel: '',
  channel_name: 'O-Pro',
  listed_since: '',
  expires_at: '',
  price_min: 0,
  price_max: 0,
  base_url: 'https://api.yintu.cc',
  api_key_encrypted: '',
  api_key_fingerprint: 'fp',
  api_key_last4: '1234',
  test_job_id: 'job-1',
  test_passed_at: 1_750_000_000,
  test_latency_ms: 100,
  test_http_code: 200,
  submitter_ip_hash: '',
  locale: 'zh-CN',
  admin_note: '',
  admin_config_json: '',
  reviewed_at: null,
  created_at: 1_750_000_000,
  updated_at: 1_750_000_000,
  agreement_accepted: true,
  agreement_accepted_at: 1_750_000_000,
  agreement_version: '2026-07-16',
};

type DetailProps = ComponentProps<typeof SubmissionDetail>;

const roots: ReturnType<typeof createRoot>[] = [];

function render(subOverrides: Partial<AdminSubmission> = {}) {
  const props: DetailProps = {
    submission: { ...baseSubmission, ...subOverrides },
    apiKey: 'sk-test',
    showApiKey: false,
    setShowApiKey: vi.fn(),
    onSave: vi.fn(),
    onTest: vi.fn().mockResolvedValue(null),
    fetchTemplates: vi.fn().mockResolvedValue(['cc-haiku-arith']),
    onReject: vi.fn(),
    onDelete: vi.fn(),
    onPublish: vi.fn(),
    onBack: vi.fn(),
  };

  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  roots.push(root);
  act(() => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <SubmissionDetail {...props} />
      </I18nextProvider>,
    );
  });
  return container;
}

/** 定位「Provider 覆盖」输入框（按 label 文本找同级 input）。 */
function providerOverrideInput(container: HTMLElement): HTMLInputElement {
  const label = [...container.querySelectorAll('label')].find(
    // 注意别撞上「服务商名称 / Provider Name」——它排在覆盖字段前面。
    (l) => /Provider (Override|覆盖)/.test(l.textContent ?? ''),
  );
  const input = label?.parentElement?.querySelector('input');
  if (!input) throw new Error('Provider 覆盖 input not found');
  return input;
}

/** 「保存修改」按钮；测试环境 i18n 落到 en-US，故按两种语言的文案找。 */
function saveButton(container: HTMLElement): HTMLButtonElement | undefined {
  return [...container.querySelectorAll('button')].find(
    (b) => /保存|Save/.test(b.textContent ?? ''),
  ) as HTMLButtonElement | undefined;
}

/** 字段级错误提示节点数（不依赖文案语言）。 */
function fieldErrorCount(container: HTMLElement): number {
  return container.querySelectorAll('p.text-danger').length;
}

function typeInto(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
  act(() => {
    setter?.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}

afterEach(() => {
  act(() => {
    for (const r of roots.splice(0)) r.unmount();
  });
  document.body.innerHTML = '';
});

describe('SubmissionDetail PSC 覆盖值校验', () => {
  it('本次输入的中文覆盖值报错并锁住保存', () => {
    const container = render();
    typeInto(providerOverrideInput(container), '银兔');

    expect(fieldErrorCount(container)).toBe(1);
    const save = saveButton(container);
    expect(save).toBeDefined();
    expect(save!.disabled).toBe(true);
  });

  it('本次输入的合法覆盖值不报错、保存可用', () => {
    const container = render();
    typeInto(providerOverrideInput(container), 'yintu');

    expect(fieldErrorCount(container)).toBe(0);
    expect(saveButton(container)!.disabled).toBe(false);
  });

  // 存量脏值场景：本闸上线前存进 DB 的非法覆盖值。提示要有，但不能连带锁死无关字段的编辑——
  // handleSave 只发变化过的字段，后端也只校验请求带来的字段，锁了纯属自伤。
  it('存量非法覆盖值提示但不锁保存', () => {
    const container = render({ target_provider: 'sai--ai', admin_note: '' });
    expect(fieldErrorCount(container)).toBe(1);
    expect(saveButton(container)).toBeUndefined(); // 未编辑时本就没有保存按钮

    // 改一个无关字段（备注）后，保存按钮应出现且可用。
    const noteLabel = [...container.querySelectorAll('label')].find(
      (l) => /备注|Note/.test(l.textContent ?? ''),
    );
    const noteInput = noteLabel?.parentElement?.querySelector('textarea, input');
    expect(noteInput).toBeTruthy();
    act(() => {
      const proto = noteInput instanceof HTMLTextAreaElement
        ? HTMLTextAreaElement.prototype
        : HTMLInputElement.prototype;
      Object.getOwnPropertyDescriptor(proto, 'value')?.set?.call(noteInput, '只改备注');
      noteInput!.dispatchEvent(new Event('input', { bubbles: true }));
    });

    const save = saveButton(container);
    expect(save).toBeDefined();
    expect(save!.disabled).toBe(false);
  });
});
