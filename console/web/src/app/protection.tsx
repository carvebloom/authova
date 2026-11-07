import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { useBlocker } from 'react-router-dom'
import { createPortal } from 'react-dom'
import { useSession } from '../features/session/useSession'

import { ProtectionContext, type Register } from './protectionState'

/** One router-level barrier covers clicks, Back and hash navigation. */
export function ProtectionProvider({ children }: { children: ReactNode }) {
  const { status } = useSession()
  const entries = useRef(new Map<string, string>())
  const [revision, setRevision] = useState(0)
  const [notice, setNotice] = useState('')
  const register = useCallback<Register>((id, message) => {
    if (message === null) {
      if (!entries.current.delete(id)) return
    } else {
      if (entries.current.get(id) === message) return
      entries.current.set(id, message)
    }
    setRevision((v) => v + 1)
  }, [])
  const blocker = useBlocker(() => status === 'ready' && entries.current.size > 0)
  useEffect(() => {
    if (blocker.state === 'blocked') {
      setNotice(Array.from(entries.current.values())[0] ?? '请先完成当前操作，再离开此页面。')
      blocker.reset()
    }
  }, [blocker])
  useEffect(() => {
    if (entries.current.size === 0 || status !== 'ready') setNotice('')
    const beforeUnload = (event: BeforeUnloadEvent) => {
      if (status !== 'ready' || entries.current.size === 0) return
      event.preventDefault()
      event.returnValue = ''
    }
    window.addEventListener('beforeunload', beforeUnload)
    return () => window.removeEventListener('beforeunload', beforeUnload)
  }, [revision, status])
  return (
    <ProtectionContext.Provider value={register}>
      {children}
      {notice
        ? createPortal(
            <div className="navigation-notice" role="status">
              {notice}
            </div>,
            document.body,
          )
        : null}
    </ProtectionContext.Provider>
  )
}
