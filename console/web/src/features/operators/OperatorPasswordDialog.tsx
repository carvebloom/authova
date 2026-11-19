import { useEffect, useRef, useState, type FormEvent } from 'react'
import { ApiError } from '../../api/client'
import * as api from '../../api/endpoints'
import { useProtection } from '../../app/protectionState'
import { Button } from '../../shared/components/Button'
import { Dialog } from '../../shared/components/Dialog'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { PasswordField, TextAreaField } from '../../shared/components/Field'
import { useSession } from '../session/useSession'

export function OperatorPasswordDialog({
  target,
  self,
  previousUnknown,
  onUnknown,
  onClose,
  onUpdated,
}: {
  target: { id: string; email: string }
  self: boolean
  previousUnknown: boolean
  onUnknown: () => void
  onClose: () => void
  onUpdated: () => void
}) {
  const [current, setCurrent] = useState('')
  const [password, setPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [reason, setReason] = useState('')
  const [validation, setValidation] = useState<string | null>(null)
  const [error, setError] = useState<ApiError | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [unknown, setUnknown] = useState(previousUnknown)
  const inFlight = useRef(false)
  const mounted = useRef(true)
  const { operator, status } = useSession()
  const actor = `${status}:${operator?.app_id ?? ''}:${operator?.id ?? ''}:${operator?.role ?? ''}`
  const currentActor = useRef(actor)
  currentActor.current = actor
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  const release = useProtection(submitting || unknown)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (inFlight.current || unknown) return
    setValidation(null)
    if (!current) return setValidation('请输入你当前的登录密码')
    if ([...password].length < 15 || new TextEncoder().encode(password).length > 1024)
      return setValidation('新密码至少 15 个字符，且不超过 1024 字节')
    if (password !== confirmation) return setValidation('两次输入的新密码不一致')
    if (!reason.trim() || reason.trim().length > 500)
      return setValidation('请填写操作原因（不超过 500 个字符）')
    inFlight.current = true
    setSubmitting(true)
    setError(null)
    const requestActor = actor
    try {
      await api.updatePlatformOperatorPassword(target.id, {
        current_password: current,
        password,
        reason: reason.trim(),
      })
      if (!mounted.current || currentActor.current !== requestActor) return
      release()
      onUpdated()
    } catch (failure) {
      if (!mounted.current || currentActor.current !== requestActor) return
      const value = failure instanceof ApiError ? failure : new ApiError({ status: 0 })
      if (value.isOutcomeUnknown || value.status >= 500) {
        setUnknown(true)
        onUnknown()
      }
      setError(value)
    } finally {
      // Credentials never enter pending mutations or browser storage.
      inFlight.current = false
      if (mounted.current) {
        setCurrent('')
        setPassword('')
        setConfirmation('')
        setSubmitting(false)
      }
    }
  }

  return (
    <Dialog
      open
      title={self ? '修改我的密码' : '重置运营账号密码'}
      onClose={onClose}
      dismissible={!submitting}
    >
      <p className="dialog-desc">
        账号：{target.email}。修改后该账号的所有现有会话将失效，TOTP 配置保持不变。
      </p>
      {unknown ? (
        <p className="notice notice-warn" role="status">
          改密结果未知，可能已经生效。本次操作已锁定，请退出当前会话并用目标账号重新登录核实，不要重复提交。
        </p>
      ) : null}
      {error ? <ErrorNotice error={error} /> : null}
      {validation ? (
        <p role="alert" className="field-error-text">
          {validation}
        </p>
      ) : null}
      <form className="form" aria-label="修改运营账号密码" onSubmit={submit} noValidate>
        <fieldset className="submission-fields" disabled={submitting || unknown}>
          <PasswordField
            label="你当前的登录密码"
            autoComplete="current-password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
            required
            hint="验证操作者本人，即使重置其他账号也填写你自己的密码"
          />
          <PasswordField
            label="新密码"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            hint="至少 15 个字符；请通过安全渠道传达给账号使用者"
          />
          <PasswordField
            label="确认新密码"
            autoComplete="new-password"
            value={confirmation}
            onChange={(e) => setConfirmation(e.target.value)}
            required
          />
          <TextAreaField
            label="操作原因"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            maxLength={500}
            required
            hint="将记录到审计日志，请勿填写密码"
          />
          <Button type="submit" variant="primary" loading={submitting}>
            确认修改密码
          </Button>
        </fieldset>
        <div className="dialog-actions">
          <Button type="button" onClick={onClose} disabled={submitting}>
            关闭
          </Button>
        </div>
      </form>
    </Dialog>
  )
}
