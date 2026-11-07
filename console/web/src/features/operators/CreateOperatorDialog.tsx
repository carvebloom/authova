import { useRef, useState, type FormEvent } from 'react'
import { ApiError } from '../../api/client'
import * as api from '../../api/endpoints'
import type { AdminRole, CreatedOperator } from '../../api/types'
import { useProtection } from '../../app/protectionState'
import { Button } from '../../shared/components/Button'
import { Dialog } from '../../shared/components/Dialog'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { PasswordField, SelectField, TextField } from '../../shared/components/Field'
import { OutcomeUnknownNotice } from '../../shared/components/OutcomeUnknownNotice'
import { EMAIL_RE } from '../../shared/utils/format'

import { isAdminRole } from './model'
export function CreateOperatorDialog({
  open,
  onClose,
  onCloseAndRefresh,
  onCreated,
  previousUnknown,
  onUnknown,
  onReconciled,
}: {
  open: boolean
  previousUnknown: { email: string; error: ApiError } | null
  onUnknown: (value: { email: string; error: ApiError }) => void
  onReconciled: () => void
  onClose: () => void
  /** 结果未知流程中关闭对话框并刷新列表（核对用） */
  onCloseAndRefresh: () => void
  onCreated: (result: CreatedOperator) => void
}) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState<AdminRole>('auditor')
  const [errors, setErrors] = useState<{ email?: string; password?: string }>({})
  const [formError, setFormError] = useState<ApiError | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const submission = useRef(false)
  const releaseProtection = useProtection(submitting)
  // 结果未知：该接口无幂等支持，且先前的请求仍可能延迟生效——
  // 本对话框实例永久锁定提交；列表查证只用于指导后续处置，绝不作为“安全重试”依据
  const [unknown, setUnknown] = useState<{ email: string; error: ApiError } | null>(previousUnknown)
  const [checking, setChecking] = useState(false)
  const [checkError, setCheckError] = useState<ApiError | null>(null)
  const [checkOutcome, setCheckOutcome] = useState<'found' | 'absent' | 'inconclusive' | null>(null)

  const runCheck = async () => {
    if (!unknown) return
    setChecking(true)
    setCheckError(null)
    try {
      const target = unknown.email.toLowerCase()
      let cursor: string | undefined
      const seen = new Set<string>()
      for (let page = 0; page < 1000; page++) {
        const res = await api.listPlatformOperators({ cursor, limit: 100 })
        if (res.items.some((op) => op.email.trim().toLowerCase() === target)) {
          setCheckOutcome('found')
          return
        }
        if (!res.next_cursor) {
          // 未找到不证明先前请求不会迟到生效，仍不允许自动重试。
          setCheckOutcome('absent')
          return
        }
        if (seen.has(res.next_cursor)) break
        seen.add(res.next_cursor)
        cursor = res.next_cursor
      }
      setCheckOutcome('inconclusive')
    } catch (err) {
      setCheckError(
        err instanceof ApiError ? err : new ApiError({ status: 0, detail: String(err) }),
      )
    } finally {
      setChecking(false)
    }
  }

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (submission.current) return
    // 一旦结果未知，本对话框实例不再接受任何提交（查证不存在也不能解锁）
    if (unknown) return
    const next: typeof errors = {}
    const trimmedEmail = email.trim()
    if (!EMAIL_RE.test(trimmedEmail)) next.email = '请输入有效的邮箱地址'
    if (password.length < 15) next.password = '初始密码至少 15 个字符'
    setErrors(next)
    setFormError(null)
    if (Object.keys(next).length > 0) return
    submission.current = true
    setSubmitting(true)
    try {
      const result = await api.createPlatformOperator({ email: trimmedEmail, password, role })
      // 清空本地凭据状态；密钥只进入一次性展示面板，绝不写入 web 存储
      setEmail('')
      setPassword('')
      onCreated(result)
    } catch (err) {
      const apiErr =
        err instanceof ApiError ? err : new ApiError({ status: 0, detail: String(err) })
      if (apiErr.isOutcomeUnknown) {
        setPassword('')
        setUnknown({ email: trimmedEmail, error: apiErr })
        onUnknown({ email: trimmedEmail, error: apiErr })
      } else {
        // 明确失败（400/403/409 等）：账号未创建，可直接修正后重试
        setFormError(apiErr)
      }
    } finally {
      releaseProtection()
      submission.current = false
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} title="创建运营账号" onClose={onClose} dismissible={!submitting}>
      <form className="form" onSubmit={handleSubmit} noValidate>
        <fieldset className="submission-fields" disabled={submitting}>
          {unknown ? (
            <OutcomeUnknownNotice
              error={unknown.error}
              detail={`创建运营账号不支持幂等重试，「${unknown.email}」的账号可能已创建成功。本次提交已锁定；可检查账号列表核对实际状态，但查证结果不会让此处恢复提交。`}
              action={
                checkOutcome === 'found' ? (
                  <p className="notice-detail">
                    列表中已存在「{unknown.email}」：先前的创建实际已成功，但一次性 TOTP
                    密钥未能传达。平台账号邮箱唯一，不能禁用后同名重建；请由管理员使用本地
                    recover-operator 流程为该账号恢复初始 TOTP。
                    <Button
                      size="sm"
                      onClick={() => {
                        onReconciled()
                        onCloseAndRefresh()
                      }}
                    >
                      关闭并刷新列表
                    </Button>
                  </p>
                ) : checkOutcome === 'inconclusive' ? (
                  <p className="notice-detail">
                    列表内容不完整，无法完成核对。本次提交已锁定；请关闭对话框，人工核实平台账号状态后再决定后续操作。
                    <Button size="sm" onClick={onCloseAndRefresh}>
                      关闭并刷新列表
                    </Button>
                  </p>
                ) : checkOutcome === 'absent' ? (
                  <p className="notice-detail">
                    当前列表中未找到「{unknown.email}
                    」，但先前的请求仍可能延迟生效，不能据此视为可以安全重试。
                    本次提交已锁定；请稍后刷新列表再次核对，或由平台管理员核实。本界面不会将一次未找到转换为可以安全重试。
                    <Button size="sm" onClick={onCloseAndRefresh}>
                      关闭并刷新列表
                    </Button>
                  </p>
                ) : (
                  <p className="notice-detail">
                    <Button size="sm" onClick={runCheck} loading={checking}>
                      检查账号列表
                    </Button>
                  </p>
                )
              }
            />
          ) : null}
          {checkError ? <ErrorNotice error={checkError} onRetry={runCheck} /> : null}
          {formError ? <ErrorNotice error={formError} /> : null}
          <TextField
            label="邮箱"
            type="email"
            autoComplete="off"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            error={errors.email}
            required
            autoFocus
            disabled={unknown !== null}
          />
          <PasswordField
            label="初始密码"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            error={errors.password}
            hint="仅用于本次创建；控制台不会保存，请通过安全渠道传达"
            required
            disabled={unknown !== null}
          />
          <SelectField
            label="角色"
            disabled={unknown !== null}
            value={role}
            onChange={(e) => {
              if (isAdminRole(e.target.value)) setRole(e.target.value)
            }}
            hint="拥有者可管理运营账号；管理员管理应用与数据；审计员只读"
          >
            <option value="owner">拥有者（owner）</option>
            <option value="admin">管理员（admin）</option>
            <option value="auditor">审计员（auditor）</option>
          </SelectField>
          <div className="dialog-actions">
            <Button type="button" onClick={onClose} disabled={submitting}>
              取消
            </Button>
            <Button
              type="submit"
              variant="primary"
              loading={submitting}
              disabled={unknown !== null}
            >
              创建
            </Button>
          </div>
        </fieldset>
      </form>
    </Dialog>
  )
}
