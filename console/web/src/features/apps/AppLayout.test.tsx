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

describe('AppLayout 应用内导航', () => {
  it('局部 tabs 准确导航并标记当前项，返回应用回到总览', async () => {
    const user = userEvent.setup()
    stubFetch((call) =>
      call.url === '/bff/apps/app-1'
        ? jsonResponse(app1)
        : jsonResponse({ items: [], next_cursor: null }),
    )
    renderWithSession(
      <Routes>
        <Route path="/apps" element={<div>应用总览</div>} />
        <Route path="/apps/:appId" element={<AppLayout />}>
          <Route index element={<div>概览内容</div>} />
          <Route path="settings" element={<div>配置内容</div>} />
          <Route path="clients" element={<div>客户端内容</div>} />
          <Route path="users" element={<div>用户内容</div>} />
          <Route path="connections" element={<div>连接内容</div>} />
          <Route path="agents" element={<div>Agent 访问内容</div>} />
          <Route path="audit" element={<div>审计日志内容</div>} />
        </Route>
      </Routes>,
      makeSession(),
      ['/apps/app-1'],
    )

    expect(await screen.findByRole('heading', { name: '交易前台' })).toBeInTheDocument()
    expect(screen.getByRole('navigation', { name: '应用内导航' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '概览' })).toHaveAttribute('aria-current', 'page')

    const destinations = ['配置', '客户端', '用户', '连接', 'Agent 访问', '审计日志']
    for (const label of destinations) {
      const link = screen.getByRole('link', { name: label })
      await user.click(link)
      expect(await screen.findByText(`${label}内容`)).toBeInTheDocument()
      expect(link).toHaveAttribute('aria-current', 'page')
    }

    await user.click(screen.getByRole('link', { name: '返回应用' }))
    expect(await screen.findByText('应用总览')).toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: '应用内导航' })).not.toBeInTheDocument()
  })
})
