import { useProtection } from '../../app/protectionState'
import { useRef, useState, type FormEvent, type ReactNode } from 'react'
import * as api from '../../api/endpoints'
import { ApiError } from '../../api/client'
import type { App, AppPatch } from '../../api/types'
import { useAppCapabilities } from '../../app/appContext'
import { Button } from '../../shared/components/Button'
import { TextField } from '../../shared/components/Field'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { OutcomeUnknownNotice } from '../../shared/components/OutcomeUnknownNotice'
import { PageHeader } from '../../shared/components/PageHeader'

import { LoginPolicySection } from './LoginPolicySection'
import { ResourcePolicySection } from './ResourcePolicySection'

/**
 * 应用配置：名称与 require_mfa（契约 AppPatch，config_version 乐观锁）。
 * 登录策略为独立版块（LoginPolicySection，仅平台 owner/admin 或 App owner 可写）。
 * 写操作不自动重试：409 冻结，读回最新配置后重新编辑；
 * 结果未知冻结，读回权威状态（本地快照 rebase）后解除。
 */
export function AppSettingsTab() {
  const { app, reloadApp, canMutate, canManageLoginPolicy } = useAppCapabilities()

  if (!canMutate) {
    return (
      <div className="tab-panel">
        <PageHeader level={2} title="配置" sub="应用名称与登录安全策略。" />
        <div className="panel">
          <dl className="detail-grid">
            <div className="detail-item">
              <dt>名称</dt>
              <dd>{app.name}</dd>
            </div>
            <div className="detail-item">
              <dt>要求用户 MFA</dt>
              <dd>{app.require_mfa ? '已启用' : '未启用'}</dd>
            </div>
            <div className="detail-item">
              <dt>配置版本</dt>
              <dd>{app.config_version}</dd>
            </div>
          </dl>
        </div>
        <p className="field-hint section-gap">当前账号为只读角色，不能修改配置。</p>
        <LoginPolicySection appId={app.id} canWrite={canManageLoginPolicy} />
        <ResourcePolicySection appId={app.id} canWrite={canManageLoginPolicy} />
      </div>
    )
  }

  // key 限定作用域：切换 App 时表单整体重置，杜绝跨 App 复用旧数据/旧版本
  return (
    <SettingsForm key={app.id} app={app} onSaved={reloadApp}>
      <LoginPolicySection appId={app.id} canWrite={canManageLoginPolicy} />
      <ResourcePolicySection appId={app.id} canWrite={canManageLoginPolicy} />
    </SettingsForm>
  )
}

function SettingsForm({
  app,
  onSaved,
  children,
}: {
  app: App
  onSaved: () => void
  children?: ReactNode
}) {
  // current：本地权威快照；提交/冲突/结果未知均以它为准，与页头共享上下文经 onSaved 同步
  const [current, setCurrent] = useState(app)
  const [name, setName] = useState(app.name)
  const [requireMfa, setRequireMfa] = useState(app.require_mfa)
  const [nameError, setNameError] = useState<string | null>(null)
  const [formError, setFormError] = useState<ApiError | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [stale, setStale] = useState(false)
  const [unknown, setUnknown] = useState<ApiError | null>(null)
  const [reloading, setReloading] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const submission = useRef(false)

  const changed = name.trim() !== current.name || requireMfa !== current.require_mfa
  const frozen = unknown !== null || stale
  useProtection(submitting || reloading || frozen)

  /** 权威读回：rebase 本地快照并重置输入；解除 409 / 结果未知冻结 */
  const reloadCurrent = async () => {
    setReloading(true)
    setFormError(null)
    try {
      const fresh = await api.getApp(current.id)
      setCurrent(fresh)
      setName(fresh.name)
      setRequireMfa(fresh.require_mfa)
      setNameError(null)
      setStale(false)
      setUnknown(null)
      onSaved()
    } catch (err) {
      setFormError(err instanceof ApiError ? err : new ApiError({ status: 0, detail: String(err) }))
    } finally {
      setReloading(false)
    }
  }

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (submission.current) return
    if (frozen || !changed || submitting || reloading) return
    const trimmed = name.trim()
    if (!trimmed) {
      setNameError('请输入应用名称')
      return
    }
    setNameError(null)
    setFormError(null)
    setNotice(null)
    // payload 捕获提交时的 scope（current.id）与 config_version
    const patch: AppPatch = { config_version: current.config_version }
    if (trimmed !== current.name) patch.name = trimmed
    if (requireMfa !== current.require_mfa) patch.require_mfa = requireMfa
    submission.current = true
    setSubmitting(true)
    try {
      const updated = await api.updateApp(current.id, patch)
      setCurrent(updated)
      setName(updated.name)
      setRequireMfa(updated.require_mfa)
      setNotice(`应用配置已更新（配置版本 ${updated.config_version}）。`)
      onSaved()
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
      重新加载最新配置
    </Button>
  )

  return (
    <div className="tab-panel">
      <PageHeader
        level={2}
        title="配置"
        sub={`修改名称或 MFA 策略；当前配置版本 ${current.config_version}，提交携带版本号，冲突时需读回后重试。`}
      />

      {notice ? (
        <div className="notice notice-ok" role="status">
          <div className="notice-body">
            <p className="notice-title">{notice}</p>
          </div>
        </div>
      ) : null}

      <div className="panel panel-narrow">
        {unknown ? (
          <OutcomeUnknownNotice
            error={unknown}
            detail="该配置更新可能已在服务端生效。请点击「重新加载最新配置」读回权威状态后再重新编辑；核对前表单保持冻结，避免覆盖并发修改。"
            action={reloadAction}
          />
        ) : null}
        {stale ? (
          <div className="notice notice-warn" role="alert">
            <div className="notice-body">
              <p className="notice-title">配置版本已过期（409 冲突）</p>
              <p className="notice-detail">
                该应用刚被其他操作修改。请点击「重新加载最新配置」获取最新版本，确认当前内容后再重新提交；系统不会自动重试。
              </p>
            </div>
            {reloadAction}
          </div>
        ) : null}
        {formError ? <ErrorNotice error={formError} /> : null}

        <form className="form" onSubmit={handleSubmit} noValidate>
          <fieldset className="submission-fields" disabled={submitting || frozen || reloading}>
            <TextField
              label="应用名称"
              value={name}
              onChange={(e) => {
                setName(e.target.value)
                if (nameError) setNameError(null)
              }}
              error={nameError ?? undefined}
              required
            />
            <div className="setting-rows">
              <div className="setting-row">
                <div className="setting-text">
                  <span className="setting-label" id="app-require-mfa-label">
                    要求用户 MFA
                  </span>
                  <span className="setting-hint" id="app-require-mfa-hint">
                    登录该应用的用户必须完成二次验证（TOTP）；与平台运营账号的强制 TOTP 无关。
                  </span>
                </div>
                <input
                  type="checkbox"
                  className="switch"
                  aria-labelledby="app-require-mfa-label"
                  aria-describedby="app-require-mfa-hint"
                  checked={requireMfa}
                  onChange={(e) => setRequireMfa(e.target.checked)}
                />
              </div>
            </div>
            {!changed ? <p className="field-hint">修改名称或 MFA 策略后才能保存。</p> : null}
            <div className="form-footer">
              {frozen ? null : reloadAction}
              <Button
                type="submit"
                variant="primary"
                loading={submitting}
                disabled={frozen || !changed}
              >
                保存
              </Button>
            </div>
          </fieldset>
        </form>
      </div>
      {children}
    </div>
  )
}
