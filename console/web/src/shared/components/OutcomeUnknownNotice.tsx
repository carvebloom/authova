import type { ReactNode } from 'react'
import type { ApiError } from '../../api/client'

/**
 * 写操作结果未知（网络错误或 503 outcome_unknown）的警示：
 * 请求可能已在服务端成功，直接重试可能产生重复写入。
 * 该提示只陈述风险并引导先核对实际状态，绝不把“再次提交”伪装成安全操作。
 */
export function OutcomeUnknownNotice({
  error,
  detail,
  action,
}: {
  error: ApiError
  detail: string
  action?: ReactNode
}) {
  return (
    <div className="notice notice-warn" role="alert">
      <div className="notice-body">
        <p className="notice-title">提交结果未知：请求可能已在服务端生效</p>
        <p className="notice-detail">{detail}</p>
        {error.code || error.requestId ? (
          <p className="notice-meta">
            {error.code ? <span>错误码 {error.code}</span> : null}
            {error.requestId ? <span className="mono">request_id {error.requestId}</span> : null}
          </p>
        ) : null}
        {action}
      </div>
    </div>
  )
}
