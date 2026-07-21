// @vitest-environment jsdom
//
// ChangeRequestList 只读 diff 审批 UI 守护测试。
// 锁定三处核心契约：
//   #1 auto 来源时 diff 当前值取 live_current（现网真实名），非提交时快照
//   #2 non-auto 来源时 diff 当前值取提交时快照，并显示原因提示
//   #3 保存审核备注时 onUpdate 只发 { admin_note }，不含 proposed 字段
import { act } from 'react';
import type { ComponentProps } from 'react';
import { createRoot } from 'react-dom/client';
import { I18nextProvider } from 'react-i18next';
import { describe, it, expect, beforeAll, afterEach, vi } from 'vitest';
import i18n from '../../i18n';
import { ChangeRequestList } from './ChangeRequestList';
import type { AdminChangeRequest } from '../../types/change';

// React 19 的 act 需要此全局标记，否则会告警（无 testing-library 自动设置）
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// ── 测试 fixture ──────────────────────────────────────────

const baseCr: AdminChangeRequest = {
  id: 1,
  public_id: 'cr-1234567890',
  status: 'pending',
  target_provider: 'prov',
  target_service: 'cc',
  target_channel: 'chan',
  target_key: 'prov--cc--chan',
  apply_mode: 'auto',
  auth_fingerprint: 'fp-test',
  auth_last4: '9999',
  current_snapshot: JSON.stringify({ provider_name: '提交时旧名', sponsor_level: 'core' }),
  proposed_changes: JSON.stringify({ provider_name: '用户提议名' }),
  live_current: { provider_name: '现网真实名' },
  live_current_source: 'auto',
  requires_test: false,
  created_at: 1_750_000_000,
  updated_at: 1_750_000_000,
};

function makeCr(overrides: Partial<AdminChangeRequest> = {}): AdminChangeRequest {
  return { ...baseCr, ...overrides };
}

// ── 渲染工具 ──────────────────────────────────────────────

type ListProps = ComponentProps<typeof ChangeRequestList>;

function makeDefaultProps(overrides: Partial<ListProps> = {}): ListProps {
  return {
    changes: [makeCr()],
    isLoading: false,
    statusFilter: 'all',
    setStatusFilter: vi.fn(),
    onUpdate: vi.fn(),
    onApprove: vi.fn(),
    onReject: vi.fn<(id: string, note: string) => void>(),
    onApply: vi.fn(),
    onDelete: vi.fn(),
    pendingActions: {},
    error: null,
    featureDisabled: false,
    ...overrides,
  };
}

/** 挂载组件并点击指定通道行的展开按钮，返回 container。 */
function renderExpanded(
  crOverrides: Partial<AdminChangeRequest> = {},
  propOverrides: Partial<ListProps> = {},
): { container: HTMLElement; root: ReturnType<typeof createRoot> } {
  const cr = makeCr(crOverrides);
  const props = makeDefaultProps({ changes: [cr], ...propOverrides });

  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);

  act(() => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <ChangeRequestList {...props} />
      </I18nextProvider>,
    );
  });

  // 点击行 header 展开
  const rowBtn = [...container.querySelectorAll('button')].find(
    (b) => b.textContent?.includes(cr.target_key),
  );
  if (!rowBtn) throw new Error('row header button not found');

  act(() => {
    rowBtn.dispatchEvent(new MouseEvent('click', { bubbles: true }));
  });

  return { container, root };
}

// ── 清理 ──────────────────────────────────────────────────

const roots: ReturnType<typeof createRoot>[] = [];

afterEach(() => {
  act(() => {
    for (const r of roots.splice(0)) r.unmount();
  });
  document.body.innerHTML = '';
});

beforeAll(async () => {
  await i18n.changeLanguage('zh-CN');
});

// ── 测试用例 ──────────────────────────────────────────────

