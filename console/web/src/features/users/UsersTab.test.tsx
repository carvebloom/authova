import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Outlet, Route, Routes } from 'react-router-dom'
import type { App } from '../../api/types'
import { jsonResponse, makeSession, renderWithSession, stubFetch } from '../../test/utils'
import { UsersTab } from './UsersTab'

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

function FakeAppLayout() {
  return <Outlet context={{ app, reloadApp: vi.fn() }} />
}

function renderUsers() {
  renderWithSession(
    <Routes>
      <Route path="/apps/:appId" element={<FakeAppLayout />}>
        <Route path="users" element={<UsersTab />} />
      </Route>
    </Routes>,
    makeSession(),
    ['/apps/app-1/users'],
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('UsersTab 服务端精确查询', () => {
  it('完整邮箱查询：q 原样随请求提交', async () => {
    const user = userEvent.setup()
    const { calls } = stubFetch(() =>
      jsonResponse({
        items: [
          {
            id: 'u-1',
            app_id: 'app-1',
            display_name: '张三',
            status: 'active',
            email: 'z***@example.com',
            created_at: '2026-09-05T08:00:00Z',
            session_epoch: 1,
          },
        ],
        next_cursor: null,
      }),
    )
    renderUsers()
    // 初始列表（无 q）已返回一条用户
    await screen.findByText('张三')
    await user.type(screen.getByLabelText('搜索用户'), 'zhangsan@example.com')
    await user.click(screen.getByRole('button', { name: '搜索' }))

    await waitFor(() => {
      const urls = calls.map((c) => c.url)
      expect(urls.some((u) => u.includes('q=zhangsan%40example.com'))).toBe(true)
    })
    expect(await screen.findByText('张三')).toBeInTheDocument()
  })

  it('E.164 手机号可查询；格式不符的输入本地拦截、不发请求', async () => {
    const user = userEvent.setup()
    const { calls } = stubFetch(() => jsonResponse({ items: [], next_cursor: null }))
    renderUsers()
    await screen.findByText('该应用还没有注册用户。')

    // 模糊片段：本地拦截，不发请求
    await user.type(screen.getByLabelText('搜索用户'), 'zhang')
    await user.click(screen.getByRole('button', { name: '搜索' }))
    expect(await screen.findByText(/完整邮箱地址，或 E.164 格式手机号/)).toBeInTheDocument()
    expect(calls.filter((c) => c.url.includes('q='))).toHaveLength(0)

    // E.164：放行
    await user.clear(screen.getByLabelText('搜索用户'))
    await user.type(screen.getByLabelText('搜索用户'), '+8613800138000')
    await user.click(screen.getByRole('button', { name: '搜索' }))
    await waitFor(() => {
      expect(calls.some((c) => c.url.includes('q=%2B8613800138000'))).toBe(true)
    })
  })
})
