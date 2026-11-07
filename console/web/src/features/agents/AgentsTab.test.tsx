import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Outlet, Route, Routes } from 'react-router-dom'
import { jsonResponse, makeSession, renderWithSession, stubFetch } from '../../test/utils'
import { AgentsTab } from './AgentsTab'

function AppLayout() {
  return (
    <Outlet
      context={{
        app: { id: 'app-1', issuer: 'https://identity.example/a/app-1' },
        reloadApp: vi.fn(),
      }}
    />
  )
}
afterEach(() => vi.unstubAllGlobals())
describe('Agent 工作区角色边界', () => {
  it.each([
    { role: 'owner', app_id: undefined, privateRead: true, write: true },
    { role: 'admin', app_id: undefined, privateRead: true, write: false },
    { role: 'auditor', app_id: undefined, privateRead: false, write: false },
    { role: 'auditor', app_id: 'app-1', privateRead: true, write: false },
  ])('$role / App $app_id 的入口与查询遵循私有数据权限', async (policy) => {
    const user = userEvent.setup()
    const { calls } = stubFetch(() => jsonResponse({ items: [], next_cursor: null }))
    renderWithSession(
      <Routes>
        <Route path="/apps/:appId" element={<AppLayout />}>
          <Route path="agents" element={<AgentsTab />} />
        </Route>
      </Routes>,
      makeSession({
        operator: {
          id: 'op-1',
          email: 'audit@example.test',
          role: policy.role,
          app_id: policy.app_id,
        },
      }),
      ['/apps/app-1/agents'],
    )
    await screen.findByText('暂无 Agent')
    expect(Boolean(screen.queryByRole('button', { name: '注册 Agent' }))).toBe(policy.write)
    expect(Boolean(screen.queryByRole('button', { name: '接入记录' }))).toBe(policy.privateRead)
    expect(Boolean(screen.queryByRole('button', { name: '授权记录' }))).toBe(policy.privateRead)
    if (policy.privateRead) {
      await user.click(screen.getByRole('button', { name: '授权记录' }))
      await screen.findByText('暂无授权记录')
      expect(calls.some((c) => c.url.includes('/agent-grants'))).toBe(true)
    } else {
      expect(
        calls.every(
          (c) => !c.url.includes('/agent-grants') && !c.url.includes('/agent-installations'),
        ),
      ).toBe(true)
      expect(screen.getByText(/平台审计员仅可查看 Agent 元数据/)).toBeInTheDocument()
    }
  })
})
