import { useRef, useState, type FormEvent } from 'react'
import { useProtection } from '../../app/protectionState'
import { Link, useParams } from 'react-router-dom'
import * as api from '../../api/endpoints'
import { ApiError } from '../../api/client'
import type { AdminRole, UserSession } from '../../api/types'
import { useAsync } from '../../shared/hooks/useAsync'
import { usePagedList } from '../../shared/hooks/usePagedList'
import { useAppCapabilities } from '../../app/appContext'
import { Button } from '../../shared/components/Button'
import { Dialog } from '../../shared/components/Dialog'
import { SelectField, TextAreaField } from '../../shared/components/Field'
import { ReasonDialog } from '../../shared/components/ReasonDialog'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { EmptyState, ForbiddenState, Loading } from '../../shared/components/States'
import { StatusBadge } from '../../shared/components/StatusBadge'
import { LoadMore } from '../../shared/components/LoadMore'
import { adminRoleLabel, formatTime, joinList, shortenId } from '../../shared/utils/format'

export function UserDetailPage() {
  const { appId = '', userId = '' } = useParams<{ appId: string; userId: string }>()
  const { canMutate, canManageAdministrators } = useAppCapabilities()
  const user = useAsync(`user:${appId}:${userId}`, () => api.getUser(appId, userId))
  const sessions = usePagedList(`sessions:${appId}:${userId}`, (cursor) =>
    api.listUserSessions(appId, userId, { cursor, limit: 20 }),
  )
  const [disableOpen, setDisableOpen] = useState(false)
  const [enableOpen, setEnableOpen] = useState(false)
  const [revoking, setRevoking] = useState<UserSession | null>(null)
  const [roleOpen, setRoleOpen] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)

  if (user.loading) return <Loading />
  if (user.error || !user.data) {
    if (user.error?.isForbidden) return <ForbiddenState error={user.error} />
    return <ErrorNotice error={user.error} onRetry={user.reload} />
  }
  const u = user.data
  const disabled = u.status === 'disabled'
  // 已删除用户为终态：不可恢复，也不再提供禁用入口
  const deleted = u.status === 'deleted'
  const quarantined = u.status === 'quarantined'

  return (
    <div className="tab-panel">
      <p className="back-row">
        <Link to={`/apps/${appId}/users`} className="table-link">
          ← 返回用户列表
        </Link>
      </p>

      <div className="page-head">
        <div>
          <h2 className="section-title">{u.display_name || '未命名用户'}</h2>
          <p className="page-sub mono">{u.id}</p>
        </div>
        <div className="page-actions">
          {canManageAdministrators && !quarantined ? (
            <Button
              onClick={() => {
                setNotice(null)
                setRoleOpen(true)
              }}
            >
              {u.admin_role ? '变更管理员角色' : '设为管理员'}
            </Button>
          ) : null}
          {canMutate && disabled ? (
            <Button
              variant="primary"
              onClick={() => {
                setNotice(null)
                setEnableOpen(true)
              }}
            >
              恢复用户
            </Button>
          ) : null}
          {canMutate && !disabled && !deleted && !quarantined ? (
            <Button
              variant="danger"
              onClick={() => {
                setNotice(null)
                setDisableOpen(true)
              }}
            >
              禁用用户
            </Button>
          ) : null}
        </div>
      </div>

      {quarantined ? (
        <div className="notice notice-warn" role="status">
          该身份处于恢复隔离，请由部署管理员核验并执行本地恢复流程。
        </div>
      ) : null}

      {notice ? (
        <div className="notice notice-ok" role="status">
          <div className="notice-body">
            <p className="notice-title">{notice}</p>
          </div>
        </div>
      ) : null}

      <div className="panel">
        <dl className="detail-grid">
          <div className="detail-item">
            <dt>显示名</dt>
            <dd>{u.display_name || '—'}</dd>
          </div>
          <div className="detail-item">
            <dt>状态</dt>
            <dd>
              <StatusBadge status={u.status} />
            </dd>
          </div>
          <div className="detail-item">
            <dt>邮箱（已脱敏）</dt>
            <dd>{u.email || '—'}</dd>
          </div>
          <div className="detail-item">
            <dt>手机号（已脱敏）</dt>
            <dd>{u.phone || '—'}</dd>
          </div>
          <div className="detail-item">
            <dt>管理员角色</dt>
            <dd>{adminRoleLabel(u.admin_role)}</dd>
          </div>
          <div className="detail-item">
            <dt>会话纪元（session_epoch）</dt>
            <dd>{u.session_epoch}</dd>
          </div>
          <div className="detail-item">
            <dt>创建时间</dt>
            <dd>{formatTime(u.created_at)}</dd>
          </div>
        </dl>
      </div>

      <div className="section-gap page-head">
        <h3 className="section-title">会话</h3>
      </div>
      {sessions.loading ? (
        <Loading />
      ) : sessions.error ? (
        <ErrorNotice error={sessions.error} onRetry={sessions.reload} />
      ) : sessions.items.length === 0 ? (
        <EmptyState title="暂无会话" />
      ) : (
        <>
          <div className="table-scroll">
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">会话 ID</th>
                  <th scope="col">认证方式（amr）</th>
                  <th scope="col">状态</th>
                  <th scope="col">创建时间</th>
                  <th scope="col">过期时间</th>
                  {canMutate ? (
                    <th scope="col">
                      <span className="visually-hidden">操作</span>
                    </th>
                  ) : null}
                </tr>
              </thead>
              <tbody>
                {sessions.items.map((s) => (
                  <tr key={s.id}>
                    <td className="mono" title={s.id}>
                      {shortenId(s.id)}
                    </td>
                    <td className="mono">{joinList(s.amr)}</td>
                    <td>
                      <StatusBadge status={s.status} />
                    </td>
                    <td className="nowrap">{formatTime(s.created_at)}</td>
                    <td className="nowrap">{formatTime(s.expires_at)}</td>
                    {canMutate ? (
                      <td className="table-actions">
                        {s.status !== 'revoked' ? (
                          <Button
                            size="sm"
                            variant="danger"
                            onClick={() => {
                              setNotice(null)
                              setRevoking(s)
                            }}
                          >
                            撤销
                          </Button>
                        ) : null}
                      </td>
                    ) : null}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {sessions.loadMoreError ? <ErrorNotice error={sessions.loadMoreError} /> : null}
          <LoadMore
            nextCursor={sessions.nextCursor}
            loading={sessions.loadingMore}
            onLoadMore={sessions.loadMore}
          />
        </>
      )}

      <ReasonDialog
        open={disableOpen}
        title="禁用用户"
        description="禁用后该用户无法登录，现有会话和刷新令牌将失效；离线访问令牌在过期前仍可能被业务服务接受。"
        confirmLabel="确认禁用"
        danger
        onClose={() => setDisableOpen(false)}
        onReconcile={user.reload}
        onSubmit={async (reason) => {
          await api.disableUser(appId, userId, reason)
          setNotice('用户已禁用。')
          user.reload()
        }}
      />

      <ReasonDialog
        open={enableOpen}
        title="恢复用户"
        description={`恢复后该用户可重新登录；禁用期间被撤销的会话不会恢复，用户需重新登录。提交将携带当前会话纪元（session_epoch ${u.session_epoch}）做并发校验，期间若用户状态被其他操作修改，服务端会拒绝并需重新核对。`}
        confirmLabel="确认恢复"
        onClose={() => setEnableOpen(false)}
        onReconcile={user.reload}
        onSubmit={async (reason) => {
          await api.enableUser(appId, userId, { reason, session_epoch: u.session_epoch })
          setNotice('用户已恢复；其旧会话保持撤销，需重新登录。')
          user.reload()
        }}
      />

      <ReasonDialog
        open={revoking !== null}
        title="撤销会话"
        description={revoking ? `会话 ${revoking.id} 将立即失效。` : undefined}
        confirmLabel="确认撤销"
        danger
        onClose={() => setRevoking(null)}
        onReconcile={sessions.reload}
        onSubmit={async (reason) => {
          if (!revoking) return
          await api.revokeUserSession(appId, userId, revoking.id, reason)
          setNotice('会话已撤销。')
          sessions.reload()
        }}
      />

      <AdminRoleDialog
        key={`role-${String(roleOpen)}-${u.admin_role ?? ''}`}
        open={roleOpen}
        appId={appId}
        userId={userId}
        currentRole={u.admin_role ?? ''}
        displayName={u.display_name || u.id}
        onClose={() => setRoleOpen(false)}
        onDone={(role) => {
          setNotice(role ? `管理员角色已设置为「${adminRoleLabel(role)}」。` : '管理员角色已撤销。')
          user.reload()
        }}
      />
    </div>
  )
}

/**
 * 固定管理员角色设置（owner 专属能力）：仅 owner/admin/auditor 三种角色，
 * 空角色即撤销；最后一名 owner 等冲突由服务端拒绝并原样展示错误。
 */
function AdminRoleDialog({
  open,
  appId,
  userId,
  currentRole,
  displayName,
  onClose,
  onDone,
}: {
  open: boolean
  appId: string
  userId: string
  currentRole: AdminRole | ''
  displayName: string
  onClose: () => void
  onDone: (role: AdminRole | '') => void
}) {
  const [role, setRole] = useState<AdminRole | ''>(currentRole)
  const [reason, setReason] = useState('')
  const [fieldError, setFieldError] = useState<string | null>(null)
  const [formError, setFormError] = useState<ApiError | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [frozen, setFrozen] = useState(false)
  const [reloading, setReloading] = useState(false)
  const submission = useRef(false)
  const releaseProtection = useProtection(open && (submitting || frozen || reloading))

  const reloadCurrent = async () => {
    if (submission.current) return
    submission.current = true
    setReloading(true)
    try {
      const current = await api.getUser(appId, userId)
      const next = current.admin_role ?? ''
      if (next !== '' && next !== 'owner' && next !== 'admin' && next !== 'auditor') {
        throw new ApiError({ status: 409, detail: '当前角色无法编辑，请联系部署方。' })
      }
      setRole(next)
      setReason('')
      setFormError(null)
      setFrozen(false)
    } catch (error) {
      setFormError(error instanceof ApiError ? error : new ApiError({ status: 0 }))
    } finally {
      submission.current = false
      setReloading(false)
    }
  }

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (submission.current || frozen) return
    const trimmed = reason.trim()
    if (!trimmed) {
      setFieldError('请填写操作原因')
      return
    }
    setFieldError(null)
    setFormError(null)
    submission.current = true
    setSubmitting(true)
    try {
      await api.setAppAdministrator(appId, userId, role, trimmed)
      releaseProtection()
      onDone(role)
      onClose()
    } catch (err) {
      // 服务端冲突（如最后一名 owner）原样展示，对话框保持打开
      const failure = err instanceof ApiError ? err : new ApiError({ status: 0 })
      if (failure.isOutcomeUnknown || failure.status === 409) setFrozen(true)
      setFormError(failure)
    } finally {
      submission.current = false
      setSubmitting(false)
    }
  }

  return (
    <Dialog
      open={open}
      title="设置管理员角色"
      onClose={onClose}
      dismissible={!submitting && !frozen && !reloading}
    >
      {frozen ? (
        <div className="notice notice-warn" role="status">
          <p>请先读取当前管理员角色，核对后再重新编辑。</p>
          <Button onClick={reloadCurrent} loading={reloading}>
            重新加载最新状态
          </Button>
        </div>
      ) : null}
      <form className="form" onSubmit={handleSubmit} noValidate aria-label="设置管理员角色">
        <fieldset className="submission-fields" disabled={submitting || frozen || reloading}>
          <p className="dialog-desc">
            用户 <strong>{displayName}</strong>{' '}
            在本应用内的固定管理员角色；角色集合固定，不支持自定义权限。
          </p>
          {formError ? <ErrorNotice error={formError} /> : null}
          <SelectField
            label="管理员角色"
            value={role}
            onChange={(e) => setRole(e.target.value as AdminRole | '')}
          >
            <option value="">无（非管理员，选择即撤销）</option>
            <option value="owner">拥有者（owner）</option>
            <option value="admin">管理员（admin）</option>
            <option value="auditor">审计员（auditor）</option>
          </SelectField>
          <TextAreaField
            label="操作原因"
            value={reason}
            onChange={(e) => {
              setReason(e.target.value)
              if (fieldError) setFieldError(null)
            }}
            error={fieldError ?? undefined}
            hint="将随请求一并记录到审计日志"
            rows={3}
            required
          />
          <div className="dialog-actions">
            <Button type="button" onClick={onClose} disabled={submitting}>
              取消
            </Button>
            <Button type="submit" variant="primary" loading={submitting}>
              保存
            </Button>
          </div>
        </fieldset>
      </form>
    </Dialog>
  )
}
