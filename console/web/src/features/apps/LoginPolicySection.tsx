import { useRef, useState, type FormEvent } from 'react'
import * as api from '../../api/endpoints'
import { ApiError } from '../../api/client'
import type { AuthenticationConfig, AuthenticationPolicy, Connection } from '../../api/types'
import { useProtection } from '../../app/protectionState'
import { useAsync } from '../../shared/hooks/useAsync'
import { Button } from '../../shared/components/Button'
import { TextAreaField } from '../../shared/components/Field'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { OutcomeUnknownNotice } from '../../shared/components/OutcomeUnknownNotice'
import { Loading } from '../../shared/components/States'

/**
 * 登录策略七个开关的展示元信息（契约 AuthenticationPolicy，PATCH 须完整提交）。
 * 语义边界：
 * - registration_enabled 关闭 = 拒绝一切新用户注册；
 * - external_registration_enabled 在其之上额外约束第三方身份源首次登录是否自动创建用户；
 * - OTP 开关是“OTP 登录”开关，不代表投递通道已配置，也不代表邮箱/手机号已验证；
 * - passkey_enabled 仅对已在验证地址后注册 Passkey 的用户可用，不替代应用可要求的 TOTP。
 */
const POLICY_FIELDS: ReadonlyArray<{
  key: keyof AuthenticationPolicy
  label: string
  hint: string
}> = [
  {
    key: 'registration_enabled',
    label: '允许新用户注册',
    hint: '关闭后拒绝一切新用户注册，包括第三方首次登录的自动创建。',
  },
  {
    key: 'password_login_enabled',
    label: '密码登录',
    hint: '系统管理客户端的密码 + MFA 登录不受此策略影响，始终可用。',
  },
  {
    key: 'email_otp_enabled',
    label: '邮箱验证码登录',
    hint: '需部署邮件投递通道；不代表用户邮箱已验证。',
  },
  {
    key: 'phone_otp_enabled',
    label: '短信验证码登录',
    hint: '需部署短信投递通道；不代表用户手机号已验证。',
  },
  {
    key: 'federation_enabled',
    label: '第三方登录',
    hint: '通过「连接」中已启用的身份源（微信、支付宝等）登录。',
  },
  {
    key: 'passkey_enabled',
    label: 'Passkey 登录',
    hint: '仅已注册 Passkey 的用户可用；注册入口在验证地址后的设置流程与账号安全页。不替代应用可要求的 TOTP 二次验证。',
  },
  {
    key: 'external_registration_enabled',
    label: '第三方首次登录创建用户',
    hint: '需先允许新用户注册；关闭后仅已绑定该连接的用户可登录，同邮箱的已有账号不会自动关联。',
  },
]

/** 登录方式五项（可独立开关）；注册两项单独分组，与登录方式、部署就绪状态分开陈述 */
const METHOD_KEYS: ReadonlyArray<keyof AuthenticationPolicy> = [
  'password_login_enabled',
  'email_otp_enabled',
  'phone_otp_enabled',
  'federation_enabled',
  'passkey_enabled',
]
const REGISTRATION_KEYS: ReadonlyArray<keyof AuthenticationPolicy> = [
  'registration_enabled',
  'external_registration_enabled',
]
const FIELD_BY_KEY = new Map(POLICY_FIELDS.map((field) => [field.key, field]))

function policyLabel(value: boolean): string {
  return value ? '已启用' : '未启用'
}

/** 部署就绪状态（只读）：就绪仅表示部署配置存在，不代表供应商通道已验通 */
interface Readiness {
  password_login: boolean
  email_otp_configured: boolean
  phone_otp_configured: boolean
  passkey_available: boolean
}

function readinessOf(config: AuthenticationConfig): Readiness {
  return {
    password_login: config.password_login,
    email_otp_configured: config.email_otp_configured,
    phone_otp_configured: config.phone_otp_configured,
    passkey_available: config.passkey_available,
  }
}

interface MethodChip {
  key: string
  label: string
  tone: 'accent' | 'warn'
}

/**
 * 方式摘要徽章：陈述「已配置的策略」，并就部署缺失如实降级（通道未配置/部署未启用）；
 * 不声称供应商通道已验通。第三方登录附带按 App 作用域真实读回的已启用连接数；
 * activeConnections 为 null 表示读取未知（加载中/失败/响应形状不符），不编造计数。
 */
