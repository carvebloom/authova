import { useRef, useState, type FormEvent } from 'react'
import { ApiError } from '../../api/client'
import * as api from '../../api/endpoints'
import type { Client, ClientInput, CreatedClient } from '../../api/types'
import { useProtection } from '../../app/protectionState'
import { Button } from '../../shared/components/Button'
import { Dialog } from '../../shared/components/Dialog'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { SelectField, TextAreaField, TextField } from '../../shared/components/Field'
import { formatTime } from '../../shared/utils/format'
import {
  isReplayWindowOpen,
  randomIdempotencyKey,
  samePayload,
  type PendingCreate,
} from '../../shared/utils/idempotency'

import { payloadFromForm, stateFromClient, stateFromPending } from './formModel'
import type { ClientFormState } from './formModel'
export function ClientFormDialog({
  open,
  appId,
  client,
  pending,
  onClose,
  onUnknown,
  onCreated,
  onUpdated,
}: {
  open: boolean
  appId: string
  client: Client | null
  /** 未确认的创建请求（仅创建模式有意义）；存在时表单预填原内容，只能用原键重试 */
  pending: PendingCreate<ClientInput> | null
  onClose: () => void
  onUnknown: (pending: PendingCreate<ClientInput>) => void
  onCreated: (client: CreatedClient) => void
  onUpdated: () => void
}) {
  const [currentClient, setCurrentClient] = useState(client)
  const isEdit = currentClient !== null
  const activePending = isEdit ? null : pending
  const [form, setForm] = useState<ClientFormState>(() =>
    activePending ? stateFromPending(activePending) : stateFromClient(client),
  )
  const [errors, setErrors] = useState<{
    name?: string
    redirectUris?: string
    scopes?: string
    audiences?: string
    grantTypes?: string
  }>({})
  const [formError, setFormError] = useState<ApiError | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const submission = useRef(false)
  const [reloading, setReloading] = useState(false)
  const [uncertain, setUncertain] = useState(false)
  const frozen = isEdit && uncertain
  const releaseProtection = useProtection(open && (submitting || frozen || reloading))
  const reloadCurrent = async () => {
    if (!currentClient || submission.current) return
    submission.current = true
    setReloading(true)
    try {
      const fresh = await api.getClient(appId, currentClient.client_id)
      setCurrentClient(fresh)
      setForm(stateFromClient(fresh))
      setFormError(null)
      setUncertain(false)
    } catch (error) {
      setFormError(error instanceof ApiError ? error : new ApiError({ status: 0 }))
    } finally {
      submission.current = false
      setReloading(false)
    }
  }

  const set = (patch: Partial<ClientFormState>) => setForm((prev) => ({ ...prev, ...patch }))

  const currentPayload = payloadFromForm(form)
  /** 内容偏离未确认请求：原请求可能已生效，修改后提交即是新请求 */
  const diverged = activePending !== null && !samePayload(currentPayload, activePending.payload)
  const windowOpen = activePending !== null && isReplayWindowOpen(activePending.at)
  const blocked = activePending !== null && (diverged || !windowOpen)

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (submission.current || frozen) return
    if (
      blocked ||
      (activePending !== null &&
        (!isReplayWindowOpen(activePending.at) ||
          !samePayload(currentPayload, activePending.payload)))
    ) {
      setFormError(
        new ApiError({
          status: 409,
          detail: '未确认请求已超出安全重放窗口或内容已变化，请先核对列表。',
        }),
      )
      return
    }
    const next: {
      name?: string
      redirectUris?: string
      scopes?: string
      audiences?: string
      grantTypes?: string
    } = {}
    if (!form.name.trim()) next.name = '请输入客户端名称'
    const invalidUri = currentPayload.redirect_uris.find((uri) => !/^https?:\/\/.+/.test(uri))
    if (invalidUri) next.redirectUris = `回调地址必须以 http(s) 开头：${invalidUri}`
    if (currentPayload.allowed_scopes.length === 0) next.scopes = '至少填写一个允许的作用域'
    if (currentPayload.allowed_audiences.length === 0)
      next.audiences = '填写业务 API 的受众标识，例如 https://api.example.com 或 urn:example:api'
    if (currentPayload.grant_types.length === 0) next.grantTypes = '至少选择一个授权类型'
    if (currentPayload.allowed_audiences.some((value) => !/^[a-z][a-z0-9+.-]*:/i.test(value)))
      next.audiences = '受众应为 URI，例如 https://api.example.com 或 urn:example:api'
    setErrors(next)
    setFormError(null)
    if (Object.keys(next).length > 0) return

    const base = {
      name: currentPayload.name,
      redirect_uris: currentPayload.redirect_uris,
      allowed_scopes: currentPayload.allowed_scopes,
      allowed_audiences: currentPayload.allowed_audiences,
      allowed_introspection_audiences: currentPayload.allowed_introspection_audiences,
      grant_types: currentPayload.grant_types,
    }

    submission.current = true
    setSubmitting(true)
    try {
      if (isEdit) {
        await api.updateClient(appId, currentClient!.client_id, {
          ...base,
          status: form.status === 'active' || form.status === 'disabled' ? form.status : undefined,
          config_version: currentClient!.config_version,
        })
        onUpdated()
      } else {
        // 同一未确认请求复用原键；无未确认请求才生成新键
        const key = activePending?.key ?? randomIdempotencyKey()
        try {
          const created = await api.createClient(appId, currentPayload, key)
          releaseProtection()
          onCreated(created)
        } catch (err) {
          const apiErr =
            err instanceof ApiError ? err : new ApiError({ status: 0, detail: String(err) })
          if (apiErr.isOutcomeUnknown) {
            // 结果未知：记录键与内容（保留首次提交时间），此后只能用原键重试
            onUnknown({ payload: currentPayload, key, at: activePending?.at ?? Date.now() })
          }
          throw apiErr
        }
      }
    } catch (err) {
      const failure = err instanceof ApiError ? err : new ApiError({ status: 0 })
      if (isEdit && (failure.isOutcomeUnknown || failure.status === 409)) setUncertain(true)
      setFormError(failure)
    } finally {
      if (!isEdit) releaseProtection()
      submission.current = false
      setSubmitting(false)
    }
  }

  return (
    <Dialog
      open={open}
      title={isEdit ? '编辑客户端' : activePending ? '处理未确认的创建' : '创建客户端'}
      onClose={onClose}
      wide
      dismissible={!submitting && !frozen && !reloading}
    >
      {frozen ? (
        <div className="notice notice-warn" role="status">
          <p>请重新读取当前客户端配置，核对后再编辑。</p>
          <Button onClick={reloadCurrent} loading={reloading}>
            重新加载最新配置
          </Button>
        </div>
      ) : null}
      <form className="form" onSubmit={handleSubmit} noValidate>
        <fieldset className="submission-fields" disabled={submitting || frozen || reloading}>
          {activePending ? (
            <div className="notice notice-warn" role="alert">
              <div className="notice-body">
                <p className="notice-title">先前的创建请求结果未知，可能已生效</p>
                <p className="notice-detail">
                  提交于 {formatTime(new Date(activePending.at).toISOString())}；客户端「
                  {activePending.payload.name}」可能已创建成功。
                  {diverged
                    ? '当前内容已修改：继续提交将是全新请求，可能造成重复。请先关闭对话框，在列表中核对原请求是否生效；确认未生效后在页面提示中放弃该记录，再重新填写。'
                    : windowOpen
                      ? '内容与原请求一致：可用原幂等键安全重试，服务端会按键去重；机密客户端的密钥仅在创建后 10 分钟内随回放再次返回。'
                      : '已超过 10 分钟幂等回放窗口，不能再按原键安全重试；请关闭对话框，刷新并核对列表实际状态后再处理。'}
                </p>
              </div>
            </div>
          ) : null}
          {formError ? <ErrorNotice error={formError} /> : null}
          {isEdit ? (
            <p className="field-hint">
              Client ID <span className="mono">{currentClient!.client_id}</span>，类型不可修改。
            </p>
          ) : null}
          {isEdit ? (
            <SelectField
              label="状态"
              value={form.status}
              onChange={(e) => set({ status: e.target.value })}
            >
              <option value="active">活跃</option>
              <option value="disabled">已禁用</option>
            </SelectField>
          ) : null}
          <TextField
            label="名称"
            value={form.name}
            onChange={(e) => set({ name: e.target.value })}
            error={errors.name}
            required
            autoFocus
          />
          {!isEdit ? (
            <SelectField
              label="类型"
              value={form.kind}
              onChange={(e) =>
                set({ kind: e.target.value === 'confidential' ? 'confidential' : 'public' })
              }
              hint="公开：浏览器/移动端应用，不持有密钥；机密：服务端应用，创建成功后一次性返回 client_secret"
            >
              <option value="public">公开（public）</option>
              <option value="confidential">机密（confidential）</option>
            </SelectField>
          ) : null}
          <fieldset className="fieldset">
            <legend>回调与授权</legend>
            <TextAreaField
              label="回调地址（redirect_uris）"
              value={form.redirectUris}
              onChange={(e) => set({ redirectUris: e.target.value })}
              error={errors.redirectUris}
              hint="每行一个，例如 https://app.example.com/callback"
              rows={3}
            />
            <TextField
              label="授权类型（grant_types）"
              value={form.grantTypes}
              onChange={(e) => set({ grantTypes: e.target.value })}
              hint="以空格分隔，如 authorization_code refresh_token"
              error={errors.grantTypes}
              required
            />
          </fieldset>
          <fieldset className="fieldset">
            <legend>作用域与受众</legend>
            <TextField
              label="允许的作用域（allowed_scopes）"
              value={form.scopes}
              onChange={(e) => set({ scopes: e.target.value })}
              hint="以空格分隔，如 openid profile email"
              error={errors.scopes}
              required
            />
            <TextField
              label="允许的受众（allowed_audiences）"
              error={errors.audiences}
              required
              value={form.audiences}
              onChange={(e) => set({ audiences: e.target.value })}
              hint="业务 API 接受令牌时校验的标识；以空格分隔 URI，如 https://api.example.com 或 urn:example:api"
            />
            <TextField
              label="允许的自省受众（allowed_introspection_audiences）"
              value={form.introspectionAudiences}
              onChange={(e) => set({ introspectionAudiences: e.target.value })}
              hint="以空格分隔"
            />
          </fieldset>
          <div className="dialog-actions">
            <Button type="button" onClick={onClose} disabled={submitting || frozen || reloading}>
              取消
            </Button>
            <Button type="submit" variant="primary" loading={submitting} disabled={blocked}>
              {isEdit ? '保存' : activePending ? '重试原请求' : '创建'}
            </Button>
          </div>
        </fieldset>
      </form>
    </Dialog>
  )
}
