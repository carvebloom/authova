import { describe, expect, it, vi } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Outlet, Route, Routes } from 'react-router-dom'
import { ClientsTab } from './ClientsTab'
import {
  errorResponse,
  jsonResponse,
  makeSession,
  renderWithSession,
  stubFetch,
} from '../../test/utils'
import type { App, Client } from '../../api/types'

const app: App = {
  id: 'app',
  app_key: 'key',
  name: 'App',
  slug: 'app',
  issuer: 'https://auth.test/a/key',
  status: 'active',
  config_version: 1,
  require_mfa: false,
  created_at: '2026-10-15T00:00:00Z',
}
const client: Client = {
  id: 'id',
  app_id: 'app',
  client_id: 'client',
  name: 'Client',
  kind: 'public',
  status: 'active',
  config_version: 1,
  previous_secret_expires_at: 0,
  redirect_uris: ['https://rp.test/cb'],
  allowed_scopes: ['openid'],
  allowed_audiences: ['https://api.example.com'],
  allowed_introspection_audiences: [],
  grant_types: ['authorization_code'],
  created_at: app.created_at,
}
function Layout() {
  return <Outlet context={{ app, reloadApp: vi.fn().mockResolvedValue(true) }} />
}

describe('client mutation reconciliation', () => {
  it('keeps the form frozen after a failed read-back and submits only the newly read version', async () => {
    const user = userEvent.setup()
    let reads = 0
    let writes = 0
    const { calls } = stubFetch((call) => {
      if (call.method === 'PATCH') {
        writes++
        return writes === 1
          ? errorResponse(409, { code: 'conflict' })
          : jsonResponse({ ...client, name: 'Revised', config_version: 3 })
      }
      if (call.url.endsWith('/clients/client')) {
        reads++
        return reads === 1
          ? errorResponse(503, { code: 'dependency_unavailable' })
          : jsonResponse({ ...client, config_version: 2 })
      }
      return jsonResponse({ items: [client], next_cursor: null })
    })
    renderWithSession(
      <Routes>
        <Route path="/apps/:appId" element={<Layout />}>
          <Route path="clients" element={<ClientsTab />} />
        </Route>
      </Routes>,
      makeSession(),
      ['/apps/app/clients'],
    )
    await user.click(await screen.findByRole('button', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))
    expect(within(dialog).getByRole('button', { name: '保存' })).toBeDisabled()
    await user.click(within(dialog).getByRole('button', { name: '重新加载最新配置' }))
    expect(within(dialog).getByRole('button', { name: '保存' })).toBeDisabled()
    expect(writes).toBe(1)
    await user.click(within(dialog).getByRole('button', { name: '重新加载最新配置' }))
    await user.clear(within(dialog).getByLabelText('名称'))
    await user.type(within(dialog).getByLabelText('名称'), 'Revised')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))
    const last = calls.filter((call) => call.method === 'PATCH').at(-1)
    expect(last?.body).toMatchObject({ config_version: 2, name: 'Revised' })
    expect(writes).toBe(2)
  })
})