function methodChips(
  policy: AuthenticationPolicy,
  readiness: Readiness,
  activeConnections: number | null,
): MethodChip[] {
  const chips: MethodChip[] = []
  if (policy.password_login_enabled)
    chips.push(
      readiness.password_login
        ? { key: 'password', label: '密码登录', tone: 'accent' }
        : { key: 'password', label: '密码登录 · 部署未启用', tone: 'warn' },
    )
  if (policy.email_otp_enabled)
    chips.push(
      readiness.email_otp_configured
        ? { key: 'email-otp', label: '邮箱验证码', tone: 'accent' }
        : { key: 'email-otp', label: '邮箱验证码 · 通道未配置', tone: 'warn' },
    )
  if (policy.phone_otp_enabled)
    chips.push(
      readiness.phone_otp_configured
        ? { key: 'phone-otp', label: '短信验证码', tone: 'accent' }
        : { key: 'phone-otp', label: '短信验证码 · 通道未配置', tone: 'warn' },
    )
  if (policy.federation_enabled) {
    chips.push({
      key: 'federation',
      label:
        activeConnections === null
          ? '第三方登录'
          : activeConnections > 0
            ? `第三方登录 · ${activeConnections} 个已启用连接`
            : '第三方登录 · 无已启用连接',
      tone: activeConnections === 0 ? 'warn' : 'accent',
    })
  }
  // Passkey 无部署就绪标志：开启即提供入口，但仅已注册用户可用，摘要不外推可用性
  if (policy.passkey_enabled)
    chips.push({
      key: 'passkey',
      label: readiness.passkey_available ? 'Passkey' : 'Passkey · 域名未就绪',
      tone: readiness.passkey_available ? 'accent' : 'warn',
    })
  return chips
}

function MethodSummaryRow({
  label,
  policy,
  readiness,
  activeConnections,
}: {
  label: string
  policy: AuthenticationPolicy
  readiness: Readiness
  activeConnections: number | null
}) {
  const chips = methodChips(policy, readiness, activeConnections)
  return (
    <div className="method-summary-row">
      <span className="method-summary-label">{label}</span>
      <span className="method-chips">
        {chips.length === 0 ? (
          <span className="badge badge-muted">无可用登录方式</span>
        ) : (
          chips.map((chip) => (
            <span className={`badge badge-${chip.tone}`} key={chip.key}>
              {chip.label}
            </span>
          ))
        )}
      </span>
    </div>
  )
}

/** 部署就绪状态明细（只读，与策略相互独立） */
function ReadinessItems({ config }: { config: AuthenticationConfig }) {
  return (
    <>
      <div className="detail-item">
        <dt>Passkey（部署）</dt>
        <dd>
          {config.passkey_available
            ? '域名已配置，仍需浏览器与设备支持'
            : '域名不可用，需 HTTPS 域名或 localhost'}
        </dd>
      </div>
      <div className="detail-item">
        <dt>密码登录（部署）</dt>
        <dd>{policyLabel(config.password_login)}</dd>
      </div>
      <div className="detail-item">
        <dt>邮件投递通道（部署）</dt>
        <dd>{config.email_otp_configured ? '已配置' : '未配置'}</dd>
      </div>
      <div className="detail-item">
        <dt>短信投递通道（部署）</dt>
        <dd>{config.phone_otp_configured ? '已配置' : '未配置'}</dd>
      </div>
    </>
  )
}

/**
 * 连接列表读取结果：仅采纳最近一次成功读取的计数；
 * 加载中/刷新中/失败（含 useAsync 保留的旧快照）一律按未知处理，绝不用过期数据推断。
 */
function useActiveConnectionCount(appId: string) {
  const connections = useAsync(`policy-connections:${appId}`, () =>
    api.listConnections(appId, { limit: 100 }),
  )
  const fresh = !connections.loading && !connections.refreshing && connections.error === null
  const items: Connection[] | null =
    fresh && Array.isArray(connections.data?.items) ? connections.data.items : null
  const activeCount = items === null ? null : items.filter((c) => c.status === 'active').length
  return {
    activeCount,
    error: connections.error,
    reload: connections.reload,
  }
}

/**
 * 登录策略（authentication-config policy）：
 * - 写权限仅平台 owner/admin 或 App owner（canWrite），其余角色只读；
 * - 提交完整七个开关 + config_version 乐观锁 + reason（入审计）；
 * - 409 / 结果未知冻结表单，必须权威读回后重新编辑；不自动重试；
 * - 策略由服务端强制执行，界面不提供绕过。
 */
