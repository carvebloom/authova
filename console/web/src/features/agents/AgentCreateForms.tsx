import { useRef, useState, type FormEvent, type ReactNode } from 'react'
import * as api from '../../api/agents'
import { ApiError } from '../../api/client'
import type { Agent, AgentCreate, AgentInstallationCreate, CreatedAgent } from '../../api/types'
import { useProtection } from '../../app/protectionState'
import { Button } from '../../shared/components/Button'
import { Dialog } from '../../shared/components/Dialog'
import { TextField, TextAreaField, SelectField } from '../../shared/components/Field'
import { ErrorNotice } from '../../shared/components/ErrorNotice'
import { OutcomeUnknownNotice } from '../../shared/components/OutcomeUnknownNotice'

const words = (s: string) => s.split(/[,\s]+/).filter(Boolean)
type Mode = 'delegated' | 'autonomous'
function Modes({
  allowed,
  value,
  onChange,
}: {
  allowed: Mode[]
  value: Mode[]
  onChange: (v: Mode[]) => void
}) {
  return (
    <div className="field">
      <span className="field-label">运行方式</span>
      {allowed.map((mode) => (
        <label className="check-row" key={mode}>
          <input
            type="checkbox"
            checked={value.includes(mode)}
            onChange={(e) =>
              onChange(e.target.checked ? [...value, mode] : value.filter((v) => v !== mode))
            }
          />
          {mode === 'delegated' ? '用户委托：用户登录并确认授权' : '自主运行：管理员授予机器权限'}
        </label>
      ))}
    </div>
  )
}
function Creation<T>({
  appId,
  title,
  submit,
  onDone,
  onClose,
  children,
}: {
  appId: string
  title: string
  submit: (form: HTMLFormElement, key: string) => Promise<T>
  onDone: (result: T) => void
  onClose: () => void
  children: ReactNode
}) {
  const [key] = useState(() => crypto.randomUUID())
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ApiError | null>(null)
  const [frozen, setFrozen] = useState(false)
  const [receipt, setReceipt] = useState('')
  const lock = useRef(false)
  useProtection(busy || frozen)
  const run = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (lock.current || frozen) return
    lock.current = true
    setBusy(true)
    setError(null)
    try {
      onDone(await submit(event.currentTarget, key))
    } catch (e) {
      const failure = e instanceof ApiError ? e : new ApiError({ status: 0, detail: String(e) })
      setError(failure)
      if (failure.isOutcomeUnknown || failure.status === 409) setFrozen(true)
    } finally {
      lock.current = false
      setBusy(false)
    }
  }
  const reconcile = async () => {
    if (lock.current) return
    lock.current = true
    setBusy(true)
    try {
      const operation = await api.getAgentOperation(appId, key)
      if (!operation.resource_id || !['agent', 'installation'].includes(operation.kind))
        throw new ApiError({ status: 503, detail: '核对响应不完整' })
      if (operation.kind === 'agent') {
        const a = await api.getAgent(appId, operation.resource_id)
        setReceipt(
          `已创建 Agent「${a.name}」，Client ID：${a.client_id}。${a.kind === 'confidential' ? '未收到的密钥不能通过查询取回，请到客户端页面轮换。' : '公开客户端使用 PKCE，不含客户端密钥。'}`,
        )
      } else {
        const i = await api.getAgentInstallation(appId, operation.resource_id)
        setReceipt(`已创建接入记录 ${i.id}，当前状态 ${i.status}。`)
      }
      setError(null)
    } catch (e) {
      setError(e instanceof ApiError ? e : new ApiError({ status: 0, detail: String(e) }))
    } finally {
      lock.current = false
      setBusy(false)
    }
  }
  return (
    <Dialog open title={title} onClose={onClose} dismissible={!busy && !frozen} wide>
      {frozen ? (
        <>
          <OutcomeUnknownNotice
            error={error ?? new ApiError({ status: 409, detail: '需要核对创建结果' })}
            detail="表单已冻结。读取本次请求的创建回执，确认资源后结束表单；不会重新创建或读取密钥。回执保留十分钟，查不到不代表没有创建。"
            action={
              <Button onClick={reconcile} loading={busy}>
                核对本次创建
              </Button>
            }
          />
          <p className="field-hint">
            创建请求标识：<code>{key}</code>
          </p>
          {receipt ? (
            <div className="notice" role="status">
              <p>{receipt}</p>
              <Button onClick={onClose}>已核对，关闭表单</Button>
            </div>
          ) : null}
        </>
      ) : null}
      {error && !error.isOutcomeUnknown ? <ErrorNotice error={error} /> : null}
      <form className="form" onSubmit={run}>
        <fieldset className="submission-fields" disabled={busy || frozen}>
          {children}
          <div className="form-footer">
            <Button type="submit" variant="primary" loading={busy}>
              确认创建
            </Button>
          </div>
        </fieldset>
      </form>
    </Dialog>
  )
}
export function AgentCreateDialog({
  appId,
  onDone,
  onClose,
}: {
  appId: string
  onDone: (a: CreatedAgent) => void
  onClose: () => void
}) {
  const [modes, setModes] = useState<Mode[]>(['delegated'])
  const [kind, setKind] = useState<'public' | 'confidential'>('confidential')
  const submit = (form: HTMLFormElement, key: string) => {
    const f = new FormData(form)
    const input: AgentCreate = {
      name: String(f.get('name')).trim(),
      description: String(f.get('description')),
      kind,
      allowed_modes: modes,
      allowed_scopes: words(String(f.get('scopes'))),
      allowed_resources: words(String(f.get('resources'))),
      redirect_uris: words(String(f.get('redirects'))),
      token_ttl_seconds: Number(f.get('ttl')),
      grant_ttl_seconds: Number(f.get('grant_ttl')),
    }
    if (!modes.length || (kind === 'public' && modes.includes('autonomous')))
      throw new ApiError({ status: 400, detail: '请选择运行方式；公开客户端不能自主运行。' })
    return api.createAgent(appId, input, key)
  }
  return (
    <Creation appId={appId} title="注册 Agent" submit={submit} onDone={onDone} onClose={onClose}>
      <p className="field-hint">先定义权限上限，再创建具体接入。用户委托需要用户亲自登录确认。</p>
      <TextField label="Agent 名称" name="name" required maxLength={200} />
      <TextAreaField label="用途说明" name="description" maxLength={2000} />
      <SelectField
        label="客户端类型"
        value={kind}
        onChange={(e) => {
          const value = e.target.value as 'public' | 'confidential'
          setKind(value)
          if (value === 'public') setModes(['delegated'])
        }}
      >
        <option value="confidential">服务端客户端（可保管密钥）</option>
        <option value="public">公开客户端（浏览器或本地应用）</option>
      </SelectField>
      <Modes
        allowed={kind === 'public' ? ['delegated'] : ['delegated', 'autonomous']}
        value={modes}
        onChange={setModes}
      />
      <TextAreaField
        label="允许的业务权限"
        name="scopes"
        required
        hint="空格或换行分隔，例如 tickets:read。身份权限由系统设置。"
      />
      <TextAreaField
        label="允许的资源 URI"
        name="resources"
        required
        hint="每行一个 HTTPS 地址或 URN。每次接入只能选择一个目标资源。"
      />
      <TextAreaField
        label="登录回调地址"
        name="redirects"
        required={modes.includes('delegated')}
        hint="用户委托必填。地址必须与 Agent 实际回调完全一致。"
      />
      <TextField
        label="访问令牌有效期（秒）"
        name="ttl"
        type="number"
        min={30}
        max={300}
        defaultValue={120}
        required
      />
      <TextField
        label="授权最长有效期（秒）"
        name="grant_ttl"
        type="number"
        min={300}
        max={2592000}
        defaultValue={604800}
        required
      />
    </Creation>
  )
}
export function InstallationCreateDialog({
  appId,
  agent,
  onDone,
  onClose,
}: {
  appId: string
  agent: Agent
  onDone: () => void
  onClose: () => void
}) {
  const [modes, setModes] = useState<Mode[]>([])
  const [scopes, setScopes] = useState<string[]>([])
  const submit = async (form: HTMLFormElement, key: string) => {
    const f = new FormData(form)
    const input: AgentInstallationCreate = {
      agent_id: agent.id,
      tenant_id: String(f.get('tenant')).trim(),
      user_id: String(f.get('user')).trim(),
      responsible_party: String(f.get('responsible')).trim(),
      resource: String(f.get('resource')),
      allowed_scopes: scopes,
      allowed_modes: modes,
      expires_at: Math.floor(new Date(String(f.get('expires'))).getTime() / 1000),
      reason: String(f.get('reason')).trim(),
    }
    if (!modes.length || !scopes.length || !Number.isSafeInteger(input.expires_at))
      throw new ApiError({ status: 400, detail: '请选择运行方式、业务权限和有效到期时间。' })
    if (input.tenant_id && modes.includes('delegated') && !input.user_id)
      throw new ApiError({ status: 400, detail: '租户级用户委托必须指定已验证的 App 用户 ID。' })
    return api.createAgentInstallation(appId, input, key)
  }
  return (
    <Creation
      appId={appId}
      title={`接入 ${agent.name}`}
      submit={submit}
      onDone={onDone}
      onClose={onClose}
    >
      <p className="field-hint">
        接入只授予所选权限。租户标识来自业务系统，不会自动建立组织或授予所有成员数据访问权。
      </p>
      <TextField
        label="租户标识"
        name="tenant"
        maxLength={128}
        hint="App 级接入可留空；租户成员关系由可信业务系统核实。"
      />
      <TextField
        label="绑定用户 ID"
        name="user"
        maxLength={36}
        hint="租户级用户委托必填；自主运行不会继承此用户权限。"
      />
      <TextField label="责任人" name="responsible" maxLength={200} required />
      <SelectField label="目标资源" name="resource" required>
        <option value="">请选择资源</option>
        {agent.allowed_resources.map((r) => (
          <option key={r} value={r}>
            {r}
          </option>
        ))}
      </SelectField>
      <Modes allowed={agent.allowed_modes} value={modes} onChange={setModes} />
      <div className="field">
        <span className="field-label">授予的业务权限</span>
        {agent.allowed_scopes.map((scope) => (
          <label className="check-row" key={scope}>
            <input
              type="checkbox"
              checked={scopes.includes(scope)}
              onChange={(e) =>
                setScopes(e.target.checked ? [...scopes, scope] : scopes.filter((v) => v !== scope))
              }
            />
            <code>{scope}</code>
          </label>
        ))}
      </div>
      <TextField
        label="接入到期时间"
        hint="未来五分钟至一年内；到期后需要重新审批接入。"
        name="expires"
        type="datetime-local"
        required
      />
      <TextAreaField label="接入原因" name="reason" maxLength={500} required />
    </Creation>
  )
}
