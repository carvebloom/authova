import { describe, expect, it, vi, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { Route, Routes, Outlet } from 'react-router-dom'
import { makeSession, renderWithSession, stubFetch, jsonResponse } from '../../test/utils'
import { AppsPage } from './AppsPage'
import { OverviewTab } from './OverviewTab'
import { ClientsTab } from '../clients/ClientsTab'
import type { App, Client } from '../../api/types'

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

const systemClient: Client = {
  config_version: 1,
  id: 'c-sys',
  client_id: 'cid-system',
  app_id: 'app-1',
  name: '系统内置客户端',
  kind: 'public',
  status: 'active',
  system: true,
  previous_secret_expires_at: 0,
  redirect_uris: [],
  allowed_scopes: ['openid'],
  allowed_audiences: [],
  allowed_introspection_audiences: [],
  grant_types: ['authorization_code'],
  created_at: '2026-09-01T08:00:00Z',
}

const normalClient: Client = {
  ...systemClient,
  id: 'c-2',
  client_id: 'cid-normal',
  name: '业务客户端',
  system: undefined,
}

function FakeAppLayout() {
  return <Outlet context={{ app, reloadApp: vi.fn() }} />
}

const appScopedAdmin = () =>
  makeSession({
    operator: { id: 'op-9', email: 'app-admin@example.com', role: 'admin', app_id: 'app-1' },
  })

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('App 级管理员的 UI 隐藏', () => {
  it('应用列表：不展示创建应用入口（表头与空态均无）', async () => {
    stubFetch(() => jsonResponse({ items: [app], next_cursor: null }))
    renderWithSession(
      <Routes>
        <Route path="/apps" element={<AppsPage />} />
      </Routes>,
      appScopedAdmin(),
      ['/apps'],
    )
    expect(await screen.findByText('交易前台')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '创建应用' })).not.toBeInTheDocument()
  })

  it('应用概览：App 级 admin 无暂停/恢复，也无密钥轮换', async () => {
    renderWithSession(
      <Routes>
        <Route path="/apps/:appId" element={<FakeAppLayout />}>
          <Route index element={<OverviewTab />} />
        </Route>
      </Routes>,
      appScopedAdmin(),
      ['/apps/app-1'],
    )
    expect(await screen.findByRole('heading', { name: '概览' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '暂停应用' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '轮换密钥' })).not.toBeInTheDocument()
  })

  it('应用概览：App 级 owner 可轮换密钥，但仍无平台级暂停/恢复', async () => {
    renderWithSession(
      <Routes>
        <Route path="/apps/:appId" element={<FakeAppLayout />}>
          <Route index element={<OverviewTab />} />
        </Route>
      </Routes>,
      makeSession({
        operator: { id: 'op-8', email: 'owner@example.com', role: 'owner', app_id: 'app-1' },
      }),
      ['/apps/app-1'],
    )
    expect(await screen.findByRole('heading', { name: '概览' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '轮换密钥' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '暂停应用' })).not.toBeInTheDocument()
  })

  it('应用概览：平台级 admin 保留暂停与轮换', async () => {
    renderWithSession(
      <Routes>
        <Route path="/apps/:appId" element={<FakeAppLayout />}>
          <Route index element={<OverviewTab />} />
        </Route>
      </Routes>,
      makeSession(),
      ['/apps/app-1'],
    )
    expect(await screen.findByRole('heading', { name: '概览' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '暂停应用' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '轮换密钥' })).toBeInTheDocument()
  })

  it('客户端页：App 级管理员访问非本人 App 时隐藏写入口', async () => {
    stubFetch(() => jsonResponse({ items: [normalClient], next_cursor: null }))
    renderWithSession(
      <Routes>
        <Route path="/apps/:appId" element={<FakeAppLayout />}>
          <Route path="clients" element={<ClientsTab />} />
        </Route>
      </Routes>,
      makeSession({ operator: { id: 'op-7', email: 'a@b.c', role: 'admin', app_id: 'app-other' } }),
      ['/apps/app-1/clients'],
    )
    expect(await screen.findByText('业务客户端')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '创建客户端' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '编辑' })).not.toBeInTheDocument()
  })

  it('客户端页：系统内置客户端展示受保护标识且无编辑入口', async () => {
    stubFetch(() => jsonResponse({ items: [systemClient, normalClient], next_cursor: null }))
    renderWithSession(
      <Routes>
        <Route path="/apps/:appId" element={<FakeAppLayout />}>
          <Route path="clients" element={<ClientsTab />} />
        </Route>
      </Routes>,
      makeSession(),
      ['/apps/app-1/clients'],
    )
    expect(await screen.findByText('系统内置客户端')).toBeInTheDocument()
    expect(screen.getByText('系统')).toBeInTheDocument()
    // 仅普通客户端可编辑，系统客户端不渲染编辑按钮
    await waitFor(() => expect(screen.getAllByRole('button', { name: '编辑' })).toHaveLength(1))
  })
})
