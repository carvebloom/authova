import { describe, expect, it } from 'vitest'
import { formatTime, formatUnixSeconds, shortenId, splitList } from './format'

describe('format 工具', () => {
  it('formatTime 解析 RFC3339，非法输入原样返回', () => {
    expect(formatTime('2026-10-12T08:00:00Z')).not.toBe('2026-10-12T08:00:00Z')
    expect(formatTime('not-a-date')).toBe('not-a-date')
    expect(formatTime('')).toBe('—')
    expect(formatTime(undefined)).toBe('—')
  })

  it('formatUnixSeconds 解析 Unix 秒；0/缺省展示占位符', () => {
    expect(formatUnixSeconds(0)).toBe('—')
    expect(formatUnixSeconds(undefined)).toBe('—')
    expect(formatUnixSeconds(null)).toBe('—')
    // 1760227200 = 2025-10-12T00:00:00Z，任何时区都落在 2025-10-11/12
    const rendered = formatUnixSeconds(1760227200)
    expect(rendered).toContain('2025')
    expect(rendered).not.toBe('1760227200')
  })

  it('splitList 支持空格/逗号/换行分隔', () => {
    expect(splitList('openid profile,email\nprofile')).toEqual([
      'openid',
      'profile',
      'email',
      'profile',
    ])
    expect(splitList('  ')).toEqual([])
  })

  it('shortenId 截断长 ID 保留首尾', () => {
    expect(shortenId('abcdefghijklmnop')).toBe('abcdefgh…mnop')
    expect(shortenId('short')).toBe('short')
  })
})
