import { useProtection } from '../../app/protectionState'
import { useRef, useState, type FormEvent } from 'react'
import { ApiError } from '../../api/client'
import { Button } from './Button'
import { Dialog } from './Dialog'
import { TextAreaField } from './Field'
import { ErrorNotice } from './ErrorNotice'
import { OutcomeUnknownNotice } from './OutcomeUnknownNotice'

export interface ReasonDialogProps {
  open: boolean
  title: string
  description?: string
  confirmLabel: string
  danger?: boolean
  /**
   * 页面级冻结：该动作先前提交结果未知且尚未核对。打开即处于冻结态，
   * 只能经 onReconcile 权威读回后重新编辑提交——防止“取消后立即重开”绕过冻结。
   */
  frozen?: boolean
  /** 权威读回（如 reloadApp / 列表 reload）；返回 false 表示读回失败，保持冻结 */
  onReconcile?: () => Promise<boolean | void>
  /** 结果未知时通知页面级留存记录 */
  onUnknown?: (error: ApiError) => void
  onClose: () => void
  /** 抛错即展示在对话框内，不关闭 */
  onSubmit: (reason: string) => Promise<void>
}

/**
 * 需要填写操作原因的确认对话框（暂停/恢复/禁用/撤销/轮换密钥共用）。
 * 安全语义：提交中禁止 Esc/遮罩/取消；同步互斥防同批次双提交；
 * 结果未知或 409 后锁住确认，只允许「权威读回 → 重新编辑 → 人工再提交」。
 */
export function ReasonDialog(props: ReasonDialogProps) {
  return <ReasonForm {...props} />
}

function ReasonForm({
  open,
  title,
  description,
  confirmLabel,
  danger = false,
  frozen = false,
  onReconcile,
  onUnknown,
  onClose,
  onSubmit,
}: ReasonDialogProps) {
  const [reason, setReason] = useState('')
  const [error, setError] = useState<ApiError | null>(null)
  const [fieldError, setFieldError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [reconciling, setReconciling] = useState(false)
  const [unknown, setUnknown] = useState<ApiError | null>(null)
  const [stale, setStale] = useState(false)
  // 同步互斥：同一事件批次内的双击/重复提交在 state 生效前即被拦截
  const busyRef = useRef(false)

  const locked = frozen || unknown !== null || stale
  const busy = submitting || reconciling
  useProtection(open && (busy || locked))

  const handleReconcile = async () => {
    if (!onReconcile || busyRef.current) return
    busyRef.current = true
    setReconciling(true)
    setError(null)
    try {
      const ok = await onReconcile()
      if (ok === false) return
      // 读回成功：解除冻结并清空旧输入（原因不会残留进下一次提交）
      setUnknown(null)
      setStale(false)
      setReason('')
      setFieldError(null)
    } catch (err) {
      setError(err instanceof ApiError ? err : new ApiError({ status: 0, detail: String(err) }))
    } finally {
      busyRef.current = false
      setReconciling(false)
    }
  }

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (busyRef.current || locked) return
    const trimmed = reason.trim()
    if (!trimmed) {
      setFieldError('请填写操作原因')
      return
    }
    busyRef.current = true
    setSubmitting(true)
    setError(null)
    try {
      await onSubmit(trimmed)
      onClose()
    } catch (err) {
      const apiErr =
        err instanceof ApiError ? err : new ApiError({ status: 0, detail: String(err) })
      if (apiErr.isOutcomeUnknown) {
        // 结果未知：锁定提交 + 通知页面级留存；权威读回前不得再提交
        setUnknown(apiErr)
        onUnknown?.(apiErr)
      } else if (apiErr.status === 409) {
        // 明确冲突：展示服务端原样错误并锁定，须读回最新状态后再操作
        setStale(true)
        setError(apiErr)
      } else {
        setError(apiErr)
      }
    } finally {
      busyRef.current = false
      setSubmitting(false)
    }
  }

  const reconcileAction = onReconcile ? (
    <Button type="button" size="sm" onClick={handleReconcile} loading={reconciling}>
      重新加载最新状态
    </Button>
  ) : null

  return (
    <Dialog open={open} title={title} onClose={onClose} dismissible={!busy && !locked}>
      <form className="form" onSubmit={handleSubmit} noValidate aria-label={title}>
        {description ? <p className="dialog-desc">{description}</p> : null}
        {unknown ? (
          <OutcomeUnknownNotice
            error={unknown}
            detail="该操作可能已在服务端生效（危险写操作盲目重复可能造成二次变更）。请点击「重新加载最新状态」读回权威状态，确认后再决定是否重新操作；核对前确认按钮保持锁定。"
            action={reconcileAction}
          />
        ) : frozen ? (
          <div className="notice notice-warn" role="alert">
            <div className="notice-body">
              <p className="notice-title">先前的提交结果未知，该操作已冻结</p>
              <p className="notice-detail">
                先前的请求可能已在服务端生效。请点击「重新加载最新状态」读回权威状态；核对完成前不能再次提交。
              </p>
            </div>
            {reconcileAction}
          </div>
        ) : null}
        {stale ? (
          <div className="notice notice-warn" role="alert">
            <div className="notice-body">
              <p className="notice-title">状态已变化（409 冲突）</p>
              <p className="notice-detail">
                目标状态刚被其他操作修改。请点击「重新加载最新状态」读回权威状态后再决定是否重新操作；系统不会自动重试。
              </p>
            </div>
            {error ? <ErrorNotice error={error} announce={false} /> : null}
            {reconcileAction}
          </div>
        ) : null}
        {error && !stale ? <ErrorNotice error={error} /> : null}
        <TextAreaField
          label="操作原因"
          value={reason}
          onChange={(e) => {
            setReason(e.target.value)
            if (fieldError) setFieldError(null)
          }}
          error={fieldError ?? undefined}
          hint="将随请求一并记录到审计日志"
          rows={3}
          required
          autoFocus
          disabled={busy || locked}
        />
        <div className="dialog-actions">
          <Button type="button" onClick={onClose} disabled={busy || locked}>
            取消
          </Button>
          <Button
            type="submit"
            variant={danger ? 'danger' : 'primary'}
            loading={submitting}
            disabled={locked}
          >
            {confirmLabel}
          </Button>
        </div>
      </form>
    </Dialog>
  )
}
