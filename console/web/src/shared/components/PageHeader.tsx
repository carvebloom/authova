import type { ReactNode } from 'react'

/** 页头：标题 + 副说明 + 主动作区。每屏至多一个 primary 动作。 */
export function PageHeader({
  title,
  sub,
  actions,
  level = 1,
}: {
  title: ReactNode
  sub?: ReactNode
  actions?: ReactNode
  level?: 1 | 2
}) {
  return (
    <div className="page-head">
      <div>
        {level === 1 ? (
          <h1 className="page-title">{title}</h1>
        ) : (
          <h2 className="section-title">{title}</h2>
        )}
        {sub ? <p className="page-sub">{sub}</p> : null}
      </div>
      {actions ? <div className="page-actions">{actions}</div> : null}
    </div>
  )
}
