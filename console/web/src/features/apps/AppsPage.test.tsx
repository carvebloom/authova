import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router-dom'
import type { App } from '../../api/types'
import {
  errorResponse,
  jsonResponse,
  makeSession,
  renderWithSession,
  stubFetch,
  type FetchCall,
} from '../../test/utils'
import { AppsPage } from './AppsPage'

const appA: App = {
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

function renderApps(session = makeSession()) {
  renderWithSession(
    <Routes>
      <Route path="/apps" element={<AppsPage />} />
      <Route path="/apps/:appId" element={<div>进入应用详情</div>} />
    </Routes>,
    session,
    ['/apps'],
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('AppsPage', () => {
  it('渲染应用列表与状态', async () => {
    stubFetch(() => jsonResponse({ items: [appA], next_cursor: null }))
    renderApps()
    expect(await screen.findByText('交易前台')).toBeInTheDocument()
    expect(screen.getByText('活跃')).toBeInTheDocument()
    expect(screen.getByText('trade-web')).toBeInTheDocument()
  })

  it('空列表展示空态，管理员可见创建入口', async () => {
    stubFetch(() => jsonResponse({ items: [], next_cursor: null }))
    renderApps()
    expect(await screen.findByText('暂无应用')).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: '创建应用' }).length).toBeGreaterThan(0)
  })

  it('审计员为只读：不展示任何创建入口', async () => {
    stubFetch(() => jsonResponse({ items: [appA], next_cursor: null }))
    renderApps(
      makeSession({
        operator: { id: 'op-2', email: 'audit@example.com', role: 'auditor' },
        canMutate: false,
      }),
    )
    expect(await screen.findByText('交易前台')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '创建应用' })).not.toBeInTheDocument()
  })

  it('加载失败展示错误与 request_id，点击重试恢复', async () => {
    let failed = true
    const { mock } = stubFetch(() =>
      failed
        ? errorResponse(500, { title: 'Internal', detail: '数据库连接失败', request_id: 'req-500' })
        : jsonResponse({ items: [appA], next_cursor: null }),
    )
    renderApps()
    expect(await screen.findByText('数据库连接失败')).toBeInTheDocument()
    expect(screen.getByText(/req-500/)).toBeInTheDocument()
    failed = false
    await userEvent.setup().click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('交易前台')).toBeInTheDocument()
    expect(mock.mock.calls.length).toBe(2)
  })

  it('创建应用：校验 slug，成功后提交契约字段并跳转详情', async () => {
    const user = userEvent.setup()
    const isCreate = (call: FetchCall) => call.method === 'POST' && call.url === '/bff/apps'
    const { calls } = stubFetch((call) =>
      isCreate(call)
        ? jsonResponse({ ...appA, id: 'app-new', name: '新应用', slug: 'new-app' }, 200)
        : jsonResponse({ items: [], next_cursor: null }),
    )
    renderApps()

    await user.click(await screen.findAllByRole('button', { name: '创建应用' }).then((b) => b[0]!))
    await user.click(screen.getByRole('button', { name: '创建' }))
    expect(await screen.findByText('请输入应用名称')).toBeInTheDocument()

    await user.type(screen.getByLabelText('应用名称'), '新应用')
    await user.type(screen.getByLabelText('Slug'), 'Invalid Slug!')
    await user.click(screen.getByRole('button', { name: '创建' }))
    expect(await screen.findByText(/Slug 仅支持/)).toBeInTheDocument()

    await user.clear(screen.getByLabelText('Slug'))
    await user.type(screen.getByLabelText('Slug'), 'new-app')
    await user.click(screen.getByRole('button', { name: '创建' }))
    expect(await screen.findByText('进入应用详情')).toBeInTheDocument()

    const create = calls.find(isCreate)
    expect(create?.headers['Idempotency-Key']).toBeTruthy()
  })

  it('创建结果未知后：内容不变只能用原键重试，服务端可去重', async () => {
    const user = userEvent.setup()
    const isCreate = (call: FetchCall) => call.method === 'POST' && call.url === '/bff/apps'
    let failOnce = true
    const { calls } = stubFetch((call) => {
      if (isCreate(call)) {
        if (failOnce) {
          failOnce = false
          return errorResponse(503, {
            title: 'Service Unavailable',
            detail: 'core 转发失败',
            code: 'outcome_unknown',
            request_id: 'req-unk-1',
          })
        }
        return jsonResponse({ ...appA, id: 'app-new', name: '新应用', slug: 'new-app' }, 200)
      }
      return jsonResponse({ items: [], next_cursor: null })
    })
    renderApps()

    await user.click(await screen.findAllByRole('button', { name: '创建应用' }).then((b) => b[0]!))
    await user.type(screen.getByLabelText('应用名称'), '新应用')
    await user.type(screen.getByLabelText('Slug'), 'new-app')
    await user.click(screen.getByRole('button', { name: '创建' }))
    expect(await screen.findByText('core 转发失败')).toBeInTheDocument()
    // 进入未确认模式：提示可能已生效，提交键变为“重试原请求”
    expect(screen.getByText('先前的创建请求结果未知，可能已生效')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '重试原请求' }))
    expect(await screen.findByText('进入应用详情')).toBeInTheDocument()

    const creates = calls.filter(isCreate)
    expect(creates).toHaveLength(2)
    expect(creates[0]?.headers['Idempotency-Key']).toBeTruthy()
    expect(creates[1]?.headers['Idempotency-Key']).toBe(creates[0]?.headers['Idempotency-Key'])
    // 成功后未确认记录清除
    expect(screen.queryByText(/未确认的创建请求/)).not.toBeInTheDocument()
  })

  it('结果未知后关闭对话框：请求键与内容保留在页面提示中，重开对话框可继续用原键重试', async () => {
    const user = userEvent.setup()
    const isCreate = (call: FetchCall) => call.method === 'POST' && call.url === '/bff/apps'
    let failOnce = true
    const { calls } = stubFetch((call) => {
      if (isCreate(call)) {
        if (failOnce) {
          failOnce = false
          return errorResponse(503, { code: 'outcome_unknown', detail: '转发失败' })
        }
        return jsonResponse({ ...appA, id: 'app-new', name: '新应用', slug: 'new-app' }, 200)
      }
      return jsonResponse({ items: [], next_cursor: null })
    })
    renderApps()

    await user.click(await screen.findAllByRole('button', { name: '创建应用' }).then((b) => b[0]!))
    await user.type(screen.getByLabelText('应用名称'), '新应用')
    await user.type(screen.getByLabelText('Slug'), 'new-app')
    await user.click(screen.getByRole('button', { name: '创建' }))
    expect(await screen.findByText('转发失败')).toBeInTheDocument()

    // 直接关闭对话框：不得丢失未确认请求
    await user.click(screen.getByRole('button', { name: '取消' }))
    expect(
      await screen.findByText(/未确认的创建请求：应用「新应用」（new-app）/),
    ).toBeInTheDocument()

    // 重新打开（按钮文案反映有待处理请求）：表单预填原内容，重试仍用原键
    await user.click(screen.getByRole('button', { name: '处理未确认的创建' }))
    expect(screen.getByLabelText('应用名称')).toHaveValue('新应用')
    expect(screen.getByLabelText('Slug')).toHaveValue('new-app')
    await user.click(screen.getByRole('button', { name: '重试原请求' }))
    expect(await screen.findByText('进入应用详情')).toBeInTheDocument()

    const creates = calls.filter(isCreate)
    expect(creates).toHaveLength(2)
    expect(creates[1]?.headers['Idempotency-Key']).toBe(creates[0]?.headers['Idempotency-Key'])
  })

  it('结果未知后修改内容：阻止当成新创建，须先核对并显式放弃原记录才可用新键', async () => {
    const user = userEvent.setup()
    const isCreate = (call: FetchCall) => call.method === 'POST' && call.url === '/bff/apps'
    let failOnce = true
    const { calls } = stubFetch((call) => {
      if (isCreate(call)) {
        if (failOnce) {
          failOnce = false
          return errorResponse(503, { code: 'outcome_unknown', detail: '转发失败' })
        }
        return jsonResponse({ ...appA, id: 'app-new' }, 200)
      }
      return jsonResponse({ items: [], next_cursor: null })
    })
    renderApps()

    await user.click(await screen.findAllByRole('button', { name: '创建应用' }).then((b) => b[0]!))
    await user.type(screen.getByLabelText('应用名称'), '新应用')
    await user.type(screen.getByLabelText('Slug'), 'new-app')
    await user.click(screen.getByRole('button', { name: '创建' }))
    expect(await screen.findByText('转发失败')).toBeInTheDocument()

    // 修改内容：提交被阻止，且明确提示原请求可能已生效、需要先核对
    await user.type(screen.getByLabelText('Slug'), '-2')
    expect(await screen.findByText(/内容已修改/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试原请求' })).toBeDisabled()
    expect(calls.filter(isCreate)).toHaveLength(1)

    // 关闭后在页面提示中显式确认“已核对、未生效”，才允许作为新请求创建
    await user.click(screen.getByRole('button', { name: '取消' }))
    await user.click(await screen.findByRole('button', { name: '已核对，确认未生效' }))
    expect(screen.queryByText(/未确认的创建请求/)).not.toBeInTheDocument()

    await user.click(await screen.findAllByRole('button', { name: '创建应用' }).then((b) => b[0]!))
    await user.type(screen.getByLabelText('应用名称'), '新应用')
    await user.type(screen.getByLabelText('Slug'), 'new-app-2')
    await user.click(screen.getByRole('button', { name: '创建' }))
    expect(await screen.findByText('进入应用详情')).toBeInTheDocument()

    const creates = calls.filter(isCreate)
    expect(creates).toHaveLength(2)
    expect(creates[1]?.headers['Idempotency-Key']).toBeTruthy()
    expect(creates[1]?.headers['Idempotency-Key']).not.toBe(creates[0]?.headers['Idempotency-Key'])
  })

  it('超过 10 分钟回放窗口：不得再宣称原键重试安全，要求先核对实际状态', async () => {
    const user = userEvent.setup()
    let failOnce = true
    stubFetch((call) => {
      if (call.method === 'POST') {
        if (failOnce) {
          failOnce = false
          return errorResponse(503, { code: 'outcome_unknown', detail: '转发失败' })
        }
        return jsonResponse({ ...appA, id: 'app-new' }, 200)
      }
      return jsonResponse({ items: [], next_cursor: null })
    })
    renderApps()

    await user.click(await screen.findAllByRole('button', { name: '创建应用' }).then((b) => b[0]!))
    await user.type(screen.getByLabelText('应用名称'), '新应用')
    await user.type(screen.getByLabelText('Slug'), 'new-app')
    await user.click(screen.getByRole('button', { name: '创建' }))
    expect(await screen.findByText('转发失败')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '取消' }))

    // 只冻结 Date（不影响 userEvent 的定时器），推进到 11 分钟后
    vi.useFakeTimers({ toFake: ['Date'] })
    try {
      vi.setSystemTime(Date.now() + 11 * 60 * 1000)
      await user.click(screen.getByRole('button', { name: '处理未确认的创建' }))
      expect(await screen.findByText(/已超过 10 分钟幂等回放窗口/)).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '重试原请求' })).toBeDisabled()
    } finally {
      vi.useRealTimers()
    }
  })
})
