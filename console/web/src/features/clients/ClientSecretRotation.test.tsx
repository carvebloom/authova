import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Outlet, Route, Routes } from 'react-router-dom'
import type { App, Client } from '../../api/types'
import {
  errorResponse,
  jsonResponse,
  makeSession,
  renderWithSession,
  stubFetch,
} from '../../test/utils'
import { ClientsTab } from './ClientsTab'

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

const confClient: Client = {
  config_version: 7,
  id: 'c-9',
  client_id: 'cid-conf-1',
  app_id: 'app-1',
  name: '服务端集成',
  kind: 'confidential',
  status: 'active',
  previous_secret_expires_at: 0,
  redirect_uris: ['https://api.example.com/cb'],
  allowed_scopes: ['openid'],
  allowed_audiences: ['https://api.example.com'],
  allowed_introspection_audiences: [],
  grant_types: ['client_credentials'],
  created_at: '2026-09-02T08:00:00Z',
}

const DETAIL_URL = '/bff/apps/app-1/clients/cid-conf-1'
const ROTATE_URL = '/bff/apps/app-1/clients/cid-conf-1/secret/rotate'
const REVOKE_PREV_URL = '/bff/apps/app-1/clients/cid-conf-1/secret/revoke-previous'

function FakeAppLayout() {
  return <Outlet context={{ app, reloadApp: vi.fn() }} />
}

function renderClients(session = makeSession()) {
  renderWithSession(
    <Routes>
      <Route path="/apps/:appId" element={<FakeAppLayout />}>
        <Route path="clients" element={<ClientsTab />} />
      </Route>
    </Routes>,
    session,
    ['/apps/app-1/clients'],
  )
}

