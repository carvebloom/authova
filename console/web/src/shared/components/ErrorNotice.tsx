import { ApiError } from '../../api/client'
import { Button } from './Button'

export interface ErrorNoticeProps {
  error: ApiError | Error | null
  /** 仅读取失败等可安全重试的场景传入；非幂等写操作的错误绝不传 */
  onRetry?: () => void
  announce?: boolean
  retryLabel?: string
}

/** 服务端错误展示：标题、详情、错误码与 request_id，不做任何美化改写。 */
export function ErrorNotice({
  error,
  onRetry,
  retryLabel = '重试',
  announce = true,
}: ErrorNoticeProps) {
  if (!error) return null
  const isApi = error instanceof ApiError
  const title = isApi ? error.title : '请求失败'
  const detail = isApi ? error.detail : error.message
  return (
    <div className="notice notice-error" role={announce ? 'alert' : undefined}>
      <div className="notice-body">
        <p className="notice-title">{title}</p>
        {detail && detail !== title ? <p className="notice-detail">{detail}</p> : null}
        {isApi && (error.code || error.requestId) ? (
          <p className="notice-meta">
            {error.code ? <span>错误码 {error.code}</span> : null}
            {error.requestId ? <span className="mono">request_id {error.requestId}</span> : null}
          </p>
        ) : null}
      </div>
      {onRetry ? (
        <Button size="sm" onClick={onRetry}>
          {retryLabel}
        </Button>
      ) : null}
    </div>
  )
}
