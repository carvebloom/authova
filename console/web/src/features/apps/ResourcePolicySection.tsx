import { useRef, useState, type FormEvent } from 'react'
import * as api from '../../api/endpoints'
import { ApiError } from '../../api/client'
import type { ResourceConfig, ResourceLimits } from '../../api/types'
import { useProtection } from '../../app/protectionState'
import { useAsync } from '../../shared/hooks/useAsync'
import { Button } from '../../shared/components/Button'
import { TextAreaField, TextField } from '../../shared/components/Field'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { OutcomeUnknownNotice } from '../../shared/components/OutcomeUnknownNotice'
import { Loading } from '../../shared/components/States'

const FIELDS: { key: keyof ResourceLimits; label: string; max: number; hint: string }[] = [
  {
    key: 'http_concurrency',
    label: 'HTTP 并发请求',
    max: 100000,
    hint: '每个 Core 实例同时处理的该 App 请求数。',
  },
  {
    key: 'kdf_concurrency',
    label: '密码计算并发',
    max: 512,
    hint: '每个 Core 实例的密码哈希与校验并发数，受全局容量约束。',
  },
  {
    key: 'db_concurrency',
    label: '数据库事务并发',
    max: 4096,
    hint: '每个 Core 实例的该 App 数据库事务并发数。',
  },
  {
    key: 'requests_per_second',
    label: '每秒请求上限',
    max: 1000000,
    hint: '由共享限流器统计；生产多副本应使用 Valkey。',
  },
  {
    key: 'email_challenges_per_hour',
    label: '每小时邮箱验证码',
    max: 10000000,
    hint: '限制该 App 发起的邮箱验证码请求。',
  },
  {
    key: 'sms_challenges_per_hour',
    label: '每小时短信验证码',
    max: 10000000,
    hint: '限制该 App 发起的短信验证码请求。',
  },
]

export function ResourcePolicySection({ appId, canWrite }: { appId: string; canWrite: boolean }) {
  const config = useAsync(`resources:${appId}`, () => api.getResourcePolicy(appId))
  return (
    <section className="panel panel-narrow section-gap" aria-label="资源隔离">
      <h3 className="panel-title">资源隔离</h3>
      <p className="field-hint">
        为每个 App 设置独立的资源预算，减少高流量应用对其他应用的影响。并发预算按 Core
        实例生效，管理操作保留独立容量。
      </p>
      {config.loading ? (
        <Loading label="正在读取资源预算…" />
      ) : config.error || !config.data ? (
        <ErrorNotice error={config.error} onRetry={config.reload} />
      ) : (
        <ResourceForm key={appId} appId={appId} initial={config.data} canWrite={canWrite} />
      )}
    </section>
  )
}

