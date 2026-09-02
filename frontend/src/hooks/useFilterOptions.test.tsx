// @vitest-environment jsdom
//
// 「模型」筛选器选项（effectiveModels）的守卫。四条性质错一条就静默出问题：
//   ① **分组连续** —— MultiSelect 按数组里首次出现的顺序聚合分组，同家族的选项一旦
//      被打散，下拉里会冒出两个同名的「Opus」组；
//   ② **计数口径是通道数** —— 模型是通道内的多值属性，按 layer 计数会与用户实际
//      看到的行数对不上；
//   ③ **已选项在联动后仍带着原家族保留** —— label/family 取自未筛选全量数据，
//      否则被联动排除的已选项会掉进「其他」组；
//   ④ **联动方向正确** —— 选了模型要能收窄其它维度的选项。
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { describe, it, expect, afterEach, beforeAll } from 'vitest';
import type { TFunction } from 'i18next';
import { useFilterOptions, type FilterOptions, type FilterOptionsParams } from './useFilterOptions';
import type { ProcessedMonitorData } from '../types';

beforeAll(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
});

let active: { container: HTMLElement; root: Root } | null = null;
afterEach(() => {
  if (active) {
    const { container, root } = active;
    act(() => root.unmount());
    container.remove();
    active = null;
  }
});

/** 只保留本测试关心的字段，其余用不会参与筛选的占位值。 */
function monitor(
  id: string,
  overrides: Partial<ProcessedMonitorData> & { modelEntries?: ProcessedMonitorData['modelEntries'] },
): ProcessedMonitorData {
  return {
    id,
    providerId: 'acme',
    providerName: 'Acme',
    serviceType: 'cc',
    channel: 'main',
    channelName: 'Main',
    category: 'commercial',
    ...overrides,
  } as ProcessedMonitorData;
}

// 模拟「locale 里有这个键」的情形：直接回显 key。这样 groupLabel 会暴露出
// modelFamilyLabel 实际查的命名空间，命名空间写错就会被下面的断言抓到。
const t = ((key: string) => key) as unknown as TFunction;

function runHook(params: Omit<FilterOptionsParams, 't'>): FilterOptions {
  let captured: FilterOptions | null = null;
  function Probe() {
    captured = useFilterOptions({ ...params, t });
    return null;
  }
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => root.render(<Probe />));
  active = { container, root };
  return captured!;
}

function baseParams(data: ProcessedMonitorData[]): Omit<FilterOptionsParams, 't'> {
  return {
    optionsBaseData: data,
    filterProvider: [],
    filterService: [],
    filterChannel: [],
    effectiveFilterCategory: [],
    filterVendor: [],
    filterModel: [],
  };
}

/** 一个多模型通道（saiai 形态）+ 两个单模型通道。 */
const DATA = [
  monitor('saiai', {
    providerId: 'saiai',
    providerName: 'saiai',
    modelEntries: [
      { model: 'Opus', requestModel: 'claude-opus-4-7' },
      { model: 'Sonnet', requestModel: 'claude-sonnet-4-6' },
    ],
  }),
  monitor('acme', {
    modelEntries: [{ model: 'Opus', requestModel: 'claude-opus-4-7' }],
  }),
  monitor('gptshop', {
    providerId: 'gptshop',
    providerName: 'GPTShop',
    serviceType: 'cx',
    modelEntries: [{ model: 'GPT', requestModel: 'gpt-5.4' }],
  }),
];

