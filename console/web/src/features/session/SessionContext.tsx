import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import {
  ApiError,
  advanceAuthGeneration,
  getAuthGeneration,
  setCsrfToken,
  setUnauthorizedHandler,
} from '../../api/client'
import * as api from '../../api/endpoints'
import type { Operator } from '../../api/types'
import { deriveCapabilities } from './capabilities'
import { SessionContext, type SessionStatus, type SessionValue } from './context'

export function SessionProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<SessionStatus>('loading')
  const [operator, setOperator] = useState<Operator | null>(null)
  const [sessionExpired, setSessionExpired] = useState(false)
  const [bootstrapError, setBootstrapError] = useState<ApiError | null>(null)
  const [tick, setTick] = useState(0)
  const statusRef = useRef(status)
  useEffect(() => {
    statusRef.current = status
  }, [status])

  const applySession = useCallback((info: { operator: Operator; csrf_token: string }) => {
    setCsrfToken(info.csrf_token)
    setOperator(info.operator)
    setBootstrapError(null)
    setStatus('ready')
  }, [])

  const clearSession = useCallback(() => {
    setCsrfToken(null)
    setOperator(null)
    setStatus('anonymous')
  }, [])

  useEffect(() => {
    let cancelled = false
    // 引导请求启动时的认证 generation：若期间用户完成了新登录，迟到的 bootstrap
    // 响应不得用旧 operator/CSRF 覆盖新会话
    const bootstrapGen = getAuthGeneration()
    setUnauthorizedHandler(() => {
      if (cancelled) return
      // 只有已建立的会话被 401 终止才算“会话过期”；首次访问或故障期的 401 不误导。
      // 推进 generation：同一事件后的其余迟到 401 不再重复触发，也不误杀新会话。
      advanceAuthGeneration()
      if (statusRef.current === 'ready') setSessionExpired(true)
      clearSession()
    })
    api
      .fetchMe()
      .then((info) => {
        if (cancelled || getAuthGeneration() !== bootstrapGen) return
        applySession(info)
      })
      .catch((err: unknown) => {
        if (cancelled || getAuthGeneration() !== bootstrapGen) return
        // 严格区分：仅 401 视为未登录；503 依赖故障 / 网络错误进入可手动重试的故障态，
        // 不因暂时故障清除本地登录状态（HttpOnly Cookie 仍在，恢复后可直接重试）
        if (err instanceof ApiError && err.isUnauthorized) {
          clearSession()
        } else {
          setBootstrapError(
            err instanceof ApiError ? err : new ApiError({ status: 0, detail: String(err) }),
          )
          setStatus('unavailable')
        }
      })
    return () => {
      cancelled = true
      setUnauthorizedHandler(null)
    }
  }, [applySession, clearSession, tick])

  const retryBootstrap = useCallback(() => {
    setBootstrapError(null)
    setStatus('loading')
    setTick((t) => t + 1)
  }, [])

  const signIn = useCallback(
    async (input: { email: string; password: string; totp: string }) => {
      const info = await api.login(input)
      // 新会话建立：推进 generation，旧会话期间启动的请求迟到 401 不得清除新会话
      advanceAuthGeneration()
      setSessionExpired(false)
      applySession(info)
    },
    [applySession],
  )

  /**
   * 退出登录。仅在后端明确成功（204）或明确表示会话已失效（401）时清理本地状态；
   * 网络错误 / 5xx / 结果未知时 HttpOnly Cookie 仍有效，本地登录必须保留，
   * 错误原样抛给调用方展示（绝不假装已退出——否则刷新页面即会“复活”会话）。
   */
  const signOut = useCallback(async () => {
    try {
      await api.logout()
    } catch (err) {
      if (err instanceof ApiError && err.isUnauthorized) {
        advanceAuthGeneration()
        setSessionExpired(false)
        clearSession()
        return
      }
      throw err
    }
    advanceAuthGeneration()
    setSessionExpired(false)
    clearSession()
  }, [clearSession])

  const value = useMemo<SessionValue>(() => {
    return {
      status,
      operator,
      sessionExpired,
      bootstrapError,
      retryBootstrap,
      signIn,
      signOut,
      ...deriveCapabilities(operator),
    }
  }, [status, operator, sessionExpired, bootstrapError, retryBootstrap, signIn, signOut])

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>
}
