import { useOutletContext } from 'react-router-dom'
import type { App } from '../api/types'
import { useSession } from '../features/session/useSession'

export interface AppContextValue {
  app: App
  reloadApp: () => Promise<boolean>
}

export function useAppContext(): AppContextValue {
  return useOutletContext<AppContextValue>()
}

/**
 * 当前 App 页面内的有效能力：App 级管理员只能操作自己的 App
 * （服务端仍是最终权威；此处在 UI 层隐藏越权入口）。
 */
export function useAppCapabilities() {
  const { app, reloadApp } = useAppContext()
  const session = useSession()
  const ownApp = !session.isAppScoped || session.operator?.app_id === app.id
  return {
    app,
    reloadApp,
    session,
    canMutate: session.canMutate && ownApp,
    canSuspendResume: session.canSuspendResume && ownApp,
    canRotateKeys: session.canRotateKeys && ownApp,
    canManageLoginPolicy: session.canManageLoginPolicy && ownApp,
    canManageAdministrators: session.canManageAdministrators && ownApp,
  }
}
