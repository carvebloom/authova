import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Outlet, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { App, Client, CreatedClient } from '../../api/types'
import {
  errorResponse,
  jsonResponse,
  makeSession,
  renderWithSession,
  stubFetch,
} from '../../test/utils'
import { ClientsTab } from './ClientsTab'

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

const publicClient: Client = {
  config_version: 1,
  id: 'c-1',
  client_id: 'cid-public-1',
  app_id: 'app-1',
  name: '移动端',
  kind: 'public',
  status: 'active',
  previous_secret_expires_at: 0,
  redirect_uris: ['https://m.example.com/cb'],
  allowed_scopes: ['openid'],
  allowed_audiences: ['https://api.example.com'],
  allowed_introspection_audiences: [],
  grant_types: ['authorization_code'],
  created_at: '2026-09-02T08:00:00Z',
}

function FakeAppLayout() {
  return <Outlet context={{ app, reloadApp: vi.fn() }} />
}

function renderClients(session = makeSession()) {
  renderWithSession(
    <Routes>
      <Route path="/apps/:appId" element={<FakeAppLayout />}>
        <Route path="clients" element={<ClientsTab />} />
      </Route>
    </Routes>,
    session,
    ['/apps/app-1/clients'],
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ClientsTab', () => {
  it('机密客户端创建成功后一次性展示 client_secret，关闭后消失', async () => {
    const user = userEvent.setup()
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })

    const { calls } = stubFetch((call) => {
      if (call.method === 'POST') {
        const created: CreatedClient = {
          ...publicClient,
          id: 'c-2',
          client_id: 'cid-secret-1',
          name: '服务端',
          kind: 'confidential',
          client_secret: 'secret-one-time-9',
        }
        return jsonResponse(created)
      }
      return jsonResponse({ items: [publicClient], next_cursor: null })
    })

    renderClients()
    await user.click(await screen.findByRole('button', { name: '创建客户端' }))

    await user.type(
      screen.getByLabelText('允许的受众（allowed_audiences）'),
      'https://api.example.com',
    )
    await user.type(screen.getByLabelText('名称'), '服务端')
    await user.selectOptions(screen.getByLabelText('类型'), 'confidential')
    await user.type(screen.getByLabelText(/回调地址/), 'https://api.example.com/cb')
    await user.type(screen.getByLabelText(/授权类型/), 'authorization_code')
    await user.click(screen.getByRole('button', { name: '创建' }))

    // 创建请求携带契约要求的幂等键
    const create = calls.find((c) => c.method === 'POST')
    expect(create?.headers['Idempotency-Key']).toBeTruthy()

    // 一次性密钥提示
    expect(await screen.findByText('客户端密钥仅此一次显示')).toBeInTheDocument()
    expect(screen.getByText('secret-one-time-9')).toBeInTheDocument()

    // 复制按钮调用剪贴板
    await user.click(screen.getByRole('button', { name: '复制密钥' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('secret-one-time-9'))

    // 关闭后不再展示
    await user.click(screen.getByRole('button', { name: '我已保存，关闭' }))
    await waitFor(() => expect(screen.queryByText('secret-one-time-9')).not.toBeInTheDocument())
  })

  it('公开客户端创建成功不展示密钥提示', async () => {
    const user = userEvent.setup()
    stubFetch((call) => {
      if (call.method === 'POST') return jsonResponse(publicClient)
      return jsonResponse({ items: [], next_cursor: null })
    })
    renderClients()
    await user.click(await screen.findByRole('button', { name: '创建客户端' }))
    await user.type(
      screen.getByLabelText('允许的受众（allowed_audiences）'),
      'https://api.example.com',
    )
    await user.type(screen.getByLabelText('名称'), '移动端')
    await user.click(screen.getByRole('button', { name: '创建' }))
    await waitFor(() =>
      expect(screen.queryByText('客户端密钥仅此一次显示')).not.toBeInTheDocument(),
    )
  })

  it('非法回调地址触发字段错误且不发请求', async () => {
    const user = userEvent.setup()
    const { calls } = stubFetch(() => jsonResponse({ items: [publicClient], next_cursor: null }))
    renderClients()
    await screen.findByRole('button', { name: '创建客户端' })
    expect(screen.getByText('移动端')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '创建客户端' }))
    await user.type(
      screen.getByLabelText('允许的受众（allowed_audiences）'),
      'https://api.example.com',
    )
    await user.type(screen.getByLabelText('名称'), '坏客户端')
    await user.type(screen.getByLabelText(/回调地址/), 'ftp://invalid')
    await user.click(screen.getByRole('button', { name: '创建' }))
    expect(await screen.findByText(/回调地址必须以 http/)).toBeInTheDocument()
    expect(calls.every((c) => c.method === 'GET')).toBe(true)
  })

  it('编辑客户端走 PATCH 且不修改类型', async () => {
    const user = userEvent.setup()
    const { calls } = stubFetch((call) => {
      if (call.method === 'PATCH') return jsonResponse({ ...publicClient, name: '移动端 v2' })
      return jsonResponse({ items: [publicClient], next_cursor: null })
    })
    renderClients()
    await user.click(await screen.findByRole('button', { name: '编辑' }))
    const nameInput = screen.getByLabelText('名称')
    expect(nameInput).toHaveValue('移动端')
    expect(screen.queryByLabelText('类型')).not.toBeInTheDocument()

    await user.clear(nameInput)
    await user.type(nameInput, '移动端 v2')
    await user.click(screen.getByRole('button', { name: '保存' }))

    await waitFor(() => {
      const patch = calls.find((c) => c.method === 'PATCH')
      expect(patch?.url).toBe('/bff/apps/app-1/clients/cid-public-1')
      expect(patch?.body).toMatchObject({ name: '移动端 v2' })
      expect(patch?.body).not.toHaveProperty('kind')
    })
  })

  it('审计员不展示创建与编辑入口', async () => {
    stubFetch(() => jsonResponse({ items: [publicClient], next_cursor: null }))
    renderClients(
      makeSession({
        operator: { id: 'op-2', email: 'audit@example.com', role: 'auditor' },
        canMutate: false,
      }),
    )
    expect(await screen.findByText('移动端')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '创建客户端' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '编辑' })).not.toBeInTheDocument()
  })

  it('创建结果未知：关闭对话框后原请求键与内容保留，重开用原键重试并拿回一次性密钥', async () => {
    const user = userEvent.setup()
    let failOnce = true
    const { calls } = stubFetch((call) => {
      if (call.method === 'POST') {
        if (failOnce) {
          failOnce = false
          return errorResponse(503, { code: 'outcome_unknown', detail: 'core 转发失败' })
        }
        // 幂等回放：服务端按原键返回首次创建的结果（含一次性密钥）
        return jsonResponse({
          ...publicClient,
          id: 'c-9',
          client_id: 'cid-9',
          name: '服务端',
          kind: 'confidential',
          client_secret: 'secret-replayed-1',
        })
      }
      return jsonResponse({ items: [], next_cursor: null })
    })
    renderClients()
    await user.click(await screen.findByRole('button', { name: '创建客户端' }))
    await user.type(
      screen.getByLabelText('允许的受众（allowed_audiences）'),
      'https://api.example.com',
    )
    await user.type(screen.getByLabelText('名称'), '服务端')
    await user.selectOptions(screen.getByLabelText('类型'), 'confidential')
    await user.type(screen.getByLabelText(/回调地址/), 'https://api.example.com/cb')
    await user.click(screen.getByRole('button', { name: '创建' }))
    expect(await screen.findByText('core 转发失败')).toBeInTheDocument()

    // 直接关闭对话框：未确认请求保留在页面提示中，并说明 10 分钟密钥回放窗口
    await user.click(screen.getByRole('button', { name: '取消' }))
    expect(await screen.findByText(/未确认的创建请求：客户端「服务端」/)).toBeInTheDocument()
    expect(screen.getByText(/10 分钟内随幂等回放/)).toBeInTheDocument()

    // 重新打开：预填原内容，重试复用原幂等键
    await user.click(screen.getByRole('button', { name: '处理未确认的创建' }))
    expect(screen.getByLabelText('名称')).toHaveValue('服务端')
    await user.click(screen.getByRole('button', { name: '重试原请求' }))

    expect(await screen.findByText('客户端密钥仅此一次显示')).toBeInTheDocument()
    expect(screen.getByText('secret-replayed-1')).toBeInTheDocument()
    expect(screen.queryByText(/未确认的创建请求/)).not.toBeInTheDocument()

    const posts = calls.filter((c) => c.method === 'POST')
    expect(posts).toHaveLength(2)
    expect(posts[1]?.headers['Idempotency-Key']).toBe(posts[0]?.headers['Idempotency-Key'])
  })

  it('列表接口失败展示错误与重试', async () => {
    stubFetch(() =>
      jsonResponse(
        {
          type: 'about:blank',
          title: 'Forbidden',
          status: 403,
          detail: '无权限',
          code: 'forbidden',
          request_id: 'req-403',
        },
        403,
      ),
    )
    renderClients()
    expect(await screen.findByText('无权限')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
  })
})
