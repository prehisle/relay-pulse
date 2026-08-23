import { describe, it, expect } from 'vitest';
import { shouldShowQualityPending } from './index';

describe('shouldShowQualityPending', () => {
  const base = { rpdiagEnabled: true, hasScore: false, serviceType: 'cc', board: 'hot' };
  it('active-board cc/cx 无 rpdiag match → 待测', () => {
    expect(shouldShowQualityPending(base)).toBe(true);
    expect(shouldShowQualityPending({ ...base, serviceType: 'cx' })).toBe(true);
    expect(shouldShowQualityPending({ ...base, board: 'secondary' })).toBe(true);
  });
  it('有 rpdiag match → 不占位', () => {
    expect(shouldShowQualityPending({ ...base, hasScore: true })).toBe(false);
  });
  it('gm / cold 板 / rpdiag 关闭 → 不占位', () => {
    expect(shouldShowQualityPending({ ...base, serviceType: 'gm' })).toBe(false);
    expect(shouldShowQualityPending({ ...base, board: 'cold' })).toBe(false);
    expect(shouldShowQualityPending({ ...base, rpdiagEnabled: false })).toBe(false);
  });
});
