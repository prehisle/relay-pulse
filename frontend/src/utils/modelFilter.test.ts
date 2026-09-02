// 「模型」筛选器的取值与匹配语义守卫。
//
// 两条性质最容易在改动中被无声破坏，故重点钉死：
//   ① **所见即所筛** —— 筛选选项的文字必须与表格「模型」列逐字一致。两者一旦
//      各算各的，用户会在下拉里看到表格中根本不存在的名字。本文件用同一组
//      真实数据同时驱动 modelDisplayName 与表格的 getModelDisplayList 做对拍。
//   ② **any 语义** —— 通道只要有任一 layer 命中就算命中。现有 vendor 轴用的是
//      all-agree（所有 layer 同厂商才算），照搬到模型上会让生产中 18 个多模型
//      通道（saiai / 88code / modelflare 等）永远筛不出来。
import { describe, expect, it } from 'vitest';
import { getModelDisplayList } from '../components/table/modelNames';
import {
  collectModelOptions,
  matchesModelKeys,
  modelDisplayName,
  modelFilterKey,
} from './modelFilter';

/** 取自生产 /api/status?board=all 的真实 layer 形态。 */
const SAIAI_CC = [
  { model: 'Opus', requestModel: 'claude-opus-4-7' },
  { model: 'Sonnet', requestModel: 'claude-sonnet-4-6' },
  { model: 'Fable 5.1', requestModel: 'claude-fable-5-1' },
];
const SINGLE_HAIKU = [{ model: 'Haiku', requestModel: 'claude-haiku-4-5-20251001' }];

describe('modelDisplayName：与表格模型列同源', () => {
  it.each([
    ['claude-haiku-4-5-20251001', 'haiku-4.5'],
    ['claude-opus-4-7', 'opus-4.7'],
    ['claude-opus-5', 'opus-5'],
    ['gpt-5.6-terra', 'gpt-5.6-terra'],
    ['gemini-2.5-flash', '2.5-flash'],
  ])('%s → %s', (requestModel, expected) => {
    expect(modelDisplayName({ model: 'ignored', requestModel })).toBe(expected);
  });

  it('request_model 为空时回落到 model 展示名', () => {
    expect(modelDisplayName({ model: 'Opus', requestModel: '' })).toBe('Opus');
  });

  it('两者都空时给出与表格一致的占位符', () => {
    expect(modelDisplayName({ model: '', requestModel: '' })).toBe('-');
  });

  // 这是防漂移的对拍：表格列与筛选选项必须逐字相同，否则「所见即所筛」是假的。
  it('与表格 getModelDisplayList 的输出逐字一致', () => {
    for (const entries of [SAIAI_CC, SINGLE_HAIKU]) {
      expect(entries.map(modelDisplayName)).toEqual(getModelDisplayList(entries));
    }
  });
});

describe('modelFilterKey：canonical 折叠', () => {
  it('大小写与空白折叠到同一个键', () => {
    // 生产当前不会分裂（键来自 request_model），但 request_model 为空回落到
    // model 展示名时会——那时 `Haiku` 与 `haiku` 必须是同一个筛选项。
    expect(modelFilterKey('Haiku')).toBe(modelFilterKey('haiku'));
    expect(modelFilterKey('  Opus  ')).toBe(modelFilterKey('opus'));
  });
});

describe('collectModelOptions：选项派生', () => {
  it('给出 key / label / family 三元组', () => {
    expect(collectModelOptions(SAIAI_CC)).toEqual([
      { key: 'opus-4.7', label: 'opus-4.7', family: 'opus' },
      { key: 'sonnet-4.6', label: 'sonnet-4.6', family: 'sonnet' },
      { key: 'fable-5.1', label: 'fable-5.1', family: 'fable' },
    ]);
  });

  it('family 由 request_model 决定，不受展示名影响', () => {
    // 展示名被 shortenModelName 剥掉了 gemini- 前缀（叫 2.5-flash），
    // 但归族必须仍是 gemini——family 走原始 request_model 而不是 label。
    const [option] = collectModelOptions([{ model: 'Flash', requestModel: 'gemini-2.5-flash' }]);
    expect(option).toEqual({ key: '2.5-flash', label: '2.5-flash', family: 'gemini' });
  });

  it('同一通道内重复模型只产出一个选项', () => {
    const options = collectModelOptions([
      { model: 'Haiku', requestModel: 'claude-haiku-4-5-20251001' },
      { model: 'Haiku', requestModel: 'claude-haiku-4-5-20251001' },
    ]);
    expect(options).toHaveLength(1);
  });

  it('没有 layer 时返回空数组而不是抛异常', () => {
    expect(collectModelOptions(undefined)).toEqual([]);
    expect(collectModelOptions([])).toEqual([]);
  });
});

describe('matchesModelKeys：any 语义', () => {
  it('多模型通道命中其中任一模型即整行命中', () => {
    // 这条是本文件存在的理由。若照搬 vendor 的 all-agree 语义，saiai 这类通道
    // 会因为「不是所有 layer 都是 Opus」而被筛掉，18 个多模型通道全部消失。
    expect(matchesModelKeys(SAIAI_CC, new Set(['opus-4.7']))).toBe(true);
    expect(matchesModelKeys(SAIAI_CC, new Set(['sonnet-4.6']))).toBe(true);
    expect(matchesModelKeys(SAIAI_CC, new Set(['fable-5.1']))).toBe(true);
  });

  it('多选时命中任一即可', () => {
    expect(matchesModelKeys(SINGLE_HAIKU, new Set(['opus-4.7', 'haiku-4.5']))).toBe(true);
  });

  it('一个都不沾则不命中', () => {
    expect(matchesModelKeys(SAIAI_CC, new Set(['haiku-4.5']))).toBe(false);
  });

  it('没有 layer 的通道在筛选生效时被排除', () => {
    // 与 vendor 轴口径一致：「未知」不属于任何一个具体选项。
    expect(matchesModelKeys(undefined, new Set(['haiku-4.5']))).toBe(false);
    expect(matchesModelKeys([], new Set(['haiku-4.5']))).toBe(false);
  });

  it('用 canonical key 比较，不受原始大小写影响', () => {
    expect(matchesModelKeys([{ model: 'Haiku', requestModel: '' }], new Set(['haiku']))).toBe(true);
  });
});
