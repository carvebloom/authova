import { useRef, useState, type FormEvent } from 'react'
import { useProtection } from '../../app/protectionState'
import * as api from '../../api/endpoints'
import { ApiError } from '../../api/client'
import type { Connection, ConnectionPatch } from '../../api/types'
import { Button } from '../../shared/components/Button'
import { PasswordField, SelectField, TextAreaField, TextField } from '../../shared/components/Field'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { OutcomeUnknownNotice } from '../../shared/components/OutcomeUnknownNotice'
import { PROVIDER_INFO, providerLabel } from '../../shared/utils/providers'
import type { PendingMutation } from './ConnectionCreateForm'

/** 支付宝密钥长度上限（契约）：私钥 8192、支付宝公钥 2048 */
const ALIPAY_PRIVATE_KEY_MAX = 8192
const ALIPAY_PUBLIC_KEY_MAX = 2048

/** 密钥类输入的公共属性：不让浏览器拼写/自动填充服务改写密钥内容 */
const KEY_INPUT_PROPS = {
  spellCheck: false,
  autoComplete: 'off',
  autoCapitalize: 'none',
  autoCorrect: 'off',
} as const

/**
 * 编辑连接表单：名称/状态/凭据轮换；config_version 乐观锁。
 * 支付宝使用 alipay_credentials 密钥对（轮换须两把同时提供），其余类型使用 client_secret。
 * 409 或结果未知：全字段冻结，仅允许「重新加载最新配置」权威读回；
 * 读回清空旧输入（含全部密钥），杜绝静默重复轮换。
 */
