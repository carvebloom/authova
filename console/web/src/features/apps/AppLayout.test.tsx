import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Link, Outlet, Route, Routes } from 'react-router-dom'
import type { App } from '../../api/types'
import { jsonResponse, makeSession, renderWithSession, stubFetch } from '../../test/utils'
import { AppLayout } from './AppLayout'

const app1: App = {
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

const app2: App = { ...app1, id: 'app-2', name: '支付后台', slug: 'pay-admin' }

interface Deferred<T> {
  promise: Promise<T>
  resolve: (value: T) => void
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((res) => {
    resolve = res
  })
  return { promise, resolve }
}

function Harness() {
  return (
    <>
      <Link to="/apps/app-1">去应用一</Link>
      <Link to="/apps/app-2">去应用二</Link>
      <Outlet />
    </>
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('AppLayout 跨 App 切换', () => {
  it('切换 appId 后旧 App 数据不随新路由渲染，响应按最新 key 生效', async () => {
    const user = userEvent.setup()
    const d2 = deferred<Response>()
    stubFetch((call) => {
      if (call.url === '/bff/apps/app-1') return jsonResponse(app1)
      if (call.url === '/bff/apps/app-2') return d2.promise
      return jsonResponse({ items: [], next_cursor: null })
    })
    renderWithSession(
      <Routes>
        <Route element={<Harness />}>
          <Route path="/apps/:appId" element={<AppLayout />}>
            <Route index element={<div>概览内容</div>} />
          </Route>
        </Route>
      </Routes>,
      makeSession(),
      ['/apps/app-1'],
    )

    expect(await screen.findByRole('heading', { name: '交易前台' })).toBeInTheDocument()

    await user.click(screen.getByRole('link', { name: '去应用二' }))
    // 新 App 响应未返回：任何一帧不得展示旧 App 名称（含标题与面包屑）
    expect(screen.queryByText('交易前台')).not.toBeInTheDocument()
    expect(screen.queryByText('概览内容')).not.toBeInTheDocument()

    await act(async () => d2.resolve(jsonResponse(app2)))
    expect(await screen.findByRole('heading', { name: '支付后台' })).toBeInTheDocument()
    expect(screen.getByText('概览内容')).toBeInTheDocument()
  })
})
