import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import type { Client } from '../../api/types'
import { jsonResponse, makeSession, renderWithSession, stubFetch } from '../../test/utils'
import { ClientFormDialog } from './ClientFormDialog'
const client: Client = {
  id: 'id',
  client_id: 'client',
  app_id: 'app',
  name: 'Service',
  kind: 'confidential',
  status: 'active',
  redirect_uris: [],
  allowed_scopes: ['openid', 'custom:read'],
  allowed_audiences: ['urn:example:api'],
  allowed_introspection_audiences: [],
  grant_types: ['authorization_code', 'refresh_token'],
  config_version: 3,
  created_at: '2026-01-01T00:00:00Z',
  previous_secret_expires_at: 0,
}
function renderForm(value: Client | null) {
  renderWithSession(
    <ClientFormDialog
      open
      appId="app"
      client={value}
      pending={null}
      onClose={vi.fn()}
      onUnknown={vi.fn()}
      onCreated={vi.fn()}
      onUpdated={vi.fn()}
    />,
    makeSession(),
  )
}
afterEach(() => vi.unstubAllGlobals())
it('edits grants with checkboxes and preserves custom scopes plus optimistic version', async () => {
  const { calls } = stubFetch(() => jsonResponse({ updated: true }))
  renderForm(client)
  expect(screen.queryByRole('textbox', { name: /grant_types/ })).not.toBeInTheDocument()
  expect(screen.getByRole('checkbox', { name: /authorization_code/ })).toBeChecked()
  expect(screen.getByRole('checkbox', { name: /refresh_token/ })).toBeChecked()
  const user = userEvent.setup()
  await user.click(screen.getByRole('checkbox', { name: /authorization_code/ }))
  await user.click(screen.getByRole('checkbox', { name: /client_credentials/ }))
  await user.click(screen.getByRole('button', { name: '保存' }))
  expect(calls[0]?.body).toEqual(
    expect.objectContaining({
      grant_types: ['refresh_token', 'client_credentials'],
      allowed_scopes: ['openid', 'custom:read'],
      config_version: 3,
    }),
  )
})
it('public clients cannot select client_credentials; an empty selection is rejected locally', async () => {
  const { calls } = stubFetch(() => jsonResponse({ updated: true }))
  renderForm({ ...client, kind: 'public', grant_types: ['authorization_code'] })
  expect(screen.getByRole('checkbox', { name: /client_credentials/ })).toBeDisabled()
  const user = userEvent.setup()
  await user.click(screen.getByRole('checkbox', { name: /authorization_code/ }))
  await user.click(screen.getByRole('button', { name: '保存' }))
  expect(await screen.findByText('至少选择一个授权类型')).toBeInTheDocument()
  expect(calls).toHaveLength(0)
})
it('switching a new confidential client to public requires removal of incompatible grants', async () => {
  const { calls } = stubFetch(() => jsonResponse({ updated: true }))
  renderForm(null)
  const user = userEvent.setup()
  await user.type(screen.getByLabelText('名称'), 'new app')
  await user.type(screen.getByLabelText(/允许的受众（/), 'urn:example:api')
  await user.selectOptions(screen.getByLabelText('类型'), 'confidential')
  await user.click(screen.getByRole('checkbox', { name: /client_credentials/ }))
  await user.selectOptions(screen.getByLabelText('类型'), 'public')
  await user.click(screen.getByRole('button', { name: '创建' }))
  expect(
    await screen.findByText('公开客户端不能使用客户端凭据授权，请取消该选项'),
  ).toBeInTheDocument()
  expect(screen.getByRole('checkbox', { name: /client_credentials/ })).toBeEnabled()
  await user.click(screen.getByRole('checkbox', { name: /client_credentials/ }))
  expect(screen.getByRole('checkbox', { name: /client_credentials/ })).toBeDisabled()
  expect(calls).toHaveLength(0)
})