/** 列表 + 客户端详情的默认读桩；写操作由各场景覆盖 */
function stubSecretApi(options: {
  client?: Client
  onRotate?: () => Response
  onRevokePrevious?: () => Response
  /** 第 n 次（1 起）详情 GET 的响应；缺省始终返回 options.client ?? confClient */
  detailRead?: (reads: number) => Response
}) {
  const client = options.client ?? confClient
  let detailReads = 0
  const { calls } = stubFetch((call) => {
    if (call.url === ROTATE_URL) return options.onRotate ? options.onRotate() : jsonResponse({})
    if (call.url === REVOKE_PREV_URL) {
      return options.onRevokePrevious ? options.onRevokePrevious() : jsonResponse(client)
    }
    if (call.url === DETAIL_URL) {
      detailReads += 1
      return options.detailRead ? options.detailRead(detailReads) : jsonResponse(client)
    }
    return jsonResponse({ items: [client], next_cursor: null })
  })
  return { calls, reads: () => detailReads }
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ClientsTab 客户端密钥轮换', () => {
  it('轮换成功：新密钥仅展示一次，grace=0 给出立即失效警示，确认后密钥消失', async () => {
    const user = userEvent.setup()
    const { calls } = stubSecretApi({
      onRotate: () =>
        jsonResponse({
          client: { ...confClient, config_version: 8 },
          client_secret: 'rotated-secret-1',
          previous_secret_expires_at: 0,
        }),
    })
    renderClients()

    await user.click(await screen.findByRole('button', { name: '轮换密钥' }))
    const dialog = await screen.findByRole('dialog')
    // 打开即权威读回客户端
    const graceInput = await within(dialog).findByLabelText(/旧密钥宽限期（秒）/)
    expect(calls.some((c) => c.url === DETAIL_URL && c.method === 'GET')).toBe(true)

    // grace=0：立即切换凭证的显式警示
    await user.clear(graceInput)
    await user.type(graceInput, '0')
    expect(within(dialog).getByText(/旧密钥将立即失效/)).toBeInTheDocument()

    await user.type(within(dialog).getByLabelText('操作原因'), '密钥疑似泄露')
    await user.click(within(dialog).getByRole('button', { name: '确认轮换' }))

    await waitFor(() => {
      const post = calls.find((c) => c.url === ROTATE_URL)
      expect(post?.method).toBe('POST')
      expect(post?.body).toEqual({
        config_version: 7,
        reason: '密钥疑似泄露',
        grace_seconds: 0,
      })
    })

    // 一次性密钥面板：复制 + 显式确认才关闭
    expect(await screen.findByText('新的客户端密钥仅此一次显示')).toBeInTheDocument()
    expect(screen.getByText('rotated-secret-1')).toBeInTheDocument()
    expect(screen.getByText(/宽限期为 0：旧密钥已立即失效/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '我已保存，关闭' }))
    await waitFor(() => expect(screen.queryByText('rotated-secret-1')).not.toBeInTheDocument())
    // 密钥不进入任何 Web 存储
    expect(window.localStorage.length).toBe(0)
    expect(window.sessionStorage.length).toBe(0)
  })

  it('结果未知：冻结表单；读回发现版本上升时明确提示密钥无法找回，且不自动重试', async () => {
    const user = userEvent.setup()
    let posts = 0
    const { calls } = stubSecretApi({
      onRotate: () => {
        posts += 1
        return errorResponse(503, { code: 'outcome_unknown', detail: 'core 转发失败' })
      },
      // 首次打开读回 v7；结果未知后的权威读回已升至 v8（旧请求可能已生效）
      detailRead: (reads) =>
        jsonResponse(reads > 1 ? { ...confClient, config_version: 8 } : confClient),
    })
    renderClients()

    await user.click(await screen.findByRole('button', { name: '轮换密钥' }))
    const dialog = await screen.findByRole('dialog')
    await within(dialog).findByLabelText(/旧密钥宽限期（秒）/)
    await user.type(within(dialog).getByLabelText('操作原因'), '例行轮换')
    await user.click(within(dialog).getByRole('button', { name: '确认轮换' }))

    expect(
      await within(dialog).findByText('提交结果未知：请求可能已在服务端生效'),
    ).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: '确认轮换' })).toBeDisabled()

    await user.click(within(dialog).getByRole('button', { name: '重新加载最新状态' }))
    expect(await within(dialog).findByText('检测到配置版本已变化')).toBeInTheDocument()
    expect(within(dialog).getByText(/那次返回的新密钥无法找回/)).toBeInTheDocument()
    // 冻结解除但不自动重试
    expect(within(dialog).getByRole('button', { name: '确认轮换' })).toBeEnabled()
    expect(posts).toBe(1)
    expect(calls.filter((c) => c.url === ROTATE_URL)).toHaveLength(1)
  })

  it('409 冲突：冻结并以读回的新版本重新提交', async () => {
    const user = userEvent.setup()
    let posts = 0
    const { calls } = stubSecretApi({
      onRotate: () => {
        posts += 1
        return posts === 1
          ? errorResponse(409, { code: 'version_conflict' })
          : jsonResponse({
              client: { ...confClient, config_version: 9 },
              client_secret: 'rotated-secret-2',
              previous_secret_expires_at: 0,
            })
      },
      detailRead: (reads) =>
        jsonResponse(reads > 1 ? { ...confClient, config_version: 8 } : confClient),
    })
    renderClients()

    await user.click(await screen.findByRole('button', { name: '轮换密钥' }))
    const dialog = await screen.findByRole('dialog')
    await within(dialog).findByLabelText(/旧密钥宽限期（秒）/)
    await user.type(within(dialog).getByLabelText('操作原因'), '例行轮换')
    await user.click(within(dialog).getByRole('button', { name: '确认轮换' }))

    expect(await within(dialog).findByText('配置版本已过期（409 冲突）')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: '重新加载最新状态' }))
    await waitFor(() =>
      expect(within(dialog).queryByText('配置版本已过期（409 冲突）')).not.toBeInTheDocument(),
    )
    await user.type(within(dialog).getByLabelText('操作原因'), '例行轮换')
    await user.click(within(dialog).getByRole('button', { name: '确认轮换' }))

    await waitFor(() => expect(posts).toBe(2))
    expect(calls.filter((c) => c.url === ROTATE_URL)[1]?.body).toMatchObject({
      config_version: 8,
    })
  })

  it('旧密钥宽限期内：轮换被阻止并引导撤销；撤销走 revoke-previous 携带版本与原因', async () => {
    const user = userEvent.setup()
    const live = {
      ...confClient,
      previous_secret_expires_at: Math.floor(Date.now() / 1000) + 3600,
    }
    const { calls } = stubSecretApi({
      client: live,
      onRevokePrevious: () => jsonResponse({ ...confClient, config_version: 8 }),
    })
    renderClients()

    // 行内状态徽标与撤销入口
    expect(await screen.findByText('旧密钥宽限中')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '轮换密钥' }))
    const dialog = await screen.findByRole('dialog')
    expect(await within(dialog).findByText(/暂时无法轮换/)).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: '确认轮换' })).toBeDisabled()
    await user.click(within(dialog).getByRole('button', { name: '取消' }))

    await user.click(screen.getByRole('button', { name: '撤销旧密钥' }))
    const revokeDialog = screen.getByRole('dialog')
    await user.type(within(revokeDialog).getByLabelText('操作原因'), '新密钥已全量切换')
    await user.click(within(revokeDialog).getByRole('button', { name: '确认撤销旧密钥' }))

    await waitFor(() => {
      const post = calls.find((c) => c.url === REVOKE_PREV_URL)
      expect(post?.method).toBe('POST')
      expect(post?.body).toEqual({ config_version: 7, reason: '新密钥已全量切换' })
    })
    // 整个过程中没有发起轮换
    expect(calls.filter((c) => c.url === ROTATE_URL)).toHaveLength(0)
  })

  it('只读 auditor：无轮换/撤销/编辑入口', async () => {
    stubSecretApi({})
    renderClients(
      makeSession({
        operator: { id: 'op-2', email: 'audit@example.com', role: 'auditor' },
        canMutate: false,
      }),
    )
    expect(await screen.findByText('服务端集成')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '轮换密钥' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '撤销旧密钥' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '编辑' })).not.toBeInTheDocument()
  })

  it('系统内置机密客户端不提供密钥轮换入口', async () => {
    stubSecretApi({ client: { ...confClient, system: true, name: '系统管理客户端' } })
    renderClients()
    expect(await screen.findByText('系统管理客户端')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '轮换密钥' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '编辑' })).not.toBeInTheDocument()
  })
})
