import { describe, expect, it, vi, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router-dom'
import { ApiError } from '../../api/client'
import { assignLocation } from '../../shared/utils/navigate'
import {
  makeSession,
  renderWithSession,
  stubFetch,
  jsonResponse,
  errorResponse,
} from '../../test/utils'
import { LoginPage } from './LoginPage'

vi.mock('../../shared/utils/navigate', () => ({ assignLocation: vi.fn() }))

function renderLogin(sessionOverrides = {}) {
  const session = makeSession({ status: 'anonymous', operator: null, ...sessionOverrides })
  renderWithSession(
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/apps" element={<div>应用列表页</div>} />
    </Routes>,
    session,
    ['/login'],
  )
  return session
}

describe('LoginPage', () => {
  it('渲染邮箱/密码/TOTP 三个带标签的控件', () => {
    renderLogin()
    expect(screen.getByLabelText('邮箱')).toBeInTheDocument()
    expect(screen.getByLabelText('密码')).toBeInTheDocument()
    expect(screen.getByLabelText(/动态口令/)).toBeInTheDocument()
  })

  it('客户端校验失败时不发起登录请求', async () => {
    const user = userEvent.setup()
    const session = renderLogin()
    await user.type(screen.getByLabelText('邮箱'), 'not-an-email')
    await user.type(screen.getByLabelText(/动态口令/), '123')
    await user.click(screen.getByRole('button', { name: '登录' }))
    expect(await screen.findByText('请输入有效的邮箱地址')).toBeInTheDocument()
    expect(screen.getByText('请输入密码')).toBeInTheDocument()
    expect(screen.getByText('请输入 6 位动态口令')).toBeInTheDocument()
    expect(session.signIn).not.toHaveBeenCalled()
  })

  it('登录成功后跳转到应用列表', async () => {
    const user = userEvent.setup()
    const session = renderLogin()
    await user.type(screen.getByLabelText('邮箱'), 'ops@example.com')
    await user.type(screen.getByLabelText('密码'), 'secret')
    await user.type(screen.getByLabelText(/动态口令/), '123456')
    await user.click(screen.getByRole('button', { name: '登录' }))
    await waitFor(() => expect(session.signIn).toHaveBeenCalledTimes(1))
    expect(session.signIn).toHaveBeenCalledWith({
      email: 'ops@example.com',
      password: 'secret',
      totp: '123456',
    })
    expect(await screen.findByText('应用列表页')).toBeInTheDocument()
  })

  it('服务端错误原样展示 detail 与 request_id', async () => {
    const user = userEvent.setup()
    const session = renderLogin()
    vi.mocked(session.signIn).mockRejectedValueOnce(
      new ApiError({
        status: 401,
        title: 'Unauthorized',
        detail: '邮箱、密码或动态口令错误',
        code: 'invalid_credentials',
        requestId: 'req-login-1',
      }),
    )
    await user.type(screen.getByLabelText('邮箱'), 'ops@example.com')
    await user.type(screen.getByLabelText('密码'), 'wrong')
    await user.type(screen.getByLabelText(/动态口令/), '123456')
    await user.click(screen.getByRole('button', { name: '登录' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('邮箱、密码或动态口令错误')
    expect(alert).toHaveTextContent('req-login-1')
  })

  it('会话过期后展示提示', () => {
    renderLogin({ sessionExpired: true })
    expect(screen.getByText('会话已过期，请重新登录。')).toBeInTheDocument()
  })

  it('密码默认隐藏，可切换显示（aria-pressed 反映状态）', async () => {
    const user = userEvent.setup()
    renderLogin()
    const password = screen.getByLabelText('密码')
    expect(password).toHaveAttribute('type', 'password')
    const toggle = screen.getByRole('button', { name: '显示内容' })
    await user.click(toggle)
    expect(password).toHaveAttribute('type', 'text')
    expect(screen.getByRole('button', { name: '隐藏内容' })).toHaveAttribute('aria-pressed', 'true')
    await user.click(screen.getByRole('button', { name: '隐藏内容' }))
    expect(password).toHaveAttribute('type', 'password')
  })

  it('密码显示切换在 Tab 序内，且键盘触发时不提交表单', async () => {
    const user = userEvent.setup()
    const session = renderLogin()
    // autoFocus 在邮箱；Tab 顺序必须覆盖显示切换按钮
    expect(screen.getByLabelText('邮箱')).toHaveFocus()
    await user.tab()
    expect(screen.getByLabelText('密码')).toHaveFocus()
    await user.tab()
    const toggle = screen.getByRole('button', { name: '显示内容' })
    expect(toggle).toHaveFocus()
    // 在切换按钮上回车：切换可见性，绝不提交登录表单
    await user.keyboard('{Enter}')
    expect(screen.getByLabelText('密码')).toHaveAttribute('type', 'text')
    expect(session.signIn).not.toHaveBeenCalled()
  })
})

describe('LoginPage 应用管理模式', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.mocked(assignLocation).mockClear()
  })

  it('切换后只要求 App Key，不收集用户凭据', async () => {
    const user = userEvent.setup()
    renderLogin()
    await user.click(screen.getByRole('tab', { name: '应用管理' }))
    expect(screen.getByLabelText('App Key')).toBeInTheDocument()
    expect(screen.queryByLabelText('邮箱')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('密码')).not.toBeInTheDocument()
    expect(screen.queryByLabelText(/动态口令/)).not.toBeInTheDocument()
  })

  it('提交 App Key 后经 /bff/app-login 换取 redirect_to 并整页跳转', async () => {
    const user = userEvent.setup()
    const { calls } = stubFetch(() =>
      jsonResponse({ redirect_to: 'https://auth.example.com/oidc/authorize?client_id=x' }),
    )
    renderLogin()
    await user.click(screen.getByRole('tab', { name: '应用管理' }))
    await user.type(screen.getByLabelText('App Key'), 'ak_live_1')
    await user.click(screen.getByRole('button', { name: '进入应用管理' }))
    await waitFor(() =>
      expect(assignLocation).toHaveBeenCalledWith(
        'https://auth.example.com/oidc/authorize?client_id=x',
      ),
    )
    expect(calls[0]?.url).toBe('/bff/app-login')
    expect(calls[0]?.method).toBe('POST')
    expect(calls[0]?.body).toEqual({ app_key: 'ak_live_1' })
  })

  it('App Key 为空时仅本地校验，不发请求', async () => {
    const user = userEvent.setup()
    const { calls } = stubFetch(() => jsonResponse({}))
    renderLogin()
    await user.click(screen.getByRole('tab', { name: '应用管理' }))
    await user.click(screen.getByRole('button', { name: '进入应用管理' }))
    expect(await screen.findByText('请输入 App Key')).toBeInTheDocument()
    expect(calls).toHaveLength(0)
    expect(assignLocation).not.toHaveBeenCalled()
  })

  it('BFF 未就绪（如 501）时展示真实错误，不跳转', async () => {
    const user = userEvent.setup()
    stubFetch(() =>
      errorResponse(501, {
        title: 'Not Implemented',
        detail: 'app-login 尚未开放',
        request_id: 'req-501',
      }),
    )
    renderLogin()
    await user.click(screen.getByRole('tab', { name: '应用管理' }))
    await user.type(screen.getByLabelText('App Key'), 'ak_live_1')
    await user.click(screen.getByRole('button', { name: '进入应用管理' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('app-login 尚未开放')
    expect(alert).toHaveTextContent('req-501')
    expect(assignLocation).not.toHaveBeenCalled()
  })

  it('响应缺少 redirect_to 时展示错误且不跳转', async () => {
    const user = userEvent.setup()
    stubFetch(() => jsonResponse({}))
    renderLogin()
    await user.click(screen.getByRole('tab', { name: '应用管理' }))
    await user.type(screen.getByLabelText('App Key'), 'ak_live_1')
    await user.click(screen.getByRole('button', { name: '进入应用管理' }))
    expect(await screen.findByText('服务端未返回 redirect_to')).toBeInTheDocument()
    expect(assignLocation).not.toHaveBeenCalled()
  })
})
