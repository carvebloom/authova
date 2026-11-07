import { describe, expect, it } from 'vitest'
import {
  IDEMPOTENCY_REPLAY_WINDOW_MS,
  isReplayWindowOpen,
  randomIdempotencyKey,
  samePayload,
} from './idempotency'

describe('idempotency utils', () => {
  it('randomIdempotencyKey 生成非空且互不相同的键', () => {
    const a = randomIdempotencyKey()
    const b = randomIdempotencyKey()
    expect(a).toBeTruthy()
    expect(b).toBeTruthy()
    expect(a).not.toBe(b)
  })

  it('samePayload 按内容比较：字段一致为同请求，任何差异为新请求', () => {
    expect(samePayload({ name: 'A', slug: 'a' }, { name: 'A', slug: 'a' })).toBe(true)
    expect(samePayload({ name: 'A', slug: 'a' }, { name: 'A', slug: 'b' })).toBe(false)
    expect(samePayload({ uris: ['https://a/cb'] }, { uris: ['https://a/cb'] })).toBe(true)
    expect(
      samePayload(
        { uris: ['https://a/cb', 'https://b/cb'] },
        { uris: ['https://b/cb', 'https://a/cb'] },
      ),
    ).toBe(false)
  })

  it('回放窗口：10 分钟内可用原键重试，超出后必须先核对实际状态', () => {
    const at = 1_000_000
    expect(isReplayWindowOpen(at, at)).toBe(true)
    expect(isReplayWindowOpen(at, at + IDEMPOTENCY_REPLAY_WINDOW_MS - 1)).toBe(true)
    expect(isReplayWindowOpen(at, at + IDEMPOTENCY_REPLAY_WINDOW_MS)).toBe(false)
    expect(isReplayWindowOpen(at, at + IDEMPOTENCY_REPLAY_WINDOW_MS * 2)).toBe(false)
  })
})
