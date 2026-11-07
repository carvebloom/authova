import { useEffect, useState } from 'react'
import * as api from '../../api/agents'
import type { Agent, AgentInstallation, AgentGrant, CreatedAgent } from '../../api/types'
import { useAppCapabilities } from '../../app/appContext'
import { useProtection } from '../../app/protectionState'
import { usePagedList } from '../../shared/hooks/usePagedList'
import { Button } from '../../shared/components/Button'
import { PageHeader } from '../../shared/components/PageHeader'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { Loading } from '../../shared/components/States'
import { StatusBadge } from '../../shared/components/StatusBadge'
import { CopyButton } from '../../shared/components/CopyButton'
import { SecretPanel } from '../../shared/components/SecretPanel'
import { TextField } from '../../shared/components/Field'
import { LoadMore } from '../../shared/components/LoadMore'
import { AgentCreateDialog, InstallationCreateDialog } from './AgentCreateForms'
import { AgentActionDialog, type AgentAction } from './AgentActionDialog'

type View = 'agents' | 'installations' | 'grants'
const when = (seconds: number) => new Date(seconds * 1000).toLocaleString()
export function AgentsTab() {
  const { app, session } = useAppCapabilities()
  return (
    <AgentWorkspace
      key={app.id}
      appId={app.id}
      issuer={app.issuer}
      canReadPrivate={Boolean(
        session.operator && (session.isAppScoped || session.operator.role !== 'auditor'),
      )}
      canWrite={
        session.operator?.role === 'owner' &&
        (!session.isAppScoped || session.operator.app_id === app.id)
      }
    />
  )
}
function AgentWorkspace({
  appId,
  issuer,
  canWrite,
  canReadPrivate,
}: {
  appId: string
  issuer: string
  canWrite: boolean
  canReadPrivate: boolean
}) {
  const [view, setView] = useState<View>('agents')
  const [now, setNow] = useState(() => Date.now() / 1000)
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now() / 1000), 30000)
    return () => window.clearInterval(timer)
  }, [])
  const [query, setQuery] = useState({ agent_id: '', tenant_id: '', user_id: '' })
  const [draft, setDraft] = useState(query)
  const [create, setCreate] = useState(false)
  const [install, setInstall] = useState<Agent | null>(null)
  const [action, setAction] = useState<AgentAction | null>(null)
  const [secret, setSecret] = useState<CreatedAgent | null>(null)
  const [notice, setNotice] = useState('')
  const [selected, setSelected] = useState<Agent | AgentInstallation | AgentGrant | null>(null)
  const agents = usePagedList<Agent>(
    `agents:${appId}`,
    (cursor) => api.listAgents(appId, { ...(cursor ? { cursor } : {}), limit: 30 }),
    view === 'agents',
  )
  const installations = usePagedList<AgentInstallation>(
    `agent-installations:${appId}:${JSON.stringify(query)}`,
    (cursor) =>
      api.listAgentInstallations(appId, { ...query, ...(cursor ? { cursor } : {}), limit: 30 }),
    view === 'installations' && canReadPrivate,
  )
  const grants = usePagedList<AgentGrant>(
    `agent-grants:${appId}:${JSON.stringify(query)}`,
    (cursor) => api.listAgentGrants(appId, { ...query, ...(cursor ? { cursor } : {}), limit: 30 }),
    view === 'grants' && canReadPrivate,
  )
  const list = view === 'agents' ? agents : view === 'installations' ? installations : grants
  useProtection(create || install !== null || action !== null || secret !== null)
  const closeCreate = () => {
    setCreate(false)
    void agents.reload()
  }
  const created = (out: CreatedAgent) => {
    setCreate(false)
    setNotice(`Agent「${out.agent.name}」已注册。请选择「创建接入」授予具体访问范围。`)
    if (out.client_secret) setSecret(out)
    void agents.reload()
  }
  const done = () => {
    setAction(null)
    setInstall(null)
    setNotice('操作已确认。列表正在重新读取。')
    void list.reload()
  }
  const tabs: { key: View; label: string }[] = [
    { key: 'agents', label: 'Agent' },
    { key: 'installations', label: '接入记录' },
    { key: 'grants', label: '授权记录' },
  ]
  return (
    <div className="tab-panel">
      <PageHeader
        level={2}
        title="Agent 访问"
        sub="管理此 App 的 Agent、接入范围和用户委托。业务 API 继续校验具体数据权限。"
        actions={
          <>
            {canWrite && view === 'agents' ? (
              <Button variant="primary" onClick={() => setCreate(true)}>
                注册 Agent
              </Button>
            ) : null}
            <Button
              onClick={() => {
                setSelected(null)
                void list.reload()
              }}
            >
              刷新列表
            </Button>
          </>
        }
      />
      <div className="toolbar agent-views" role="group" aria-label="Agent 管理视图">
        {tabs
          .filter((tab) => canReadPrivate || tab.key === 'agents')
          .map((tab) => (
            <Button
              key={tab.key}
              aria-pressed={view === tab.key}
              variant={view === tab.key ? 'primary' : 'ghost'}
              onClick={() => {
                setView(tab.key)
                setSelected(null)
                setNotice('')
              }}
            >
              {tab.label}
            </Button>
          ))}
      </div>
      {!canWrite ? (
        <p className="field-hint">
          {canReadPrivate
            ? '当前账号可查看，只有 owner 可以管理 Agent 接入和授权。'
            : '平台审计员仅可查看 Agent 元数据，接入与授权中的用户信息不对该角色开放。'}
        </p>
      ) : null}
      {notice ? (
        <p className="notice notice-ok" role="status">
          {notice}
        </p>
      ) : null}
      {view !== 'agents' ? (
        <form
          className="toolbar agent-filters"
          aria-label="筛选 Agent 记录"
          onSubmit={(e) => {
            e.preventDefault()
            setQuery({ ...draft })
            setSelected(null)
          }}
        >
          <TextField
            label="Agent ID"
            value={draft.agent_id}
            maxLength={36}
            onChange={(e) => setDraft({ ...draft, agent_id: e.target.value.trim() })}
          />
          <TextField
            label="租户标识"
            value={draft.tenant_id}
            maxLength={128}
            onChange={(e) => setDraft({ ...draft, tenant_id: e.target.value.trim() })}
          />
          <TextField
            label="用户 ID"
            value={draft.user_id}
            maxLength={36}
            onChange={(e) => setDraft({ ...draft, user_id: e.target.value.trim() })}
          />
          <Button type="submit">筛选</Button>
        </form>
      ) : null}
      {view === 'grants' ? (
        <p className="field-hint">
          用户委托由真实 OAuth 登录和确认授权产生。自主运行授权在创建相应接入时产生，可单独撤销。
        </p>
      ) : null}
      {view === 'installations' ? (
        <p className="field-hint">
          在 Agent 列表选择「创建接入」。租户级用户委托需要由可信业务系统核实并绑定具体用户。
        </p>
      ) : null}
      {list.error ? (
        <ErrorNotice
          error={list.error}
          onRetry={() => {
            void list.reload()
          }}
        />
      ) : null}
      {list.loading ? (
        <Loading />
      ) : !list.error && list.items.length === 0 ? (
        <div className="empty">
          <p className="empty-title">
            暂无{view === 'agents' ? ' Agent' : view === 'installations' ? '接入记录' : '授权记录'}
          </p>
          <p className="empty-hint">
            {view === 'agents'
              ? '注册 Agent 后，创建具体接入，再由用户确认委托或管理员授予自主运行权限。'
              : '调整筛选条件，或完成接入和授权流程后重新读取。'}
          </p>
        </div>
      ) : null}
      {!list.loading && list.items.length > 0 ? (
        <div className="table-scroll">
          <table className="table table-agents">
            <thead>
              <tr>
                <th>{view === 'agents' ? 'Agent' : '身份与范围'}</th>
                <th>{view === 'agents' ? '权限上限' : '资源与权限'}</th>
                <th>状态</th>
                <th>{view === 'agents' ? '凭证策略' : '到期时间'}</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {view === 'agents'
                ? agents.items.map((a) => (
                    <tr key={a.id}>
                      <td>
                        <button className="table-link" onClick={() => setSelected(a)}>
                          {a.name}
                        </button>
                        <p className="field-hint">{a.description || '未填写用途说明'}</p>
                        <small>
                          {a.allowed_modes
                            .map((v) => (v === 'delegated' ? '用户委托' : '自主运行'))
                            .join(' / ')}
                        </small>
                      </td>
                      <td>
                        {a.allowed_scopes.join(' · ')}
                        <p className="field-hint">{a.allowed_resources.join(' · ')}</p>
                      </td>
                      <td>
                        <StatusBadge status={a.status} />
                      </td>
                      <td>
                        令牌 {a.token_ttl_seconds} 秒
                        <p className="field-hint">
                          授权最长 {Math.round(a.grant_ttl_seconds / 3600)} 小时
                        </p>
                      </td>
                      <td className="table-actions">
                        {canWrite ? (
                          <>
                            <Button
                              size="sm"
                              disabled={a.status !== 'active'}
                              onClick={() => setInstall(a)}
                            >
                              创建接入
                            </Button>
                            <Button
                              size="sm"
                              onClick={() => setAction({ kind: 'agent', record: a })}
                            >
                              {a.status === 'active' ? '停用' : '启用'}
                            </Button>
                          </>
                        ) : (
                          <Button size="sm" onClick={() => setSelected(a)}>
                            详情
                          </Button>
                        )}
                      </td>
                    </tr>
                  ))
                : null}
              {view === 'installations'
                ? installations.items.map((i) => (
                    <tr key={i.id}>
                      <td>
                        <button className="table-link mono" onClick={() => setSelected(i)}>
                          {i.id}
                        </button>
                        <p className="field-hint">
                          租户 {i.tenant_id || 'App 范围'} · 用户 {i.user_id || '未绑定'}
                        </p>
                        <small>责任人 {i.responsible_party}</small>
                      </td>
                      <td>
                        {i.resource}
                        <p className="field-hint">{i.allowed_scopes.join(' · ')}</p>
                      </td>
                      <td>
                        <StatusBadge status={i.status} />
                      </td>
                      <td>
                        <time title={String(i.expires_at)}>{when(i.expires_at)}</time>
                      </td>
                      <td className="table-actions">
                        {canWrite ? (
                          <>
                            <Button
                              size="sm"
                              onClick={() => setAction({ kind: 'installation', record: i })}
                            >
                              {i.status === 'active' ? '停用' : '启用'}
                            </Button>
                            {i.allowed_modes.includes('autonomous') ? (
                              <Button
                                size="sm"
                                disabled={i.status !== 'active' || i.expires_at <= now}
                                onClick={() => setAction({ kind: 'renew', record: i })}
                              >
                                更新自主授权
                              </Button>
                            ) : null}
                          </>
                        ) : null}
                      </td>
                    </tr>
                  ))
                : null}
              {view === 'grants'
                ? grants.items.map((g) => (
                    <tr key={g.id}>
                      <td>
                        <button className="table-link mono" onClick={() => setSelected(g)}>
                          {g.id}
                        </button>
                        <p className="field-hint">
                          {g.mode === 'delegated' ? `代表用户 ${g.user_id}` : '自主运行'} · 租户{' '}
                          {g.tenant_id || 'App 范围'}
                        </p>
                      </td>
                      <td>
                        {g.resource}
                        <p className="field-hint">{g.scopes.join(' · ')}</p>
                      </td>
                      <td>
                        <StatusBadge status={g.status} />
                        {g.status === 'active' && g.expires_at <= now ? (
                          <small>已到期</small>
                        ) : null}
                      </td>
                      <td>
                        <time title={String(g.expires_at)}>{when(g.expires_at)}</time>
                      </td>
                      <td>
                        {canWrite ? (
                          <Button
                            size="sm"
                            variant="danger"
                            disabled={g.status !== 'active' || g.expires_at <= now}
                            onClick={() => setAction({ kind: 'grant', record: g })}
                          >
                            撤销授权
                          </Button>
                        ) : null}
                      </td>
                    </tr>
                  ))
                : null}
            </tbody>
          </table>
        </div>
      ) : null}
      {list.loadMoreError ? <ErrorNotice error={list.loadMoreError} /> : null}
      <LoadMore
        nextCursor={list.nextCursor}
        loading={list.loadingMore}
        onLoadMore={list.loadMore}
      />
      {selected ? (
        <section className="panel section-gap" aria-label="记录详情">
          <PageHeader
            level={2}
            title="记录详情"
            actions={<Button onClick={() => setSelected(null)}>收起</Button>}
          />
          <dl className="detail-grid">
            {Object.entries(selected).map(([key, value]) => (
              <div className="detail-item" key={key}>
                <dt>{key}</dt>
                <dd className="mono">
                  {Array.isArray(value) ? value.join(' · ') : String(value)}
                  {key === 'client_id' || key === 'id' ? (
                    <CopyButton value={String(value)} label="复制" />
                  ) : null}
                </dd>
              </div>
            ))}
          </dl>
          {'allowed_resources' in selected ? (
            <p className="field-hint">
              授权端点：{issuer}/authorize · 委托请求须指定 installation_id、resource 与 S256 PKCE。
            </p>
          ) : null}
        </section>
      ) : null}
      {create ? <AgentCreateDialog appId={appId} onDone={created} onClose={closeCreate} /> : null}
      {install ? (
        <InstallationCreateDialog
          appId={appId}
          agent={install}
          onDone={() => {
            setInstall(null)
            setNotice('接入已创建。请在接入记录中核对状态。')
          }}
          onClose={() => setInstall(null)}
        />
      ) : null}
      {action ? (
        <AgentActionDialog
          appId={appId}
          action={action}
          onDone={done}
          onClose={() => {
            setAction(null)
            void list.reload()
          }}
        />
      ) : null}
      {secret?.client_secret ? (
        <SecretPanel
          title="保存 Agent 客户端密钥"
          description={`Client ID：${secret.agent.client_id}。密钥只在本次确认响应中显示，请交给可信 Agent 服务端保管。`}
          secret={secret.client_secret}
          confirmLabel="已安全保存"
          onClose={() => setSecret(null)}
        />
      ) : null}
    </div>
  )
}
