import { useState } from 'react'
import { useProtection } from '../../app/protectionState'
import * as api from '../../api/endpoints'
import type { Client, ClientInput, ClientSecretRotated, CreatedClient } from '../../api/types'
import { useAppCapabilities } from '../../app/appContext'
import { Button, IconButton } from '../../shared/components/Button'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { LoadMore } from '../../shared/components/LoadMore'
import { PageHeader } from '../../shared/components/PageHeader'
import { ReasonDialog } from '../../shared/components/ReasonDialog'
import { SecretPanel } from '../../shared/components/SecretPanel'
import { EmptyState, Loading } from '../../shared/components/States'
import { StatusBadge } from '../../shared/components/StatusBadge'
import { usePagedList } from '../../shared/hooks/usePagedList'
import { formatTime, formatUnixSeconds, shortenId } from '../../shared/utils/format'
import { type PendingCreate } from '../../shared/utils/idempotency'

import { ClientFormDialog } from './ClientFormDialog'
import { ClientSecretDialog } from './ClientSecretDialog'
import { previousSecretLive } from './formModel'

export function ClientsTab() {
  const { app, canMutate } = useAppCapabilities()
  const list = usePagedList(`clients:${app.id}`, (cursor) =>
    api.listClients(app.id, { cursor, limit: 20 }),
  )
  const [createOpen, setCreateOpen] = useState(false)
  const [editing, setEditing] = useState<Client | null>(null)
  const [secret, setSecret] = useState<CreatedClient | null>(null)
  /** 轮换成功后的一次性密钥（仅内存，SecretPanel 显式确认后清除） */
  const [rotated, setRotated] = useState<ClientSecretRotated | null>(null)
  const [rotating, setRotating] = useState<Client | null>(null)
  /** 待撤销旧密钥的 client_id；对象本体始终从列表实时取，避免核对后用过期的 config_version */
  const [revokingId, setRevokingId] = useState<string | null>(null)
  // 结果未知的创建请求：键与内容保留在内存中，核对/放弃前不允许当作新创建
  const [pending, setPending] = useState<PendingCreate<ClientInput> | null>(null)
  useProtection(pending !== null)
  const revoking = revokingId ? (list.items.find((c) => c.client_id === revokingId) ?? null) : null

  return (
    <div className="tab-panel">
      <PageHeader
        level={2}
        title="客户端"
        sub="OAuth / OIDC 客户端及其回调、作用域与授权类型配置。"
        actions={
          <>
            <IconButton icon="refresh" label="刷新列表" onClick={list.reload} />
            {canMutate ? (
              <Button variant="primary" icon="plus" onClick={() => setCreateOpen(true)}>
                {pending ? '处理未确认的创建' : '创建客户端'}
              </Button>
            ) : null}
          </>
        }
      />

      {pending ? (
        <div className="notice notice-warn" role="alert">
          <div className="notice-body">
            <p className="notice-title">未确认的创建请求：客户端「{pending.payload.name}」</p>
            <p className="notice-detail">
              提交于 {formatTime(new Date(pending.at).toISOString())}
              ，结果未知，该客户端可能已创建成功。 机密客户端的密钥仅在创建后 10
              分钟内随幂等回放再次返回；窗口期内可打开创建对话框用原幂等键重试（内容须保持一致），
              超时请先在列表中核对实际状态。核对确认未生效前，不会把同一表单当作新的创建。
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

      {secret?.client_secret ? (
        <SecretPanel
          title="客户端密钥仅此一次显示"
          description="请立即复制并妥善保存。关闭此提示后将无法再次查看，只能重新创建客户端。"
          secret={secret.client_secret}
          confirmLabel="我已保存，关闭"
          onClose={() => setSecret(null)}
        />
      ) : null}

      {rotated ? (
        <SecretPanel
          title="新的客户端密钥仅此一次显示"
          description="请立即复制并妥善保存。该密钥无法通过任何接口再次读取；如丢失，只能基于当前配置版本显式发起一次新的轮换。"
          secret={rotated.client_secret}
          confirmLabel="我已保存，关闭"
          onClose={() => setRotated(null)}
        >
          <p className="notice-detail">
            {rotated.previous_secret_expires_at > 0
              ? `旧密钥宽限期至 ${formatUnixSeconds(rotated.previous_secret_expires_at)}；到期或显式撤销前旧密钥继续可用。`
              : '宽限期为 0：旧密钥已立即失效。'}
          </p>
        </SecretPanel>
      ) : null}

      {list.loading ? (
        <Loading />
      ) : list.error && list.items.length === 0 ? (
        <ErrorNotice error={list.error} onRetry={list.reload} />
      ) : list.items.length === 0 ? (
        <EmptyState
          title="暂无客户端"
          hint={canMutate ? '创建一个客户端以接入登录流程。' : '该应用还没有配置客户端。'}
        />
      ) : (
        <>
          {list.error ? <ErrorNotice error={list.error} onRetry={list.reload} /> : null}
          <div className="table-scroll">
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">Client ID</th>
                  <th scope="col">名称</th>
                  <th scope="col">类型</th>
                  <th scope="col">状态</th>
                  <th scope="col">创建时间</th>
                  {canMutate ? (
                    <th scope="col">
                      <span className="visually-hidden">操作</span>
                    </th>
                  ) : null}
                </tr>
              </thead>
              <tbody>
                {list.items.map((client) => (
                  <tr key={client.id}>
                    <td className="mono" title={client.client_id}>
                      {shortenId(client.client_id)}
                    </td>
                    <td>
                      {client.name}
                      {client.system ? (
                        <span
                          className="badge badge-muted sys-badge"
                          title="系统内置客户端，不可编辑"
                        >
                          系统
                        </span>
                      ) : null}
                    </td>
                    <td>
                      {client.kind === 'public'
                        ? '公开'
                        : client.kind === 'confidential'
                          ? '机密'
                          : client.kind}
                    </td>
                    <td>
                      <StatusBadge status={client.status} />
                      {client.kind === 'confidential' &&
                      previousSecretLive(client.previous_secret_expires_at) ? (
                        <span
                          className="badge badge-warn sys-badge"
                          title={`旧密钥宽限期至 ${formatUnixSeconds(client.previous_secret_expires_at)}，期间服务端拒绝新的轮换`}
                        >
                          旧密钥宽限中
                        </span>
                      ) : null}
                    </td>
                    <td className="nowrap">{formatTime(client.created_at)}</td>
                    {canMutate ? (
                      <td className="table-actions">
                        {client.system ? null : (
                          <>
                            <Button size="sm" onClick={() => setEditing(client)}>
                              编辑
                            </Button>
                            {client.kind === 'confidential' ? (
                              <>
                                <Button size="sm" onClick={() => setRotating(client)}>
                                  轮换密钥
                                </Button>
                                {previousSecretLive(client.previous_secret_expires_at) ? (
                                  <Button
                                    size="sm"
                                    variant="danger"
                                    onClick={() => setRevokingId(client.client_id)}
                                  >
                                    撤销旧密钥
                                  </Button>
                                ) : null}
                              </>
                            ) : null}
                          </>
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

      <ClientFormDialog
        key={createOpen ? 'create' : editing ? `edit-${editing.id}` : 'closed'}
        open={createOpen || editing !== null}
        appId={app.id}
        client={editing}
        pending={pending}
        onClose={() => {
          setCreateOpen(false)
          setEditing(null)
        }}
        onUnknown={(next) => setPending(next)}
        onCreated={(created) => {
          setPending(null)
          setCreateOpen(false)
          if (created.client_secret) setSecret(created)
          list.reload()
        }}
        onUpdated={() => {
          setEditing(null)
          list.reload()
        }}
      />

      {rotating ? (
        <ClientSecretDialog
          appId={app.id}
          client={rotating}
          onClose={() => setRotating(null)}
          onChanged={() => {
            list.reload()
          }}
          onRotated={(result) => {
            setRotating(null)
            setRotated(result)
          }}
        />
      ) : null}

      <ReasonDialog
        open={revoking !== null}
        title="撤销旧客户端密钥"
        description={
          revoking
            ? `客户端「${revoking.name}」仍在宽限期内的旧密钥将立即失效，仍在使用旧密钥的集成会立刻中断；当前密钥不受影响。（宽限期至 ${formatUnixSeconds(revoking.previous_secret_expires_at)}）`
            : undefined
        }
        confirmLabel="确认撤销旧密钥"
        danger
        onClose={() => setRevokingId(null)}
        onReconcile={list.reload}
        onSubmit={async (reason) => {
          if (!revoking) return
          await api.revokePreviousClientSecret(app.id, revoking.client_id, {
            config_version: revoking.config_version,
            reason,
          })
          setRevokingId(null)
          list.reload()
        }}
      />
    </div>
  )
}
