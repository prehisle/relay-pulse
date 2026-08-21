import { describe, it, expect } from 'vitest';
import type { ModelOption, OnboardingMeta } from '../../types/onboarding';
import {
  describeModelSelection,
  isSelectionValid,
  resolveDefaultSelection,
  selectionFromOption,
} from './modelSelection';

const haiku: ModelOption = {
  key: 'cc-haiku-arith',
  label: 'Claude Haiku 4.5',
  vendor: 'anthropic',
  template: 'cc-haiku-arith',
  request_model: 'claude-haiku-4-5-20251001',
};

const opus: ModelOption = {
  key: 'cc-opus-arith',
  label: 'Claude Opus 4.7',
  vendor: 'anthropic',
  template: 'cc-opus-arith',
  request_model: 'claude-opus-4-7',
};

// 第一方厂商模型现在也是一个专属模板条目，与原厂模型同形状——
// 没有行级模型、没有可编辑标记，模型 ID 只作只读展示。
const glm: ModelOption = {
  key: 'cc-glm52-arith',
  label: 'GLM-5.2',
  vendor: 'zhipu',
  template: 'cc-glm52-arith',
  request_model: 'glm-5.2',
};

const meta = {
  test_types: [{ id: 'cc', name: 'cc', description: '', default_variant: 'cc-haiku-arith', variants: [] }],
  models_by_service: { cc: [opus, haiku, glm] },
} as unknown as OnboardingMeta;

describe('resolveDefaultSelection', () => {
  it('优先选中该 service 的自助默认模板对应的条目，而不是目录首项', () => {
    // 目录首项是 opus，但后端声明的默认是 haiku（最便宜、上游覆盖最广）
    expect(resolveDefaultSelection(meta, 'cc')).toEqual({
      modelKey: 'cc-haiku-arith',
      testVariant: 'cc-haiku-arith',
    });
  });

  it('默认模板不在目录里时退回首项', () => {
    const shifted = {
      ...meta,
      test_types: [{ ...meta.test_types[0], default_variant: 'cc-nosuch' }],
    } as OnboardingMeta;
    expect(resolveDefaultSelection(shifted, 'cc')?.modelKey).toBe('cc-opus-arith');
  });

  it('该 service 没有任何可选模型时返回 null（部署异常，不硬选）', () => {
    expect(resolveDefaultSelection(meta, 'gm')).toBeNull();
    expect(resolveDefaultSelection(null, 'cc')).toBeNull();
  });
});

describe('isSelectionValid', () => {
  it('目录里存在的 key 有效，换 service 后失效', () => {
    expect(isSelectionValid(meta, 'cc', 'cc-haiku-arith')).toBe(true);
    expect(isSelectionValid(meta, 'gm', 'cc-haiku-arith')).toBe(false);
  });

  it('空 key 与已下线的 key 一律失效', () => {
    expect(isSelectionValid(meta, 'cc', '')).toBe(false);
    expect(isSelectionValid(meta, 'cc', 'cc-retired')).toBe(false);
  });
});

describe('selectionFromOption', () => {
  it('选中一个模型只产生「选项键 + 模板」两项', () => {
    // 模型 ID、厂商、请求形态都由模板在服务端决定，表单不再持有它们——
    // 这条断言就是「提交方无从指定模型元数据」这条不变量在前端的落点。
    expect(selectionFromOption(glm)).toEqual({
      modelKey: 'cc-glm52-arith',
      testVariant: 'cc-glm52-arith',
    });
    expect(Object.keys(selectionFromOption(glm))).toEqual(['modelKey', 'testVariant']);
  });
});

describe('describeModelSelection', () => {
  it('显示人话名，不显示模板名', () => {
    expect(describeModelSelection(meta, 'cc', haiku.key)).toBe('Claude Haiku 4.5');
    expect(describeModelSelection(meta, 'cc', glm.key)).toBe('GLM-5.2');
  });

  it('目录取不到时回落到 key，不返回空串', () => {
    expect(describeModelSelection(null, 'cc', 'cc-haiku-arith')).toBe('cc-haiku-arith');
  });
});
