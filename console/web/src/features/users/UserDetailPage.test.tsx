import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Outlet, Route, Routes } from 'react-router-dom'
import type { App, User } from '../../api/types'
import {
  errorResponse,
  jsonResponse,
  makeSession,
  renderWithSession,
  stubFetch,
} from '../../test/utils'
import { UserDetailPage } from './UserDetailPage'

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

const baseUser: User = {
  id: 'user-1',
  app_id: 'app-1',
  display_name: '张三',
  status: 'active',
  email: 'z***@example.com',
  phone: '138****0000',
  created_at: '2026-09-05T08:00:00Z',
  session_epoch: 2,
  admin_role: 'auditor',
}

function FakeAppLayout() {
  return <Outlet context={{ app, reloadApp: vi.fn() }} />
}

function renderDetail(session = makeSession()) {
  renderWithSession(
    <Routes>
      <Route path="/apps/:appId" element={<FakeAppLayout />}>
        <Route path="users/:userId" element={<UserDetailPage />} />
      </Route>
    </Routes>,
    session,
    ['/apps/app-1/users/user-1'],
  )
}

function stubUserApi(user: User, putHandler?: () => Response) {
  return stubFetch((call) => {
    if (call.method === 'PUT') return putHandler ? putHandler() : jsonResponse(null, 204)
    if (call.url === '/bff/apps/app-1/users/user-1') return jsonResponse(user)
    return jsonResponse({ items: [], next_cursor: null })
  })
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('UserDetailPage 固定管理员角色', () => {
  it('展示用户的 admin_role（auditor → 审计员）', async () => {
    stubUserApi(baseUser)
    renderDetail()
    expect(await screen.findByRole('heading', { name: '张三' })).toBeInTheDocument()
    expect(screen.getByText('审计员')).toBeInTheDocument()
  })

  it('App 级 owner 可设置角色：PUT 携带 {role,reason} 并展示成功', async () => {
    const user = userEvent.setup()
    const { calls } = stubUserApi(baseUser)
    renderDetail(
      makeSession({
        operator: { id: 'op-1', email: 'owner@example.com', role: 'owner', app_id: 'app-1' },
      }),
    )
    await user.click(await screen.findByRole('button', { name: '变更管理员角色' }))

    await user.selectOptions(screen.getByLabelText('管理员角色'), 'admin')
    await user.type(screen.getByLabelText('操作原因'), '提升为管理员')
    await user.click(screen.getByRole('button', { name: '保存' }))

    await waitFor(() => {
      const put = calls.find((c) => c.method === 'PUT')
      expect(put?.url).toBe('/bff/apps/app-1/administrators/user-1')
      expect(put?.body).toEqual({ role: 'admin', reason: '提升为管理员' })
    })
    expect(await screen.findByText('管理员角色已设置为「管理员」。')).toBeInTheDocument()
  })

  it('选择“无”即撤销：PUT 携带空 role', async () => {
    const user = userEvent.setup()
    const { calls } = stubUserApi({ ...baseUser, admin_role: 'owner' })
    renderDetail(
      makeSession({
        operator: { id: 'op-1', email: 'owner@example.com', role: 'owner', app_id: 'app-1' },
      }),
    )
    await user.click(await screen.findByRole('button', { name: '变更管理员角色' }))
    await user.selectOptions(screen.getByLabelText('管理员角色'), '')
    await user.type(screen.getByLabelText('操作原因'), '卸任')
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => {
      const put = calls.find((c) => c.method === 'PUT')
      expect(put?.body).toEqual({ role: '', reason: '卸任' })
    })
    expect(await screen.findByText('管理员角色已撤销。')).toBeInTheDocument()
  })

  it('最后一名 owner 冲突：服务端错误原样展示且对话框保持打开', async () => {
    const user = userEvent.setup()
    const { calls } = stubUserApi({ ...baseUser, admin_role: 'owner' }, () =>
      errorResponse(409, {
        title: 'Conflict',
        detail: '必须至少保留一名拥有者',
        code: 'last_owner',
        request_id: 'req-owner-1',
      }),
    )
    renderDetail(
      makeSession({
        operator: { id: 'op-1', email: 'owner@example.com', role: 'owner', app_id: 'app-1' },
      }),
    )
    await user.click(await screen.findByRole('button', { name: '变更管理员角色' }))
    await user.selectOptions(screen.getByLabelText('管理员角色'), '')
    await user.type(screen.getByLabelText('操作原因'), '卸任')
    await user.click(screen.getByRole('button', { name: '保存' }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('必须至少保留一名拥有者')
    expect(alert).toHaveTextContent('req-owner-1')
    expect(screen.getByRole('button', { name: '保存' })).toBeInTheDocument()
    expect(screen.queryByText('管理员角色已撤销。')).not.toBeInTheDocument()
    expect(calls.filter((c) => c.method === 'PUT')).toHaveLength(1)
  })

  it('App 级 admin 不能设置角色，但可禁用用户', async () => {
    stubUserApi(baseUser)
    renderDetail(
      makeSession({ operator: { id: 'op-2', email: 'a@b.c', role: 'admin', app_id: 'app-1' } }),
    )
    expect(await screen.findByRole('heading', { name: '张三' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '变更管理员角色' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '设为管理员' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '禁用用户' })).toBeInTheDocument()
  })

  it('unknown role writes stay frozen through failed read-back and clear the old reason after reconciliation', async () => {
    const user = userEvent.setup()
    let reads = 0
    const { calls } = stubFetch((call) => {
      if (call.method === 'PUT') return errorResponse(503, { code: 'outcome_unknown' })
      if (call.url === '/bff/apps/app-1/users/user-1') {
        reads++
        if (reads === 2) return errorResponse(503, { code: 'dependency_unavailable' })
        return jsonResponse({ ...baseUser, admin_role: reads > 2 ? 'admin' : 'auditor' })
      }
      return jsonResponse({ items: [], next_cursor: null })
    })
    renderDetail(
      makeSession({
        operator: { id: 'op-1', email: 'owner@example.com', role: 'owner', app_id: 'app-1' },
      }),
    )
    await user.click(await screen.findByRole('button', { name: '变更管理员角色' }))
    await user.selectOptions(screen.getByLabelText('管理员角色'), 'admin')
    await user.type(screen.getByLabelText('操作原因'), 'uncertain change')
    await user.click(screen.getByRole('button', { name: '保存' }))
    const readBack = await screen.findByRole('button', { name: '重新加载最新状态' })
    expect(screen.getByLabelText('管理员角色')).toBeDisabled()
    expect(screen.getByRole('button', { name: '取消' })).toBeDisabled()
    await user.click(readBack)
    await waitFor(() => expect(readBack).toBeEnabled())
    expect(screen.getByLabelText('管理员角色')).toBeDisabled()
    await user.click(readBack)
    await waitFor(() => expect(screen.getByLabelText('管理员角色')).toBeEnabled())
    expect(screen.getByLabelText('管理员角色')).toHaveValue('admin')
    expect(screen.getByLabelText('操作原因')).toHaveValue('')
    expect(calls.filter((call) => call.method === 'PUT')).toHaveLength(1)
  })

  it('App 级 auditor 完全只读', async () => {
    stubUserApi(baseUser)
    renderDetail(
      makeSession({ operator: { id: 'op-3', email: 'a@b.c', role: 'auditor', app_id: 'app-1' } }),
    )
    expect(await screen.findByRole('heading', { name: '张三' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '变更管理员角色' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '禁用用户' })).not.toBeInTheDocument()
  })
})

describe('UserDetailPage 恢复用户（enable）', () => {
  const disabledUser: User = { ...baseUser, status: 'disabled' }

  it('禁用用户：确认对话框展示当前会话纪元，POST 携带 {reason, session_epoch}，成功后读回', async () => {
    const user = userEvent.setup()
    let userReads = 0
    const { calls } = stubFetch((call) => {
      if (call.method === 'POST' && call.url.endsWith('/enable')) {
        return jsonResponse({ ...disabledUser, status: 'active', session_epoch: 3 })
      }
      if (call.url === '/bff/apps/app-1/users/user-1') {
        userReads += 1
        return jsonResponse(
          userReads > 1 ? { ...disabledUser, status: 'active', session_epoch: 3 } : disabledUser,
        )
      }
      return jsonResponse({ items: [], next_cursor: null })
    })
    renderDetail()

    await user.click(await screen.findByRole('button', { name: '恢复用户' }))
    const dialog = screen.getByRole('dialog')
    // 确认对话框呈现将提交的当前 session_epoch，并说明旧会话不恢复
    expect(dialog).toHaveTextContent('session_epoch 2')
    expect(dialog).toHaveTextContent(/被撤销的会话不会恢复/)

    await user.type(within(dialog).getByLabelText('操作原因'), '误操作禁用，恢复')
    await user.click(within(dialog).getByRole('button', { name: '确认恢复' }))

    await waitFor(() => {
      const post = calls.find((c) => c.url.endsWith('/enable'))
      expect(post?.method).toBe('POST')
      expect(post?.body).toEqual({ reason: '误操作禁用，恢复', session_epoch: 2 })
    })
    expect(
      await screen.findByText('用户已恢复；其旧会话保持撤销，需重新登录。'),
    ).toBeInTheDocument()
    // 成功后重新读回用户详情
    await waitFor(() => expect(userReads).toBeGreaterThanOrEqual(2))
  })

  it('409 冲突：冻结确认按钮，重新读回后才能再次提交', async () => {
    const user = userEvent.setup()
    let posts = 0
    const { calls } = stubFetch((call) => {
      if (call.method === 'POST' && call.url.endsWith('/enable')) {
        posts += 1
        return posts === 1
          ? errorResponse(409, { code: 'epoch_conflict', detail: 'session_epoch 已变化' })
          : jsonResponse({ ...disabledUser, status: 'active', session_epoch: 4 })
      }
      if (call.url === '/bff/apps/app-1/users/user-1') {
        return jsonResponse({ ...disabledUser, session_epoch: 3 })
      }
      return jsonResponse({ items: [], next_cursor: null })
    })
    renderDetail()

    await user.click(await screen.findByRole('button', { name: '恢复用户' }))
    const dialog = screen.getByRole('dialog')
    await user.type(within(dialog).getByLabelText('操作原因'), '恢复')
    await user.click(within(dialog).getByRole('button', { name: '确认恢复' }))

    expect(await within(dialog).findByText(/409 冲突/)).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: '确认恢复' })).toBeDisabled()
    expect(posts).toBe(1)

    // 权威读回后解除冻结，并以新的 session_epoch 重新提交
    await user.click(within(dialog).getByRole('button', { name: '重新加载最新状态' }))
    await waitFor(() =>
      expect(within(dialog).getByRole('button', { name: '确认恢复' })).toBeEnabled(),
    )
    await user.type(within(dialog).getByLabelText('操作原因'), '恢复')
    await user.click(within(dialog).getByRole('button', { name: '确认恢复' }))
    await waitFor(() => expect(posts).toBe(2))
    expect(calls.filter((c) => c.url.endsWith('/enable'))[1]?.body).toEqual({
      reason: '恢复',
      session_epoch: 3,
    })
  })

  it('已删除用户为终态：不提供恢复或禁用入口', async () => {
    stubUserApi({ ...baseUser, status: 'deleted' })
    renderDetail()
    expect(await screen.findByRole('heading', { name: '张三' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '恢复用户' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '禁用用户' })).not.toBeInTheDocument()
  })

  it('auditor 对禁用用户也没有恢复入口', async () => {
    stubUserApi(disabledUser)
    renderDetail(
      makeSession({ operator: { id: 'op-3', email: 'a@b.c', role: 'auditor', app_id: 'app-1' } }),
    )
    expect(await screen.findByRole('heading', { name: '张三' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '恢复用户' })).not.toBeInTheDocument()
  })
})

it('恢复隔离身份展示核验提示且不提供普通状态和角色变更入口', async () => {
  stubUserApi({ ...baseUser, status: 'quarantined' })
  renderDetail()
  expect(await screen.findByText('恢复隔离')).toBeInTheDocument()
  expect(screen.getByText(/该身份处于恢复隔离/)).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '恢复用户' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '禁用用户' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /管理员角色/ })).not.toBeInTheDocument()
})
