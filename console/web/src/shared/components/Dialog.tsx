import { useId, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { useModalA11y } from './useModalA11y'

export interface DialogProps {
  open: boolean
  title: string
  onClose: () => void
  children: ReactNode
  /** 较宽表单（如客户端编辑）使用 */
  wide?: boolean
  /** 提交中传 false：Esc/遮罩/关闭按钮均不可用，禁止取消与离开 */
  dismissible?: boolean
}

/** 可访问模态对话框：焦点陷阱、背景 inert、滚动锁、关闭后焦点恢复。 */
export function Dialog({
  open,
  title,
  onClose,
  children,
  wide = false,
  dismissible = true,
}: DialogProps) {
  const titleId = useId()
  const panelRef = useModalA11y<HTMLDivElement>({ open, onClose, dismissible })

  if (!open) return null

  return createPortal(
    <div
      className="dialog-overlay"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && dismissible) onClose()
      }}
    >
      <div
        ref={panelRef}
        className={`dialog${wide ? ' dialog-wide' : ''}`}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
      >
        <div className="dialog-head">
          <h2 className="dialog-title" id={titleId}>
            {title}
          </h2>
          {dismissible ? (
            <button type="button" className="icon-btn" aria-label="关闭" onClick={onClose}>
              <svg
                width={16}
                height={16}
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth={1.5}
                strokeLinecap="round"
                aria-hidden="true"
              >
                <path d="m6 6 12 12M18 6 6 18" />
              </svg>
            </button>
          ) : null}
        </div>
        <div className="dialog-body">{children}</div>
      </div>
    </div>,
    document.body,
  )
}
