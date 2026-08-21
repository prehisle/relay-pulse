// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import React from 'react';
import type { AuthCandidate } from '../types/change';

// 记录每次 apiPost 的 path 与 body，供断言请求契约。
interface PostCall {
  path: string;
  body: Record<string, unknown>;
}
const posts = vi.hoisted(() => [] as PostCall[]);

vi.mock('../utils/apiClient', () => ({
  apiGet: vi.fn(),
  apiPost: vi.fn((path: string, body: Record<string, unknown>) => {
    posts.push({ path, body });
    return Promise.resolve({ probe_id: 'pb-1', probe_status: 1, test_proof: 'proof', latency: 12 });
  }),
  apiPut: vi.fn(),
  apiDelete: vi.fn(),
  ApiError: class ApiError extends Error {
    status = 500;
  },
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (k: string) => k, i18n: { language: 'zh-CN' } }),
}));

import { useChangeRequest } from './useChangeRequest';

(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

let hook: ReturnType<typeof useChangeRequest>;
function Harness() {
  // eslint-disable-next-line react-hooks/globals -- 测试 harness：把 hook 返回值暴露给用例断言
  hook = useChangeRequest();
  return null;
}

const candidate = {
  provider: 'acme',
  service: 'cc',
  channel: 'o-nat-glm',
  monitor_key: 'acme--cc--o-nat-glm',
  test_type: 'cc',
  base_url: 'https://old.example.com',
  default_test_variant: 'cc-native-arith-nothink',
} as unknown as AuthCandidate;

describe('useChangeRequest 测试步请求契约', () => {
  let root: Root;

  beforeEach(async () => {
    posts.length = 0;
    root = createRoot(document.createElement('div'));
    await act(async () => {
      root.render(React.createElement(Harness));
    });
    await act(async () => {
      hook.setApiKey('sk-old-key');
      hook.setSelectedCandidate(candidate);
    });
  });

  afterEach(async () => {
    await act(async () => {
      root.unmount();
    });
  });

  // 后端要求 target_key 必填：轮换 key 时请求里带的是**新** key，它还没进 AuthIndex，
  // 服务端只能靠 target_key 定位通道、取它正在跑的模型。漏发这个字段整个测试步会 400，
  // 而 apiPost 的 body 没有静态类型约束，TypeScript 不会替我们发现。
  it('轮换 key 时带上 target_key，且用新 key 探测', async () => {
    await act(async () => {
      hook.setNewApiKey('sk-new-key');
    });
    await act(async () => {
      await hook.runTest();
    });

    const call = posts.find((p) => p.path === '/api/change/test');
    expect(call).toBeDefined();
    expect(call!.body.target_key).toBe('acme--cc--o-nat-glm');
    expect(call!.body.api_key).toBe('sk-new-key');
    // 未改 base_url 时用候选自身的地址
    expect(call!.body.base_url).toBe('https://old.example.com');
  });

  it('改 base_url 时带上 target_key，并用变更后的地址探测', async () => {
    await act(async () => {
      hook.updateChange('base_url', 'https://new.example.com');
    });
    await act(async () => {
      await hook.runTest();
    });

    const call = posts.find((p) => p.path === '/api/change/test');
    expect(call).toBeDefined();
    expect(call!.body.target_key).toBe('acme--cc--o-nat-glm');
    expect(call!.body.base_url).toBe('https://new.example.com');
    // 没轮换 key 时沿用认证用的 key
    expect(call!.body.api_key).toBe('sk-old-key');
  });
});