function ResourceForm({
  appId,
  initial,
  canWrite,
}: {
  appId: string
  initial: ResourceConfig
  canWrite: boolean
}) {
  const [current, setCurrent] = useState(initial)
  const [draft, setDraft] = useState(
    () =>
      Object.fromEntries(FIELDS.map((f) => [f.key, String(initial.limits[f.key])])) as Record<
        keyof ResourceLimits,
        string
      >,
  )
  const [reason, setReason] = useState('')
  const [error, setError] = useState<ApiError | null>(null)
  const [validation, setValidation] = useState('')
  const [notice, setNotice] = useState('')
  const [stale, setStale] = useState(false)
  const [unknown, setUnknown] = useState<ApiError | null>(null)
  const [busy, setBusy] = useState(false)
  const guard = useRef(false)
  const frozen = stale || unknown !== null
  const changed = FIELDS.some((f) => draft[f.key] !== String(current.limits[f.key]))
  useProtection(busy || frozen)
  const rebase = (fresh: ResourceConfig) => {
    setCurrent(fresh)
    setDraft(
      Object.fromEntries(FIELDS.map((f) => [f.key, String(fresh.limits[f.key])])) as Record<
        keyof ResourceLimits,
        string
      >,
    )
    setReason('')
    setStale(false)
    setUnknown(null)
    setValidation('')
  }
  const reload = async () => {
    if (guard.current) return
    guard.current = true
    setBusy(true)
    setError(null)
    try {
      rebase(await api.getResourcePolicy(appId))
    } catch (e) {
      setError(e instanceof ApiError ? e : new ApiError({ status: 0, detail: String(e) }))
    } finally {
      guard.current = false
      setBusy(false)
    }
  }
  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (guard.current || frozen || !canWrite || !changed) return
    for (const f of FIELDS) {
      const value = Number(draft[f.key])
      if (
        !/^\d+$/.test(draft[f.key]) ||
        !Number.isSafeInteger(value) ||
        value < 1 ||
        value > f.max
      ) {
        setValidation(`${f.label}须为 1 至 ${f.max.toLocaleString()} 的整数`)
        return
      }
    }
    if (!reason.trim()) {
      setValidation('请填写操作原因')
      return
    }
    const limits = Object.fromEntries(
      FIELDS.map((f) => [f.key, Number(draft[f.key])]),
    ) as ResourceLimits
    guard.current = true
    setBusy(true)
    setError(null)
    setNotice('')
    setValidation('')
    try {
      const fresh = await api.updateResourcePolicy(appId, {
        limits,
        config_version: current.config_version,
        reason: reason.trim(),
      })
      rebase(fresh)
      setNotice('资源预算已更新。其他实例将在缓存到期后读取新配置（缓存最长 5 秒）。')
    } catch (e) {
      const failure = e instanceof ApiError ? e : new ApiError({ status: 0, detail: String(e) })
      if (failure.status === 409) setStale(true)
      else if (failure.isOutcomeUnknown) setUnknown(failure)
      else setError(failure)
    } finally {
      guard.current = false
      setBusy(false)
    }
  }
  const reloadAction = (
    <Button type="button" size="sm" onClick={reload} disabled={busy}>
      重新读取资源预算
    </Button>
  )
  return (
    <>
      <dl className="detail-grid section-gap">
        {FIELDS.map((f) => (
          <div className="detail-item" key={f.key}>
            <dt>{f.label}</dt>
            <dd>
              {current.limits[f.key].toLocaleString()}
              {current.effective_limits[f.key] !== current.limits[f.key] ? (
                <span className="field-hint">
                  {' '}
                  · 本实例生效 {current.effective_limits[f.key].toLocaleString()}
                </span>
              ) : null}
            </dd>
          </div>
        ))}
      </dl>
      <p className="field-hint">
        限额超出时返回 429，共享服务容量不足时返回
        503。这些预算提供逻辑隔离；仍需根据机器、数据库与供应商容量规划部署。
      </p>
      {notice ? (
        <p className="notice notice-ok" role="status">
          {notice}
        </p>
      ) : null}
      {error ? <ErrorNotice error={error} /> : null}
      {unknown ? (
        <OutcomeUnknownNotice
          error={unknown}
          detail="更新可能已经生效，请读取当前预算后再编辑，系统不会自动重试。"
          action={reloadAction}
        />
      ) : null}
      {stale ? (
        <div className="notice notice-warn" role="alert">
          <p>配置已被其他操作修改，请重新读取资源预算后再编辑。</p>
          {reloadAction}
        </div>
      ) : null}
      {canWrite ? (
        <details className="settings-details">
          <summary>调整资源预算</summary>
          <form className="form section-gap" aria-label="资源预算" onSubmit={submit} noValidate>
            <fieldset className="submission-fields" disabled={busy || frozen}>
              {FIELDS.map((f) => (
                <TextField
                  key={f.key}
                  label={f.label}
                  type="number"
                  min={1}
                  max={f.max}
                  step={1}
                  value={draft[f.key]}
                  hint={f.hint}
                  required
                  onChange={(e) => setDraft({ ...draft, [f.key]: e.target.value })}
                />
              ))}
              <TextAreaField
                label="资源调整原因"
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                maxLength={1000}
                required
                rows={2}
                hint="记录到审计日志。"
              />
              {validation ? (
                <p role="alert" className="field-error">
                  {validation}
                </p>
              ) : null}
              <div className="form-footer">
                {!frozen ? reloadAction : null}
                <Button
                  type="submit"
                  variant="primary"
                  loading={busy}
                  disabled={frozen || !changed}
                >
                  保存资源预算
                </Button>
              </div>
            </fieldset>
          </form>
        </details>
      ) : (
        <p className="field-hint">当前账号仅可读取资源预算。</p>
      )}
    </>
  )
}
