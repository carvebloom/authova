import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Outlet, Route, Routes } from 'react-router-dom'
import type { App, AuthenticationConfig, SigningKeyPage } from '../../api/types'
import {
  errorResponse,
  jsonResponse,
  makeSession,
  renderWithSession,
  stubFetch,
} from '../../test/utils'
import { OverviewTab } from './OverviewTab'

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

const authConfig: AuthenticationConfig = {
  password_login: true,
  email_otp_configured: true,
  phone_otp_configured: true,
  passkey_available: true,
  policy: {
    registration_enabled: true,
    password_login_enabled: true,
    email_otp_enabled: true,
    phone_otp_enabled: false,
    federation_enabled: true,
    external_registration_enabled: true,
    passkey_enabled: false,
  },
  config_version: 2,
  provider_types: ['oidc'],
  federation_callback_url: 'https://auth.example.com/trade-web/federation/callback',
}

const NOW_S = Math.floor(Date.now() / 1000)

const keysPage: SigningKeyPage = {
  items: [
    {
      key_id: 'kid-current-0001',
      current: true,
      created_at: NOW_S - 86400,
      retire_at: 0,
      revoked_at: 0,
    },
    {
      key_id: 'kid-retiring-0002',
      current: false,
      created_at: NOW_S - 172800,
      retire_at: NOW_S + 86400,
      revoked_at: 0,
    },
    {
      key_id: 'kid-revoked-0003',
      current: false,
      created_at: NOW_S - 259200,
      retire_at: 0,
      revoked_at: NOW_S - 3600,
    },
  ],
  config_version: 9,
}

const KEYS_URL = '/bff/apps/app-1/keys'
const REVOKE_URL = `/bff/apps/app-1/keys/${keysPage.items[0]!.key_id}/revoke`

function FakeAppLayout() {
  return <Outlet context={{ app, reloadApp: vi.fn().mockResolvedValue(true) }} />
}

function renderOverview(session = makeSession()) {
  renderWithSession(
    <Routes>
      <Route path="/apps/:appId" element={<FakeAppLayout />}>
        <Route index element={<OverviewTab />} />
      </Route>
    </Routes>,
    session,
    ['/apps/app-1'],
  )
}

