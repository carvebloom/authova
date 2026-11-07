import type { ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { useModalA11y } from './useModalA11y'
import { IconButton } from './Button'

export interface DrawerProps {
  open: boolean
  title: string
  onClose: () => void
  children: ReactNode
}

/** 移动端导航抽屉：与 Dialog 相同的焦点陷阱 / Esc / 焦点恢复规则。 */
export function Drawer({ open, title, onClose, children }: DrawerProps) {
  const panelRef = useModalA11y<HTMLDivElement>({ open, onClose })

  if (!open) return null

  return createPortal(
    <>
      <div
        className="drawer-overlay"
        onMouseDown={(e) => {
          if (e.target === e.currentTarget) onClose()
        }}
      />
      <div
        ref={panelRef}
        className="drawer"
        role="dialog"
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
      >
        <div className="drawer-head">
          <span className="brand-name">{title}</span>
          <IconButton icon="close" label="关闭导航" onClick={onClose} />
        </div>
        {children}
      </div>
    </>,
    document.body,
  )
}
