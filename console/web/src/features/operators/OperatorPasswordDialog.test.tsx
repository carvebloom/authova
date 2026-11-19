import { render, screen, waitFor } from '@testing-library/react'
import { Link, RouterProvider, createMemoryRouter } from 'react-router-dom'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import {
  errorResponse,
  jsonResponse,
  makeSession,
  renderWithSession,
  stubFetch,
} from '../../test/utils'
import { PlatformOperatorsPage } from './PlatformOperatorsPage'
import { ProtectionProvider } from '../../app/protection'
import { SessionContext } from '../session/context'
import { OperatorPasswordDialog } from './OperatorPasswordDialog'

const owner = { id: 'owner', email: 'owner@example.test', role: 'owner', status: 'active' }
const target = { id: 'target', email: 'admin@example.test', role: 'admin', status: 'active' }
const password = 'a new private password phrase'
function renderPage(session = makeSession({ operator: owner })) {
  return renderWithSession(<PlatformOperatorsPage />, session)
}
async function fill(confirmation = password) {
  const user = userEvent.setup()
  await user.type(screen.getByLabelText('你当前的登录密码'), 'current actor password')
  await user.type(screen.getByLabelText('新密码'), password)
  await user.type(screen.getByLabelText('确认新密码'), confirmation)
  await user.type(screen.getByLabelText('操作原因'), 'approved rotation')
  return user
}
afterEach(() => vi.unstubAllGlobals())

it('keeps an unknown password outcome protected after closing the dialog and attempting route navigation', async () => {
  const { calls } = stubFetch((call) =>
    call.method === 'GET'
      ? jsonResponse({ items: [target], next_cursor: null })
      : errorResponse(503, { code: 'outcome_unknown' }),
  )
  const router = createMemoryRouter(
    [
      {
        path: '*',
        element: (
          <SessionContext.Provider value={makeSession({ operator: owner })}>
            <ProtectionProvider>
              <Link to="/apps">应用列表</Link>
              <PlatformOperatorsPage />
            </ProtectionProvider>
          </SessionContext.Provider>
        ),
      },
    ],
    { initialEntries: ['/platform/operators'] },
  )
  render(<RouterProvider router={router} />)
  await userEvent.click(await screen.findByRole('button', { name: '重置密码' }))
  const user = await fill()
  await user.click(screen.getByRole('button', { name: '确认修改密码' }))
  await screen.findByText(/改密结果未知/)
  await user.click(screen.getByText('关闭', { selector: 'button' }))
  await user.click(screen.getByRole('link', { name: '应用列表' }))
  await waitFor(() => expect(router.state.location.pathname).toBe('/platform/operators'))
  await user.click(screen.getByRole('button', { name: '重置密码' }))
  expect(screen.getByRole('button', { name: '确认修改密码' })).toBeDisabled()
  expect(calls.filter((call) => call.method === 'PATCH')).toHaveLength(1)
})

it('ignores a late success after the initiating actor changes', async () => {
  let finish!: (value: Response) => void
  const { calls } = stubFetch(
    () =>
      new Promise<Response>((resolve) => {
        finish = resolve
      }),
  )
  const updated = vi.fn()
  const unknown = vi.fn()
  const dialog = (
    <OperatorPasswordDialog
      target={target}
      self={false}
      previousUnknown={false}
      onUnknown={unknown}
      onClose={vi.fn()}
      onUpdated={updated}
    />
  )
  const page = render(
    <SessionContext.Provider value={makeSession({ operator: owner })}>
      {dialog}
    </SessionContext.Provider>,
  )
  const user = await fill()
  await user.click(screen.getByRole('button', { name: '确认修改密码' }))
  await waitFor(() => expect(calls).toHaveLength(1))
  page.rerender(
    <SessionContext.Provider value={makeSession({ operator: target })}>
      {dialog}
    </SessionContext.Provider>,
  )
  finish(jsonResponse({ updated: true }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: '确认修改密码' })).not.toBeDisabled(),
  )
  expect(updated).not.toHaveBeenCalled()
  expect(unknown).not.toHaveBeenCalled()
  expect(calls).toHaveLength(1)
})

