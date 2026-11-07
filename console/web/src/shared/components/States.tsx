import type { ReactNode } from 'react'
import type { ApiError } from '../../api/client'
import { ErrorNotice } from './ErrorNotice'
import { Icon } from './Icon'

export function EmptyState({
  title,
  hint,
  action,
}: {
  title: string
  hint?: string
  action?: ReactNode
}) {
  return (
    <div className="empty">
      <p className="empty-title">{title}</p>
      {hint ? <p className="empty-hint">{hint}</p> : null}
      {action ? <div className="empty-action">{action}</div> : null}
    </div>
  )
}

export function Loading({ label = '加载中…' }: { label?: string }) {
  return (
    <div className="loading" role="status">
      <span className="spinner" aria-hidden="true" />
      <span>{label}</span>
    </div>
  )
}

export function FullScreenLoading() {
  return (
    <div className="fullscreen-center">
      <Loading label="正在恢复会话…" />
    </div>
  )
}

/** 会话引导失败（503 依赖故障 / 网络错误）：不清除本地登录，仅提供手动重试。 */
export function FullScreenError({
  error,
  onRetry,
}: {
  error: ApiError | null
  onRetry: () => void
}) {
  return (
    <div className="fullscreen-center">
      <div className="fullscreen-panel">
        <ErrorNotice error={error} onRetry={onRetry} />
        <a className="btn btn-ghost" href="#/login">
          重新登录
        </a>
        <p className="field-hint">服务暂时不可用；本地登录状态已保留，恢复后可直接重试。</p>
      </div>
    </div>
  )
}

/** 403 专用态：明确「无权限」语义，附服务端 detail 与 request_id。 */
export function ForbiddenState({
  error,
  title = '没有权限查看此内容',
  hint,
}: {
  error?: ApiError | null
  title?: string
  hint?: string
}) {
  return (
    <div className="empty" role="status">
      <p className="empty-icon">
        <Icon name="lock" size={20} />
      </p>
      <p className="empty-title">{title}</p>
      <p className="empty-hint">
        {hint ?? '当前账号的角色不包含此操作的权限；权限以服务端校验为准。'}
      </p>
      {error?.detail ? <p className="empty-hint">{error.detail}</p> : null}
      {error?.requestId ? <p className="empty-hint mono">request_id {error.requestId}</p> : null}
    </div>
  )
}
