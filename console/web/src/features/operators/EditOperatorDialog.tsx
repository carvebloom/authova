import { useRef, useState, type FormEvent } from 'react'
import { ApiError } from '../../api/client'
import * as api from '../../api/endpoints'
import type { AdminRole, PlatformOperator } from '../../api/types'
import { useProtection } from '../../app/protectionState'
import { Button } from '../../shared/components/Button'
import { Dialog } from '../../shared/components/Dialog'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { SelectField, TextAreaField } from '../../shared/components/Field'
import { roleLabel } from '../../shared/utils/format'

import { isAdminRole, OPERATOR_STATUSES } from './model'
export function EditOperatorDialog({
  operator,
  onClose,
  onUpdated,
}: {
  operator: PlatformOperator | null
  onClose: () => void
  onUpdated: () => void
}) {
  const [role, setRole] = useState<AdminRole>(
    operator && isAdminRole(operator.role) ? operator.role : 'auditor',
  )
  const [status, setStatus] = useState(operator?.status ?? 'active')
  const [reason, setReason] = useState('')
  const [reasonError, setReasonError] = useState<string | null>(null)
  const [formError, setFormError] = useState<ApiError | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const submission = useRef(false)
  const [frozen, setFrozen] = useState(false)
  const [reloading, setReloading] = useState(false)
  const releaseProtection = useProtection(operator !== null && (submitting || frozen || reloading))
  const reloadCurrent = async () => {
    if (!operator || submission.current) return
    submission.current = true
    setReloading(true)
    try {
      let cursor: string | undefined
      const seen = new Set<string>()
      let found: PlatformOperator | undefined
      for (let page = 0; page < 1000; page++) {
        const result = await api.listPlatformOperators({ cursor, limit: 100 })
        found = result.items.find((item) => item.id === operator.id)
        if (found || !result.next_cursor) break
        if (seen.has(result.next_cursor))
          throw new ApiError({ status: 502, detail: '分页游标异常，无法核对状态。' })
        seen.add(result.next_cursor)
        cursor = result.next_cursor
      }
      if (!found) throw new ApiError({ status: 404, detail: '未能核对当前运营账号。' })
      if (!isAdminRole(found.role))
        throw new ApiError({ status: 409, detail: '当前角色无法编辑，请联系部署方。' })
      setRole(found.role)
      setStatus(found.status)
      setReason('')
      setFormError(null)
      setFrozen(false)
    } catch (error) {
      setFormError(error instanceof ApiError ? error : new ApiError({ status: 0 }))
    } finally {
      submission.current = false
      setReloading(false)
    }
  }

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (submission.current || frozen) return
    if (!operator) return
    const trimmed = reason.trim()
    if (!trimmed) {
      setReasonError('请填写操作原因')
      return
    }
    setReasonError(null)
    setFormError(null)
    submission.current = true
    setSubmitting(true)
    try {
      await api.updatePlatformOperator(operator.id, { role, status, reason: trimmed })
      releaseProtection()
      onUpdated()
    } catch (err) {
      // 最后一名可用拥有者等冲突由服务端拒绝（409），原样展示且保持打开
      const failure = err instanceof ApiError ? err : new ApiError({ status: 0 })
      if (failure.isOutcomeUnknown || failure.status === 409) setFrozen(true)
      setFormError(failure)
    } finally {
      submission.current = false
      setSubmitting(false)
    }
  }

  return (
    <Dialog
      open={operator !== null}
      title="变更运营账号"
      onClose={onClose}
      dismissible={!submitting && !frozen && !reloading}
    >
      {frozen ? (
        <div className="notice notice-warn" role="status">
          <p>请先核对当前账号状态，再重新编辑。</p>
          <Button onClick={reloadCurrent} loading={reloading}>
            重新加载最新状态
          </Button>
        </div>
      ) : null}
      <form className="form" onSubmit={handleSubmit} noValidate aria-label="变更运营账号">
        <fieldset className="submission-fields" disabled={submitting || frozen || reloading}>
          {operator ? (
            <p className="dialog-desc">
              账号 <strong>{operator.email}</strong>（{roleLabel(operator.role)}）
            </p>
          ) : null}
          {formError ? <ErrorNotice error={formError} /> : null}
          <SelectField
            label="角色"
            value={role}
            onChange={(e) => {
              if (isAdminRole(e.target.value)) setRole(e.target.value)
            }}
          >
            <option value="owner">拥有者（owner）</option>
            <option value="admin">管理员（admin）</option>
            <option value="auditor">审计员（auditor）</option>
          </SelectField>
          <SelectField label="状态" value={status} onChange={(e) => setStatus(e.target.value)}>
            {OPERATOR_STATUSES.map((s) => (
              <option key={s.value} value={s.value}>
                {s.label}（{s.value}）
              </option>
            ))}
          </SelectField>
          <TextAreaField
            label="操作原因"
            value={reason}
            onChange={(e) => {
              setReason(e.target.value)
              if (reasonError) setReasonError(null)
            }}
            error={reasonError ?? undefined}
            hint="将随请求一并记录到审计日志"
            rows={3}
            required
          />
          <div className="dialog-actions">
            <Button type="button" onClick={onClose} disabled={submitting || frozen || reloading}>
              取消
            </Button>
            <Button type="submit" variant="primary" loading={submitting}>
              保存
            </Button>
          </div>
        </fieldset>
      </form>
    </Dialog>
  )
}
