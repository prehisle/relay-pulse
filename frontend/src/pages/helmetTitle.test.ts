import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { describe, it, expect } from 'vitest'

/**
 * react-helmet-async v3 起，`<title>` 只认**单个字符串子节点**。
 * 写成 `<title>{t('x')} | RelayPulse</title>` 时 children 是数组，v3 会静默渲染成
 * 空 `<title></title>`——页面照常渲染、控制台无报错，只有标签页标题空掉，
 * 2026-09-05 随 react-helmet-async 2→3 升级在 /contact/apply 与 /admin 上实际发生过。
 * 故这里静态锁死写法：title 的内容必须整体是一个表达式（模板字符串拼接）。
 */
const SRC = join(process.cwd(), 'src')

function collectTsx(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) collectTsx(full, out)
    else if (entry.endsWith('.tsx')) out.push(full)
  }
  return out
}

describe('Helmet <title> 写法守卫', () => {
  it('每个 <title> 的内容都是单个表达式，不是「表达式 + 字面量」的数组', () => {
    const offenders: string[] = []
    for (const file of collectTsx(SRC)) {
      const source = readFileSync(file, 'utf8')
      for (const match of source.matchAll(/<title>([\s\S]*?)<\/title>/g)) {
        const inner = match[1].trim()
        const singleExpression = inner.startsWith('{') && inner.endsWith('}')
        const plainLiteral = !inner.includes('{')
        if (!singleExpression && !plainLiteral) {
          offenders.push(`${file.slice(SRC.length + 1)}: <title>${inner}</title>`)
        }
      }
    }
    expect(offenders).toEqual([])
  })
})
