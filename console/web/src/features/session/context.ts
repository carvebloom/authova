import { createContext } from 'react'
import type { ApiError } from '../../api/client'
import type { Operator } from '../../api/types'
import type { Capabilities } from './capabilities'

/**
 * loading：正在恢复会话；anonymous：确定未登录（/bff/me 返回 401）；
 * ready：已登录；unavailable：暂时无法确认会话（网络错误 / 503 依赖故障），
 * 本地登录状态不清除，展示可手动重试的故障页。
 */
export type SessionStatus = 'loading' | 'anonymous' | 'ready' | 'unavailable'

export interface SessionValue extends Capabilities {
  status: SessionStatus
  operator: Operator | null
  /** 已登录会话被 401 终止时为 true，登录页提示“会话已过期”；登录失败不置位 */
  sessionExpired: boolean
  /** status 为 unavailable 时的引导错误 */
  bootstrapError: ApiError | null
  /** unavailable 状态下手动重试恢复会话（不自动重试） */
  retryBootstrap: () => void
  signIn: (input: { email: string; password: string; totp: string }) => Promise<void>
  /** 退出登录：失败（结果未知/后端不可达）会抛错，调用方必须展示并保留当前登录 */
  signOut: () => Promise<void>
}

export const SessionContext = createContext<SessionValue | null>(null)
