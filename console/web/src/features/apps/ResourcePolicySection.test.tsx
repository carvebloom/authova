import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ResourceConfig } from '../../api/types'
import { jsonResponse, errorResponse, stubFetch } from '../../test/utils'
import { ResourcePolicySection } from './ResourcePolicySection'

const initial: ResourceConfig = {
  config_version: 3,
  concurrency_scope: 'per_process',
  counter_scope: 'shared_limiter',
  propagation_seconds: 5,
  limits: {
    http_concurrency: 256,
    kdf_concurrency: 2,
    db_concurrency: 32,
    requests_per_second: 20000,
    email_challenges_per_hour: 100000,
    sms_challenges_per_hour: 10000,
  },
  effective_limits: {
    http_concurrency: 256,
    kdf_concurrency: 2,
    db_concurrency: 28,
    requests_per_second: 20000,
    email_challenges_per_hour: 100000,
    sms_challenges_per_hour: 10000,
  },
}
afterEach(() => vi.unstubAllGlobals())
async function edit() {
  const user = userEvent.setup()
  await screen.findByText('调整资源预算')
  await user.click(screen.getByText('调整资源预算'))
  await user.clear(screen.getByLabelText('每秒请求上限'))
  await user.type(screen.getByLabelText('每秒请求上限'), '500')
  await user.type(screen.getByLabelText('资源调整原因'), '保护其他 App')
  return user
}

describe('ResourcePolicySection', () => {
  it('submits full App scoped budgets with CAS and reason', async () => {
    const { calls } = stubFetch((call) =>
      jsonResponse(
        call.method === 'PATCH'
          ? {
              ...initial,
              ...(call.body as object),
              effective_limits: { ...initial.effective_limits, requests_per_second: 500 },
              config_version: 4,
            }
          : initial,
      ),
    )
    render(<ResourcePolicySection appId="app-1" canWrite />)
    const user = await edit()
    await user.click(screen.getByRole('button', { name: '保存资源预算' }))
    await screen.findByText(/资源预算已更新/)
    expect(calls.filter((c) => c.method === 'PATCH')).toHaveLength(1)
    expect(calls.find((c) => c.method === 'PATCH')).toMatchObject({
      url: '/bff/apps/app-1/resource-policy',
      body: {
        config_version: 3,
        reason: '保护其他 App',
        limits: { ...initial.limits, requests_per_second: 500 },
      },
    })
  })
  it('read-only has no writable fields', async () => {
    stubFetch(() => jsonResponse(initial))
    render(<ResourcePolicySection appId="app-1" canWrite={false} />)
    await screen.findByText('当前账号仅可读取资源预算。')
    expect(screen.queryByRole('spinbutton')).not.toBeInTheDocument()
    expect(screen.queryByText('调整资源预算')).not.toBeInTheDocument()
  })
  it('rejects invalid budgets without mutation', async () => {
    const { calls } = stubFetch(() => jsonResponse(initial))
    render(<ResourcePolicySection appId="app-1" canWrite />)
    const user = await edit()
    await user.clear(screen.getByLabelText('密码计算并发'))
    await user.type(screen.getByLabelText('密码计算并发'), '0')
    await user.click(screen.getByRole('button', { name: '保存资源预算' }))
    await screen.findByRole('alert')
    expect(calls.filter((c) => c.method === 'PATCH')).toHaveLength(0)
  })
  it.each([409, 503])(
    'freezes on conflict or unknown %s until explicit read-back',
    async (status) => {
      const { calls } = stubFetch((c) =>
        c.method === 'PATCH'
          ? errorResponse(status, { code: status === 409 ? 'conflict' : 'outcome_unknown' })
          : jsonResponse(initial),
      )
      render(<ResourcePolicySection appId="app-1" canWrite />)
      const user = await edit()
      await user.click(screen.getByRole('button', { name: '保存资源预算' }))
      await waitFor(() =>
        expect(screen.getByRole('button', { name: '保存资源预算' })).toBeDisabled(),
      )
      expect(screen.getByLabelText('每秒请求上限')).toBeDisabled()
      expect(calls.filter((c) => c.method === 'PATCH')).toHaveLength(1)
      await user.click(screen.getByRole('button', { name: '重新读取资源预算' }))
      await waitFor(() => expect(screen.getByLabelText('每秒请求上限')).toHaveValue(20000))
      expect(screen.getByLabelText('资源调整原因')).toHaveValue('')
      expect(calls.filter((c) => c.method === 'PATCH')).toHaveLength(1)
    },
  )
  it('invalid acknowledgement is treated as unknown', async () => {
    stubFetch((c) =>
      jsonResponse(
        c.method === 'PATCH' ? { ...initial, config_version: 4, effective_limits: {} } : initial,
      ),
    )
    render(<ResourcePolicySection appId="app-1" canWrite />)
    const user = await edit()
    await user.click(screen.getByRole('button', { name: '保存资源预算' }))
    await waitFor(() => expect(screen.getByLabelText('每秒请求上限')).toBeDisabled())
    expect(screen.getByRole('button', { name: '重新读取资源预算' })).toBeEnabled()
  })
  it('App switch resets draft and reads only new App state', async () => {
    const { calls } = stubFetch(() => jsonResponse(initial))
    const view = render(<ResourcePolicySection appId="app-1" canWrite />)
    await edit()
    view.rerender(<ResourcePolicySection appId="app-2" canWrite />)
    await waitFor(() =>
      expect(calls.some((c) => c.url === '/bff/apps/app-2/resource-policy')).toBe(true),
    )
    await screen.findByText('调整资源预算')
    await userEvent.click(screen.getByText('调整资源预算'))
    expect(screen.getByLabelText('每秒请求上限')).toHaveValue(20000)
    expect(screen.getByLabelText('资源调整原因')).toHaveValue('')
  })
})
