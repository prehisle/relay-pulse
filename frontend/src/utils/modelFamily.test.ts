// 模型家族归类守卫。
//
// 这张表是「模型」筛选器下拉分组的唯一依据，漏一条规则 = 该家族的通道在筛选器里
// 掉进「其他」组，用户按家族一键全选时静默漏掉它们。故本测试守两件事：
//   1. 归类正确 —— 用**生产真实全集**做表驱动（数据取自 relaypulse.top
//      /api/status?board=all 的 21 个 distinct request_model），删表里任一条规则必红。
//   2. 断言非真空 —— 21 个 case 必须真的覆盖到全部 11 个家族、且没有一个落进 other。
//      少了这条，把整张表删空、让所有输入都返回 'other' 也能让上面的用例「绿」。
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { MODEL_FAMILIES, MODEL_FAMILY_OTHER, deriveModelFamily, modelFamilyLabel } from './modelFamily';

/** 生产真实 request_model → 期望家族 code。新增家族时同步补 case。 */
const productionCases: [string, string][] = [
  // Anthropic 系：家族段在 claude- 之后
  ['claude-opus-4-6', 'opus'],
  ['claude-opus-4-7', 'opus'],
  ['claude-opus-4-8', 'opus'],
  ['claude-opus-5', 'opus'],
  ['claude-sonnet-4-6', 'sonnet'],
  ['claude-sonnet-5', 'sonnet'],
  ['claude-haiku-4-5-20251001', 'haiku'],
  ['claude-fable-5-1', 'fable'],

  // OpenAI 系：含 -sol / -terra / -luna 这类后缀变体
  ['gpt-5.4', 'gpt'],
  ['gpt-5.5', 'gpt'],
  ['gpt-5.6-sol', 'gpt'],
  ['gpt-5.6-terra', 'gpt'],
  ['gpt-5.6-luna', 'gpt'],

  // Google 系
  ['gemini-2.5-flash', 'gemini'],
  ['gemini-2.5-flash-thinking', 'gemini'],
  ['gemini-3-flash-preview', 'gemini'],

  // 国内厂商：一家一族
  ['deepseek-v4-pro', 'deepseek'],
  ['doubao-seed-2.1-turbo', 'doubao'],
  ['glm-5.2', 'glm'],
  ['kimi-k2.7-code', 'kimi'],
  ['minimax-m3', 'minimax'],
];

describe('deriveModelFamily：生产全集归类', () => {
  it.each(productionCases)('%s → %s', (requestModel, expected) => {
    expect(deriveModelFamily(requestModel)).toBe(expected);
  });

  // ── 以下三条是「咬人」断言：没有它们，一张空表也能让上面全绿 ──

  it('21 个生产 case 覆盖了家族表里除 other 外的每一个家族', () => {
    const covered = new Set(productionCases.map(([, family]) => family));
    const declared = MODEL_FAMILIES.map((f) => f.code).filter((code) => code !== MODEL_FAMILY_OTHER);
    expect([...covered].sort()).toEqual([...declared].sort());
  });

  it('没有任何生产 request_model 落进 other', () => {
    const fellThrough = productionCases
      .map(([requestModel]) => requestModel)
      .filter((requestModel) => deriveModelFamily(requestModel) === MODEL_FAMILY_OTHER);
    expect(fellThrough, '这些生产模型没被任何家族规则命中，会在筛选器里掉进「其他」组').toEqual([]);
  });

  it('家族之间的前缀互不吞并（deriveModelFamily 的 find() 因此与表顺序无关）', () => {
    // deriveModelFamily 用 find() 线性扫描，天然「先命中先赢」。只要任意两个家族的
    // 前缀不存在「A 是 B 的段前缀」关系，每个输入就恰好命中一族，顺序便不影响结果。
    // 没有这条约束，将来给某族加一个 'gpt-5' 之类的前缀会让 gpt 族被悄悄截胡——
    // 症状是某些模型随家族表排序而漂移，极难定位。
    const all = MODEL_FAMILIES.flatMap((f) => f.prefixes.map((prefix) => ({ code: f.code, prefix })));
    const swallowed: string[] = [];
    for (const a of all) {
      for (const b of all) {
        if (a.code === b.code || a.prefix === b.prefix) continue;
        // b 落在 a 的「前缀 + 段边界」判定内 = a 会先吃掉本该属于 b 的输入
        if (b.prefix.startsWith(a.prefix) && /[-. ]/.test(b.prefix[a.prefix.length] ?? '')) {
          swallowed.push(`${a.code}:"${a.prefix}" 吞并 ${b.code}:"${b.prefix}"`);
        }
      }
    }
    expect(swallowed).toEqual([]);
  });

  it('家族表顺序即下拉分组顺序，改动需显式改本断言', () => {
    // 顺序不是字母序而是「Anthropic 四族 → OpenAI → Google → 国内厂商 → 其他」，
    // 与 internal/modelvendor 的 catalog 排布口径一致（先原厂后国内）。
    expect(MODEL_FAMILIES.map((f) => f.code)).toEqual([
      'opus', 'sonnet', 'haiku', 'fable',
      'gpt', 'gemini',
      'deepseek', 'doubao', 'glm', 'kimi', 'minimax',
      'other',
    ]);
  });
});