/** 概览的四个读取源 + 写操作桩 */
function stubOverviewApi(
  options: {
    keys?: SigningKeyPage | (() => Response)
    onRevoke?: () => Response
    onRotate?: () => Response
  } = {},
) {
  let keysReads = 0
  const { calls } = stubFetch((call) => {
    if (call.url === REVOKE_URL) {
      return options.onRevoke
        ? options.onRevoke()
        : jsonResponse({
            key_id: 'kid-current-0001',
            new_key_id: 'kid-new-0004',
            config_version: 10,
          })
    }
    if (call.url === `${KEYS_URL}/rotate`) {
      return options.onRotate ? options.onRotate() : jsonResponse(null, 204)
    }
    if (call.url === KEYS_URL) {
      keysReads += 1
      if (typeof options.keys === 'function') return options.keys()
      return jsonResponse(options.keys ?? keysPage)
    }
    if (call.url.endsWith('/authentication-config')) return jsonResponse(authConfig)
    return jsonResponse({ items: [], next_cursor: null })
  })
  return { calls, keysReads: () => keysReads }
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('OverviewTab 签名密钥', () => {
  it('展示密钥列表与状态；常规轮换文案为 24 小时保留，不再宣称立即失效', async () => {
    const user = userEvent.setup()
    stubOverviewApi()
    renderOverview()

    // 列表与状态徽标（表头列名相同，徽标含具体时间）
    expect(await screen.findByText('当前签名')).toBeInTheDocument()
    expect(screen.getByText(/验签保留至 \d{4}/)).toBeInTheDocument()
    expect(screen.getByText('已吊销')).toBeInTheDocument()
    // 紧急吊销：已吊销的密钥不再提供操作
    expect(screen.getAllByRole('button', { name: '紧急吊销' })).toHaveLength(2)

    // 危险操作区的常规轮换不再宣称“立即失效”
    expect(screen.getByText(/常规密钥轮换保留旧验证密钥/)).toBeInTheDocument()
    expect(screen.queryByText(/密钥轮换使旧密钥立即失效/)).not.toBeInTheDocument()
    expect(screen.queryByText(/轮换后旧密钥将失效/)).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '轮换密钥' }))
    const dialog = screen.getByRole('dialog')
    expect(dialog).toHaveTextContent(/旧验证密钥保留 24 小时后退役/)
    expect(dialog).toHaveTextContent(/紧急吊销/)
    await user.click(within(dialog).getByRole('button', { name: '取消' }))
  })

  it('紧急吊销当前签名密钥：确认 + 原因 + config_version，成功后提示继任密钥并读回', async () => {
    const user = userEvent.setup()
    const { calls, keysReads } = stubOverviewApi()
    renderOverview()

    const readsBefore = keysReads()
    const revokeButtons = await screen.findAllByRole('button', { name: '紧急吊销' })
    await user.click(revokeButtons[0]!)

    const dialog = screen.getByRole('dialog')
    // 吊销当前签名密钥的语义必须显式呈现
    expect(dialog).toHaveTextContent(/当前签名密钥/)
    expect(dialog).toHaveTextContent(/从 JWKS 移除/)
    expect(dialog).toHaveTextContent(/离线缓存了 JWKS/)
    await user.type(within(dialog).getByLabelText('操作原因'), '密钥疑似泄露')
    await user.click(within(dialog).getByRole('button', { name: '确认紧急吊销' }))

    await waitFor(() => {
      const post = calls.find((c) => c.url === REVOKE_URL)
      expect(post?.method).toBe('POST')
      expect(post?.body).toEqual({ config_version: 9, reason: '密钥疑似泄露' })
    })
    expect(await screen.findByText(/新签名密钥 .* 已启用/)).toBeInTheDocument()
    // 成功后重新读回密钥列表
    await waitFor(() => expect(keysReads()).toBeGreaterThan(readsBefore + 0))
  })

  it('吊销 409：冻结确认，GET /keys 权威读回后才能再次操作', async () => {
    const user = userEvent.setup()
    let posts = 0
    const { calls, keysReads } = stubOverviewApi({
      onRevoke: () => {
        posts += 1
        return errorResponse(409, { code: 'version_conflict', detail: 'config_version 已过期' })
      },
    })
    renderOverview()

    const revokeButtons = await screen.findAllByRole('button', { name: '紧急吊销' })
    await user.click(revokeButtons[0]!)
    const dialog = screen.getByRole('dialog')
    await user.type(within(dialog).getByLabelText('操作原因'), '密钥疑似泄露')
    await user.click(within(dialog).getByRole('button', { name: '确认紧急吊销' }))

    expect(await within(dialog).findByText(/409 冲突/)).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: '确认紧急吊销' })).toBeDisabled()
    expect(posts).toBe(1)

    const readsBefore = keysReads()
    await user.click(within(dialog).getByRole('button', { name: '重新加载最新状态' }))
    // 读回走 GET /keys（权威核对），解除冻结但不自动重试
    await waitFor(() => expect(keysReads()).toBeGreaterThan(readsBefore))
    await waitFor(() =>
      expect(within(dialog).getByRole('button', { name: '确认紧急吊销' })).toBeEnabled(),
    )
    expect(calls.filter((c) => c.url === REVOKE_URL)).toHaveLength(1)
  })

  it('auditor：密钥列表只读，无紧急吊销与危险操作入口', async () => {
    stubOverviewApi()
    renderOverview(
      makeSession({ operator: { id: 'op-3', email: 'a@b.c', role: 'auditor', app_id: 'app-1' } }),
    )
    expect(await screen.findByText('当前签名')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '紧急吊销' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '轮换密钥' })).not.toBeInTheDocument()
    expect(screen.queryByText('危险操作')).not.toBeInTheDocument()
  })
})

it('恢复隔离应用不提供普通恢复及密钥变更入口', async () => {
  stubOverviewApi()
  function QuarantinedLayout() {
    return <Outlet context={{ app: { ...app, status: 'quarantined' }, reloadApp: vi.fn() }} />
  }
  renderWithSession(
    <Routes>
      <Route path="/apps/:appId" element={<QuarantinedLayout />}>
        <Route index element={<OverviewTab />} />
      </Route>
    </Routes>,
    makeSession(),
    ['/apps/app-1'],
  )
  expect(await screen.findByText(/该应用处于恢复隔离/)).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '恢复应用' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '暂停应用' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /轮换签名密钥/ })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /紧急吊销/ })).not.toBeInTheDocument()
})
