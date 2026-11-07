import { describe, expect, it } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Link, RouterProvider, createMemoryRouter } from 'react-router-dom'
import { useState } from 'react'
import { ProtectionProvider } from './protection'
import { useProtection } from './protectionState'
import { SessionContext } from '../features/session/context'
import { makeSession } from '../test/utils'

function ProtectedPage() {
  const [active, setActive] = useState(true)
  useProtection(active, '请先保存密钥')
  return (
    <>
      <h1>当前页面</h1>
      <Link to="/next">离开</Link>
      <button onClick={() => setActive(false)}>已保存</button>
    </>
  )
}

describe('router protection', () => {
  it('blocks internal navigation until the protected operation is acknowledged', async () => {
    const user = userEvent.setup()
    const router = createMemoryRouter([
      {
        path: '*',
        element: (
          <SessionContext.Provider value={makeSession()}>
            <ProtectionProvider>
              <ProtectedPage />
            </ProtectionProvider>
          </SessionContext.Provider>
        ),
      },
    ])
    render(<RouterProvider router={router} />)
    await user.click(screen.getByRole('link', { name: '离开' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
    expect(await screen.findByRole('status')).toHaveTextContent('请先保存密钥')
    await user.click(screen.getByRole('button', { name: '已保存' }))
    await user.click(screen.getByRole('link', { name: '离开' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/next'))
  })
  it('marks full-document unload as protected without persisting the secret', () => {
    const router = createMemoryRouter([
      {
        path: '*',
        element: (
          <SessionContext.Provider value={makeSession()}>
            <ProtectionProvider>
              <ProtectedPage />
            </ProtectionProvider>
          </SessionContext.Provider>
        ),
      },
    ])
    render(<RouterProvider router={router} />)
    const event = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(true)
    expect(Object.keys(sessionStorage)).toEqual([])
  })
})
