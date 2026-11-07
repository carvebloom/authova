import { useState } from 'react'
import { ApiError } from '../../api/client'
import * as api from '../../api/endpoints'
import type { CreatedOperator, PlatformOperator } from '../../api/types'
import { useProtection } from '../../app/protectionState'
import { Button, IconButton } from '../../shared/components/Button'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { LoadMore } from '../../shared/components/LoadMore'
import { PageHeader } from '../../shared/components/PageHeader'
import { SecretPanel } from '../../shared/components/SecretPanel'
import { EmptyState, ForbiddenState, Loading } from '../../shared/components/States'
import { StatusBadge } from '../../shared/components/StatusBadge'
import { usePagedList } from '../../shared/hooks/usePagedList'
import { roleLabel, shortenId } from '../../shared/utils/format'
import { useSession } from '../session/useSession'

import { CreateOperatorDialog } from './CreateOperatorDialog'
import { EditOperatorDialog } from './EditOperatorDialog'
export function PlatformOperatorsPage() {
  const { canViewPlatformOperators, canManageOperators } = useSession()
  const list = usePagedList(
    'platform:operators',
    (cursor) => api.listPlatformOperators({ cursor, limit: 20 }),
    canViewPlatformOperators,
  )
  const [createOpen, setCreateOpen] = useState(false)
  const [editing, setEditing] = useState<PlatformOperator | null>(null)
  const [created, setCreated] = useState<CreatedOperator | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [unconfirmed, setUnconfirmed] = useState<{ email: string; error: ApiError } | null>(null)
  useProtection(unconfirmed !== null)

  if (!canViewPlatformOperators) {
    return (
      <ForbiddenState
        title="当前账号无权查看平台运营账号。"
        hint="该页面仅面向平台级运营账号开放。"
      />
    )
  }

  return (
    <section aria-label="平台运营账号">
      <PageHeader
        title="平台运营账号"
        sub="独立的平台登录账号及其固定角色；无自定义权限。"
        actions={
          <>
            <IconButton icon="refresh" label="刷新列表" onClick={list.reload} />
            {canManageOperators ? (
              <Button
                variant="primary"
                icon="plus"
                onClick={() => {
                  setNotice(null)
                  setCreateOpen(true)
                }}
              >
                创建运营账号
              </Button>
            ) : null}
          </>
        }
      />

      {notice ? (
        <div className="notice notice-ok" role="status">
          <div className="notice-body">
            <p className="notice-title">{notice}</p>
          </div>
        </div>
      ) : null}

      {created ? (
        <SecretPanel
          title="TOTP 配置密钥仅此一次显示"
          description={`新账号 ${created.operator.email} 的初始密码与下方 TOTP 密钥请通过安全渠道分别传达给使用者；关闭后控制台不再显示，也无法找回。`}
          secret={created.totp_secret}
          confirmLabel="我已安全传达，关闭"
          onClose={() => setCreated(null)}
        />
      ) : null}

      {list.loading ? (
        <Loading />
      ) : list.error ? (
        <ErrorNotice error={list.error} onRetry={list.reload} />
      ) : list.items.length === 0 ? (
        <EmptyState
          title="暂无运营账号"
          hint={
            canManageOperators
              ? '创建独立的平台运营账号，并线下传达初始凭据。'
              : '当前没有可查看的运营账号。'
          }
        />
      ) : (
        <>
          <div className="table-scroll">
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">邮箱</th>
                  <th scope="col">角色</th>
                  <th scope="col">状态</th>
                  <th scope="col">范围</th>
                  {canManageOperators ? (
                    <th scope="col">
                      <span className="visually-hidden">操作</span>
                    </th>
                  ) : null}
                </tr>
              </thead>
              <tbody>
                {list.items.map((op) => (
                  <tr key={op.id}>
                    <td>{op.email}</td>
                    <td>{roleLabel(op.role)}</td>
                    <td>
                      <StatusBadge status={op.status} />
                    </td>
                    <td>
                      {op.app_id ? (
                        <span className="mono" title={op.app_id}>
                          App 级（{shortenId(op.app_id)}）
                        </span>
                      ) : (
                        '平台级'
                      )}
                    </td>
                    {canManageOperators ? (
                      <td className="table-actions">
                        {op.status === 'quarantined' ? (
                          <span className="help">需部署管理员带外核验后恢复</span>
                        ) : (
                          <Button
                            size="sm"
                            onClick={() => {
                              setNotice(null)
                              setEditing(op)
                            }}
                          >
                            变更
                          </Button>
                        )}
                      </td>
                    ) : null}
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

      <CreateOperatorDialog
        key={createOpen ? 'open' : 'closed'}
        open={createOpen}
        previousUnknown={unconfirmed}
        onUnknown={setUnconfirmed}
        onReconciled={() => setUnconfirmed(null)}
        onClose={() => setCreateOpen(false)}
        onCloseAndRefresh={() => {
          setCreateOpen(false)
          list.reload()
        }}
        onCreated={(result) => {
          setCreateOpen(false)
          setCreated(result)
          list.reload()
        }}
      />

      <EditOperatorDialog
        key={editing ? `edit-${editing.id}` : 'closed'}
        operator={editing}
        onClose={() => setEditing(null)}
        onUpdated={() => {
          setEditing(null)
          setNotice('运营账号已更新。')
          list.reload()
        }}
      />
    </section>
  )
}