describe('deriveModelFamily：回落与容错', () => {
  it('request_model 为空时回落到 model 展示名', () => {
    // native 族模板刻意不声明 request_model（见 templates/*-native-*.json），
    // 这类通道只有行级 model 展示名可用。
    expect(deriveModelFamily(undefined, 'Opus')).toBe('opus');
    expect(deriveModelFamily('', 'GPT-5.6-Terra')).toBe('gpt');
  });

  it('request_model 优先于 model', () => {
    // 两者都在时以实际请求的模型 ID 为准——展示名可能是跨版本的家族抽象。
    expect(deriveModelFamily('gemini-2.5-flash', 'Flash')).toBe('gemini');
  });

  it('大小写与空白不影响归类', () => {
    expect(deriveModelFamily('  CLAUDE-OPUS-4-8  ')).toBe('opus');
  });

  it('未知模型归 other 而不是抛异常', () => {
    expect(deriveModelFamily('totally-unknown-model-v9')).toBe(MODEL_FAMILY_OTHER);
    expect(deriveModelFamily(undefined, undefined)).toBe(MODEL_FAMILY_OTHER);
    expect(deriveModelFamily('', '')).toBe(MODEL_FAMILY_OTHER);
  });

  it('家族前缀不做子串匹配，避免误伤', () => {
    // 'glm' 出现在中间不该命中 glm 家族；规则必须锚定在模型 ID 的起始段。
    expect(deriveModelFamily('foo-glm-bar')).toBe(MODEL_FAMILY_OTHER);
  });

  it('前缀必须整段命中，粘连的更长词不算', () => {
    // 放宽成 startsWith 而不检查段边界的话，这一组会全部误归。
    expect(deriveModelFamily('gptish-1')).toBe(MODEL_FAMILY_OTHER);
    expect(deriveModelFamily('glmx-1')).toBe(MODEL_FAMILY_OTHER);
    expect(deriveModelFamily('opusx')).toBe(MODEL_FAMILY_OTHER);
  });

  it('下划线不是段边界，这类 ID 归 other 而不是误归', () => {
    // 已知的漏归形态（宁可掉进「其他」组，也不要把别家模型认成 GPT）。
    // 生产 21 个 ID 无一使用下划线；真出现了再扩边界字符集。
    expect(deriveModelFamily('gpt_5')).toBe(MODEL_FAMILY_OTHER);
  });

  it('通用词前缀只在整段命中时才归族', () => {
    // 'flash' 是为 Gemini 展示名回落（Flash / Flash Preview）加的通用词，风险最高。
    // 这里钉住它的作用范围：整段命中才算，其余一律 other。
    expect(deriveModelFamily(undefined, 'Flash')).toBe('gemini');
    expect(deriveModelFamily(undefined, 'Flash Preview')).toBe('gemini');
    expect(deriveModelFamily('flashpoint-1')).toBe(MODEL_FAMILY_OTHER);
  });
});

describe('modelFamilyLabel', () => {
  // i18n 完备性测试只读 JSON 键，发现不了函数把命名空间拼错。这里真调一次。
  const fakeT = ((key: string, opts?: { defaultValue?: string }) =>
    key === 'modelFamilies.opus' ? 'Opus' : (opts?.defaultValue ?? key)) as unknown as Parameters<
    typeof modelFamilyLabel
  >[0];

  it('查 modelFamilies.<code> 命名空间', () => {
    expect(modelFamilyLabel(fakeT, 'opus')).toBe('Opus');
  });

  it('词表外的 code 原样显示 code，不猜名字', () => {
    expect(modelFamilyLabel(fakeT, 'nonexistent')).toBe('nonexistent');
  });
});

describe('modelFamilies i18n 完备性', () => {
  // locales.parity.test.ts 的 B 类扫描只认字面键 t('a.b')，而家族 label 走的是
  // 动态键 t(`modelFamilies.${code}`)——那套守卫对它完全失明。故在此单独钉死。
  const LOCALES = ['zh-CN', 'en-US', 'ja-JP', 'ru-RU'];
  const localesDir = join(dirname(fileURLToPath(import.meta.url)), '..', 'i18n', 'locales');

  it.each(LOCALES)('%s 为每个家族 code 都提供了 label', (locale) => {
    const raw = JSON.parse(readFileSync(join(localesDir, `${locale}.json`), 'utf8')) as {
      modelFamilies?: Record<string, string>;
    };
    const missing = MODEL_FAMILIES.map((f) => f.code).filter((code) => !raw.modelFamilies?.[code]);
    expect(missing, `${locale} 缺少这些家族的 label，UI 会裸露 code`).toEqual([]);
  });
});
