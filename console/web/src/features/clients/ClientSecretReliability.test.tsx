import { StrictMode } from 'react'
import { afterEach, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ClientSecretDialog } from './ClientSecretDialog'
import type { Client } from '../../api/types'
import { errorResponse, jsonResponse, stubFetch } from '../../test/utils'
import { rotateClientSecret } from '../../api/endpoints'
import { setCsrfToken } from '../../api/client'

const client: Client = {
  id: 'row',
  app_id: 'app-1',
  client_id: 'client-1',
  name: 'Integration',
  kind: 'confidential',
  status: 'active',
  config_version: 7,
  previous_secret_expires_at: 0,
  redirect_uris: [],
  allowed_scopes: ['api.read'],
  allowed_audiences: ['urn:test'],
  allowed_introspection_audiences: [],
  grant_types: ['client_credentials'],
}
const rotated = {
  client: { ...client, config_version: 8 },
  client_secret: 'test-fixture-once-only',
  previous_secret_expires_at: 0,
}
function dialog(strict = false) {
  const element = (
    <ClientSecretDialog
      appId="app-1"
      client={client}
      onClose={vi.fn()}
      onChanged={vi.fn()}
      onRotated={vi.fn()}
    />
  )
  return render(strict ? <StrictMode>{element}</StrictMode> : element)
}
afterEach(() => {
  vi.unstubAllGlobals()
  setCsrfToken(null)
})

it('initial authoritative read completes under the application StrictMode', async () => {
  stubFetch(() => jsonResponse(client))
  dialog(true)
  expect(await screen.findByLabelText(/旧密钥宽限期（秒）/)).toBeEnabled()
})
it('a failed initial read cannot authorize rotation from the stale list snapshot', async () => {
  stubFetch(() => errorResponse(503, { code: 'dependency_unavailable' }))
  dialog()
  await screen.findByText(/dependency_unavailable/)
  expect(screen.getByRole('button', { name: '确认轮换' })).toBeDisabled()
})
it('an empty grace field cannot silently become immediate credential cutover', async () => {
  setCsrfToken('test')
  const user = userEvent.setup()
  const { calls } = stubFetch((call) => jsonResponse(call.method === 'GET' ? client : rotated))
  dialog()
  const input = await screen.findByLabelText(/旧密钥宽限期（秒）/)
  await user.clear(input)
  await user.type(screen.getByLabelText(/操作原因/), 'Test explicit validation')
  await user.click(screen.getByRole('button', { name: '确认轮换' }))
  await waitFor(() => expect(calls.filter((call) => call.method === 'POST')).toHaveLength(0))
})
it.each([
  {},
  { ...rotated, client: { ...rotated.client, app_id: 'another-app' } },
  { ...rotated, client_secret: '' },
])(
  'a malformed successful rotation response is outcome_unknown, never a confirmed secret',
  async (payload) => {
    setCsrfToken('test')
    stubFetch(() => jsonResponse(payload))
    await expect(
      rotateClientSecret('app-1', 'client-1', {
        config_version: 7,
        reason: 'Test malformed acknowledgement',
        grace_seconds: 0,
      }),
    ).rejects.toMatchObject({ code: 'outcome_unknown' })
  },
)
