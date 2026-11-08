import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { useLayoutEffect } from 'react'
import * as client from '../../api/client'
import { useSession } from './useSession'
import { apiFetch, getCsrfToken } from '../../api/client'
import { errorResponse, jsonResponse, stubFetch } from '../../test/utils'
import { SessionProvider } from './SessionContext'
import App from '../../app/App'

const admin = { id: 'op-1', email: 'admin@example.com', role: 'admin' }

function renderApp(initialPath: string) {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <SessionProvider>
        <App />
      </SessionProvider>
    </MemoryRouter>,
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('SessionProvider 会话引导与失效语义', () => {
  it('引导期 503 依赖故障：进入可手动重试的故障态，不清除本地登录、不跳转登录页', async () => {
    let meFails = true
    stubFetch((call) => {
      if (call.url === '/bff/me') {
        if (meFails) {
          return errorResponse(503, {
            code: 'dependency_unavailable',
            detail: 'core 暂不可用',
            request_id: 'req-me-503',
          })
        }
        return jsonResponse({ operator: admin, csrf_token: 'tok' })
      }
      return jsonResponse({ items: [], next_cursor: null })
    })
    renderApp('/apps')

    expect(await screen.findByText('core 暂不可用')).toBeInTheDocument()
    expect(screen.getByText(/服务暂时不可用/)).toBeInTheDocument()
    expect(screen.queryByRole('tab', { name: '平台管理' })).not.toBeInTheDocument()

    meFails = false
    await userEvent.setup().click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByRole('heading', { name: '应用' })).toBeInTheDocument()
  })

  it('引导期网络错误同样进入故障态而非当作未登录', async () => {
    let down = true
    stubFetch((call) => {
      if (down && call.url === '/bff/me') throw new TypeError('fetch failed')
      if (call.url === '/bff/me') return jsonResponse({ operator: admin, csrf_token: 'tok' })
      return jsonResponse({ items: [], next_cursor: null })
    })
    renderApp('/apps')
    expect(await screen.findByText(/服务暂时不可用/)).toBeInTheDocument()
    expect(screen.queryByRole('tab', { name: '平台管理' })).not.toBeInTheDocument()
    down = false
    await userEvent.setup().click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByRole('heading', { name: '应用' })).toBeInTheDocument()
  })

  it('引导期 401：视为未登录进入登录页，但不误报“会话已过期”', async () => {
    stubFetch(() => errorResponse(401, { code: 'unauthorized' }))
    renderApp('/apps')
    expect(await screen.findByRole('tab', { name: '平台管理' })).toBeInTheDocument()
    expect(screen.queryByText('会话已过期，请重新登录。')).not.toBeInTheDocument()
  })

  it('已登录会话被 401 终止：清除本地状态并提示会话已过期', async () => {
    let kick = false
    stubFetch((call) => {
      if (call.url === '/bff/me') return jsonResponse({ operator: admin, csrf_token: 'tok' })
      if (kick) return errorResponse(401, { code: 'unauthorized' })
      return jsonResponse({ items: [], next_cursor: null })
    })
    renderApp('/apps')
    expect(await screen.findByRole('heading', { name: '应用' })).toBeInTheDocument()

    kick = true
    await act(async () => {
      await expect(apiFetch('/apps')).rejects.toMatchObject({ status: 401 })
    })
    expect(await screen.findByText('会话已过期，请重新登录。')).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: '平台管理' })).toBeInTheDocument()
  })

  it('引导成功提交后立即收到 401，即使 passive effect 尚未运行也提示会话过期', async () => {
    const registration = vi.spyOn(client, 'setUnauthorizedHandler')
    stubFetch(() => jsonResponse({ operator: admin, csrf_token: 'tok' }))
    function ImmediateUnauthorized() {
      const { status, sessionExpired } = useSession()
      useLayoutEffect(() => {
        if (status !== 'ready') return
        // Trigger the actual registered 401 boundary in the first ready commit,
        // before the provider's passive effects can synchronize render state.
        const handler = registration.mock.calls.find(([handler]) => handler !== null)?.[0]
        expect(handler).toBeTypeOf('function')
        handler?.()
      }, [status])
      return (
        <p>
          {status}:{String(sessionExpired)}
        </p>
      )
    }
    render(
      <SessionProvider>
        <ImmediateUnauthorized />
      </SessionProvider>,
    )
    expect(await screen.findByText('anonymous:true')).toBeInTheDocument()
    expect(getCsrfToken()).toBeNull()
  })

  it('登录凭据错误的 401：只展示凭据错误，不提示会话已过期', async () => {
    stubFetch((call) => {
      if (call.url === '/bff/login') {
        return errorResponse(401, {
          code: 'invalid_credentials',
          detail: '邮箱、密码或动态口令错误',
          request_id: 'req-login-401',
        })
      }
      return errorResponse(401, { code: 'unauthorized' })
    })
    renderApp('/login')
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText('邮箱'), 'ops@example.com')
    await user.type(screen.getByLabelText('密码'), 'wrong')
    await user.type(screen.getByLabelText(/动态口令/), '123456')
    await user.click(screen.getByRole('button', { name: '登录' }))

    expect(await screen.findByText('邮箱、密码或动态口令错误')).toBeInTheDocument()
    expect(screen.queryByText('会话已过期，请重新登录。')).not.toBeInTheDocument()
  })

  it('退出登录失败（503 结果未知）：保留登录并显式展示错误，手动重试成功后才离开', async () => {
    let logoutFails = true
    stubFetch((call) => {
      if (call.url === '/bff/me') return jsonResponse({ operator: admin, csrf_token: 'tok' })
      if (call.url === '/bff/logout') {
        if (logoutFails) {
          return errorResponse(503, {
            code: 'logout_failed',
            detail: 'core 暂不可用，退出结果未知',
            request_id: 'req-logout-503',
          })
        }
        return jsonResponse(null, 204)
      }
      return jsonResponse({ items: [], next_cursor: null })
    })
    renderApp('/apps')
    const user = userEvent.setup()
    expect(await screen.findByRole('heading', { name: '应用' })).toBeInTheDocument()

    await user.click(screen.getAllByRole('button', { name: '退出登录' })[0]!)
    // 显式报错，仍停留在原页面且会话信息保留
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('core 暂不可用，退出结果未知')
    expect(screen.getByRole('heading', { name: '应用' })).toBeInTheDocument()
    expect(screen.getByText('admin@example.com')).toBeInTheDocument()

    logoutFails = false
    await user.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByRole('tab', { name: '平台管理' })).toBeInTheDocument()
  })

  it('退出登录收到 401：服务端会话已失效，清理本地状态且不提示“会话已过期”', async () => {
    stubFetch((call) => {
      if (call.url === '/bff/me') return jsonResponse({ operator: admin, csrf_token: 'tok' })
      if (call.url === '/bff/logout') return errorResponse(401, { code: 'unauthorized' })
      return jsonResponse({ items: [], next_cursor: null })
    })
    renderApp('/apps')
    const user = userEvent.setup()
    expect(await screen.findByRole('heading', { name: '应用' })).toBeInTheDocument()

    await user.click(screen.getAllByRole('button', { name: '退出登录' })[0]!)
    expect(await screen.findByRole('tab', { name: '平台管理' })).toBeInTheDocument()
    expect(screen.queryByText('会话已过期，请重新登录。')).not.toBeInTheDocument()
  })

  it('bootstrap 迟到成功不得回写覆盖已完成的新登录（operator/CSRF 保持新会话）', async () => {
    let resolveMe: ((r: Response) => void) | undefined
    stubFetch((call) => {
      if (call.url === '/bff/me') {
        return new Promise<Response>((res) => {
          resolveMe = res
        })
      }
      if (call.url === '/bff/login') {
        return jsonResponse({ operator: admin, csrf_token: 'login-csrf' })
      }
      return jsonResponse({ items: [], next_cursor: null })
    })
    renderApp('/login')
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText('邮箱'), 'ops@example.com')
    await user.type(screen.getByLabelText('密码'), 'pw')
    await user.type(screen.getByLabelText(/动态口令/), '123456')
    await user.click(screen.getByRole('button', { name: '登录' }))
    await waitFor(() => expect(getCsrfToken()).toBe('login-csrf'))

    // 引导期发出的 /me 此刻才返回旧会话数据：不得覆盖新登录
    await act(async () => {
      resolveMe!(
        jsonResponse({
          operator: { id: 'op-old', email: 'old@example.com', role: 'auditor' },
          csrf_token: 'boot-csrf',
        }),
      )
    })
    expect(getCsrfToken()).toBe('login-csrf')
    expect(screen.queryByText('old@example.com')).not.toBeInTheDocument()
  })
})