describe('ChangeRequestList 只读审 diff', () => {
  it('#1 auto 来源：diff 当前值取 live_current（现网真实名），非提交时快照', () => {
    const { container, root } = renderExpanded();
    roots.push(root);
    const html = container.innerHTML;

    // live_current 里的"现网真实名"应出现
    expect(html).toContain('现网真实名');
    // proposed 里的"用户提议名"应出现
    expect(html).toContain('用户提议名');
    // 提交时快照里的"提交时旧名"不应出现（已被 live_current 覆盖）
    expect(html).not.toContain('提交时旧名');
  });

  it('#2 非 proposed 字段（sponsor_level=core）不渲染 — 无全量编辑器', () => {
    const { container, root } = renderExpanded();
    roots.push(root);
    // proposed_changes 只有 provider_name，快照里 sponsor_level=core 不应出现
    expect(container.innerHTML).not.toContain('core');
  });

  it('#2b auto 来源但 live_current 缺失对应字段时，回退到提交时快照', () => {
    // proposed_changes 有 base_url，但 live_current 里没有 base_url，应回退到 snapshot
    const { container, root } = renderExpanded({
      proposed_changes: JSON.stringify({ base_url: 'https://proposed.example.com' }),
      current_snapshot: JSON.stringify({ base_url: 'https://snapshot.example.com' }),
      live_current: { provider_name: '现网真实名' }, // 无 base_url
      live_current_source: 'auto',
    });
    roots.push(root);
    const html = container.innerHTML;

    // 应回退到提交时快照里的 base_url
    expect(html).toContain('https://snapshot.example.com');
    // proposed 值出现
    expect(html).toContain('https://proposed.example.com');
    // live 里无关 provider_name 不应被误当作当前值
    expect(html).not.toContain('现网真实名');
  });

  it('#3 manual 来源：当前值取提交时快照，且显示原因提示', () => {
    const { container, root } = renderExpanded({
      live_current_source: 'manual',
      live_current: { provider_name: '现网真实名' }, // 有 live 但 source=manual，应忽略
    });
    roots.push(root);
    const html = container.innerHTML;

    // 提示文字（liveUnavailable.manual）应出现
    expect(html).toContain('该通道为人工模式');
    // 当前值应取快照里的"提交时旧名"
    expect(html).toContain('提交时旧名');
    // live 里的"现网真实名"不应作为当前值出现
    expect(html).not.toContain('现网真实名');
  });

  it('#3b error 来源：当前值不展示陈旧快照（显示破折号），避免误导基线', () => {
    const { container, root } = renderExpanded({
      proposed_changes: JSON.stringify({ provider_name: '提议X' }),
      current_snapshot: JSON.stringify({ provider_name: '快照旧值' }),
      live_current: undefined, // 读失败，没有可用的 live 值
      live_current_source: 'error',
    });
    roots.push(root);
    const html = container.innerHTML;

    // 提议值正常展示
    expect(html).toContain('提议X');
    // error 时不可信，陈旧快照绝不出现在"当前值"列
    expect(html).not.toContain('快照旧值');
    // 当前值列回退为破折号
    expect(html).toContain('—');
    // 提示文字（liveUnavailable.error）应出现
    expect(html).toContain('读取当前配置失败');
  });

  it('#4 保存审核备注时 onUpdate 只发 { admin_note }', () => {
    const onUpdate = vi.fn<(id: string, updates: Record<string, unknown>) => void>();
    const { container, root } = renderExpanded(
      { admin_note: '旧备注' },
      { onUpdate },
    );
    roots.push(root);

    // 改变 textarea 值：通过 React 内部属性描述符 + input 事件触发
    const textarea = container.querySelector('textarea');
    if (!textarea) throw new Error('admin note textarea not found');

    const nativeSetter = Object.getOwnPropertyDescriptor(
      HTMLTextAreaElement.prototype,
      'value',
    )?.set;
    if (!nativeSetter) throw new Error('textarea native value setter not found');

    act(() => {
      nativeSetter.call(textarea, '新备注');
      textarea.dispatchEvent(new Event('input', { bubbles: true }));
    });

    // 点击"保存"按钮
    const saveBtn = [...container.querySelectorAll('button')].find(
      (b) => b.textContent?.trim() === '保存',
    );
    if (!saveBtn) throw new Error('save button not found');

    act(() => {
      saveBtn.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    });

    expect(onUpdate).toHaveBeenCalledTimes(1);
    expect(onUpdate).toHaveBeenCalledWith('cr-1234567890', { admin_note: '新备注' });
  });
});

describe('反作弊 attestation 审计展示', () => {
  it('改监测字段且已确认：显示已确认 + 版本，且不含其它两态文案', () => {
    const { container, root } = renderExpanded({
      requires_test: true,
      agreement_accepted: true,
      agreement_version: '2026-07-16',
      agreement_accepted_at: 1721563200,
    });
    roots.push(root);
    const html = container.innerHTML;

    expect(html).toContain('已确认');
    expect(html).toContain('2026-07-16');
    // 三态互斥：确认态不应混入不适用/未确认文案
    expect(html).not.toContain('不适用');
    expect(html).not.toContain('⚠');
  });

  it('改监测字段但未确认（历史行）：醒目未确认', () => {
    const { container, root } = renderExpanded({
      requires_test: true,
      agreement_accepted: false,
    });
    roots.push(root);
    const html = container.innerHTML;

    expect(html).toContain('未确认');
    expect(html).toContain('⚠');
  });

  it('已确认但缺版本/时间戳：只显示已确认，无 ·v 后缀与 Invalid Date', () => {
    const { container, root } = renderExpanded({
      requires_test: true,
      agreement_accepted: true,
      // 有意省略 agreement_version 与 agreement_accepted_at，覆盖守卫插值分支
    });
    roots.push(root);
    const html = container.innerHTML;

    expect(html).toContain('已确认');
    expect(html).not.toContain('· v');
    expect(html).not.toContain('Invalid Date');
  });

  it('纯展示变更：不适用', () => {
    const { container, root } = renderExpanded({
      requires_test: false,
    });
    roots.push(root);
    const html = container.innerHTML;

    expect(html).toContain('不适用');
  });
});
