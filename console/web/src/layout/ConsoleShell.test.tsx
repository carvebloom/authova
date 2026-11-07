import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router-dom'
import { ApiError } from '../api/client'
import { jsonResponse, makeSession, renderWithSession, stubFetch } from '../test/utils'
import { ConsoleShell } from './ConsoleShell'

function renderShell(session = makeSession()) {
  renderWithSession(
    <Routes>
      <Route element={<ConsoleShell />}>
        <Route path="*" element={<div>工作区内容</div>} />
      </Route>
    </Routes>,
    session,
    ['/apps'],
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ConsoleShell 角色门控', () => {
  it('未知角色展示只读提示条，并显示原始角色名', async () => {
    stubFetch(() => jsonResponse({ items: [], next_cursor: null }))
    renderShell(
      makeSession({
        operator: { id: 'op-9', email: 'support@example.com', role: 'support' },
        canMutate: false,
        unknownRole: true,
      }),
    )
    expect(await screen.findByText(/角色（support）未识别/)).toBeInTheDocument()
    expect(screen.getByText('工作区内容')).toBeInTheDocument()
  })

  it('已知角色（审计员）不展示未知角色提示条', async () => {
    stubFetch(() => jsonResponse({ items: [], next_cursor: null }))
    renderShell(
      makeSession({
        operator: { id: 'op-2', email: 'audit@example.com', role: 'auditor' },
        canMutate: false,
        unknownRole: false,
      }),
    )
    expect(await screen.findByText('工作区内容')).toBeInTheDocument()
    expect(screen.queryByText(/未识别/)).not.toBeInTheDocument()
    expect(screen.getByText('audit@example.com')).toBeInTheDocument()
    expect(screen.getByText('审计员')).toBeInTheDocument()
  })

  it('退出登录失败：显式展示错误、保留当前页面，不吞错不跳转', async () => {
    stubFetch(() => jsonResponse({ items: [], next_cursor: null }))
    const signOut = vi.fn(async () => {
      throw new ApiError({
        status: 503,
        title: 'Service Unavailable',
        detail: 'core 暂不可用，退出结果未知',
        code: 'logout_failed',
        requestId: 'req-logout-1',
      })
    })
    renderShell(makeSession({ signOut }))
    const user = userEvent.setup()
    await user.click(await screen.findAllByRole('button', { name: '退出登录' }).then((b) => b[0]!))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('core 暂不可用，退出结果未知')
    expect(alert).toHaveTextContent('req-logout-1')
    // 仍在原页面，会话未被清理
    expect(screen.getByText('工作区内容')).toBeInTheDocument()
    expect(signOut).toHaveBeenCalledTimes(1)
  })

  it('退出登录成功：跳转登录页', async () => {
    stubFetch(() => jsonResponse({ items: [], next_cursor: null }))
    const signOut = vi.fn(async () => {})
    renderWithSession(
      <Routes>
        <Route element={<ConsoleShell />}>
          <Route path="/apps" element={<div>工作区内容</div>} />
        </Route>
        <Route path="/login" element={<div>登录页</div>} />
      </Routes>,
      makeSession({ signOut }),
      ['/apps'],
    )
    const user = userEvent.setup()
    await user.click(await screen.findAllByRole('button', { name: '退出登录' }).then((b) => b[0]!))
    expect(await screen.findByText('登录页')).toBeInTheDocument()
  })
})
