import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import * as api from '../api/endpoints'
import { usePagedList } from '../shared/hooks/usePagedList'
import { Dialog } from '../shared/components/Dialog'
import { ErrorNotice } from '../shared/components/ErrorNotice'
import { EmptyState, Loading } from '../shared/components/States'
import { LoadMore } from '../shared/components/LoadMore'
import { StatusBadge } from '../shared/components/StatusBadge'

/**
 * 应用选择器：游标分页加载全部应用（契约无服务端过滤，见 docs/console-architecture.md
 * 集成需求 1），输入仅筛选「已加载」条目并在界面如实说明；未找到时可继续加载更多。
 */
export function AppSwitcher({ open, onClose }: { open: boolean; onClose: () => void }) {
  const navigate = useNavigate()
  const [filter, setFilter] = useState('')
  const list = usePagedList('app-switcher', (cursor) => api.listApps({ cursor, limit: 20 }), open)

  const visible = useMemo(() => {
    const f = filter.trim().toLowerCase()
    if (!f) return list.items
    return list.items.filter(
      (a) => a.name.toLowerCase().includes(f) || a.slug.toLowerCase().includes(f),
    )
  }, [list.items, filter])

  const pick = (appId: string) => {
    onClose()
    navigate(`/apps/${appId}`)
  }

  return (
    <Dialog open={open} title="切换应用" onClose={onClose}>
      <div className="picker-search">
        <label className="visually-hidden" htmlFor="app-switcher-filter">
          筛选已加载的应用
        </label>
        <input
          id="app-switcher-filter"
          className="input"
          type="search"
          placeholder="按名称或 Slug 筛选已加载条目"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          autoFocus
        />
      </div>
      {list.loading ? (
        <Loading label="正在加载应用…" />
      ) : list.error ? (
        <ErrorNotice error={list.error} onRetry={list.reload} />
      ) : visible.length === 0 ? (
        <EmptyState
          title={filter ? '已加载条目中无匹配' : '暂无应用'}
          hint={
            filter && list.nextCursor
              ? '筛选只作用于已加载部分；可点击下方「加载更多」继续查找。'
              : undefined
          }
        />
      ) : (
        <ul className="picker-list">
          {visible.map((app) => (
            <li key={app.id}>
              <button type="button" className="picker-item" onClick={() => pick(app.id)}>
                <span className="picker-item-name">{app.name}</span>
                <span className="picker-item-slug mono">{app.slug}</span>
                <StatusBadge status={app.status} />
              </button>
            </li>
          ))}
        </ul>
      )}
      {list.loadMoreError ? <ErrorNotice error={list.loadMoreError} /> : null}
      {list.nextCursor ? (
        <div className="picker-foot">
          <LoadMore
            nextCursor={list.nextCursor}
            loading={list.loadingMore}
            onLoadMore={list.loadMore}
          />
        </div>
      ) : null}
    </Dialog>
  )
}