export function LoginPolicySection({ appId, canWrite }: { appId: string; canWrite: boolean }) {
  const config = useAsync(`authentication-config:${appId}`, () =>
    api.getAuthenticationConfig(appId),
  )
  const {
    activeCount,
    error: connectionsError,
    reload: reloadConnections,
  } = useActiveConnectionCount(appId)

  return (
    <section className="panel panel-narrow section-gap" aria-label="登录策略">
      <h3 className="panel-title">登录策略</h3>
      {config.loading ? (
        <Loading label="正在加载登录策略…" />
      ) : config.error || !config.data ? (
        <ErrorNotice error={config.error} onRetry={config.reload} />
      ) : canWrite ? (
        <PolicyForm
          appId={appId}
          initial={config.data}
          activeCount={activeCount}
          connectionsError={connectionsError}
          onRetryConnections={reloadConnections}
        />
      ) : (
        <PolicyReadOnly config={config.data} activeCount={activeCount} />
      )}
    </section>
  )
}

function PolicyReadOnly({
  config,
  activeCount,
}: {
  config: AuthenticationConfig
  activeCount: number | null
}) {
  const readiness = readinessOf(config)
  return (
    <>
      <div className="method-summary">
        <MethodSummaryRow
          label="当前配置"
          policy={config.policy}
          readiness={readiness}
          activeConnections={activeCount}
        />
      </div>
      <dl className="detail-grid section-gap">
        {POLICY_FIELDS.map((field) => (
          <div className="detail-item" key={field.key}>
            <dt>{field.label}</dt>
            <dd>{policyLabel(config.policy[field.key])}</dd>
          </div>
        ))}
      </dl>
      <details className="settings-details">
        <summary>部署就绪状态（只读）</summary>
        <dl className="detail-grid">
          <ReadinessItems config={config} />
        </dl>
        <p className="field-hint">
          就绪仅表示部署配置存在（如短信所需的 AUTHOVA_SMS_URL/TOKEN
          已设置），不代表供应商通道已验通。
        </p>
      </details>
      <p className="field-hint section-gap">
        策略仅约束本应用的终端用户登录，由服务端强制执行；部署就绪状态与策略相互独立。当前账号对登录策略为只读，如需变更请联系拥有者。
      </p>
    </>
  )
}

