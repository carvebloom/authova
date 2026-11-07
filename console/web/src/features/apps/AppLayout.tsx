import { Link, Outlet, useParams } from 'react-router-dom'
import * as api from '../../api/endpoints'
import { useAsync } from '../../shared/hooks/useAsync'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { ForbiddenState, Loading } from '../../shared/components/States'
import { StatusBadge } from '../../shared/components/StatusBadge'
import { CopyButton } from '../../shared/components/CopyButton'
import { IconButton } from '../../shared/components/Button'
import { AppSwitcher } from '../../layout/AppSwitcher'
import { useState } from 'react'
import type { AppContextValue } from '../../app/appContext'

/**
 * App 上下文头：面包屑（应用 / 当前应用）、名称与状态、应用切换器；
 * 技术元数据（Slug / App Key / Issuer）收进可折叠区，主界面以名称/状态/动作组织。
 */
export function AppLayout() {
  const { appId = '' } = useParams<{ appId: string }>()
  const state = useAsync(`app:${appId}`, () => api.getApp(appId))
  const [switcherOpen, setSwitcherOpen] = useState(false)

  if (state.loading) return <Loading />
  if (!state.data || state.error?.isForbidden || state.error?.isUnauthorized) {
    if (state.error?.isForbidden) return <ForbiddenState error={state.error} />
    return <ErrorNotice error={state.error} onRetry={state.reload} />
  }
  const app = state.data

  return (
    <section aria-label={`应用 ${app.name}`}>
      <nav className="crumbs" aria-label="面包屑">
        <Link className="crumb" to="/apps">
          应用
        </Link>
        <span className="crumb-sep" aria-hidden="true">
          /
        </span>
        <span className="crumb-current">{app.name}</span>
      </nav>

      <div className="app-head">
        <div className="app-title-row">
          <h1 className="page-title">{app.name}</h1>
          <StatusBadge status={app.status} />
          <IconButton icon="chevron-down" label="切换应用" onClick={() => setSwitcherOpen(true)} />
        </div>
        {state.refreshing ? (
          <p className="page-sub" role="status">
            正在刷新…
          </p>
        ) : null}
        <details className="app-meta-details">
          <summary className="app-meta-summary">技术详情（Slug / App Key / Issuer）</summary>
          <dl className="app-meta">
            <div className="app-meta-item">
              <dt>Slug</dt>
              <dd className="mono">{app.slug}</dd>
            </div>
            <div className="app-meta-item">
              <dt>App Key</dt>
              <dd className="mono">
                {app.app_key} <CopyButton value={app.app_key} label="复制" />
              </dd>
            </div>
            <div className="app-meta-item">
              <dt>Issuer</dt>
              <dd className="mono">{app.issuer}</dd>
            </div>
          </dl>
        </details>
      </div>

      {state.error ? <ErrorNotice error={state.error} onRetry={state.reload} /> : null}
      <Outlet context={{ app, reloadApp: state.reload } satisfies AppContextValue} />

      <AppSwitcher open={switcherOpen} onClose={() => setSwitcherOpen(false)} />
    </section>
  )
}
