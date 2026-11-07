import { useContext } from 'react'
import { SessionContext, type SessionValue } from './context'

export function useSession(): SessionValue {
  const ctx = useContext(SessionContext)
  if (!ctx) throw new Error('useSession 必须在 SessionProvider 内使用')
  return ctx
}
