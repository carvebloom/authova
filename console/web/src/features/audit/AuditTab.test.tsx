import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Outlet, Route, Routes } from 'react-router-dom'
import type { App, AuditEvent } from '../../api/types'
import { jsonResponse, makeSession, renderWithSession, stubFetch } from '../../test/utils'
import { AuditTab } from './AuditTab'

const app: App = {
  id: 'app-1',
  app_key: 'ak_live_1',
  name: '交易前台',
  slug: 'trade-web',
  status: 'active',
  issuer: 'https://auth.example.com/trade-web',
  created_at: '2026-09-01T08:00:00Z',
  config_version: 3,
  require_mfa: false,
}

const events: AuditEvent[] = [
  {
    id: 'evt-1',
    app_id: 'app-1',
    actor_id: 'op-owner-1',
    action: 'connection.update',
    subject_id: 'conn-1',
    created_at: '2026-09-10T08:00:00Z',
    reason: '例行轮换密钥',
  },
  {
    id: 'evt-2',
    app_id: 'app-1',
    actor_id: 'op-owner-1',
    action: 'future.unknown_action',
    subject_id: 'thing-9',
    created_at: '2026-09-09T08:00:00Z',
  },
]

function FakeAppLayout() {
  return <Outlet context={{ app, reloadApp: vi.fn() }} />
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('AuditTab 中文动作与详情', () => {
  it('已知动作展示中文说明并附原 action；未知动作原样展示', async () => {
    stubFetch(() => jsonResponse({ items: events, next_cursor: null }))
    renderWithSession(
      <Routes>
        <Route path="/apps/:appId" element={<FakeAppLayout />}>
          <Route path="audit" element={<AuditTab />} />
        </Route>
      </Routes>,
      makeSession(),
      ['/apps/app-1/audit'],
    )

    expect(await screen.findByText('更新连接')).toBeInTheDocument()
    expect(screen.getByText('connection.update')).toBeInTheDocument()
    // 未知动作：原样展示，不发明语义
    expect(screen.getByText('future.unknown_action')).toBeInTheDocument()
  })

  it('详情对话框含操作原因/操作者/对象的完整值与复制；无 reason 时不展示该行', async () => {
    const user = userEvent.setup()
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    stubFetch(() => jsonResponse({ items: events, next_cursor: null }))
    renderWithSession(
      <Routes>
        <Route path="/apps/:appId" element={<FakeAppLayout />}>
          <Route path="audit" element={<AuditTab />} />
        </Route>
      </Routes>,
      makeSession(),
      ['/apps/app-1/audit'],
    )

    await user.click((await screen.findAllByRole('button', { name: '详情' }))[0]!)
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent('例行轮换密钥')
    expect(dialog).toHaveTextContent('op-owner-1')
    expect(dialog).toHaveTextContent('conn-1')

    await user.click(screen.getAllByRole('button', { name: '复制' })[0]!)
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('op-owner-1'))

    // 关闭后查看无 reason 的事件：不出现「操作原因」行
    await user.keyboard('{Escape}')
    await user.click(screen.getAllByRole('button', { name: '详情' })[1]!)
    const dialog2 = await screen.findByRole('dialog')
    expect(dialog2).not.toHaveTextContent('操作原因')
    expect(dialog2).toHaveTextContent('future.unknown_action')
  })
})
