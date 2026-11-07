import { useRef, useState } from 'react'
import * as api from '../../api/agents'
import { ApiError } from '../../api/client'
import type { Agent, AgentInstallation, AgentGrant } from '../../api/types'
import { useProtection } from '../../app/protectionState'
import { Dialog } from '../../shared/components/Dialog'
import { Button } from '../../shared/components/Button'
import { TextAreaField } from '../../shared/components/Field'
import { ErrorNotice } from '../../shared/components/ErrorNotice'

export type AgentAction =
  | { kind: 'agent'; record: Agent }
  | { kind: 'installation' | 'renew'; record: AgentInstallation }
  | { kind: 'grant'; record: AgentGrant }
export function AgentActionDialog({
  appId,
  action,
  onDone,
  onClose,
}: {
  appId: string
  action: AgentAction
  onDone: () => void
  onClose: () => void
}) {
  const [current, setCurrent] = useState(action)
  const [reason, setReason] = useState('')
  const [confirmed, setConfirmed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [frozen, setFrozen] = useState(false)
  const [error, setError] = useState<ApiError | null>(null)
  const lock = useRef(false)
  useProtection(busy || frozen)
  const status = current.record.status === 'active' ? 'disabled' : 'active'
  const title =
    current.kind === 'grant'
      ? '撤销授权'
      : current.kind === 'renew'
        ? '更新自主运行授权'
        : `${status === 'disabled' ? '停用' : '启用'}${current.kind === 'agent' ? ' Agent' : '接入'}`
  const read = async () => {
    if (lock.current) return
    lock.current = true
    setBusy(true)
    try {
      if (current.kind === 'agent')
        setCurrent({ kind: 'agent', record: await api.getAgent(appId, current.record.id) })
      else if (current.kind === 'grant')
        setCurrent({ kind: 'grant', record: await api.getAgentGrant(appId, current.record.id) })
      else
        setCurrent({
          kind: current.kind,
          record: await api.getAgentInstallation(appId, current.record.id),
        })
      setReason('')
      setConfirmed(false)
      setFrozen(false)
      setError(null)
    } catch (e) {
      setError(e instanceof ApiError ? e : new ApiError({ status: 0, detail: String(e) }))
    } finally {
      lock.current = false
      setBusy(false)
    }
  }
  const submit = async () => {
    if (lock.current || frozen || !reason.trim() || !confirmed) return
    lock.current = true
    setBusy(true)
    setError(null)
    const patch = { config_version: current.record.config_version, reason: reason.trim() }
    try {
      if (current.kind === 'agent')
        await api.updateAgent(appId, current.record.id, { ...patch, status })
      else if (current.kind === 'installation')
        await api.updateAgentInstallation(appId, current.record.id, { ...patch, status })
      else if (current.kind === 'renew')
        await api.renewAutonomousAgent(appId, current.record.id, patch)
      else await api.revokeAgentGrant(appId, current.record.id, patch)
      onDone()
    } catch (e) {
      const failure = e instanceof ApiError ? e : new ApiError({ status: 0, detail: String(e) })
      setError(failure)
      if (failure.isOutcomeUnknown || failure.status === 409) {
        setFrozen(true)
        setReason('')
        setConfirmed(false)
      }
    } finally {
      lock.current = false
      setBusy(false)
    }
  }
  return (
    <Dialog open title={title} onClose={onClose} dismissible={!busy && !frozen}>
      <p className="field-hint">
        {current.kind === 'renew'
          ? '旧自主运行授权会被撤销，并建立一份有新到期时间的授权。'
          : current.kind === 'grant'
            ? '撤销后不能恢复这份授权；用户委托需要用户重新确认。'
            : '停用后停止签发和刷新令牌，在线权限检查拒绝旧令牌。重新启用仍需有效授权。'}
      </p>
      <dl className="detail-grid">
        <div>
          <dt>记录</dt>
          <dd className="mono">{current.record.id}</dd>
        </div>
        <div>
          <dt>当前状态</dt>
          <dd>{current.record.status}</dd>
        </div>
        <div>
          <dt>配置版本</dt>
          <dd>{current.record.config_version}</dd>
        </div>
      </dl>
      {error ? <ErrorNotice error={error} /> : null}
      {frozen ? (
        <div className="notice notice-warn" role="alert">
          <p>操作已冻结。请重新读取当前记录，核对后明确决定下一步；系统不会自动重试。</p>
          <Button onClick={read} loading={busy}>
            读取当前记录
          </Button>
        </div>
      ) : null}
      <fieldset className="submission-fields" disabled={busy || frozen}>
        <TextAreaField
          label="操作原因"
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          maxLength={500}
          required
        />
        <label className="check-row">
          <input
            type="checkbox"
            checked={confirmed}
            onChange={(e) => setConfirmed(e.target.checked)}
          />
          我已确认本次操作及影响
        </label>
        <div className="dialog-actions">
          <Button
            onClick={submit}
            variant="danger"
            disabled={
              !reason.trim() ||
              !confirmed ||
              (current.kind === 'grant' && current.record.status !== 'active')
            }
            loading={busy}
          >
            {title}
          </Button>
        </div>
      </fieldset>
    </Dialog>
  )
}