export function EditConnectionForm({
  appId,
  connection,
  pending,
  onClose,
  onBusy,
  onUnknown,
  onReconciled,
  onUpdated,
}: {
  appId: string
  connection: Connection
  /** 页面级未确认的更新请求；属于本连接时冻结提交，须先读回核对 */
  pending: PendingMutation | null
  onClose: () => void
  onBusy: (busy: boolean) => void
  onUnknown: (pending: PendingMutation) => void
  /** 读回最新配置成功：结果未知的更新已核对，解除页面级冻结 */
  onReconciled: () => void
  onUpdated: (updated: Connection) => void
}) {
  const [current, setCurrent] = useState(connection)
  const [name, setName] = useState(connection.name)
  const [status, setStatus] = useState<Connection['status']>(connection.status)
  const [clientSecret, setClientSecret] = useState('')
  const [alipayPrivateKey, setAlipayPrivateKey] = useState('')
  const [alipayPublicKey, setAlipayPublicKey] = useState('')
  const [reason, setReason] = useState('')
  const [errors, setErrors] = useState<{ name?: string; reason?: string; alipayKeyPair?: string }>(
    {},
  )
  const [formError, setFormError] = useState<ApiError | null>(null)
  // 409 配置版本过期：必须重新加载最新配置后才可再次提交，不做静默重试
  const [stale, setStale] = useState(false)
  const [reloading, setReloading] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [unknown, setUnknown] = useState<ApiError | null>(null)
  // 同步互斥：同一事件批次内的重复提交/读回在 state 生效前即被拦截
  const busyRef = useRef(false)

  const isAlipay = PROVIDER_INFO[current.provider_type].credentials === 'alipay_keypair'
  const blocked = pending !== null && pending.id === current.id
  const frozen = blocked || unknown !== null
  const alipayKeyEntered = alipayPrivateKey.trim() !== '' || alipayPublicKey.trim() !== ''
  // 支付宝：任一把非空即视为轮换意图，提交前强制完整密钥对
  const rotating = isAlipay ? alipayKeyEntered : clientSecret.trim() !== ''
  const changed = name.trim() !== current.name || status !== current.status || rotating
  const fieldsDisabled = frozen || stale || submitting || reloading
  useProtection(fieldsDisabled)

  const reloadCurrent = async () => {
    if (busyRef.current) return
    busyRef.current = true
    setReloading(true)
    setFormError(null)
    try {
      // Rebase to the server state. An uncertain secret cannot be read back;
      // clear it so reconciliation cannot silently repeat a rotation.
      const fresh = await api.getConnection(appId, current.id)
      setCurrent(fresh)
      setName(fresh.name)
      setStatus(fresh.status)
      setClientSecret('')
      setAlipayPrivateKey('')
      setAlipayPublicKey('')
      setReason('')
      setErrors({})
      setStale(false)
      if (unknown !== null || blocked) {
        setUnknown(null)
        onReconciled()
      }
    } catch (err) {
      setFormError(err instanceof ApiError ? err : new ApiError({ status: 0, detail: String(err) }))
    } finally {
      busyRef.current = false
      setReloading(false)
    }
  }

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (fieldsDisabled || !changed || busyRef.current) return
    const next: typeof errors = {}
    if (!name.trim()) next.name = '请输入连接名称'
    if (!reason.trim()) next.reason = '请填写操作原因'
    // 密钥对原子轮换：两把同时提供，否则整体省略（保留原密钥对）
    if (isAlipay && alipayKeyEntered && (!alipayPrivateKey.trim() || !alipayPublicKey.trim()))
      next.alipayKeyPair = '轮换须同时填写应用私钥与支付宝公钥；两把都留空则保留原密钥对'
    setErrors(next)
    setFormError(null)
    if (Object.keys(next).length > 0) return
    // 仅提交发生变化的字段；密钥留空即省略（保留原密钥），显式空值不会被发送；
    // 支付宝只提交 alipay_credentials，绝不提交 client_secret
    const patch: ConnectionPatch = {
      config_version: current.config_version,
      reason: reason.trim(),
    }
    if (name.trim() !== current.name) patch.name = name.trim()
    if (status !== current.status) patch.status = status
    if (rotating) {
      if (isAlipay) {
        patch.alipay_credentials = {
          private_key: alipayPrivateKey,
          alipay_public_key: alipayPublicKey,
        }
      } else {
        patch.client_secret = clientSecret
      }
    }
    busyRef.current = true
    setSubmitting(true)
    onBusy(true)
    try {
      const updated = await api.updateConnection(appId, current.id, patch)
      onUpdated(updated)
    } catch (err) {
      const apiErr =
        err instanceof ApiError ? err : new ApiError({ status: 0, detail: String(err) })
      if (apiErr.status === 409) {
        setStale(true)
      } else if (apiErr.isOutcomeUnknown) {
        onUnknown({ id: current.id, name: name.trim() || current.name, at: Date.now() })
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

  const reloadAction = (
    <Button type="button" size="sm" onClick={reloadCurrent} loading={reloading}>
      重新加载最新配置
    </Button>
  )

  return (
    <form className="form" onSubmit={handleSubmit} noValidate>
      {unknown ? (
        <OutcomeUnknownNotice
          error={unknown}
          detail="该更新可能已在服务端生效。请点击「重新加载最新配置」读回实际状态后重新编辑；旧输入与密钥将被清除，核对之前表单保持冻结，避免覆盖他人的并发修改或重复轮换密钥。"
          action={reloadAction}
        />
      ) : blocked ? (
        <div className="notice notice-warn" role="alert">
          <div className="notice-body">
            <p className="notice-title">先前的更新请求结果未知，更新已冻结</p>
            <p className="notice-detail">
              该请求可能已在服务端生效。请点击「重新加载最新配置」读回实际状态，确认后再继续编辑。
            </p>
          </div>
          {reloadAction}
        </div>
      ) : null}
      {stale ? (
        <div className="notice notice-warn" role="alert">
          <div className="notice-body">
            <p className="notice-title">配置版本已过期（409 冲突）</p>
            <p className="notice-detail">
              该连接刚被其他操作修改。请点击「重新加载最新配置」获取最新版本，确认当前内容后再重新提交；
              系统不会自动重试。
            </p>
          </div>
          {reloadAction}
        </div>
      ) : null}
      {formError ? <ErrorNotice error={formError} /> : null}
      <p className="field-hint">当前配置版本 {current.config_version}</p>
      <p className="field-hint">
        {providerLabel(current.provider_type)} 连接的 Issuer{' '}
        <span className="mono">{current.issuer}</span> 与 Client ID{' '}
        <span className="mono">{current.client_id}</span> 不可修改。
      </p>
      <TextField
        label="名称"
        value={name}
        onChange={(e) => setName(e.target.value)}
        error={errors.name}
        required
        autoFocus
        disabled={fieldsDisabled}
      />
      <SelectField
        label="状态"
        value={status}
        onChange={(e) => setStatus(e.target.value === 'disabled' ? 'disabled' : 'active')}
        hint="禁用后该连接不能再用于登录，已有绑定关系保留；不提供删除。连接还需应用级「第三方登录」开启才会出现在终端用户登录页。"
        disabled={fieldsDisabled}
      >
        <option value="active">活跃</option>
        <option value="disabled">已禁用</option>
      </SelectField>
      {isAlipay ? (
        <>
          <TextAreaField
            label="新应用私钥"
            value={alipayPrivateKey}
            onChange={(e) => setAlipayPrivateKey(e.target.value)}
            hint={`支付宝开放平台生成的应用 RSA2 私钥（不超过 ${ALIPAY_PRIVATE_KEY_MAX} 字符）`}
            className="input-mono"
            rows={8}
            maxLength={ALIPAY_PRIVATE_KEY_MAX}
            disabled={fieldsDisabled}
            {...KEY_INPUT_PROPS}
          />
          <TextAreaField
            label="新支付宝公钥"
            value={alipayPublicKey}
            onChange={(e) => setAlipayPublicKey(e.target.value)}
            hint={`在支付宝开放平台查看的「支付宝公钥」（不超过 ${ALIPAY_PUBLIC_KEY_MAX} 字符），用于验签——不是应用自己的公钥`}
            className="input-mono"
            rows={5}
            maxLength={ALIPAY_PUBLIC_KEY_MAX}
            disabled={fieldsDisabled}
            {...KEY_INPUT_PROPS}
          />
          {errors.alipayKeyPair ? (
            <p className="field-error-text" role="alert">
              {errors.alipayKeyPair}
            </p>
          ) : null}
          <p className="field-hint">
            两把密钥都留空则保留原密钥对；轮换须同时提供，一次提交原子生效。读取响应永不包含密钥，轮换后请立即在支付宝开放平台核对。
          </p>
        </>
      ) : (
        <PasswordField
          label="新 Client Secret"
          autoComplete="off"
          value={clientSecret}
          onChange={(e) => setClientSecret(e.target.value)}
          hint="留空则保留原密钥；读取响应永不包含密钥，轮换后请立即在上游核对"
          disabled={fieldsDisabled}
        />
      )}
      <TextAreaField
        label="操作原因"
        value={reason}
        onChange={(e) => setReason(e.target.value)}
        error={errors.reason}
        hint="将随请求一并记录到审计日志"
        rows={3}
        required
        disabled={fieldsDisabled}
      />
      {!changed && !frozen && !stale ? (
        <p className="field-hint">修改名称、状态或密钥后才能保存。</p>
      ) : null}
      <div className="dialog-actions">
        <Button type="button" onClick={onClose} disabled={submitting || reloading}>
          取消
        </Button>
        <Button
          type="submit"
          variant="primary"
          loading={submitting}
          disabled={frozen || stale || !changed}
        >
          保存
        </Button>
      </div>
    </form>
  )
}
