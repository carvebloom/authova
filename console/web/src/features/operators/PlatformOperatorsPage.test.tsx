import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { PlatformOperator } from '../../api/types'
import { ConsoleShell } from '../../layout/ConsoleShell'
import {
  errorResponse,
  jsonResponse,
  makeSession,
  renderWithSession,
  stubFetch,
} from '../../test/utils'
import { PlatformOperatorsPage } from './PlatformOperatorsPage'

const platformOwner: PlatformOperator = {
  id: 'op-1',
  email: 'owner@example.com',
  role: 'owner',
  status: 'active',
}

const appScopedOp: PlatformOperator = {
  id: 'op-2',
  email: 'app-admin@example.com',
  role: 'admin',
  status: 'active',
  app_id: 'app-1',
}

function renderPage(
  session = makeSession({ operator: { id: 'op-1', email: 'owner@example.com', role: 'owner' } }),
) {
  renderWithSession(
    <Routes>
      <Route path="/platform/operators" element={<PlatformOperatorsPage />} />
    </Routes>,
    session,
    ['/platform/operators'],
  )
}

function stubList(items: PlatformOperator[]) {
  return stubFetch((call) => {
    if (call.method === 'GET') return jsonResponse({ items, next_cursor: null })
    if (call.method === 'POST') {
      return jsonResponse({
        operator: { id: 'op-new', email: 'new@example.com', role: 'auditor', status: 'active' },
        totp_secret: 'JBSWY3DPEHPK3PXP',
      })
    }
    return jsonResponse({ updated: true })
  })
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('PlatformOperatorsPage', () => {
  it('owner：渲染列表（邮箱/角色/状态/范围）与操作入口', async () => {
    stubList([platformOwner, appScopedOp])
    renderPage()
    expect(await screen.findByText('owner@example.com')).toBeInTheDocument()
    expect(screen.getByText('app-admin@example.com')).toBeInTheDocument()
    expect(screen.getByText('拥有者')).toBeInTheDocument()
    expect(screen.getByText('平台级')).toBeInTheDocument()
    expect(screen.getByText(/App 级/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '创建运营账号' })).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: '变更' })).toHaveLength(2)
  })

  it('创建：校验失败后提交契约字段，一次性展示 TOTP 密钥且不落地存储', async () => {
    const user = userEvent.setup()
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const { calls } = stubList([])

    renderPage()
    await user.click(await screen.findByRole('button', { name: '创建运营账号' }))

    await user.type(screen.getByLabelText('邮箱'), 'not-an-email')
    await user.type(screen.getByLabelText('初始密码'), 'short')
    await user.click(screen.getByRole('button', { name: '创建' }))
    expect(await screen.findByText('请输入有效的邮箱地址')).toBeInTheDocument()
    expect(screen.getByText('初始密码至少 15 个字符')).toBeInTheDocument()
    expect(calls.every((c) => c.method === 'GET')).toBe(true)

    await user.clear(screen.getByLabelText('邮箱'))
    await user.type(screen.getByLabelText('邮箱'), 'new@example.com')
    await user.clear(screen.getByLabelText('初始密码'))
    await user.type(screen.getByLabelText('初始密码'), 'A valid operator password phrase')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      const post = calls.find((c) => c.method === 'POST')
      expect(post?.url).toBe('/bff/platform/operators')
      expect(post?.body).toEqual({
        email: 'new@example.com',
        password: 'A valid operator password phrase',
        role: 'auditor',
      })
    })

    // 一次性密钥面板：密钥 + 交接说明 + 复制
    expect(await screen.findByText('TOTP 配置密钥仅此一次显示')).toBeInTheDocument()
    expect(screen.getByText('JBSWY3DPEHPK3PXP')).toBeInTheDocument()
    expect(screen.getByText(/安全渠道分别传达/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '复制密钥' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('JBSWY3DPEHPK3PXP'))

    await user.click(screen.getByRole('button', { name: '我已安全传达，关闭' }))
    await waitFor(() => expect(screen.queryByText('JBSWY3DPEHPK3PXP')).not.toBeInTheDocument())

    // 密码与密钥绝不写入 web 存储
    expect(window.localStorage.length).toBe(0)
    expect(window.sessionStorage.length).toBe(0)
  })

  it('变更：PATCH 携带 {role,status,reason}，成功后刷新列表', async () => {
    const user = userEvent.setup()
    const { calls } = stubList([platformOwner])
    renderPage()
    await user.click(await screen.findByRole('button', { name: '变更' }))

    await user.selectOptions(screen.getByLabelText('角色'), 'admin')
    await user.selectOptions(screen.getByLabelText('状态'), 'disabled')
    await user.type(screen.getByLabelText('操作原因'), '岗位调整')
    await user.click(screen.getByRole('button', { name: '保存' }))

    await waitFor(() => {
      const patch = calls.find((c) => c.method === 'PATCH')
      expect(patch?.url).toBe('/bff/platform/operators/op-1')
      expect(patch?.body).toEqual({ role: 'admin', status: 'disabled', reason: '岗位调整' })
    })
    expect(await screen.findByText('运营账号已更新。')).toBeInTheDocument()
  })

  it('变更必须填写原因', async () => {
    const user = userEvent.setup()
    const { calls } = stubList([platformOwner])
    renderPage()
    await user.click(await screen.findByRole('button', { name: '变更' }))
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText('请填写操作原因')).toBeInTheDocument()
    expect(calls.every((c) => c.method === 'GET')).toBe(true)
  })

  it('最后一名可用拥有者：服务端 409 原样展示且对话框保持打开', async () => {
    const user = userEvent.setup()
    const { calls } = stubFetch((call) => {
      if (call.method === 'GET') return jsonResponse({ items: [platformOwner], next_cursor: null })
      return errorResponse(409, {
        title: 'Conflict',
        detail: '必须至少保留一名可用拥有者',
        code: 'last_available_owner',
        request_id: 'req-last-owner',
      })
    })
    renderPage()
    await user.click(await screen.findByRole('button', { name: '变更' }))
    await user.selectOptions(screen.getByLabelText('角色'), 'admin')
    await user.type(screen.getByLabelText('操作原因'), '降级')
    await user.click(screen.getByRole('button', { name: '保存' }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('必须至少保留一名可用拥有者')
    expect(alert).toHaveTextContent('req-last-owner')
    expect(screen.getByRole('button', { name: '保存' })).toBeInTheDocument()
    expect(calls.filter((c) => c.method === 'PATCH')).toHaveLength(1)
  })

  it('admin：只读（无创建/变更入口）', async () => {
    stubList([platformOwner])
    renderPage(makeSession({ operator: { id: 'op-5', email: 'admin@example.com', role: 'admin' } }))
    expect(await screen.findByText('owner@example.com')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '创建运营账号' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '变更' })).not.toBeInTheDocument()
  })

  it('auditor：只读', async () => {
    stubList([platformOwner])
    renderPage(
      makeSession({ operator: { id: 'op-6', email: 'audit@example.com', role: 'auditor' } }),
    )
    expect(await screen.findByText('owner@example.com')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '创建运营账号' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '变更' })).not.toBeInTheDocument()
  })

  it('创建结果未知：提交永久锁定；查证已存在时引导 recover-operator 恢复，不重建同名账号', async () => {
    const user = userEvent.setup()
    let postCount = 0
    stubFetch((call) => {
      if (call.method === 'POST') {
        postCount += 1
        return errorResponse(503, {
          title: 'Service Unavailable',
          detail: 'core 转发失败',
          code: 'outcome_unknown',
          request_id: 'req-unk-1',
        })
      }
      // 首个请求实际已在服务端成功：列表中已存在该邮箱
      return jsonResponse({
        items: [{ id: 'op-new', email: 'new@example.com', role: 'auditor', status: 'active' }],
        next_cursor: null,
      })
    })
    renderPage()
    await user.click(await screen.findByRole('button', { name: '创建运营账号' }))
    await user.type(screen.getByLabelText('邮箱'), 'new@example.com')
    await user.type(screen.getByLabelText('初始密码'), 'A valid operator password phrase')
    await user.click(screen.getByRole('button', { name: '创建' }))

    // 明确提示结果未知且可能已生效；创建按钮被禁用而非伪装成安全重试
    expect(await screen.findByText('提交结果未知：请求可能已在服务端生效')).toBeInTheDocument()
    expect(screen.getByText(/req-unk-1/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()

    await user.click(screen.getByRole('button', { name: '检查账号列表' }))
    // 引导使用 recover-operator 恢复 TOTP，且不得建议禁用后同名重建（邮箱唯一）
    expect(await screen.findByText(/recover-operator/)).toBeInTheDocument()
    expect(screen.queryByText(/禁用该账号后重新创建/)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()

    await user.click(screen.getByRole('button', { name: '关闭并刷新列表' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    // 全程只发出过一次创建请求，未产生重复写
    expect(postCount).toBe(1)
  })

  it('创建网络异常：即使查证列表不存在也不解锁提交（先前请求可能延迟生效）', async () => {
    const user = userEvent.setup()
    let postCount = 0
    stubFetch((call) => {
      if (call.method === 'POST') {
        postCount += 1
        throw new TypeError('fetch failed')
      }
      return jsonResponse({ items: [], next_cursor: null })
    })
    renderPage()
    await user.click(await screen.findByRole('button', { name: '创建运营账号' }))
    await user.type(screen.getByLabelText('邮箱'), 'new@example.com')
    await user.type(screen.getByLabelText('初始密码'), 'A valid operator password phrase')
    await user.click(screen.getByRole('button', { name: '创建' }))

    expect(await screen.findByText('提交结果未知：请求可能已在服务端生效')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()

    await user.click(screen.getByRole('button', { name: '检查账号列表' }))
    // 单次未找到不等于安全：仍按未知处理，保持锁定，不宣称可安全重试
    expect(await screen.findByText(/仍可能延迟生效/)).toBeInTheDocument()
    expect(screen.getByText(/不能据此视为可以安全重试/)).toBeInTheDocument()
    expect(screen.queryByText(/现在可以安全重新提交/)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
    expect(postCount).toBe(1)
  })

  it('创建被明确拒绝（409 冲突）：不进入结果未知流程，可直接修正重试', async () => {
    const user = userEvent.setup()
    stubFetch((call) => {
      if (call.method === 'POST') {
        return errorResponse(409, {
          title: 'Conflict',
          detail: '该邮箱已存在',
          code: 'email_taken',
          request_id: 'req-409',
        })
      }
      return jsonResponse({ items: [], next_cursor: null })
    })
    renderPage()
    await user.click(await screen.findByRole('button', { name: '创建运营账号' }))
    await user.type(screen.getByLabelText('邮箱'), 'dup@example.com')
    await user.type(screen.getByLabelText('初始密码'), 'A valid operator password phrase')
    await user.click(screen.getByRole('button', { name: '创建' }))

    expect(await screen.findByText('该邮箱已存在')).toBeInTheDocument()
    expect(screen.queryByText('提交结果未知：请求可能已在服务端生效')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '创建' })).toBeEnabled()
  })

  it('App 级账号：展示无权提示且完全不发起请求', async () => {
    const { calls } = stubList([platformOwner])
    renderPage(
      makeSession({ operator: { id: 'op-7', email: 'a@b.c', role: 'owner', app_id: 'app-1' } }),
    )
    expect(await screen.findByText('当前账号无权查看平台运营账号。')).toBeInTheDocument()
    expect(calls).toHaveLength(0)
  })

  it('空态与错误态（含重试）', async () => {
    stubList([])
    const { unmount } = renderWithSession(
      <Routes>
        <Route path="/platform/operators" element={<PlatformOperatorsPage />} />
      </Routes>,
      makeSession({ operator: { id: 'op-1', email: 'owner@example.com', role: 'owner' } }),
      ['/platform/operators'],
    )
    expect(await screen.findByText('暂无运营账号')).toBeInTheDocument()
    unmount()

    let failed = true
    stubFetch(() =>
      failed
        ? errorResponse(500, {
            title: 'Internal',
            detail: 'core 不可用',
            request_id: 'req-ops-500',
          })
        : jsonResponse({ items: [platformOwner], next_cursor: null }),
    )
    renderPage()
    expect(await screen.findByText('core 不可用')).toBeInTheDocument()
    expect(screen.getByText(/req-ops-500/)).toBeInTheDocument()
    failed = false
    await userEvent.setup().click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('owner@example.com')).toBeInTheDocument()
  })
})

describe('ConsoleShell 平台导航可见性', () => {
  function renderShell(session: ReturnType<typeof makeSession>) {
    stubFetch(() => jsonResponse({ items: [], next_cursor: null }))
    renderWithSession(
      <Routes>
        <Route element={<ConsoleShell />}>
          <Route path="*" element={<div>工作区</div>} />
        </Route>
      </Routes>,
      session,
      ['/apps'],
    )
  }

  it('平台级 owner 可见“平台运营账号”导航', async () => {
    renderShell(
      makeSession({ operator: { id: 'op-1', email: 'owner@example.com', role: 'owner' } }),
    )
    expect(await screen.findByRole('link', { name: '平台运营账号' })).toBeInTheDocument()
  })

  it('App 级账号不可见该导航', async () => {
    renderShell(
      makeSession({ operator: { id: 'op-9', email: 'a@b.c', role: 'owner', app_id: 'app-1' } }),
    )
    expect(await screen.findByText('工作区')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: '平台运营账号' })).not.toBeInTheDocument()
  })
})

it('恢复隔离的运营账号不提供普通变更入口', async () => {
  stubList([{ ...platformOwner, status: 'quarantined' }])
  renderPage()
  expect(await screen.findByText('恢复隔离')).toBeInTheDocument()
  expect(screen.getByText('需部署管理员带外核验后恢复')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '变更' })).not.toBeInTheDocument()
})
