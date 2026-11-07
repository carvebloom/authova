/**
 * 幂等创建（创建 App / 客户端）的未确认请求记录。
 * 结果未知（网络错误 / outcome_unknown）时，请求可能已在服务端生效；
 * 记录原始 payload 与幂等键，保证同一未确认请求只能用原键重试。
 * 仅存内存（React state），绝不写入浏览器存储。
 */

/** 幂等回放窗口：与契约一致（client_secret 仅在创建或创建方幂等回放后至多 10 分钟内返回） */
export const IDEMPOTENCY_REPLAY_WINDOW_MS = 10 * 60 * 1000

export interface PendingCreate<P> {
  /** 首次提交时的完整 payload；变化即视为新请求 */
  payload: P
  /** 首次提交使用的 Idempotency-Key；同一未确认请求的重试必须复用 */
  key: string
  /** 首次提交时间（Date.now()）；回放窗口从首次提交起算（保守口径） */
  at: number
}

export function randomIdempotencyKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

/** 键序稳定的逐字段相等（payload 由同一构造函数生成，JSON 序列化即可靠） */
export function samePayload(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b)
}

/** 是否仍在幂等回放窗口内；超出后不得再宣称“原键重试安全”，须先核对实际状态 */
export function isReplayWindowOpen(at: number, now: number = Date.now()): boolean {
  return now - at < IDEMPOTENCY_REPLAY_WINDOW_MS
}
