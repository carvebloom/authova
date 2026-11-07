import { createContext, useCallback, useContext, useEffect, useId } from 'react'
export type Register = (id: string, message: string | null) => void
export const ProtectionContext = createContext<Register>(() => {})

/** Release before an intentional navigation after confirmed success. */
export function useProtection(active: boolean, message = '请先完成或核对当前操作，再离开此页面。') {
  const register = useContext(ProtectionContext)
  const id = useId()
  useEffect(() => {
    register(id, active ? message : null)
    return () => register(id, null)
  }, [active, id, message, register])
  return useCallback(() => register(id, null), [id, register])
}
