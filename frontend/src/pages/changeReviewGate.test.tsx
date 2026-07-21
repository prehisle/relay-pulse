// @vitest-environment jsdom
//
// 变更复核页反作弊门控守护：改 base_url/密钥时出现反作弊勾选框且门控提交，
// 纯展示变更（改名）不出现勾选框、不被协议阻塞。
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { I18nextProvider } from 'react-i18next';
import { MemoryRouter } from 'react-router-dom';
import { describe, it, expect, beforeAll } from 'vitest';
import type { ReactNode } from 'react';
import i18n from '../i18n';
import { ReviewStep } from './ChangeRequestPage';
import type { AuthCandidate } from '../types/change';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

beforeAll(async () => {
  await i18n.changeLanguage('zh-CN');
});

const candidate: AuthCandidate = {
  provider: 'p', service: 'cc', channel: 'c',
  monitor_key: 'p--cc--c', apply_mode: 'auto',
  provider_name: 'P', provider_url: 'https://p.com', channel_name: 'C',
  category: 'commercial', sponsor_level: 'pulse',
  base_url: 'https://old.com', key_last4: '0001',
  test_type: 'cc', test_type_name: 'Claude Code',
};
const noop = () => {};

function mount(node: ReactNode) {
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(
      <I18nextProvider i18n={i18n}><MemoryRouter>{node}</MemoryRouter></I18nextProvider>,
    );
  });
  return {
    container,
    cleanup: () => { act(() => root.unmount()); container.remove(); },
  };
}

const clauseText = '针对平台的可用性监测或质量检测探针作弊'; // clauseNoCheat 片段

describe('ReviewStep 反作弊门控', () => {
  it('改 base_url：出现反作弊勾选框，未勾时提交按钮 disabled', () => {
    const { container, cleanup } = mount(
      <ReviewStep
        selectedCandidate={candidate}
        changes={{ base_url: 'https://new.com' }}
        newApiKey=""
        isSubmitting={false}
        submit={noop}
        goBack={noop}
        error={null}
      />,
    );
    expect(container.innerHTML).toContain(clauseText);
    const checkbox = container.querySelector('input[type="checkbox"]');
    expect(checkbox).not.toBeNull();
    const submitBtn = container.querySelector('button[class*="flex-1"]') as HTMLButtonElement;
    expect(submitBtn.disabled).toBe(true);
    cleanup();
  });

  it('勾选反作弊后提交按钮启用', () => {
    const { container, cleanup } = mount(
      <ReviewStep
        selectedCandidate={candidate}
        changes={{ base_url: 'https://new.com' }}
        newApiKey=""
        isSubmitting={false}
        submit={noop}
        goBack={noop}
        error={null}
      />,
    );
    const checkbox = container.querySelector('input[type="checkbox"]') as HTMLInputElement;
    act(() => { checkbox.click(); });
    const submitBtn = container.querySelector('button[class*="flex-1"]') as HTMLButtonElement;
    expect(submitBtn.disabled).toBe(false);
    cleanup();
  });

  it('改新 API Key：同样出现反作弊勾选框，门控提交（勾前 disabled、勾后 enabled）', () => {
    const { container, cleanup } = mount(
      <ReviewStep
        selectedCandidate={candidate}
        changes={{}}
        newApiKey="sk-newkey-9999"
        isSubmitting={false}
        submit={noop}
        goBack={noop}
        error={null}
      />,
    );
    expect(container.innerHTML).toContain(clauseText);
    const checkbox = container.querySelector('input[type="checkbox"]') as HTMLInputElement;
    expect(checkbox).not.toBeNull();
    const submitBtn = container.querySelector('button[class*="flex-1"]') as HTMLButtonElement;
    expect(submitBtn.disabled).toBe(true);
    act(() => { checkbox.click(); });
    expect(submitBtn.disabled).toBe(false);
    cleanup();
  });

  it('纯改名变更：无反作弊勾选框，提交按钮不被协议阻塞', () => {
    const { container, cleanup } = mount(
      <ReviewStep
        selectedCandidate={candidate}
        changes={{ provider_name: 'NewName' }}
        newApiKey=""
        isSubmitting={false}
        submit={noop}
        goBack={noop}
        error={null}
      />,
    );
    expect(container.innerHTML).not.toContain(clauseText);
    const submitBtn = container.querySelector('button[class*="flex-1"]') as HTMLButtonElement;
    expect(submitBtn.disabled).toBe(false);
    cleanup();
  });
});
