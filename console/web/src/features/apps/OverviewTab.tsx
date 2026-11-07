import { useState } from 'react'
import { useProtection } from '../../app/protectionState'
import { Link } from 'react-router-dom'
import * as api from '../../api/endpoints'
import type { AuthenticationConfig } from '../../api/types'
import { useAppCapabilities } from '../../app/appContext'
import { useAsync } from '../../shared/hooks/useAsync'
import { Button } from '../../shared/components/Button'
import { ReasonDialog } from '../../shared/components/ReasonDialog'
import { CopyButton } from '../../shared/components/CopyButton'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { Loading } from '../../shared/components/States'
import { PageHeader } from '../../shared/components/PageHeader'
import { Icon, type IconName } from '../../shared/components/Icon'
import { providerLabel } from '../../shared/utils/providers'

import { SigningKeysSection } from './SigningKeysSection'

type PendingAction = 'suspend' | 'resume' | 'rotate' | null
type ActionKind = Exclude<PendingAction, null>

const ACTION_TEXT = {
  suspend: {
    title: '暂停应用',
    description: '暂停后该应用的登录与令牌签发将不可用，直至恢复。',
    confirm: '确认暂停',
  },
  resume: {
    title: '恢复应用',
    description: '恢复后该应用将重新开始提供服务。',
    confirm: '确认恢复',
  },
  rotate: {
    title: '轮换密钥（常规）',
    description:
      '常规轮换：新密钥立即用于签名，旧验证密钥保留 24 小时后退役，已签发的令牌在过期前仍可验证。如需立即废止某个密钥，请在「签名密钥」列表中使用紧急吊销。',
    confirm: '确认轮换',
  },
} as const

interface TaskEntry {
  icon: IconName
  title: string
  to: string
  /** 真实读取得出的就绪状态；无 API 支撑时不展示 */
  status?: string
  ready?: boolean
  hint: string
}

/**
 * 任务化概览：接入准备入口（真实读取得出的就绪状态）、登录安全策略与已配置渠道、
 * 危险操作分区。技术元数据（ID/Issuer/版本/时间）只在页头「技术详情」，不重复占主视口。
 */