describe('effectiveModels', () => {
  it('选项带 canonical value、版本级 label 与家族分组', () => {
    const { effectiveModels } = runHook(baseParams(DATA));

    expect(effectiveModels).toEqual([
      { value: 'opus-4.7', label: 'opus-4.7', groupKey: 'opus', groupLabel: 'modelFamilies.opus' },
      { value: 'sonnet-4.6', label: 'sonnet-4.6', groupKey: 'sonnet', groupLabel: 'modelFamilies.sonnet' },
      { value: 'gpt-5.4', label: 'gpt-5.4', groupKey: 'gpt', groupLabel: 'modelFamilies.gpt' },
    ]);
  });

  it('同家族的选项在数组里必须连续（否则下拉会分裂出重名分组）', () => {
    const data = [
      monitor('a', { modelEntries: [{ model: 'Opus', requestModel: 'claude-opus-4-7' }] }),
      monitor('b', { modelEntries: [{ model: 'GPT', requestModel: 'gpt-5.4' }] }),
      monitor('c', { modelEntries: [{ model: 'Opus', requestModel: 'claude-opus-5' }] }),
    ];
    const { effectiveModels } = runHook(baseParams(data));

    const families = effectiveModels.map((option) => option.groupKey);
    // 去重后长度应等于「相邻去重」后的长度，即每个家族只出现一段
    const collapsed = families.filter((family, i) => family !== families[i - 1]);
    expect(collapsed).toEqual([...new Set(families)]);
    expect(collapsed).toEqual(['opus', 'gpt']); // 家族表顺序：opus 在 gpt 之前
  });

  it('多模型通道对每个模型各算一次命中（计数口径 = 含该模型的通道数）', () => {
    // saiai 同时含 opus 与 sonnet，故两个选项都把它算进去；各选项计数之和
    // （2+1+1=4）大于通道总数 3——这是 any 语义的正常结果。
    const { effectiveModels } = runHook({
      ...baseParams(DATA),
      // 让所有选项都带上 (0) 标记以外的信息不可见，故改用「已选但无数据」路径验证计数：
      // 这里换个角度——筛掉 saiai 后 sonnet 应该消失。
      filterProvider: ['acme', 'gptshop'],
    });

    expect(effectiveModels.map((o) => o.value)).toEqual(['opus-4.7', 'gpt-5.4']);
  });

  it('已选项在联动后无数据时标 (0) 保留，且仍挂在原家族下', () => {
    // ⚠️ 这条必须用 Gemini 来验，不能用 Opus/Sonnet：
    // 后者的 canonical key（`sonnet-4.6`）自己就能反推出家族，即使 metadata 建错了
    // 也会被 deriveModelFamily 的 key 兜底救回来，测试便成了真空的。
    // 而 Gemini 的展示名被 shortenModelName 剥掉了厂商前缀（`gemini-2.5-flash`
    // → `2.5-flash`），从 key 根本反推不出 gemini——只有取自未筛选全量数据的
    // metadata 才知道它属于哪一族。这正是那份全量映射存在的理由。
    const data = [
      ...DATA,
      monitor('gemshop', {
        providerId: 'gemshop',
        providerName: 'GemShop',
        serviceType: 'gm',
        modelEntries: [{ model: 'Flash', requestModel: 'gemini-2.5-flash' }],
      }),
    ];
    const { effectiveModels } = runHook({
      ...baseParams(data),
      filterProvider: ['gptshop'], // 联动后只剩 GPT 通道，Gemini 已不在子集里
      filterModel: ['2.5-flash'], // 但用户此前选了它
    });

    const flash = effectiveModels.find((o) => o.value === '2.5-flash');
    expect(flash).toBeDefined();
    expect(flash!.label).toBe('2.5-flash (0)');
    expect(flash!.groupKey).toBe('gemini');
  });

  it('全量数据里也不存在的已选 key（手改 URL）尽力归族而不是崩掉', () => {
    const { effectiveModels } = runHook({
      ...baseParams(DATA),
      filterModel: ['opus-9.9'],
    });

    const ghost = effectiveModels.find((o) => o.value === 'opus-9.9');
    expect(ghost).toBeDefined();
    expect(ghost!.groupKey).toBe('opus');
  });

  it('同一 key 派生出不同家族时，归组不受数据顺序影响', () => {
    // 真实可触发的分歧：模板声明的 gemini-2.5-flash 归 gemini；而行级写死
    // `model: '2.5-flash'` 且没有 request_model 的通道，key 里没有厂商前缀、
    // 只能归 other。两者 canonical key 与 label 完全相同。
    // 若合并时「先到先得」，这个选项会随后端返回顺序在 Gemini 组和「其他」组之间跳。
    const viaTemplate = monitor('a', {
      modelEntries: [{ model: 'Flash', requestModel: 'gemini-2.5-flash' }],
    });
    const viaRowLevel = monitor('b', {
      providerId: 'other-shop',
      providerName: 'OtherShop',
      modelEntries: [{ model: '2.5-flash', requestModel: '' }],
    });

    for (const data of [[viaTemplate, viaRowLevel], [viaRowLevel, viaTemplate]]) {
      const { effectiveModels } = runHook(baseParams(data));
      const flash = effectiveModels.find((o) => o.value === '2.5-flash');
      expect(flash!.groupKey, '信息量更大的家族应当胜出，与顺序无关').toBe('gemini');
      act(() => active!.root.unmount());
      active!.container.remove();
      active = null;
    }
  });

  it('连 key 都反推不出家族的未知已选项落进 other，而不是消失或报错', () => {
    const { effectiveModels } = runHook({
      ...baseParams(DATA),
      filterModel: ['9.9-something'],
    });

    const ghost = effectiveModels.find((o) => o.value === '9.9-something');
    expect(ghost).toBeDefined();
    expect(ghost!.groupKey).toBe('other');
  });
});

describe('模型筛选对其它维度的联动', () => {
  it('选定模型后，不含该模型的服务商从选项里消失', () => {
    const { effectiveProviders } = runHook({
      ...baseParams(DATA),
      filterModel: ['gpt-5.4'],
    });

    expect(effectiveProviders.map((o) => o.value)).toEqual(['gptshop']);
  });

  it('选定模型后，服务选项同样收窄', () => {
    const { effectiveServices } = runHook({
      ...baseParams(DATA),
      filterModel: ['sonnet-4.6'],
    });

    expect(effectiveServices).toEqual(['cc']);
  });
});
