// @vitest-environment jsdom
//
// 冷板原因常驻行的三条性质：
//   ① board=cold 且原因非空 → 文案渲染出来（不必悬浮），完整内容进 title；
//   ② 非冷板行绝不渲染 —— 判据是通道自身的 board，不是"当前在哪个 tab"，
//      因为 board=all 视图里冷热混排，一处判错就会给热板通道挂上停探说明；
//   ③ 原因为空/纯空白 → 整块不渲染（生产上大半冷板通道没写原因，不能占位出噪音）。
// 用 react-dom/client 在 jsdom 真实渲染，沿用 modelVendorColumn.test 形态、零新依赖。
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { describe, it, expect, beforeAll } from 'vitest';
import { I18nextProvider } from 'react-i18next';
import i18n from '../i18n';
import { ColdReasonNote } from './ColdReasonNote';
import type { BoardValue } from '../types';

beforeAll(async () => {
  if (!i18n.isInitialized) await i18n.init();
});

function render(board: BoardValue, reason?: string): HTMLDivElement {
  const container = document.createElement('div');
  document.body.appendChild(container);
  act(() => {
    createRoot(container).render(
      <I18nextProvider i18n={i18n}>
        <ColdReasonNote board={board} reason={reason} />
      </I18nextProvider>,
    );
  });
  return container;
}

describe('ColdReasonNote', () => {
  it('冷板 + 有原因：文案常驻可见，完整内容进 title', () => {
    const c = render('cold', '上游长期不可用，已停止探测');
    expect(c.textContent).toContain('上游长期不可用，已停止探测');
    const withTitle = c.querySelector('[title]');
    expect(withTitle?.getAttribute('title')).toContain('上游长期不可用，已停止探测');
    // 非真空断言：确实渲染了元素，不是靠空 container 蒙混过关
    expect(c.querySelector('span')).not.toBeNull();
  });

  it('热板/备板即使带着原因也不渲染', () => {
    expect(render('hot', '不该出现的原因').textContent).toBe('');
    expect(render('secondary', '不该出现的原因').textContent).toBe('');
  });

  it('冷板但原因为空或纯空白：整块不渲染', () => {
    expect(render('cold', undefined).textContent).toBe('');
    expect(render('cold', '').textContent).toBe('');
    expect(render('cold', '   ').textContent).toBe('');
  });
});