export function OverviewTab() {
  const { app, reloadApp, canSuspendResume, canRotateKeys: mayRotateKeys } = useAppCapabilities()
  const [action, setAction] = useState<PendingAction>(null)
  // 结果未知的危险操作：页面级留存记录，读回权威状态前该动作保持冻结（重开对话框也是冻结态）
  const [unknownAction, setUnknownAction] = useState<ActionKind | null>(null)
  useProtection(unknownAction !== null)
  const [notice, setNotice] = useState<string | null>(null)

  // 就绪状态各自独立读取，互不阻塞
  const clients = useAsync(`overview:clients:${app.id}`, () =>
    api.listClients(app.id, { limit: 20 }),
  )
  const connections = useAsync(`overview:connections:${app.id}`, () =>
    api.listConnections(app.id, { limit: 100 }),
  )
  const authConfig = useAsync(`overview:authentication-config:${app.id}`, () =>
    api.getAuthenticationConfig(app.id),
  )
  const signingKeys = useAsync(`overview:signing-keys:${app.id}`, () => api.listSigningKeys(app.id))

  const suspended = app.status === 'suspended'
  const quarantined = app.status === 'quarantined'
  const canRotateKeys = mayRotateKeys && !quarantined

  const reconcile = async () => {
    const ok = await reloadApp()
    if (ok) setUnknownAction(null)
    return ok
  }

  const handleSubmit = async (reason: string) => {
    if (action === 'suspend') await api.suspendApp(app.id, reason)
    else if (action === 'resume') await api.resumeApp(app.id, reason)
    else if (action === 'rotate') await api.rotateKeys(app.id, reason)
    setNotice(
      action === 'suspend'
        ? '应用已暂停。'
        : action === 'resume'
          ? '应用已恢复。'
          : '密钥已轮换：新签名密钥已启用，旧验证密钥保留 24 小时。',
    )
    reloadApp()
    if (action === 'rotate') signingKeys.reload()
  }

  const businessClients = clients.data?.items.filter((client) => !client.system) ?? []
  const clientStatus = clients.data
    ? businessClients.length > 0
      ? '已配置业务客户端'
      : clients.data.next_cursor
        ? '查看客户端列表'
        : '待添加业务客户端'
    : undefined
  const connectionStatus = connections.data
    ? connections.data.items.length === 0
      ? '待添加连接'
      : `已创建 ${connections.data.items.length} 个连接`
    : undefined

  const tasks: TaskEntry[] = [
    {
      icon: 'clients',
      title: '客户端',
      to: `/apps/${app.id}/clients`,
      status: clientStatus,
      ready: clients.data ? businessClients.length > 0 : undefined,
      hint: 'OAuth / OIDC 客户端：回调地址、作用域与授权类型。',
    },
    {
      icon: 'connections',
      title: '登录连接',
      to: `/apps/${app.id}/connections`,
      status: connectionStatus,
      ready: connections.data ? connections.data.items.length > 0 : undefined,
      hint: '上游身份源：标准 OIDC、GitHub、微信网站扫码、QQ 网站登录。',
    },
    {
      icon: 'users',
      title: '用户排查',
      to: `/apps/${app.id}/users`,
      hint: '按完整邮箱或 E.164 手机号精确查询用户，管理会话与角色。',
    },
    {
      icon: 'audit',
      title: '审计日志',
      to: `/apps/${app.id}/audit`,
      hint: '该应用上的管理操作记录，含操作者与原因。',
    },
  ]

  return (
    <div className="tab-panel">
      <PageHeader level={2} title="概览" sub="应用的接入任务与运行状态。" />

      {notice ? (
        <div className="notice notice-ok" role="status">
          <div className="notice-body">
            <p className="notice-title">{notice}</p>
          </div>
        </div>
      ) : null}

      {unknownAction ? (
        <div className="notice notice-warn" role="alert">
          <div className="notice-body">
            <p className="notice-title">未确认的操作：{ACTION_TEXT[unknownAction].title}</p>
            <p className="notice-detail">
              先前提交的结果未知，该操作可能已生效。在重新打开对话框并「重新加载最新状态」完成核对前，该操作保持冻结。
            </p>
          </div>
        </div>
      ) : null}

      <section className="panel" aria-label="接入准备">
        <h3 className="panel-title">接入准备</h3>
        <ul className="task-list">
          {tasks.map((task) => (
            <li key={task.to}>
              <Link to={task.to} className="task-row">
                <span className="task-icon">
                  <Icon name={task.icon} size={16} />
                </span>
                <span className="task-body">
                  <span className="task-title">{task.title}</span>
                  <span className="task-hint">{task.hint}</span>
                </span>
                {task.status !== undefined ? (
                  <span className={`badge ${task.ready ? 'badge-ok' : 'badge-muted'}`}>
                    {task.status}
                  </span>
                ) : null}
                <Icon name="chevron-right" size={14} />
              </Link>
            </li>
          ))}
        </ul>
        {clients.error ? <ErrorNotice error={clients.error} onRetry={clients.reload} /> : null}
        {connections.error ? (
          <ErrorNotice error={connections.error} onRetry={connections.reload} />
        ) : null}
      </section>

      <section className="panel authconfig" aria-label="登录安全与渠道">
        <div className="panel-head-row">
          <h3 className="panel-title">登录安全与渠道</h3>
          <Link to={`/apps/${app.id}/settings`} className="btn btn-ghost btn-sm">
            前往配置
          </Link>
        </div>
        {authConfig.loading ? (
          <Loading label="正在加载登录方式…" />
        ) : authConfig.error ? (
          <ErrorNotice error={authConfig.error} onRetry={authConfig.reload} />
        ) : authConfig.data ? (
          <AuthConfigSummary config={authConfig.data} requireMfa={app.require_mfa} />
        ) : null}
      </section>

      {quarantined ? (
        <div className="notice notice-warn" role="status">
          该应用处于恢复隔离。请先完成身份、凭据和业务权威事实核验，再通过本地恢复流程解除隔离。
        </div>
      ) : null}

      <SigningKeysSection appId={app.id} canRotateKeys={canRotateKeys} keys={signingKeys} />

      {canSuspendResume || canRotateKeys ? (
        <div className="danger-zone">
          <p className="danger-zone-title">危险操作</p>
          <p className="danger-zone-desc">
            暂停/恢复影响该应用的线上登录；常规密钥轮换保留旧验证密钥 24
            小时，立即废止单个密钥请用「签名密钥」中的紧急吊销。所有操作均需填写原因并记入审计日志。
          </p>
          <div className="action-bar" aria-label="应用操作">
            {canSuspendResume && !quarantined ? (
              !suspended ? (
                <Button
                  variant="danger"
                  onClick={() => {
                    setNotice(null)
                    setAction('suspend')
                  }}
                >
                  暂停应用
                </Button>
              ) : (
                <Button
                  variant="primary"
                  onClick={() => {
                    setNotice(null)
                    setAction('resume')
                  }}
                >
                  恢复应用
                </Button>
              )
            ) : null}
            {canRotateKeys ? (
              <Button
                onClick={() => {
                  setNotice(null)
                  setAction('rotate')
                }}
              >
                轮换密钥
              </Button>
            ) : null}
          </div>
        </div>
      ) : null}

      {action ? (
        <ReasonDialog
          open
          title={ACTION_TEXT[action].title}
          description={ACTION_TEXT[action].description}
          confirmLabel={ACTION_TEXT[action].confirm}
          danger={action !== 'resume'}
          frozen={unknownAction === action}
          onReconcile={reconcile}
          onUnknown={() => setUnknownAction(action)}
          onClose={() => setAction(null)}
          onSubmit={handleSubmit}
        />
      ) : null}
    </div>
  )
}

