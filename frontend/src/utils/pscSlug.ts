/**
 * PSC 代号（provider/service/channel 三段机器标识）的格式规则与建议值生成。
 *
 * 规则与后端 `config.ValidateProviderSlug` 逐条对齐——它同时是 `monitors.d/` 的文件名分段与
 * 公开 URL slug，前端这份只是把错误提前到管理员输入的那一刻，后端仍是唯一权威闸。
 * 尤其「不能连续短横线」不是洁癖：文件名形如 `{provider}--{service}--{channel}`，段内再出现
 * `--` 会让分段解析错位。
 */

/** 代号长度上限（与后端一致，按字节/ASCII 字符计）。 */
export const PSC_SLUG_MAX_LEN = 100;

/** 判断一个已 trim 的非空代号是否合法。空串请由调用方按「不覆盖」处理，不要传进来。 */
export function isValidPscSlug(value: string): boolean {
  if (!value || value.length > PSC_SLUG_MAX_LEN) return false;
  if (!/^[a-z0-9-]+$/.test(value)) return false;
  if (value.startsWith('-') || value.endsWith('-')) return false;
  return !value.includes('--');
}

/** 把任意文本规范成候选代号；得不到合法值时返回空串（绝不返回半成品让人误采纳）。 */
function normalizeToSlug(raw: string): string {
  const slug = raw
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/-+/g, '-')
    .replace(/^-|-$/g, '')
    .slice(0, PSC_SLUG_MAX_LEN)
    .replace(/-$/, '');
  return isValidPscSlug(slug) ? slug : '';
}

/**
 * 从官网地址推一个 provider 代号候选：取域名里除顶级域外最长的一段。
 *
 * `api.yintu.cc` → `yintu`、`fast.qianxing.pro` → `qianxing`——中转商的官网普遍是
 * `{子域}.{品牌}.{tld}`，品牌段通常也最长。这只是**建议**，最终值由管理员确认，猜错的代价是
 * 他改一格；所以宁可用一条看得懂的启发式，也不引入公共后缀表那种重依赖。
 */
export function suggestSlugFromUrl(websiteURL: string): string {
  const trimmed = websiteURL.trim();
  if (!trimmed) return '';

  let host: string;
  try {
    host = new URL(/^https?:\/\//i.test(trimmed) ? trimmed : `https://${trimmed}`).hostname;
  } catch {
    return '';
  }

  // punycode 段（国际化域名，`中文域名.中国` → `xn--fiq06l2rdsvs`）排除：它字符集合法却毫无可读性，
  // 且规范化会把 `xn--` 里的连续短横线压掉、变成一个看不出来源的乱串。这类域名不给建议更诚实。
  const labels = host.replace(/^www\./i, '').split('.').filter((l) => l && !l.startsWith('xn--'));
  const candidates = labels.length > 1 ? labels.slice(0, -1) : labels;
  const longest = candidates.reduce((best, cur) => (cur.length > best.length ? cur : best), '');
  return normalizeToSlug(longest);
}

/** 从服务商展示名推候选代号（`SaiAI` → `saiai`）；中文名等推不出合法值时返回空串。 */
export function suggestSlugFromName(providerName: string): string {
  return normalizeToSlug(providerName);
}
