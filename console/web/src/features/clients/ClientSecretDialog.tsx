import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import * as api from '../../api/endpoints'
import { ApiError } from '../../api/client'
import type { Client, ClientSecretRotated } from '../../api/types'
import { useProtection } from '../../app/protectionState'
import { Button } from '../../shared/components/Button'
import { Dialog } from '../../shared/components/Dialog'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { OutcomeUnknownNotice } from '../../shared/components/OutcomeUnknownNotice'
import { Loading } from '../../shared/components/States'
import { TextAreaField, TextField } from '../../shared/components/Field'
import { formatUnixSeconds } from '../../shared/utils/format'

import { previousSecretLive } from './formModel'

const MAX_GRACE_SECONDS = 86400

/**
 * 机密客户端密钥轮换：
 * - 打开即 GET 读回权威客户端，提交捕获最新 config_version（乐观锁）；
 * - grace_seconds=0 表示旧密钥立即失效（立即切换凭证），>0 旧密钥宽限期内继续可用；
 * - 旧密钥宽限期内服务端拒绝新的轮换：表单禁用并引导先撤销或等待过期；
 * - 无幂等回放，绝不自动重试；409 / 结果未知冻结，权威读回后人工决定；
 *   结果未知且读回发现版本上升时，明确提示旧请求可能已生效、新密钥无法找回。
 */
