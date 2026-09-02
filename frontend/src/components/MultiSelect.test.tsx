// @vitest-environment jsdom
//
// MultiSelect 分组能力守护。这个组件同时服务两类调用方，两类都必须钉死：
//   ① 扁平模式 —— provider/service/channel/vendor 四个既有筛选器一行没改就跟着
//      走了新代码路径，「零行为变化」必须可证，不能靠"跑一眼没崩"；
//   ② 分组模式 —— 「模型」筛选器靠它实现「一次勾中所有 Opus」。组标题的三态、
//      点击语义、搜索下的整组隐藏，都是漏一个就静默错的性质。
// 用 react-dom/client 在 jsdom 真实渲染，沿用 modelVendorColumn.test 形态、零新依赖。
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { describe, it, expect, beforeAll, afterEach, vi } from 'vitest';
import { I18nextProvider } from 'react-i18next';
import i18n from '../i18n';
import { MultiSelect, type MultiSelectOption } from './MultiSelect';

beforeAll(async () => {
  // 没有这个标志 React 会对每次 act() 发 "not configured to support act" 警告。
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  if (!i18n.isInitialized) await i18n.init();
});

/**
 * 往受控 input 里打字。
 *
 * 不能直接 `input.value = x` —— React 在 HTMLInputElement.prototype 上装了 value
 * tracker，绕过 setter 直接赋值会让它认为值没变、onChange 根本不触发（本测试第一版
 * 就栽在这里，搜索框看着填上了但组件状态纹丝不动）。必须走原生 setter 再派发事件。
 */
function type(input: HTMLInputElement, text: string) {
  const setValue = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
  act(() => {
    setValue.call(input, text);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}

let active: { container: HTMLElement; root: Root } | null = null;

afterEach(() => {
  if (active) {
    const { container, root } = active;
    act(() => root.unmount());
    container.remove();
    active = null;
  }
});

function render(props: {
  value: string[];
  options: MultiSelectOption[];
  onChange: (values: string[]) => void;
  searchable?: boolean;
}) {
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <MultiSelect {...props} onChange={props.onChange} />
      </I18nextProvider>,
    );
  });
  active = { container, root };
  return container;
}

/** 展开下拉。触发按钮是面板外唯一的 role="button"。 */
function open(container: HTMLElement) {
  const trigger = container.querySelector('[role="button"]') as HTMLElement;
  act(() => trigger.click());
}

function optionLabels(container: HTMLElement): string[] {
  return [...container.querySelectorAll('[role="option"]')].map((el) => el.textContent?.trim() ?? '');
}

const FLAT: MultiSelectOption[] = [
  { value: 'a', label: 'Alpha' },
  { value: 'b', label: 'Beta' },
];

/** 两组、组内各两项，外加一个未归组散项——覆盖混合形态。 */
const GROUPED: MultiSelectOption[] = [
  { value: 'opus-4.8', label: 'opus-4.8', groupKey: 'opus', groupLabel: 'Opus' },
  { value: 'opus-5', label: 'opus-5', groupKey: 'opus', groupLabel: 'Opus' },
  { value: 'gpt-5.4', label: 'gpt-5.4', groupKey: 'gpt', groupLabel: 'GPT' },
  { value: 'gpt-5.5', label: 'gpt-5.5', groupKey: 'gpt', groupLabel: 'GPT' },
  { value: 'loner', label: 'Loner' },
];

describe('扁平模式向后兼容', () => {
  it('没有任何 groupKey 时不产生 group 结构', () => {
    const container = render({ value: [], options: FLAT, onChange: vi.fn() });
    open(container);

    expect(container.querySelectorAll('[role="group"]')).toHaveLength(0);
    // "全部" + 两个选项
    expect(optionLabels(container)).toHaveLength(3);
  });

  it('点选项仍是单项 toggle', () => {
    const onChange = vi.fn();
    const container = render({ value: [], options: FLAT, onChange });
    open(container);

    const beta = [...container.querySelectorAll('[role="option"]')].find(
      (el) => el.textContent?.trim() === 'Beta',
    ) as HTMLElement;
    act(() => beta.click());

    expect(onChange).toHaveBeenCalledWith(['b']);
  });
});

