import { describe, it, expect } from 'vitest';
import { canonicalEndpointUrl } from './endpointUrl';

describe('canonicalEndpointUrl', () => {
  it('给缺协议的输入补 https://', () => {
    expect(canonicalEndpointUrl('api.example.com')).toBe('https://api.example.com');
    expect(canonicalEndpointUrl('  api.example.com/v1  ')).toBe('https://api.example.com/v1');
  });

  it('已带协议的原样保留（含 http，由后端决定拒不拒）', () => {
    expect(canonicalEndpointUrl('https://api.example.com/v1')).toBe('https://api.example.com/v1');
    expect(canonicalEndpointUrl('HTTP://api.example.com')).toBe('HTTP://api.example.com');
  });

  it('不改写路径与尾部斜杠——对上游而言可能是不同资源', () => {
    expect(canonicalEndpointUrl('https://api.example.com/V1/')).toBe('https://api.example.com/V1/');
  });

  it('空输入返回空串，不产生 https:// 这种半截地址', () => {
    expect(canonicalEndpointUrl('   ')).toBe('');
  });

  it('同一输入两次调用结果一致（测试与提交必须发出同一个串）', () => {
    const raw = ' api.example.com/v1 ';
    expect(canonicalEndpointUrl(raw)).toBe(canonicalEndpointUrl(canonicalEndpointUrl(raw)));
  });
});
