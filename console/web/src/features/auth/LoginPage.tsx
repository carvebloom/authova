import { useProtection } from '../../app/protectionState'
import { useRef, useState, type FormEvent } from 'react'
import { Navigate, useLocation, useNavigate } from 'react-router-dom'
import * as api from '../../api/endpoints'
import { ApiError } from '../../api/client'
import { useSession } from '../session/useSession'
import { assignLocation } from '../../shared/utils/navigate'
import { Button } from '../../shared/components/Button'
import { PasswordField, TextField } from '../../shared/components/Field'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { BrandMark } from '../../shared/components/Icon'
import { EMAIL_RE, TOTP_RE } from '../../shared/utils/format'

type LoginMode = 'platform' | 'app'

const MODE_DESC: Record<LoginMode, string> = {
  platform: '使用平台运营账号登录，需完成 TOTP 二次验证。',
  app: '输入 App Key 后将跳转至该应用的 Authova 登录页完成身份验证（含二次验证）；控制台不收集应用用户的凭据。',
}

export function LoginPage() {
  const { status, signIn, sessionExpired } = useSession()
  const location = useLocation()
  const stateFrom = (location.state as { from?: unknown } | null)?.from
  const from =
    typeof stateFrom === 'string' && stateFrom.startsWith('/') && stateFrom !== '/login'
      ? stateFrom
      : '/apps'

  const [mode, setMode] = useState<LoginMode>('platform')
  const [error, setError] = useState<ApiError | null>(null)

  if (status === 'ready') {
    return <Navigate to={from} replace />
  }

  const switchMode = (next: LoginMode) => {
    setMode(next)
    setError(null)
  }

  return (
    <div className="login-page">
      <div className="login-panel">
        <div className="brand brand-login">
          <BrandMark size={26} />
          <span className="brand-name">Authova</span>
          <span className="brand-sub">控制台</span>
        </div>

        <div className="login-tabs" role="tablist" aria-label="登录方式">
          <button
            type="button"
            role="tab"
            aria-selected={mode === 'platform'}
            className={`login-tab${mode === 'platform' ? ' login-tab-active' : ''}`}
            onClick={() => switchMode('platform')}
          >
            平台管理
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={mode === 'app'}
            className={`login-tab${mode === 'app' ? ' login-tab-active' : ''}`}
            onClick={() => switchMode('app')}
          >
            应用管理
          </button>
        </div>

        {sessionExpired ? (
          <div className="notice notice-warn" role="status">
            <div className="notice-body">
              <p className="notice-title">会话已过期，请重新登录。</p>
            </div>
          </div>
        ) : null}

        {error ? <ErrorNotice error={error} /> : null}

        <p className="login-desc">{MODE_DESC[mode]}</p>

        {mode === 'platform' ? (
          <PlatformLoginForm from={from} signIn={signIn} setError={setError} />
        ) : (
          <AppLoginForm setError={setError} />
        )}

        <p className="login-footnote">平台运营登录受 TOTP 二次验证保护，操作记入审计日志。</p>
      </div>
    </div>
  )
}

function PlatformLoginForm({
  from,
  signIn,
  setError,
}: {
  from: string
  signIn: (input: { email: string; password: string; totp: string }) => Promise<void>
  setError: (err: ApiError | null) => void
}) {
  const navigate = useNavigate()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [totp, setTotp] = useState('')
  const [fieldErrors, setFieldErrors] = useState<{
    email?: string
    password?: string
    totp?: string
  }>({})
  const [submitting, setSubmitting] = useState(false)
  const submission = useRef(false)
  const releaseProtection = useProtection(submitting)

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (submission.current) return
    const next: typeof fieldErrors = {}
    if (!EMAIL_RE.test(email.trim())) next.email = '请输入有效的邮箱地址'
    if (!password) next.password = '请输入密码'
    if (!TOTP_RE.test(totp.trim())) next.totp = '请输入 6 位动态口令'
    setFieldErrors(next)
    setError(null)
    if (Object.keys(next).length > 0) return
    submission.current = true
    setSubmitting(true)
    try {
      await signIn({ email: email.trim(), password, totp: totp.trim() })
      navigate(from, { replace: true })
    } catch (err) {
      setError(err instanceof ApiError ? err : new ApiError({ status: 0, detail: String(err) }))
    } finally {
      releaseProtection()
      submission.current = false
      setSubmitting(false)
    }
  }

  return (
    <form className="form" onSubmit={handleSubmit} noValidate>
      <fieldset className="submission-fields" disabled={submitting}>
        <TextField
          label="邮箱"
          type="email"
          autoComplete="email"
          autoFocus
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          error={fieldErrors.email}
          required
        />
        <PasswordField
          label="密码"
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          error={fieldErrors.password}
          required
        />
        <TextField
          label="动态口令（TOTP）"
          inputMode="numeric"
          autoComplete="one-time-code"
          placeholder="6 位数字"
          maxLength={6}
          value={totp}
          onChange={(e) => setTotp(e.target.value)}
          error={fieldErrors.totp}
          required
        />
        <Button type="submit" variant="primary" loading={submitting} className="login-submit">
          登录
        </Button>
      </fieldset>
    </form>
  )
}

/** App 管理入口：只收集 App Key，经 BFF 换取重定向后跳转真实 OIDC + PKCE + MFA 流程。 */
function AppLoginForm({ setError }: { setError: (err: ApiError | null) => void }) {
  const [appKey, setAppKey] = useState('')
  const [fieldError, setFieldError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const submission = useRef(false)
  const releaseProtection = useProtection(submitting)

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (submission.current) return
    const trimmed = appKey.trim()
    if (!trimmed) {
      setFieldError('请输入 App Key')
      return
    }
    setFieldError(null)
    setError(null)
    submission.current = true
    setSubmitting(true)
    try {
      const res = await api.appLogin(trimmed)
      if (!res.redirect_to) {
        throw new ApiError({ status: 0, title: '响应异常', detail: '服务端未返回 redirect_to' })
      }
      assignLocation(res.redirect_to)
    } catch (err) {
      setError(err instanceof ApiError ? err : new ApiError({ status: 0, detail: String(err) }))
      releaseProtection()
      submission.current = false
      setSubmitting(false)
    }
  }

  return (
    <form className="form" onSubmit={handleSubmit} noValidate>
      <fieldset className="submission-fields" disabled={submitting}>
        <TextField
          label="App Key"
          autoComplete="off"
          autoFocus
          value={appKey}
          onChange={(e) => {
            setAppKey(e.target.value)
            if (fieldError) setFieldError(null)
          }}
          error={fieldError ?? undefined}
          hint="可在应用概览页查看"
          required
        />
        <Button type="submit" variant="primary" loading={submitting} className="login-submit">
          进入应用管理
        </Button>
      </fieldset>
    </form>
  )
}