export function ClientSecretDialog({
  appId,
  client,
  onRotated,
  onChanged,
  onClose,
}: {
  appId: string
  /** 列表快照；打开后立即权威读回，提交以读回的版本为准 */
  client: Client
  /** 轮换成功：一次性密钥交给页面级 SecretPanel 展示 */
  onRotated: (rotated: ClientSecretRotated) => void
  /** 客户端数据已变化，通知列表刷新 */
  onChanged: () => void
  onClose: () => void
}) {
  const clientId = client.client_id
  const [current, setCurrent] = useState(client)
  const [grace, setGrace] = useState('3600')
  const [reason, setReason] = useState('')
  const [fieldErrors, setFieldErrors] = useState<{ grace?: string; reason?: string }>({})
  const [formError, setFormError] = useState<ApiError | null>(null)
  const [stale, setStale] = useState(false)
  const [unknown, setUnknown] = useState<ApiError | null>(null)
  /** 结果未知读回后发现版本上升：先前轮换可能已生效，新密钥无法找回 */
  const [versionDrifted, setVersionDrifted] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [reconciling, setReconciling] = useState(true)
  const [authoritative, setAuthoritative] = useState(false)
  const submission = useRef(false)
  /** 结果未知的提交所携带的 config_version；读回比对用 */
  const submittedVersion = useRef<number | null>(null)
  // 组件存活标记：卸载后忽略迟到响应（StrictMode 重挂载保留 ref，同一实例不受影响）
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])

  const frozen = stale || unknown !== null
  const busy = submitting || reconciling
  const releaseProtection = useProtection(busy || frozen)

  /** 权威读回：刷新本地快照；结果未知后读回时比对版本判断是否可能已生效 */
  const reloadCurrent = useCallback(
    async (compareDrift: boolean) => {
      if (submission.current) return
      submission.current = true
      setReconciling(true)
      setFormError(null)
      try {
        const fresh = await api.getClient(appId, clientId)
        if (!alive.current) return
        if (
          fresh.app_id !== appId ||
          fresh.client_id !== clientId ||
          !Number.isSafeInteger(fresh.config_version) ||
          fresh.config_version < 1
        ) {
          throw new ApiError({
            status: 502,
            code: 'invalid_response',
            detail: '客户端读取响应不完整，请重新读取。',
          })
        }
        setAuthoritative(true)
        setCurrent(fresh)
        if (
          compareDrift &&
          submittedVersion.current !== null &&
          fresh.config_version !== submittedVersion.current
        ) {
          setVersionDrifted(true)
        }
        submittedVersion.current = null
        setUnknown(null)
        setStale(false)
        setReason('')
        setFieldErrors({})
      } catch (err) {
        if (!alive.current) return
        setFormError(
          err instanceof ApiError ? err : new ApiError({ status: 0, detail: String(err) }),
        )
      } finally {
        submission.current = false
        if (alive.current) setReconciling(false)
      }
    },
    [appId, clientId],
  )

  // 打开即权威读回：列表快照可能滞后，轮换必须以最新 config_version 提交。
  // 初始 reconciling=true 已表达加载中；读回在微任务中启动，避免在 effect 同步阶段写状态。
  useEffect(() => {
    void Promise.resolve().then(() => reloadCurrent(false))
  }, [reloadCurrent])

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (submission.current || frozen || busy || !authoritative) return
    const graceSeconds = Number(grace)
    const errors: { grace?: string; reason?: string } = {}
    if (
      grace.trim() === '' ||
      !Number.isInteger(graceSeconds) ||
      graceSeconds < 0 ||
      graceSeconds > MAX_GRACE_SECONDS
    ) {
      errors.grace = `宽限期须为 0–${MAX_GRACE_SECONDS} 的整数秒`
    }
    const trimmed = reason.trim()
    if (!trimmed) errors.reason = '请填写操作原因'
    setFieldErrors(errors)
    if (Object.keys(errors).length > 0) return

    submission.current = true
    setSubmitting(true)
    setFormError(null)
    submittedVersion.current = current.config_version
    try {
      const rotated = await api.rotateClientSecret(appId, clientId, {
        config_version: current.config_version,
        reason: trimmed,
        grace_seconds: graceSeconds,
      })
      releaseProtection()
      onRotated(rotated)
      onChanged()
      onClose()
    } catch (err) {
      const apiErr =
        err instanceof ApiError ? err : new ApiError({ status: 0, detail: String(err) })
      if (apiErr.status === 409) {
        setStale(true)
      } else if (apiErr.isOutcomeUnknown) {
        setUnknown(apiErr)
      } else {
        setFormError(apiErr)
      }
    } finally {
      submission.current = false
      setSubmitting(false)
    }
  }

  const prevLive = previousSecretLive(current.previous_secret_expires_at)
  const graceIsZero = grace.trim() !== '' && Number(grace) === 0

  return (
    <Dialog open title="轮换客户端密钥" onClose={onClose} dismissible={!busy && !frozen}>
      {reconciling && !unknown && !stale ? (
        <Loading label="正在读取客户端最新配置…" />
      ) : (
        <form className="form" onSubmit={handleSubmit} noValidate aria-label="轮换客户端密钥">
          <p className="dialog-desc">
            客户端 <strong>{current.name}</strong>（
            <span className="mono">{current.client_id}</span>
            ）。新密钥仅在本次响应中返回一次，之后任何接口都无法再读取。
          </p>
          <dl className="detail-grid">
            <div className="detail-item">
              <dt>配置版本</dt>
              <dd>{current.config_version}</dd>
            </div>
            <div className="detail-item">
              <dt>旧密钥宽限期</dt>
              <dd>
                {current.previous_secret_expires_at > 0
                  ? `至 ${formatUnixSeconds(current.previous_secret_expires_at)}${prevLive ? '' : '（已过期）'}`
                  : '无'}
              </dd>
            </div>
          </dl>

          {prevLive ? (
            <div className="notice notice-warn" role="alert">
              <div className="notice-body">
                <p className="notice-title">旧密钥仍在宽限期内，暂时无法轮换</p>
                <p className="notice-detail">
                  服务端在旧密钥宽限期（至 {formatUnixSeconds(current.previous_secret_expires_at)}
                  ）内拒绝新的轮换，以避免意外叠加。请先撤销旧密钥，或等待其过期后再轮换。
                </p>
              </div>
            </div>
          ) : null}

          {versionDrifted ? (
            <div className="notice notice-warn" role="alert">
              <div className="notice-body">
                <p className="notice-title">检测到配置版本已变化</p>
                <p className="notice-detail">
                  先前的轮换请求可能已在服务端生效；那次返回的新密钥无法找回。如密钥已丢失，请基于当前配置版本显式发起一次新的轮换。
                </p>
              </div>
            </div>
          ) : null}

          {unknown ? (
            <OutcomeUnknownNotice
              error={unknown}
              detail="轮换可能已在服务端生效，而已返回的新密钥无法再次读取。请点击「重新加载最新状态」读回客户端：若配置版本已上升，应按“轮换已生效、密钥已丢失”处理；核对前表单保持冻结，绝不自动重试。"
              action={
                <Button
                  type="button"
                  size="sm"
                  onClick={() => void reloadCurrent(true)}
                  loading={reconciling}
                >
                  重新加载最新状态
                </Button>
              }
            />
          ) : null}
          {stale ? (
            <div className="notice notice-warn" role="alert">
              <div className="notice-body">
                <p className="notice-title">配置版本已过期（409 冲突）</p>
                <p className="notice-detail">
                  该客户端刚被其他操作修改。请点击「重新加载最新状态」读回权威配置，确认后再决定是否重新提交；系统不会自动重试。
                </p>
              </div>
              <Button
                type="button"
                size="sm"
                onClick={() => void reloadCurrent(false)}
                loading={reconciling}
              >
                重新加载最新状态
              </Button>
            </div>
          ) : null}
          {formError ? (
            <ErrorNotice
              error={formError}
              onRetry={!authoritative ? () => void reloadCurrent(false) : undefined}
              retryLabel="重新读取客户端"
            />
          ) : null}

          <fieldset
            className="submission-fields"
            disabled={busy || frozen || prevLive || !authoritative}
          >
            <TextField
              label="旧密钥宽限期（秒）"
              type="number"
              min={0}
              max={MAX_GRACE_SECONDS}
              step={1}
              value={grace}
              onChange={(e) => {
                setGrace(e.target.value)
                if (fieldErrors.grace) setFieldErrors((prev) => ({ ...prev, grace: undefined }))
              }}
              error={fieldErrors.grace}
              hint={`0–${MAX_GRACE_SECONDS} 秒。0 表示旧密钥立即失效（立即切换凭证）；大于 0 则旧密钥在宽限期内继续可用。`}
              required
            />
            {graceIsZero && !fieldErrors.grace ? (
              <div className="notice notice-warn" role="alert">
                <div className="notice-body">
                  <p className="notice-detail">
                    宽限期为
                    0：旧密钥将立即失效，仍在使用旧密钥的线上集成会立刻中断，请确认依赖方已准备好切换。
                  </p>
                </div>
              </div>
            ) : null}
            <TextAreaField
              label="操作原因"
              value={reason}
              onChange={(e) => {
                setReason(e.target.value)
                if (fieldErrors.reason) setFieldErrors((prev) => ({ ...prev, reason: undefined }))
              }}
              error={fieldErrors.reason}
              hint="将随请求一并记录到审计日志"
              rows={3}
              required
            />
          </fieldset>
          <div className="dialog-actions">
            <Button type="button" onClick={onClose} disabled={busy || frozen}>
              取消
            </Button>
            <Button
              type="submit"
              variant="primary"
              loading={submitting}
              disabled={busy || frozen || prevLive || !authoritative}
            >
              确认轮换
            </Button>
          </div>
        </form>
      )}
    </Dialog>
  )
}