describe('分组渲染', () => {
  it('按选项数组里的首次出现顺序聚合，散项排在所有分组之后', () => {
    const container = render({ value: [], options: GROUPED, onChange: vi.fn() });
    open(container);

    const groups = [...container.querySelectorAll('[role="group"]')];
    expect(groups.map((el) => el.getAttribute('aria-label'))).toEqual(['Opus', 'GPT']);

    // 「全部」→ Opus 组（标题 + 2 叶）→ GPT 组（标题 + 2 叶）→ 散项
    const labels = optionLabels(container);
    expect(labels[labels.length - 1]).toBe('Loner');
    expect(labels).toContain('Opus');
    expect(labels).toContain('opus-5');
  });

  it('搜索命中不到的整组（含标题）不渲染', () => {
    const container = render({ value: [], options: GROUPED, onChange: vi.fn(), searchable: true });
    open(container);

    type(container.querySelector('input') as HTMLInputElement, 'opus');

    const groups = [...container.querySelectorAll('[role="group"]')];
    expect(groups.map((el) => el.getAttribute('aria-label'))).toEqual(['Opus']);
    expect(optionLabels(container)).not.toContain('gpt-5.4');
  });
});

describe('组标题三态与点击语义', () => {
  /** 取某个组标题按钮：它是该 group 内的第一个 option。 */
  function groupHeader(container: HTMLElement, label: string): HTMLElement {
    const group = [...container.querySelectorAll('[role="group"]')].find(
      (el) => el.getAttribute('aria-label') === label,
    );
    return group!.querySelector('[role="option"]') as HTMLElement;
  }

  it('组内一项未选 → aria-selected=false，无图标，计数 0/2', () => {
    const container = render({ value: [], options: GROUPED, onChange: vi.fn() });
    open(container);

    const header = groupHeader(container, 'Opus');
    expect(header.getAttribute('aria-selected')).toBe('false');
    expect(header.getAttribute('aria-label')).toBe('Opus (0/2)');
    expect(header.querySelector('svg')).toBeNull();
  });

  it('组内部分选中 → aria-selected 仍是 false，靠计数与半选图标区分于未选', () => {
    const container = render({ value: ['opus-5'], options: GROUPED, onChange: vi.fn() });
    open(container);

    const header = groupHeader(container, 'Opus');
    expect(header.getAttribute('aria-selected')).toBe('false');
    // aria-selected 表达不了半选，无障碍名里的计数是屏幕阅读器唯一的区分依据。
    expect(header.getAttribute('aria-label')).toBe('Opus (1/2)');
    // 视觉上半选是 Minus 而非 Check。只断言"有个 svg"是真空的——把 Minus 换成
    // Check 照样能过，故这里比对 lucide 挂在元素上的图标类名。
    expect(header.querySelector('svg')?.getAttribute('class')).toContain('lucide-minus');
  });

  it('组内全选 → aria-selected=true 且图标是 Check 而非 Minus', () => {
    const container = render({ value: ['opus-4.8', 'opus-5'], options: GROUPED, onChange: vi.fn() });
    open(container);

    const header = groupHeader(container, 'Opus');
    expect(header.getAttribute('aria-selected')).toBe('true');
    expect(header.getAttribute('aria-label')).toBe('Opus (2/2)');
    expect(header.querySelector('svg')?.getAttribute('class')).toContain('lucide-check');
  });

  it('组状态只统计当前可见叶子：搜索缩小后，可见项全选即算全选', () => {
    // 这是有意的语义——用户看到什么，「全选」就作用于什么。若统计被搜索藏起来的
    // 叶子，会出现「可见项全勾上了、标题却半选」的费解状态。
    const container = render({
      value: ['opus-5'],
      options: GROUPED,
      onChange: vi.fn(),
      searchable: true,
    });
    open(container);
    type(container.querySelector('input') as HTMLInputElement, 'opus-5');

    const header = groupHeader(container, 'Opus');
    expect(header.getAttribute('aria-selected')).toBe('true');
    expect(header.getAttribute('aria-label')).toBe('Opus (1/1)');
  });

  it('点未全选的组标题 → 补齐该组缺的叶子，且不动其他组已选项', () => {
    const onChange = vi.fn();
    const container = render({ value: ['gpt-5.4', 'opus-5'], options: GROUPED, onChange });
    open(container);

    act(() => groupHeader(container, 'Opus').click());

    // gpt-5.4 原样保留，opus 组补上缺的 opus-4.8
    expect(onChange).toHaveBeenCalledWith(['gpt-5.4', 'opus-5', 'opus-4.8']);
  });

  it('点已全选的组标题 → 只移除该组，其他组不受影响', () => {
    const onChange = vi.fn();
    const container = render({
      value: ['opus-4.8', 'opus-5', 'gpt-5.4'],
      options: GROUPED,
      onChange,
    });
    open(container);

    act(() => groupHeader(container, 'Opus').click());

    expect(onChange).toHaveBeenCalledWith(['gpt-5.4']);
  });

  it('不丢弃 options 之外的已选值（联动筛选会让已选项暂时不在选项表里）', () => {
    // 真实场景：用户选了 opus-5 后又改了服务商筛选，opus-5 不在当前 options 里但
    // 仍被保留为已选（useFilterOptions 的 "(0)" 项）。点任何组标题都不能把它清掉。
    const onChange = vi.fn();
    const container = render({
      value: ['hidden-model', 'gpt-5.4'],
      options: GROUPED,
      onChange,
    });
    open(container);

    act(() => groupHeader(container, 'GPT').click());

    const next = onChange.mock.calls[0][0] as string[];
    expect(next).toContain('hidden-model');
    expect(next).toContain('gpt-5.5');
  });

  it('取消整组也不丢弃 options 之外的已选值', () => {
    const onChange = vi.fn();
    const container = render({
      value: ['hidden-model', 'gpt-5.4', 'gpt-5.5'],
      options: GROUPED,
      onChange,
    });
    open(container);

    act(() => groupHeader(container, 'GPT').click());

    expect(onChange).toHaveBeenCalledWith(['hidden-model']);
  });

  it('选满全部叶子不会被静默归一成空数组（空数组=未筛选，语义不同）', () => {
    const onChange = vi.fn();
    // 只差 GPT 组两项就选满全部 5 项
    const container = render({
      value: ['opus-4.8', 'opus-5', 'loner'],
      options: GROUPED,
      onChange,
    });
    open(container);

    act(() => groupHeader(container, 'GPT').click());

    const next = onChange.mock.calls[0][0] as string[];
    expect(next).toHaveLength(5);
    expect(next).not.toEqual([]);
  });
});

describe('listbox 结构合法性', () => {
  it('搜索框不在 listbox 子树内', () => {
    const container = render({ value: [], options: FLAT, onChange: vi.fn(), searchable: true });
    open(container);

    const listbox = container.querySelector('[role="listbox"]') as HTMLElement;
    expect(container.querySelector('input')).not.toBeNull();
    expect(listbox.querySelector('input')).toBeNull();
  });

  it('搜索无匹配时出现 noResults，且它在 listbox 之外', () => {
    // noResults 从 listbox 内挪到了外面——它不是一个可选项，留在 listbox 里会被
    // 屏幕阅读器当成选项读出来。这是本次 DOM 改造最容易被无声改回去的一处。
    const container = render({ value: [], options: FLAT, onChange: vi.fn(), searchable: true });
    open(container);
    type(container.querySelector('input') as HTMLInputElement, 'zzz-no-such-option');

    const status = container.querySelector('[role="status"]');
    expect(status).not.toBeNull();
    expect(container.querySelector('[role="listbox"]')!.querySelector('[role="status"]')).toBeNull();
    expect(container.querySelectorAll('[role="option"]')).toHaveLength(0);
  });
});