/**
 * 登录方式部署就绪状态与联邦回调地址。
 * 「已配置」仅表示部署配置就绪（如短信所需的部署级环境变量已设置），
 * 不代表短信厂商通道或上游 IdP 已实际验通；部署级密钥不经由控制台收集。
 */
function AuthConfigSummary({
  config,
  requireMfa,
}: {
  config: AuthenticationConfig
  requireMfa: boolean
}) {
  return (
    <>
      <dl className="detail-grid">
        <div className="detail-item">
          <dt>要求用户 MFA</dt>
          <dd>{requireMfa ? '已启用' : '未启用'}</dd>
        </div>
        <div className="detail-item">
          <dt>密码登录</dt>
          <dd>{config.password_login ? '已启用' : '未启用'}</dd>
        </div>
        <div className="detail-item">
          <dt>邮箱验证码</dt>
          <dd>{config.email_otp_configured ? '已配置' : '未配置'}</dd>
        </div>
        <div className="detail-item">
          <dt>短信验证码</dt>
          <dd>{config.phone_otp_configured ? '已配置' : '未配置'}</dd>
        </div>
        <div className="detail-item">
          <dt>部署支持的连接类型</dt>
          <dd>
            {config.provider_types.length > 0
              ? config.provider_types.map(providerLabel).join(' / ')
              : '—'}
          </dd>
        </div>
        <div className="detail-item">
          <dt>联邦回调地址</dt>
          <dd className="mono">
            {config.federation_callback_url}{' '}
            <CopyButton value={config.federation_callback_url} label="复制" />
          </dd>
        </div>
      </dl>
      {!config.phone_otp_configured ? (
        <div className="notice notice-warn authconfig-warning" role="alert">
          <div className="notice-body">
            <p className="notice-title">短信验证码未配置</p>
            <p className="notice-detail">
              需部署方在服务端设置 AUTHOVA_SMS_URL 与 AUTHOVA_SMS_TOKEN
              环境变量后方可发送短信验证码； 部署级密钥不经由控制台收集，请联系部署方处理。
            </p>
          </div>
        </div>
      ) : null}
    </>
  )
}
