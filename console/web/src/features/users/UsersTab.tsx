import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import * as api from '../../api/endpoints'
import { usePagedList } from '../../shared/hooks/usePagedList'
import { useAppContext } from '../../app/appContext'
import { Button, IconButton } from '../../shared/components/Button'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { EmptyState, Loading } from '../../shared/components/States'
import { StatusBadge } from '../../shared/components/StatusBadge'
import { LoadMore } from '../../shared/components/LoadMore'
import { PageHeader } from '../../shared/components/PageHeader'
import { E164_RE, EMAIL_RE, formatTime } from '../../shared/utils/format'

/**
 * 用户查询为服务端精确匹配（完整邮箱 / E.164 手机号），不做模糊搜索；
 * 禁止全量加载后客户端过滤。格式不符的输入在本地拦截并说明原因。
 */
export function UsersTab() {
  const { app } = useAppContext()
  const [input, setInput] = useState('')
  const [q, setQ] = useState('')
  const [inputError, setInputError] = useState<string | null>(null)
  const list = usePagedList(`users:${app.id}:${q}`, (cursor) =>
    api.listUsers(app.id, { cursor, limit: 20, q: q || undefined }),
  )

  const handleSearch = (e: FormEvent) => {
    e.preventDefault()
    const trimmed = input.trim()
    if (trimmed && !EMAIL_RE.test(trimmed) && !E164_RE.test(trimmed)) {
      setInputError('请输入完整邮箱地址，或 E.164 格式手机号（如 +8613800138000）')
      return
    }
    setInputError(null)
    setQ(trimmed)
  }

  return (
    <div className="tab-panel">
      <PageHeader
        level={2}
        title="用户"
        sub="按完整邮箱或 E.164 手机号精确查询（服务端匹配）；邮箱与手机号已脱敏展示。"
        actions={<IconButton icon="refresh" label="刷新列表" onClick={list.reload} />}
      />

      <form className="search-bar" onSubmit={handleSearch} role="search">
        <label className="visually-hidden" htmlFor="user-search">
          搜索用户
        </label>
        <input
          id="user-search"
          className="input"
          type="search"
          placeholder="完整邮箱或 +8613800138000"
          value={input}
          onChange={(e) => {
            setInput(e.target.value)
            if (inputError) setInputError(null)
          }}
          aria-invalid={inputError ? true : undefined}
          aria-describedby={inputError ? 'user-search-error' : undefined}
        />
        <Button type="submit" variant="primary" icon="search">
          搜索
        </Button>
        {q ? (
          <Button
            type="button"
            onClick={() => {
              setInput('')
              setInputError(null)
              setQ('')
            }}
          >
            清除
          </Button>
        ) : null}
      </form>
      {inputError ? (
        <p className="field-error-text" id="user-search-error" role="alert">
          {inputError}
        </p>
      ) : null}

      {list.loading ? (
        <Loading />
      ) : list.error ? (
        <ErrorNotice error={list.error} onRetry={list.reload} />
      ) : list.items.length === 0 ? (
        <EmptyState
          title={q ? '未找到匹配的用户' : '暂无用户'}
          hint={
            q
              ? `没有与“${q}”精确匹配的用户；查询为完整邮箱或 E.164 手机号的精确匹配。`
              : '该应用还没有注册用户。'
          }
        />
      ) : (
        <>
          <div className="table-scroll">
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">显示名</th>
                  <th scope="col">邮箱</th>
                  <th scope="col">手机号</th>
                  <th scope="col">状态</th>
                  <th scope="col">创建时间</th>
                  <th scope="col">
                    <span className="visually-hidden">操作</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {list.items.map((user) => (
                  <tr key={user.id}>
                    <td>{user.display_name || '—'}</td>
                    <td>{user.email || '—'}</td>
                    <td>{user.phone || '—'}</td>
                    <td>
                      <StatusBadge status={user.status} />
                    </td>
                    <td className="nowrap">{formatTime(user.created_at)}</td>
                    <td className="table-actions">
                      <Link
                        className="btn btn-ghost btn-sm"
                        to={`/apps/${app.id}/users/${user.id}`}
                      >
                        详情
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
    </div>
  )
}
