import { describe, it, expect } from 'vitest';
import type { ModelOption, OnboardingMeta } from '../../types/onboarding';
import {
  CUSTOM_MODEL_KEY,
  describeModelSelection,
  isSelectionValid,
  needsModelId,
  resolveDefaultSelection,
  selectionForCustom,
  selectionFromOption,
} from './modelSelection';

const haiku: ModelOption = {
  key: 'cc-haiku-arith',
  label: 'Claude Haiku 4.5',
  vendor: 'anthropic',
  template: 'cc-haiku-arith',
  model: '',
  request_model: 'claude-haiku-4-5-20251001',
  editable: false,
};

const opus: ModelOption = {
  key: 'cc-opus-arith',
  label: 'Claude Opus 4.7',
  vendor: 'anthropic',
  template: 'cc-opus-arith',
  model: '',
  request_model: 'claude-opus-4-7',
  editable: false,
};

const glm: ModelOption = {
  key: 'cc-native-arith-nothink|glm-5.2',
  label: 'GLM-5.2（智谱）',
  vendor: 'zhipu',
  template: 'cc-native-arith-nothink',
  model: 'glm-5.2',
  request_model: 'glm-5.2',
  editable: true,
};

const meta = {
  test_types: [{ id: 'cc', name: 'cc', description: '', default_variant: 'cc-haiku-arith', variants: [] }],
  models_by_service: { cc: [opus, haiku, glm] },
  request_shapes_by_service: {
    cc: [
      { template: 'cc-native-arith-nothink', label: '默认开启思考、且支持关闭' },
      { template: 'cc-native-arith-512', label: '默认开启思考、且无法关闭' },
    ],
  },
} as unknown as OnboardingMeta;

describe('resolveDefaultSelection', () => {
  it('优先选中该 service 的自助默认模板对应的条目，而不是目录首项', () => {
    // 目录首项是 opus，但后端声明的默认是 haiku（最便宜、上游覆盖最广）
    expect(resolveDefaultSelection(meta, 'cc')).toEqual({
      modelKey: 'cc-haiku-arith',
      model: '',
      modelVendor: 'anthropic',
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

  it('自填只在该 service 有请求形态时才成立', () => {
    expect(isSelectionValid(meta, 'cc', CUSTOM_MODEL_KEY)).toBe(true);
    expect(isSelectionValid(meta, 'gm', CUSTOM_MODEL_KEY)).toBe(false);
  });
});

describe('selectionFromOption / selectionForCustom', () => {
  it('目录条目把模板、模型、厂商一次带全', () => {
    expect(selectionFromOption(glm)).toEqual({
      modelKey: 'cc-native-arith-nothink|glm-5.2',
      model: 'glm-5.2',
      modelVendor: 'zhipu',
      testVariant: 'cc-native-arith-nothink',
    });
  });

  it('自填清空模型与厂商，请求形态取第一个', () => {
    expect(selectionForCustom(meta.request_shapes_by_service.cc)).toEqual({
      modelKey: CUSTOM_MODEL_KEY,
      model: '',
      modelVendor: '',
      testVariant: 'cc-native-arith-nothink',
    });
  });

  it('没有请求形态时自填给出空模板（调用方据此不放行测试）', () => {
    expect(selectionForCustom([]).testVariant).toBe('');
  });
});

describe('needsModelId', () => {
  it('自填与第一方厂商条目要填模型 ID，专属模板条目不要', () => {
    const options = meta.models_by_service.cc;
    expect(needsModelId(options, CUSTOM_MODEL_KEY)).toBe(true);
    expect(needsModelId(options, glm.key)).toBe(true);
    expect(needsModelId(options, haiku.key)).toBe(false);
    expect(needsModelId(options, 'unknown')).toBe(false);
  });
});

describe('describeModelSelection', () => {
  it('专属模板显示人话名，不显示模板名', () => {
    expect(describeModelSelection(meta, 'cc', haiku.key, '')).toBe('Claude Haiku 4.5');
  });

  it('第一方厂商显示人话名 + 实际填的模型 ID', () => {
    expect(describeModelSelection(meta, 'cc', glm.key, 'glm-5.2-air')).toBe('GLM-5.2（智谱）（glm-5.2-air）');
  });

  it('自填直接显示模型 ID', () => {
    expect(describeModelSelection(meta, 'cc', CUSTOM_MODEL_KEY, 'kimi-k2.7-code')).toBe('kimi-k2.7-code');
  });

  it('目录取不到时回落，不返回空串', () => {
    expect(describeModelSelection(null, 'cc', 'cc-haiku-arith', '')).toBe('cc-haiku-arith');
  });
});
