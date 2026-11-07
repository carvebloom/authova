import { useRef, useState, type FormEvent } from 'react'
import * as api from '../../api/endpoints'
import { ApiError } from '../../api/client'
import type { Connection, ConnectionInput, ConnectionProviderType } from '../../api/types'
import { Button } from '../../shared/components/Button'
import { PasswordField, SelectField, TextAreaField, TextField } from '../../shared/components/Field'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { OutcomeUnknownNotice } from '../../shared/components/OutcomeUnknownNotice'
import { PROVIDER_INFO } from '../../shared/utils/providers'

/** 标准 OIDC issuer 使用规范的 HTTPS（开发用 localhost HTTP 也不在此放宽） */
const HTTPS_URL_RE = /^https:\/\/\S+/
/** 支付宝 AppID 为纯数字字符串；保持字符串传输，绝不转为 number */
const ALIPAY_APP_ID_RE = /^\d+$/

/** 支付宝密钥长度上限（契约）：私钥 8192、支付宝公钥 2048 */
const ALIPAY_PRIVATE_KEY_MAX = 8192
const ALIPAY_PUBLIC_KEY_MAX = 2048

/** 结果未知的写操作记录：仅存内存，核对前冻结对应表单的再次提交 */
export interface PendingMutation {
  /** 更新请求的连接 id；创建请求缺省 */
  id?: string
  name: string
  at: number
}

/**
 * 各提供方的一句话边界说明（不虚构能力）：控制台只收集配置，
 * 上游凭据校验与连通性以服务端与实际登录结果为准。
 */
const PROVIDER_DESCRIPTIONS: Record<ConnectionProviderType, string> = {
  oidc: '接入符合 OpenID Connect 规范的上游身份提供方，Issuer 须为 HTTPS。',
  github: 'GitHub.com OAuth App，凭据在 GitHub 开发者设置中创建。',
  wechat: '微信开放平台网站应用扫码，凭据在微信开放平台创建。',
  qq: 'QQ 互联（QQ Connect）网站应用，凭据在 QQ 互联创建。',
  alipay: '支付宝开放平台网页应用（网页授权，RSA2 公钥模式），凭据在支付宝开放平台创建。',
}

/** 密钥类输入的公共属性：不让浏览器拼写/自动填充服务改写密钥内容 */
const KEY_INPUT_PROPS = {
  spellCheck: false,
  autoComplete: 'off',
  autoCapitalize: 'none',
  autoCorrect: 'off',
} as const

