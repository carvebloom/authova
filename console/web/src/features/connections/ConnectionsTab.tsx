import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useProtection } from '../../app/protectionState'
import * as api from '../../api/endpoints'
import { useAsync } from '../../shared/hooks/useAsync'
import { useAppCapabilities } from '../../app/appContext'
import { Button, IconButton } from '../../shared/components/Button'
import { Dialog } from '../../shared/components/Dialog'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { EmptyState, Loading } from '../../shared/components/States'
import { StatusBadge } from '../../shared/components/StatusBadge'
import { PageHeader } from '../../shared/components/PageHeader'
import { formatTime } from '../../shared/utils/format'
import { providerLabel } from '../../shared/utils/providers'
import type { Connection } from '../../api/types'
import { CreateConnectionForm, type PendingMutation } from './ConnectionCreateForm'
import { EditConnectionForm } from './ConnectionEditForm'

export function ConnectionsTab() {
  const { app, canMutate } = useAppCapabilities()
  // 连接列表由后端限定上限（100），不做分页加载
  const list = useAsync(`connections:${app.id}`, () => api.listConnections(app.id, { limit: 100 }))
  // 应用级第三方登录总开关（只读读取）：连接已启用 ∧ 总开关开启 才会出现在终端用户登录页
  const gate = useAsync(`connections:authentication-config:${app.id}`, () =>
    api.getAuthenticationConfig(app.id),
  )
  const [createOpen, setCreateOpen] = useState(false)
  const [createBusy, setCreateBusy] = useState(false)
  const [editBusy, setEditBusy] = useState(false)
  const [editing, setEditing] = useState<Connection | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [pendingCreate, setPendingCreate] = useState<PendingMutation | null>(null)
  const [pendingEdit, setPendingEdit] = useState<PendingMutation | null>(null)
  useProtection(createBusy || editBusy || pendingCreate !== null || pendingEdit !== null)

  const connections = list.data?.items ?? []
  // 刷新失败时 useAsync 保留旧数据：旧快照不得作为「当前生效」依据，仅标注为可能过期
  const listStale = list.error !== null && list.data !== null
  const listFresh =
    !list.loading && !list.refreshing && list.error === null && Array.isArray(list.data?.items)
  // 总开关同理：仅采纳最近一次成功读取（加载中/刷新中/失败/形状不符一律按未知处理，不编造状态）
  const gateFresh = !gate.loading && !gate.refreshing && gate.error === null
  const gateValue: unknown = gateFresh ? gate.data?.policy?.federation_enabled : undefined
  const federationGate: boolean | null = typeof gateValue === 'boolean' ? gateValue : null
  // 「登录页生效」派生态需要列表与总开关均为最新成功读取
  const effectiveKnown = federationGate !== null && listFresh
  // 应用已暂停：登录页与令牌签发不可用，任何连接都不可能出现在登录页（配置仍可查看/编辑）
  const appSuspended = app.status === 'suspended'
  const openCreate = () => {
    setNotice(null)
    setCreateOpen(true)
  }

  const listFailed = list.error !== null && list.data === null
  const showEmpty = !list.loading && !listFailed && connections.length === 0

  const staleNotice = listStale ? (
    <div className="notice notice-warn" role="alert">
      <div className="notice-body">
        <p className="notice-title">连接列表刷新失败</p>
        <p className="notice-detail">
          以下为最近一次成功读取的快照，可能已过期；「登录页生效」状态已暂不显示。请重试刷新以核对实际状态。
        </p>
      </div>
      <Button size="sm" onClick={list.reload}>
        重试
      </Button>
    </div>
  ) : null

  return (
    <div className="tab-panel">
      <PageHeader
        level={2}
        title="连接"
        sub="上游身份源：标准 OIDC、GitHub、微信、QQ、支付宝网页授权。连接已启用且应用级「第三方登录」开启后，才会出现在用户登录页；读取响应不包含密钥。"
        actions={
          <>
            <IconButton icon="refresh" label="刷新列表" onClick={list.reload} />
            {canMutate && !showEmpty ? (
              <Button
                variant="primary"
                icon="plus"
                disabled={createBusy || editBusy}
                onClick={openCreate}
              >
                {pendingCreate ? '处理未确认的创建' : '创建连接'}
              </Button>
            ) : null}
          </>
        }
      />

      {appSuspended ? (
        <div className="notice notice-warn" role="status">
          <div className="notice-body">
            <p className="notice-title">应用已暂停：登录页与令牌签发不可用</p>
            <p className="notice-detail">
              在应用恢复前，任何连接都不会出现在终端用户登录页；连接配置仍可查看与编辑。
            </p>
          </div>
        </div>
      ) : null}

      {federationGate === false && listFresh && connections.some((c) => c.status === 'active') ? (
        <div className="notice notice-info" role="status">
          <div className="notice-body">
            <p className="notice-title">第三方登录总开关已关闭，已启用的连接暂不出现在登录页</p>
            <p className="notice-detail">
              连接的启用状态与应用级「第三方登录」开关共同决定生效：两者同时满足，终端用户才能使用该连接登录。
            </p>
          </div>
          <Link className="btn btn-sm" to={`/apps/${app.id}/settings`}>
            前往配置
          </Link>
        </div>
      ) : null}

      {gate.error ? (
        <p className="field-hint">
          应用登录策略暂时无法读取，连接的「登录页生效」状态暂不显示；以「配置 →
          登录策略」与服务端实际状态为准。
        </p>
      ) : null}

      {pendingCreate ? (
        <div className="notice notice-warn" role="alert">
          <div className="notice-body">
            <p className="notice-title">未确认的创建请求：连接「{pendingCreate.name}」</p>
            <p className="notice-detail">
              提交于 {formatTime(new Date(pendingCreate.at).toISOString())}
              ，结果未知，该连接可能已创建成功。创建连接不支持幂等重试：请先刷新列表核对实际状态；
              核对并确认未生效前，创建表单保持冻结，避免重复配置。
            </p>
          </div>
          <div className="dialog-actions">
            <Button size="sm" onClick={list.reload}>
              刷新列表
            </Button>
            <Button size="sm" variant="danger" onClick={() => setPendingCreate(null)}>
              已核对，确认未生效
            </Button>
          </div>
        </div>
      ) : null}

      {pendingEdit ? (
        <div className="notice notice-warn" role="alert">
          <div className="notice-body">
            <p className="notice-title">未确认的更新请求：连接「{pendingEdit.name}」</p>
            <p className="notice-detail">
              提交于 {formatTime(new Date(pendingEdit.at).toISOString())}
              ，结果未知，该更新可能已生效。请刷新列表核对，或重新打开编辑对话框用「重新加载最新配置」
              读回实际状态；核对前该连接的更新表单保持冻结。
            </p>
          </div>
          <div className="dialog-actions">
            <Button size="sm" onClick={list.reload}>
              刷新列表
            </Button>
            <Button size="sm" variant="danger" onClick={() => setPendingEdit(null)}>
              已核对，解除冻结
            </Button>
          </div>
        </div>
      ) : null}

      {notice ? (
        <div className="notice notice-ok" role="status">
          <div className="notice-body">
            <p className="notice-title">{notice}</p>
          </div>
        </div>
      ) : null}

      {staleNotice}

      {list.loading ? (
        <Loading />
      ) : listFailed ? (
        <ErrorNotice error={list.error} onRetry={list.reload} />
      ) : showEmpty ? (
        <EmptyState
          title="暂无连接"
          hint={
            canMutate
              ? '创建 OIDC、GitHub、微信、QQ 或支付宝网页授权连接，接入外部身份源。'
              : '该应用还没有配置连接。'
          }
          action={
            canMutate ? (
              <Button
                variant="primary"
                icon="plus"
                disabled={createBusy || editBusy}
                onClick={openCreate}
              >
                {pendingCreate ? '处理未确认的创建' : '创建连接'}
              </Button>
            ) : undefined
          }
        />
      ) : (
        <div
          className="table-scroll"
          role="region"
          aria-label="登录连接列表，可横向滚动"
          tabIndex={0}
        >
          <table className="table table-connections">
            <thead>
              <tr>
                <th scope="col">名称</th>
                <th scope="col">类型</th>
                <th scope="col">Issuer</th>
                <th scope="col">Client ID</th>
                <th scope="col">状态</th>
                {canMutate ? (
                  <th scope="col">
                    <span className="visually-hidden">操作</span>
                  </th>
                ) : null}
              </tr>
            </thead>
            <tbody>
              {connections.map((conn) => (
                <tr key={conn.id}>
                  <td>{conn.name}</td>
                  <td>{providerLabel(conn.provider_type)}</td>
                  <td className="mono" title={conn.issuer}>
                    {conn.issuer}
                  </td>
                  <td className="mono" title={conn.client_id}>
                    {conn.client_id}
                  </td>
                  <td>
                    <StatusBadge status={conn.status} />
                    {conn.status === 'active' && effectiveKnown ? (
                      <p className="cell-sub">
                        {appSuspended
                          ? '未生效：应用已暂停'
                          : federationGate
                            ? '登录页可见'
                            : '未生效：第三方登录总开关已关闭'}
                      </p>
                    ) : null}
                  </td>
                  {canMutate ? (
                    <td className="table-actions">
                      <Button
                        size="sm"
                        disabled={editBusy || (pendingEdit !== null && pendingEdit.id !== conn.id)}
                        onClick={() => {
                          setNotice(null)
                          setEditing(conn)
                        }}
                      >
                        编辑
                      </Button>
                    </td>
                  ) : null}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <Dialog
        open={createOpen}
        title="创建连接"
        onClose={() => {
          if (!createBusy) setCreateOpen(false)
        }}
        dismissible={!createBusy}
      >
        <CreateConnectionForm
          appId={app.id}
          blocked={pendingCreate !== null}
          onBusy={setCreateBusy}
          onClose={() => {
            if (!createBusy) setCreateOpen(false)
          }}
          onUnknown={(pending) => setPendingCreate(pending)}
          onCreated={() => {
            setCreateOpen(false)
            setPendingCreate(null)
            setNotice('连接已创建；密钥不会在后续读取中返回。')
            list.reload()
          }}
        />
      </Dialog>

      {editing ? (
        <Dialog
          open
          title="编辑连接"
          onClose={() => {
            if (!editBusy) setEditing(null)
          }}
          dismissible={!editBusy}
        >
          <EditConnectionForm
            appId={app.id}
            connection={editing}
            pending={pendingEdit}
            onBusy={setEditBusy}
            onClose={() => {
              if (!editBusy) setEditing(null)
            }}
            onUnknown={(pending) => setPendingEdit(pending)}
            onReconciled={() => setPendingEdit(null)}
            onUpdated={(updated) => {
              setEditing(null)
              setPendingEdit((pending) => (pending?.id === updated.id ? null : pending))
              setNotice(`连接「${updated.name}」已更新（配置版本 ${updated.config_version}）。`)
              list.reload()
            }}
          />
        </Dialog>
      ) : null}
    </div>
  )
}
