import { describe, it, expect } from 'vitest';
import { isValidPscSlug, suggestSlugFromName, suggestSlugFromUrl, PSC_SLUG_MAX_LEN } from './pscSlug';

describe('isValidPscSlug', () => {
  it('接受合法代号', () => {
    for (const v of ['yintu', 'sai-ai', 'o-max-main', 'a1', '0']) {
      expect(isValidPscSlug(v)).toBe(true);
    }
  });

  it('拒绝非法代号', () => {
    // 连续短横线单列：它过得了「仅 [a-z0-9-]」这类朴素校验，却会让
    // `{provider}--{service}--{channel}` 文件名分段解析错位，是后端最该挡的形状。
    for (const v of ['sai--ai', '银兔', 'SaiAI', '-sai', 'sai-', 'sai ai', 'sai_ai', '']) {
      expect(isValidPscSlug(v)).toBe(false);
    }
    expect(isValidPscSlug('a'.repeat(PSC_SLUG_MAX_LEN + 1))).toBe(false);
  });
});

describe('suggestSlugFromUrl', () => {
  it('取域名中除顶级域外最长的一段', () => {
    expect(suggestSlugFromUrl('https://api.yintu.cc')).toBe('yintu');
    expect(suggestSlugFromUrl('https://fast.qianxing.pro/docs')).toBe('qianxing');
    expect(suggestSlugFromUrl('https://www.example.com')).toBe('example');
    expect(suggestSlugFromUrl('example.com')).toBe('example');
  });

  it('推不出合法值时返回空串而不是半成品', () => {
    expect(suggestSlugFromUrl('')).toBe('');
    expect(suggestSlugFromUrl('https://中文域名.中国')).toBe('');
  });
});

describe('suggestSlugFromName', () => {
  it('ASCII 展示名转小写代号', () => {
    expect(suggestSlugFromName('SaiAI')).toBe('saiai');
    expect(suggestSlugFromName('Sai  AI')).toBe('sai-ai');
  });

  it('中文展示名推不出代号', () => {
    expect(suggestSlugFromName('银兔')).toBe('');
  });
});