/** 创建连接表单：无幂等支持，结果未知即冻结（本实例 + 页面级记录），核对前不允许再次提交。 */
export function CreateConnectionForm({
  appId,
  blocked,
  onClose,
  onBusy,
  onUnknown,
  onCreated,
}: {
  appId: string
  /** 页面级存在结果未知的创建请求：冻结提交，须先在列表核对并确认 */
  blocked: boolean
  onClose: () => void
  onBusy: (busy: boolean) => void
  onUnknown: (pending: PendingMutation) => void
  onCreated: (created: Connection) => void
}) {
  const [providerType, setProviderType] = useState<ConnectionProviderType>('oidc')
  const [name, setName] = useState('')
  const [issuer, setIssuer] = useState('')
  const [clientId, setClientId] = useState('')
  const [clientSecret, setClientSecret] = useState('')
  const [alipayPrivateKey, setAlipayPrivateKey] = useState('')
  const [alipayPublicKey, setAlipayPublicKey] = useState('')
  const [errors, setErrors] = useState<{
    name?: string
    issuer?: string
    clientId?: string
    clientSecret?: string
    alipayPrivateKey?: string
    alipayPublicKey?: string
  }>({})
  const [formError, setFormError] = useState<ApiError | null>(null)
  const [submitting, setSubmitting] = useState(false)
  // 结果未知：本表单实例冻结提交；关闭对话框后由页面级提示继续保留该记录
  const [unknown, setUnknown] = useState<ApiError | null>(null)
  // 同步互斥：同一事件批次内的重复提交在 state 生效前即被拦截
  const busyRef = useRef(false)

  const providerInfo = PROVIDER_INFO[providerType]
  const fixedIssuer = providerInfo.fixedIssuer
  const isAlipay = providerInfo.credentials === 'alipay_keypair'
  const frozen = blocked || unknown !== null

  /** 切换提供方：清空全部凭据草稿，避免密钥被提交到另一个提供方 */
  const switchProvider = (next: ConnectionProviderType) => {
    setProviderType(next)
    setClientSecret('')
    setAlipayPrivateKey('')
    setAlipayPublicKey('')
    setErrors({})
    setFormError(null)
  }

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (frozen || busyRef.current) return
    const next: typeof errors = {}
    if (!name.trim()) next.name = '请输入连接名称'
    if (!fixedIssuer && !HTTPS_URL_RE.test(issuer.trim()))
      next.issuer = 'Issuer 必须是以 https:// 开头的 URL'
    if (!clientId.trim()) next.clientId = '请输入 Client ID'
    if (isAlipay) {
      if (clientId.trim() && !ALIPAY_APP_ID_RE.test(clientId.trim()))
        next.clientId = '支付宝 AppID 为纯数字'
      // 密钥对必须完整：缺任一把都不发请求
      if (!alipayPrivateKey.trim()) next.alipayPrivateKey = '请输入应用私钥'
      if (!alipayPublicKey.trim()) next.alipayPublicKey = '请输入支付宝公钥'
    } else if (!clientSecret) {
      next.clientSecret = '请输入 Client Secret'
    }
    setErrors(next)
    setFormError(null)
    if (Object.keys(next).length > 0) return
    // issuer 固定的提供方（GitHub/微信/QQ/支付宝）由服务端确定 issuer，不随请求提交；
    // 支付宝只提交 alipay_credentials 密钥对，绝不提交 client_secret
    const input: ConnectionInput = {
      name: name.trim(),
      provider_type: providerType,
      client_id: clientId.trim(),
      ...(fixedIssuer ? {} : { issuer: issuer.trim() }),
      ...(isAlipay
        ? {
            alipay_credentials: {
              private_key: alipayPrivateKey,
              alipay_public_key: alipayPublicKey,
            },
          }
        : { client_secret: clientSecret }),
    }
    busyRef.current = true
    setSubmitting(true)
    onBusy(true)
    try {
      const created = await api.createConnection(appId, input)
      onCreated(created)
    } catch (err) {
      const apiErr =
        err instanceof ApiError ? err : new ApiError({ status: 0, detail: String(err) })
      if (apiErr.isOutcomeUnknown) {
        // 无幂等支持：记录到页面级并冻结本表单，核对前不允许再次提交
        onUnknown({ name: input.name, at: Date.now() })
        setUnknown(apiErr)
      } else {
        setFormError(apiErr)
      }
    } finally {
      busyRef.current = false
      setSubmitting(false)
      onBusy(false)
    }
  }

  const fieldsDisabled = submitting || frozen

  return (
    <form className="form" onSubmit={handleSubmit} noValidate>
      {unknown ? (
        <OutcomeUnknownNotice
          error={unknown}
          detail="创建连接不支持幂等重试，该连接可能已创建成功。请关闭对话框，在连接列表中确认是否已存在；确认未生效后，在页面顶部的提示中解除冻结再重新填写，避免重复配置。"
        />
      ) : blocked ? (
        <div className="notice notice-warn" role="alert">
          <div className="notice-body">
            <p className="notice-title">存在结果未知的创建请求，创建已冻结</p>
            <p className="notice-detail">
              先前的创建请求可能已在服务端生效。请关闭对话框，刷新连接列表核对实际状态，
              并在页面顶部的提示中确认核对结果后再重新创建。
            </p>
          </div>
        </div>
      ) : null}
      {formError ? <ErrorNotice error={formError} /> : null}
      <SelectField
        label="类型"
        value={providerType}
        onChange={(e) => switchProvider(e.target.value as ConnectionProviderType)}
        hint="切换类型会清空已填写的密钥"
        disabled={fieldsDisabled}
      >
        <option value="oidc">{PROVIDER_INFO.oidc.label}</option>
        <option value="github">{PROVIDER_INFO.github.label}</option>
        <option value="wechat">{PROVIDER_INFO.wechat.label}</option>
        <option value="qq">{PROVIDER_INFO.qq.label}</option>
        <option value="alipay">{PROVIDER_INFO.alipay.label}</option>
      </SelectField>
      <div className="provider-meta" role="note">
        <p>{PROVIDER_DESCRIPTIONS[providerType]}</p>
        <p>创建后默认「已启用」，还需应用级「第三方登录」开启才会出现在终端用户登录页。</p>
      </div>
      <TextField
        label="名称"
        value={name}
        onChange={(e) => setName(e.target.value)}
        error={errors.name}
        required
        autoFocus
        disabled={fieldsDisabled}
      />
      {fixedIssuer ? (
        <p className="field-hint">
          {providerInfo.label} 的 Issuer 由服务端固定为 <span className="mono">{fixedIssuer}</span>
          ，无需填写。
        </p>
      ) : (
        <TextField
          label="Issuer"
          value={issuer}
          onChange={(e) => setIssuer(e.target.value)}
          error={errors.issuer}
          hint="上游 OIDC 提供方的 issuer URL（HTTPS）"
          placeholder="https://accounts.example.com"
          required
          disabled={fieldsDisabled}
        />
      )}
      <TextField
        label="Client ID"
        value={clientId}
        onChange={(e) => setClientId(e.target.value)}
        error={errors.clientId}
        hint={providerInfo.clientIdHint}
        inputMode={isAlipay ? 'numeric' : undefined}
        required
        disabled={fieldsDisabled}
      />
      {isAlipay ? (
        <>
          <TextAreaField
            label="应用私钥"
            value={alipayPrivateKey}
            onChange={(e) => setAlipayPrivateKey(e.target.value)}
            error={errors.alipayPrivateKey}
            hint={`支付宝开放平台生成的应用 RSA2 私钥（PKCS#1/PKCS#8，PEM 或 base64 DER，不超过 ${ALIPAY_PRIVATE_KEY_MAX} 字符）；仅用于创建请求，读取响应永不包含`}
            className="input-mono"
            rows={8}
            maxLength={ALIPAY_PRIVATE_KEY_MAX}
            required
            disabled={fieldsDisabled}
            {...KEY_INPUT_PROPS}
          />
          <TextAreaField
            label="支付宝公钥"
            value={alipayPublicKey}
            onChange={(e) => setAlipayPublicKey(e.target.value)}
            error={errors.alipayPublicKey}
            hint={`在支付宝开放平台查看的「支付宝公钥」（SubjectPublicKeyInfo，PEM 或 base64 DER，不超过 ${ALIPAY_PUBLIC_KEY_MAX} 字符），用于验签——不是应用自己的公钥；公钥模式，无需证书`}
            className="input-mono"
            rows={5}
            maxLength={ALIPAY_PUBLIC_KEY_MAX}
            required
            disabled={fieldsDisabled}
            {...KEY_INPUT_PROPS}
          />
        </>
      ) : (
        <PasswordField
          label="Client Secret"
          autoComplete="off"
          value={clientSecret}
          onChange={(e) => setClientSecret(e.target.value)}
          error={errors.clientSecret}
          hint={providerInfo.clientSecretHint}
          required
          disabled={fieldsDisabled}
        />
      )}
      <div className="dialog-actions">
        <Button type="button" onClick={onClose} disabled={submitting}>
          取消
        </Button>
        <Button type="submit" variant="primary" loading={submitting} disabled={frozen}>
          创建
        </Button>
      </div>
    </form>
  )
}