it('owner resets only the selected account with actor proof, without changing role/MFA', async () => {
  const { calls } = stubFetch((call) =>
    call.method === 'GET'
      ? jsonResponse({ items: [target], next_cursor: null })
      : jsonResponse({ updated: true }),
  )
  renderPage()
  await userEvent.click(await screen.findByRole('button', { name: '重置密码' }))
  const user = await fill()
  await user.click(screen.getByRole('button', { name: '确认修改密码' }))
  expect(await screen.findByText('密码已重置，目标账号须重新登录。')).toBeInTheDocument()
  expect(calls.filter((call) => call.method === 'PATCH')).toEqual([
    expect.objectContaining({
      url: '/bff/platform/operators/target',
      body: { current_password: 'current actor password', password, reason: 'approved rotation' },
    }),
  ])
  expect(localStorage.length).toBe(0)
  expect(sessionStorage.length).toBe(0)
})

it('admin can change self, requiring confirmation before sending; refreshes revoked self session', async () => {
  const session = makeSession({ operator: target })
  const { calls } = stubFetch((call) =>
    call.method === 'GET'
      ? jsonResponse({ items: [owner], next_cursor: null })
      : jsonResponse({ updated: true }),
  )
  renderPage(session)
  await userEvent.click(await screen.findByRole('button', { name: '修改我的密码' }))
  const user = await fill('different password')
  await user.click(screen.getByRole('button', { name: '确认修改密码' }))
  expect(await screen.findByText('两次输入的新密码不一致')).toBeInTheDocument()
  expect(calls.filter((call) => call.method === 'PATCH')).toHaveLength(0)
  await user.clear(screen.getByLabelText('确认新密码'))
  await user.type(screen.getByLabelText('确认新密码'), password)
  await user.click(screen.getByRole('button', { name: '确认修改密码' }))
  await waitFor(() => expect(session.retryBootstrap).toHaveBeenCalledOnce())
  expect(calls.find((call) => call.method === 'PATCH')?.url).toBe('/bff/platform/operators/target')
  expect(screen.queryByRole('button', { name: '重置密码' })).not.toBeInTheDocument()
})

it('unknown result clears secrets and remains locked after closing/reopening, without resubmission', async () => {
  const { calls } = stubFetch((call) =>
    call.method === 'GET'
      ? jsonResponse({ items: [target], next_cursor: null })
      : errorResponse(503, { code: 'outcome_unknown' }),
  )
  renderPage()
  await userEvent.click(await screen.findByRole('button', { name: '重置密码' }))
  const user = await fill()
  await user.dblClick(screen.getByRole('button', { name: '确认修改密码' }))
  expect(await screen.findByText(/改密结果未知/)).toBeInTheDocument()
  expect(screen.getByLabelText('新密码')).toHaveValue('')
  expect(screen.getByLabelText('你当前的登录密码')).toHaveValue('')
  expect(screen.getByRole('button', { name: '确认修改密码' })).toBeDisabled()
  await user.click(screen.getByText('关闭', { selector: 'button' }))
  await user.click(screen.getByRole('button', { name: '重置密码' }))
  expect(screen.getByRole('button', { name: '确认修改密码' })).toBeDisabled()
  expect(calls.filter((call) => call.method === 'PATCH')).toHaveLength(1)
})

it('a malformed success acknowledgement does not report success or repeat the password request', async () => {
  stubFetch((call) =>
    call.method === 'GET'
      ? jsonResponse({ items: [], next_cursor: null })
      : jsonResponse({ updated: false }),
  )
  const session = makeSession({ operator: owner })
  renderPage(session)
  await userEvent.click(await screen.findByRole('button', { name: '修改我的密码' }))
  const user = await fill()
  await user.click(screen.getByRole('button', { name: '确认修改密码' }))
  expect(await screen.findByText(/改密结果未知/)).toBeInTheDocument()
  expect(session.retryBootstrap).not.toHaveBeenCalled()
})

it('auditors and app-scoped operators have no password management entry', async () => {
  const { calls } = stubFetch(() => jsonResponse({ items: [owner], next_cursor: null }))
  const { unmount } = renderPage(makeSession({ operator: { ...target, role: 'auditor' } }))
  await screen.findByText(owner.email)
  expect(screen.queryByRole('button', { name: /密码/ })).not.toBeInTheDocument()
  unmount()
  calls.length = 0
  renderPage(makeSession({ operator: { ...owner, app_id: 'app-1' } }))
  expect(screen.queryByRole('button', { name: /密码/ })).not.toBeInTheDocument()
  expect(calls).toHaveLength(0)
})
