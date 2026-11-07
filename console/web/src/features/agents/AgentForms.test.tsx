import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AgentCreateDialog, InstallationCreateDialog } from './AgentCreateForms'
import { AgentActionDialog } from './AgentActionDialog'
import { errorResponse, jsonResponse, stubFetch } from '../../test/utils'
import type { Agent, AgentGrant } from '../../api/types'
const agent: Agent = {
  id: 'agent-1',
  app_id: 'app-1',
  client_id: 'client-1',
  name: '工单助手',
  description: '工单读取',
  kind: 'confidential',
  status: 'active',
  allowed_modes: ['delegated', 'autonomous'],
  allowed_scopes: ['tickets:read', 'tickets:write'],
  allowed_resources: ['https://tickets.example/mcp'],
  token_ttl_seconds: 120,
  grant_ttl_seconds: 3600,
  config_version: 1,
  created_at: '2026-10-16T00:00:00Z',
}
const grant: AgentGrant = {
  id: 'grant-1',
  app_id: 'app-1',
  agent_id: agent.id,
  installation_id: 'install-1',
  client_id: agent.client_id,
  user_id: 'user-1',
  tenant_id: 'tenant-a',
  mode: 'delegated',
  resource: agent.allowed_resources[0]!,
  status: 'active',
  scopes: ['tickets:read'],
  config_version: 1,
  created_at: agent.created_at,
  expires_at: 2000000000,
  revoked_at: 0,
}
afterEach(() => vi.unstubAllGlobals())
describe('Agent 管理安全交互', () => {
  it('结果未知时冻结创建，通过不含密钥的回执核对，保持一次写入', async () => {
    const user = userEvent.setup()
    const close = vi.fn()
    const done = vi.fn()
    const { calls } = stubFetch((call) =>
      call.method === 'POST'
        ? errorResponse(503, { code: 'outcome_unknown' })
        : call.url.includes('agent-operations')
          ? jsonResponse({ kind: 'agent', resource_id: agent.id })
          : jsonResponse(agent),
    )
    render(<AgentCreateDialog appId="app-1" onDone={done} onClose={close} />)
    await user.type(screen.getByLabelText('Agent 名称'), agent.name)
    await user.type(screen.getByLabelText('允许的业务权限'), 'tickets:read')
    await user.type(screen.getByLabelText('允许的资源 URI'), 'https://tickets.example/mcp')
    await user.type(screen.getByLabelText('登录回调地址'), 'https://agent.example/callback')
    await user.click(screen.getByRole('button', { name: '确认创建' }))
    await screen.findByText('提交结果未知：请求可能已在服务端生效')
    expect(screen.getByLabelText('Agent 名称')).toBeDisabled()
    expect(done).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: '核对本次创建' }))
    await screen.findByText(/已创建 Agent「工单助手」/)
    expect(calls.filter((c) => c.method === 'POST')).toHaveLength(1)
    await user.click(screen.getByRole('button', { name: '已核对，关闭表单' }))
    expect(close).toHaveBeenCalledOnce()
  })
  it('租户委托没有绑定用户时阻止创建', async () => {
    const user = userEvent.setup()
    const { calls } = stubFetch(() => jsonResponse({}))
    render(
      <InstallationCreateDialog appId="app-1" agent={agent} onDone={vi.fn()} onClose={vi.fn()} />,
    )
    await user.type(screen.getByLabelText('租户标识'), 'tenant-a')
    await user.type(screen.getByLabelText('责任人'), 'admin')
    await user.selectOptions(screen.getByLabelText('目标资源'), agent.allowed_resources[0]!)
    await user.click(screen.getByLabelText('用户委托：用户登录并确认授权'))
    await user.click(screen.getByLabelText('tickets:read'))
    await user.type(screen.getByLabelText('接入到期时间'), '2026-11-16T12:00')
    await user.type(screen.getByLabelText('接入原因'), 'approved')
    await user.click(screen.getByRole('button', { name: '确认创建' }))
    await screen.findByText(/租户级用户委托必须指定/)
    expect(calls).toHaveLength(0)
  })
  it('撤销冲突后读回已撤销状态，不能再提交旧操作', async () => {
    const user = userEvent.setup()
    const done = vi.fn()
    const { calls } = stubFetch((call) =>
      call.method === 'POST'
        ? errorResponse(409, { code: 'conflict' })
        : jsonResponse({ ...grant, status: 'revoked', config_version: 2, revoked_at: 1 }),
    )
    render(
      <AgentActionDialog
        appId="app-1"
        action={{ kind: 'grant', record: grant }}
        onDone={done}
        onClose={vi.fn()}
      />,
    )
    await user.type(screen.getByLabelText('操作原因'), 'withdrawn')
    await user.click(screen.getByLabelText('我已确认本次操作及影响'))
    await user.click(screen.getByRole('button', { name: '撤销授权' }))
    await screen.findByText(/操作已冻结/)
    expect(screen.getByLabelText('操作原因')).toBeDisabled()
    await user.click(screen.getByRole('button', { name: '读取当前记录' }))
    await waitFor(() => expect(screen.getByLabelText('操作原因')).toHaveValue(''))
    expect(screen.getByRole('button', { name: '撤销授权' })).toBeDisabled()
    expect(done).not.toHaveBeenCalled()
    expect(calls.filter((c) => c.method === 'POST')).toHaveLength(1)
  })
})
