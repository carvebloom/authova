import { useProtection } from '../../app/protectionState'
import { useRef, useState, type FormEvent } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import * as api from '../../api/endpoints'
import { ApiError } from '../../api/client'
import type { App, AppInput } from '../../api/types'
import { useSession } from '../session/useSession'
import { usePagedList } from '../../shared/hooks/usePagedList'
import {
  isReplayWindowOpen,
  randomIdempotencyKey,
  samePayload,
  type PendingCreate,
} from '../../shared/utils/idempotency'
import { Button, IconButton } from '../../shared/components/Button'
import { Dialog } from '../../shared/components/Dialog'
import { TextField } from '../../shared/components/Field'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { EmptyState, ForbiddenState, Loading } from '../../shared/components/States'
import { StatusBadge } from '../../shared/components/StatusBadge'
import { LoadMore } from '../../shared/components/LoadMore'
import { PageHeader } from '../../shared/components/PageHeader'
import { SLUG_RE, formatTime } from '../../shared/utils/format'

export function AppsPage() {
  const { canCreateApps } = useSession()
  const navigate = useNavigate()
  const list = usePagedList('apps', (cursor) => api.listApps({ cursor, limit: 20 }))
  const [createOpen, setCreateOpen] = useState(false)
  // 结果未知的创建请求：键与内容保留在内存中，核对/放弃前不允许当作新创建
  const [pending, setPending] = useState<PendingCreate<AppInput> | null>(null)
  const releasePending = useProtection(pending !== null)

  const openCreate = () => setCreateOpen(true)

  return (
    <section aria-label="应用列表">
      <PageHeader
        title="应用"
        sub="管理接入 Authova 的全部应用。"
        actions={
          <>
            <IconButton icon="refresh" label="刷新列表" onClick={list.reload} />
            {canCreateApps ? (
              <Button variant="primary" icon="plus" onClick={openCreate}>
                {pending ? '处理未确认的创建' : '创建应用'}
              </Button>
            ) : null}
          </>
        }
      />

      {pending ? (
        <div className="notice notice-warn" role="alert">
          <div className="notice-body">
            <p className="notice-title">
              未确认的创建请求：应用「{pending.payload.name}」（{pending.payload.slug}）
            </p>
            <p className="notice-detail">
              提交于 {formatTime(new Date(pending.at).toISOString())}
              ，结果未知，该应用可能已创建成功。
              窗口期内可打开创建对话框用原幂等键重试（内容须保持一致）；超过 10
              分钟请先在列表中核对实际状态。 核对确认未生效前，不会把同一表单当作新的创建。
            </p>
          </div>
          <div className="dialog-actions">
            <Button size="sm" onClick={list.reload}>
              刷新列表
            </Button>
            <Button size="sm" variant="danger" onClick={() => setPending(null)}>
              已核对，确认未生效
            </Button>
          </div>
        </div>
      ) : null}

      {list.loading ? (
        <Loading />
      ) : list.error && list.items.length === 0 ? (
        list.error.isForbidden ? (
          <ForbiddenState error={list.error} />
        ) : (
          <ErrorNotice error={list.error} onRetry={list.reload} />
        )
      ) : list.items.length === 0 ? (
        <EmptyState
          title="暂无应用"
          hint={
            canCreateApps
              ? '创建第一个应用，开始管理用户、客户端与连接。'
              : '当前账号下还没有可查看的应用。'
          }
          action={
            canCreateApps ? (
              <Button variant="primary" onClick={openCreate}>
                创建应用
              </Button>
            ) : null
          }
        />
      ) : (
        <>
          {list.error ? <ErrorNotice error={list.error} onRetry={list.reload} /> : null}
          <div className="table-scroll">
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">名称</th>
                  <th scope="col">Slug</th>
                  <th scope="col">状态</th>
                  <th scope="col">创建时间</th>
                  <th scope="col">
                    <span className="visually-hidden">操作</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {list.items.map((app) => (
                  <tr key={app.id}>
                    <td>
                      <Link className="table-link" to={`/apps/${app.id}`}>
                        {app.name}
                      </Link>
                    </td>
                    <td className="mono">{app.slug}</td>
                    <td>
                      <StatusBadge status={app.status} />
                    </td>
                    <td className="nowrap">{formatTime(app.created_at)}</td>
                    <td className="table-actions">
                      <Link className="btn btn-ghost btn-sm" to={`/apps/${app.id}`}>
                        管理
                      </Link>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {list.loadMoreError ? <ErrorNotice error={list.loadMoreError} /> : null}
          <LoadMore
            nextCursor={list.nextCursor}
            loading={list.loadingMore}
            onLoadMore={list.loadMore}
          />
        </>
      )}

      <CreateAppDialog
        key={createOpen ? 'open' : 'closed'}
        open={createOpen}
        pending={pending}
        onClose={() => setCreateOpen(false)}
        onUnknown={(next) => setPending(next)}
        onCreated={(app) => {
          releasePending()
          setPending(null)
          setCreateOpen(false)
          list.reload()
          navigate(`/apps/${app.id}`)
        }}
      />
    </section>
  )
}

function CreateAppDialog({
  open,
  pending,
  onClose,
  onUnknown,
  onCreated,
}: {
  open: boolean
  pending: PendingCreate<AppInput> | null
  onClose: () => void
  onUnknown: (pending: PendingCreate<AppInput>) => void
  onCreated: (app: App) => void
}) {
  const [name, setName] = useState(pending?.payload.name ?? '')
  const [slug, setSlug] = useState(pending?.payload.slug ?? '')
  const [errors, setErrors] = useState<{ name?: string; slug?: string }>({})
  const [formError, setFormError] = useState<ApiError | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const submission = useRef(false)
  const releaseProtection = useProtection(submitting)

  const currentPayload: AppInput = { name: name.trim(), slug: slug.trim() }
  /** 内容偏离未确认请求：原请求可能已生效，修改后提交即是新请求 */
  const diverged = pending !== null && !samePayload(currentPayload, pending.payload)
  const windowOpen = pending !== null && isReplayWindowOpen(pending.at)
  const blocked = pending !== null && (diverged || !windowOpen)

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (submission.current) return
    if (
      blocked ||
      (pending !== null &&
        (!isReplayWindowOpen(pending.at) || !samePayload(currentPayload, pending.payload)))
    ) {
      setFormError(
        new ApiError({
          status: 409,
          detail: '未确认请求已超出安全重放窗口或内容已变化，请先核对列表。',
        }),
      )
      return
    }
    const next: { name?: string; slug?: string } = {}
    if (!currentPayload.name) next.name = '请输入应用名称'
    if (!SLUG_RE.test(currentPayload.slug))
      next.slug = 'Slug 仅支持小写字母、数字与连字符，且以字母或数字开头'
    setErrors(next)
    setFormError(null)
    if (Object.keys(next).length > 0) return
    submission.current = true
    setSubmitting(true)
    // 同一未确认请求复用原键；无未确认请求才生成新键
    const key = pending?.key ?? randomIdempotencyKey()
    try {
      const app = await api.createApp(currentPayload, key)
      releaseProtection()
      onCreated(app)
    } catch (err) {
      const apiErr =
        err instanceof ApiError ? err : new ApiError({ status: 0, detail: String(err) })
      if (apiErr.isOutcomeUnknown) {
        // 结果未知：记录键与内容（保留首次提交时间），此后只能用原键重试
        onUnknown({ payload: currentPayload, key, at: pending?.at ?? Date.now() })
      }
      setFormError(apiErr)
    } finally {
      releaseProtection()
      submission.current = false
      setSubmitting(false)
    }
  }

  return (
    <Dialog
      open={open}
      title={pending ? '处理未确认的创建' : '创建应用'}
      onClose={onClose}
      dismissible={!submitting}
    >
      <form className="form" onSubmit={handleSubmit} noValidate>
        <fieldset className="submission-fields" disabled={submitting}>
          {pending ? (
            <div className="notice notice-warn" role="alert">
              <div className="notice-body">
                <p className="notice-title">先前的创建请求结果未知，可能已生效</p>
                <p className="notice-detail">
                  提交于 {formatTime(new Date(pending.at).toISOString())}；应用「
                  {pending.payload.name}」（{pending.payload.slug}）可能已创建成功。
                  {diverged
                    ? '当前内容已修改：继续提交将是全新请求，可能造成重复。请先关闭对话框，在列表中核对原请求是否生效；确认未生效后在页面提示中放弃该记录，再重新填写。'
                    : windowOpen
                      ? '内容与原请求一致：可用原幂等键安全重试，服务端会按键去重。'
                      : '已超过 10 分钟幂等回放窗口，不能再按原键安全重试；请关闭对话框，刷新并核对列表实际状态后再处理。'}
                </p>
              </div>
            </div>
          ) : null}
          {formError ? <ErrorNotice error={formError} /> : null}
          <TextField
            label="应用名称"
            value={name}
            onChange={(e) => setName(e.target.value)}
            error={errors.name}
            required
            autoFocus
          />
          <TextField
            label="Slug"
            value={slug}
            onChange={(e) => setSlug(e.target.value)}
            error={errors.slug}
            hint="用于 issuer 路径的唯一标识，创建后不可修改"
            required
          />
          <div className="dialog-actions">
            <Button type="button" onClick={onClose} disabled={submitting}>
              取消
            </Button>
            <Button type="submit" variant="primary" loading={submitting} disabled={blocked}>
              {pending ? '重试原请求' : '创建'}
            </Button>
          </div>
        </fieldset>
      </form>
    </Dialog>
  )
}