function PolicyForm({
  appId,
  initial,
  activeCount,
  connectionsError,
  onRetryConnections,
}: {
  appId: string
  initial: AuthenticationConfig
  activeCount: number | null
  connectionsError: ApiError | null
  onRetryConnections: () => void
}) {
  // current：本地权威快照（最近一次 GET/PATCH 成功响应）；409/未知结果以读回为准 rebase
  const [current, setCurrent] = useState(initial)
  const [policy, setPolicy] = useState<AuthenticationPolicy>({ ...initial.policy })
  const [reason, setReason] = useState('')
  const [fieldError, setFieldError] = useState<string | null>(null)
  const [formError, setFormError] = useState<ApiError | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [stale, setStale] = useState(false)
  const [unknown, setUnknown] = useState<ApiError | null>(null)
  const [reloading, setReloading] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const submission = useRef(false)

  const changed = POLICY_FIELDS.some((field) => policy[field.key] !== current.policy[field.key])
  const frozen = stale || unknown !== null
  useProtection(submitting || reloading || frozen)

  const readiness = readinessOf(current)

  // 草稿态推导（仅陈述，不拦截）。「潜在可用」尊重部署标志与未知读取：
  // 部署未启用的密码登录、通道未配置的 OTP 不算可用；连接计数未知时不据此判不可用。
  const allMethodsOff =
    !policy.password_login_enabled &&
    !policy.email_otp_enabled &&
    !policy.phone_otp_enabled &&
    !policy.federation_enabled &&
    !policy.passkey_enabled
  const currentAllOff =
    !current.policy.password_login_enabled &&
    !current.policy.email_otp_enabled &&
    !current.policy.phone_otp_enabled &&
    !current.policy.federation_enabled &&
    !current.policy.passkey_enabled
  const unusableReasons: string[] = []
  if (policy.passkey_enabled && !current.passkey_available)
    unusableReasons.push('Passkey（域名未就绪）')
  if (policy.password_login_enabled && !current.password_login)
    unusableReasons.push('密码登录（部署未启用）')
  if (policy.email_otp_enabled && !current.email_otp_configured)
    unusableReasons.push('邮箱验证码（邮件通道未配置）')
  if (policy.phone_otp_enabled && !current.phone_otp_configured)
    unusableReasons.push('短信验证码（短信通道未配置）')
  if (policy.federation_enabled && activeCount === 0)
    unusableReasons.push('第三方登录（无已启用连接）')
  const hasUsableMethod =
    (policy.passkey_enabled && current.passkey_available) ||
    (policy.password_login_enabled && current.password_login) ||
    (policy.email_otp_enabled && current.email_otp_configured) ||
    (policy.phone_otp_enabled && current.phone_otp_configured) ||
    (policy.federation_enabled && (activeCount === null || activeCount > 0))
  const noUsableMethod = !allMethodsOff && !hasUsableMethod

  /** 「仅第三方登录」本地预设：只改草稿（零网络请求），注册两项保持不变，原因仍必填 */
  const applyThirdPartyOnlyPreset = () => {
    setPolicy((prev) => ({
      ...prev,
      passkey_enabled: false,
      password_login_enabled: false,
      email_otp_enabled: false,
      phone_otp_enabled: false,
      federation_enabled: true,
    }))
  }

  const reloadCurrent = async () => {
    if (submission.current) return
    submission.current = true
    setReloading(true)
    setFormError(null)
    try {
      const fresh = await api.getAuthenticationConfig(appId)
      setCurrent(fresh)
      setPolicy({ ...fresh.policy })
      setReason('')
      setFieldError(null)
      setStale(false)
      setUnknown(null)
    } catch (err) {
      setFormError(err instanceof ApiError ? err : new ApiError({ status: 0, detail: String(err) }))
    } finally {
      submission.current = false
      setReloading(false)
    }
  }

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (submission.current || frozen || !changed || submitting || reloading) return
    const trimmed = reason.trim()
    if (!trimmed) {
      setFieldError('请填写操作原因')
      return
    }
    setFieldError(null)
    setFormError(null)
    setNotice(null)
    // 捕获提交时的版本与完整策略：服务端以 config_version 做乐观锁
    const body = { policy: { ...policy }, config_version: current.config_version, reason: trimmed }
    submission.current = true
    setSubmitting(true)
    try {
      const updated = await api.updateAuthenticationPolicy(appId, body)
      setCurrent(updated)
      setPolicy({ ...updated.policy })
      setReason('')
      setNotice(`登录策略已更新（配置版本 ${updated.config_version}）。`)
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

  const reloadAction = (
    <Button
      type="button"
      size="sm"
      onClick={reloadCurrent}
      loading={reloading}
      disabled={submitting}
    >
      重新加载最新策略
    </Button>
  )

  const renderToggle = (key: keyof AuthenticationPolicy) => {
    const field = FIELD_BY_KEY.get(key)!
    const otpChannelMissing =
      (field.key === 'email_otp_enabled' && !current.email_otp_configured) ||
      (field.key === 'phone_otp_enabled' && !current.phone_otp_configured) ||
      (field.key === 'passkey_enabled' && !current.passkey_available)
    const labelId = `policy-${field.key}-label`
    const hintId = `policy-${field.key}-hint`
    return (
      <div className="setting-row" key={field.key}>
        <div className="setting-text">
          <span className="setting-label" id={labelId}>
            {field.label}
            {otpChannelMissing && policy[field.key] ? (
              <span className="badge badge-warn">
                {field.key === 'passkey_enabled' ? '域名未就绪' : '通道未配置'}
              </span>
            ) : null}
          </span>
          <span className="setting-hint" id={hintId}>
            {field.hint}
          </span>
          {field.key === 'federation_enabled' ? (
            <span className="setting-hint">
              {policy.federation_enabled && activeCount === 0
                ? '当前没有已启用的连接，登录页不会展示第三方入口。'
                : null}
              <a href={`#/apps/${appId}/connections`}>管理连接</a>
            </span>
          ) : null}
        </div>
        <input
          type="checkbox"
          className="switch"
          aria-labelledby={labelId}
          aria-describedby={hintId}
          checked={policy[field.key]}
          onChange={(e) => setPolicy({ ...policy, [field.key]: e.target.checked })}
        />
      </div>
    )
  }

  return (
    <>
      {notice ? (
        <div className="notice notice-ok" role="status">
          <div className="notice-body">
            <p className="notice-title">{notice}</p>
          </div>
        </div>
      ) : null}
      {unknown ? (
        <OutcomeUnknownNotice
          error={unknown}
          detail="该策略更新可能已在服务端生效。请点击「重新加载最新策略」读回权威状态后再重新编辑；核对前表单保持冻结，避免覆盖并发修改。"
          action={reloadAction}
        />
      ) : null}
      {stale ? (
        <div className="notice notice-warn" role="alert">
          <div className="notice-body">
            <p className="notice-title">配置版本已过期（409 冲突）</p>
            <p className="notice-detail">
              登录策略刚被其他操作修改。请点击「重新加载最新策略」获取最新版本，确认当前内容后再重新提交；系统不会自动重试。
            </p>
          </div>
          {reloadAction}
        </div>
      ) : null}
      {formError ? <ErrorNotice error={formError} /> : null}

      <div className="method-summary">
        <MethodSummaryRow
          label="当前配置"
          policy={current.policy}
          readiness={readiness}
          activeConnections={activeCount}
        />
        {changed ? (
          <MethodSummaryRow
            label="未保存草稿"
            policy={policy}
            readiness={readiness}
            activeConnections={activeCount}
          />
        ) : null}
      </div>

      {allMethodsOff ? (
        <div className="notice notice-warn authconfig-warning" role="alert">
          <div className="notice-body">
            <p className="notice-title">
              {currentAllOff ? '当前配置已关闭全部登录方式' : '草稿将关闭全部登录方式'}
            </p>
            <p className="notice-detail">
              终端用户无法通过任何方式登录（维护窗口期可这样配置）。系统管理客户端的密码 + MFA
              登录不受此策略影响，始终可用。
            </p>
          </div>
        </div>
      ) : noUsableMethod ? (
        <div className="notice notice-warn authconfig-warning" role="alert">
          <div className="notice-body">
            <p className="notice-title">
              {changed ? '保存后没有可用的登录方式' : '当前配置没有可用的登录方式'}
            </p>
            <p className="notice-detail">
              已开启的方式当前不可用：{unusableReasons.join('；')}
              。请开启可用方式或补齐对应配置；实际可用性以服务端为准。
            </p>
          </div>
        </div>
      ) : null}
      {policy.federation_enabled && connectionsError ? (
        <p className="field-hint">
          连接列表暂时无法读取，第三方登录的实际可用性以「连接」页与服务端为准。
          <button type="button" className="btn btn-ghost btn-sm" onClick={onRetryConnections}>
            重试读取连接
          </button>
        </p>
      ) : null}

      <form className="form section-gap" onSubmit={handleSubmit} noValidate aria-label="登录策略">
        <fieldset className="submission-fields" disabled={submitting || frozen || reloading}>
          <div className="settings-group">
            <div className="settings-group-head">
              <p className="settings-group-title">登录方式</p>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={applyThirdPartyOnlyPreset}
                title="只修改本页草稿：关闭密码与验证码登录、开启第三方登录；注册开关保持不变，保存仍需填写原因"
              >
                仅第三方登录
              </Button>
            </div>
            <div className="setting-rows">{METHOD_KEYS.map(renderToggle)}</div>
          </div>
          <div className="settings-group">
            <div className="settings-group-head">
              <p className="settings-group-title">新用户注册</p>
            </div>
            <div className="setting-rows">{REGISTRATION_KEYS.map(renderToggle)}</div>
          </div>
          <TextAreaField
            label="操作原因"
            value={reason}
            onChange={(e) => {
              setReason(e.target.value)
              if (fieldError) setFieldError(null)
            }}
            error={fieldError ?? undefined}
            hint="将随请求一并记录到审计日志"
            rows={2}
            required
          />
          {!changed ? (
            <p className="field-hint">修改任一开关后才能保存；保存将完整提交全部七项。</p>
          ) : null}
          <div className="form-footer">
            {frozen ? null : reloadAction}
            <Button
              type="submit"
              variant="primary"
              loading={submitting}
              disabled={frozen || !changed}
            >
              保存登录策略
            </Button>
          </div>
        </fieldset>
      </form>

      <details className="settings-details">
        <summary>部署就绪状态（只读）</summary>
        <dl className="detail-grid">
          <ReadinessItems config={current} />
        </dl>
        <p className="field-hint">
          就绪仅表示部署配置存在（如短信所需的 AUTHOVA_SMS_URL/TOKEN
          已设置），不代表供应商通道已验通；与上方登录策略相互独立。策略仅约束本应用终端用户，由服务端强制执行，界面不提供绕过。
        </p>
      </details>
    </>
  )
}
