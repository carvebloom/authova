import type { ReactNode } from 'react'
import { Button } from './Button'
import { Dialog } from './Dialog'
import { useProtection } from '../../app/protectionState'
import { CopyButton } from './CopyButton'

/**
 * 一次性秘密面板（client_secret / TOTP secret）：
 * 只存在 React state；显式点击确认按钮才关闭，不会被遮罩/超时误关导致秘密丢失。
 */
export function SecretPanel({
  title,
  description,
  secret,
  confirmLabel,
  onClose,
  children,
}: {
  title: string
  description: string
  secret: string
  confirmLabel: string
  onClose: () => void
  children?: ReactNode
}) {
  const release = useProtection(true, '请先安全保存一次性密钥并确认关闭，再离开此页面。')
  return (
    <Dialog open title={title} dismissible={false} onClose={() => {}}>
      <div className="secret-panel" role="alert">
        <p className="notice-detail">{description}</p>
        <div className="secret-row">
          <code className="mono secret-value">{secret}</code>
          <CopyButton value={secret} label="复制密钥" />
        </div>
        {children}
        <div className="notice-actions">
          <Button
            size="sm"
            onClick={() => {
              release()
              onClose()
            }}
          >
            {confirmLabel}
          </Button>
        </div>
      </div>
    </Dialog>
  )
}
