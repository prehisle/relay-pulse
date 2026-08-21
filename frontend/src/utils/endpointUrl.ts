/**
 * 接入点 URL 的规范化：**测试时打的地址**与**提交时落地的地址**必须逐字节相同。
 *
 * 后端把 test_api_url 与 base_url 按「同一接入点」比较（协议 / host+端口 / 路径 / 查询串，
 * 见 internal/urlutil.SameEndpoint），且测试证明本身就绑定了测试时用的那个地址。前端若在
 * 两处各自处理（历史上正是如此：提交时给 base_url 补 https://、test_api_url 却发原文），
 * 合法提交会被后端当成「换了地址」拒掉。
 *
 * 故所有出口只用这一个函数：测试请求、提交的 base_url、提交的 test_api_url。
 */

/** 剥首尾空白并补上 https:// 前缀（用户常只填 host）。不做其它改写——路径大小写、尾部斜杠
 *  一律保留原样，因为它们对上游而言可能是不同资源，改写会让用户测的和上架的对不上。 */
export function canonicalEndpointUrl(raw: string): string {
  const trimmed = raw.trim();
  if (!trimmed) return '';
  if (!/^https?:\/\//i.test(trimmed)) return `https://${trimmed}`;
  return trimmed;
}
