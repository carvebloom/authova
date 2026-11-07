import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Outlet, Route, Routes } from 'react-router-dom'
import type { App, Connection } from '../../api/types'
import {
  errorResponse,
  jsonResponse,
  makeSession,
  renderWithSession,
  stubFetch,
  type FetchCall,
  type FetchHandler,
} from '../../test/utils'
import { ConnectionsTab } from './ConnectionsTab'

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

const oidcConnection: Connection = {
  id: 'conn-1',
  name: '企业 IdP',
  provider_type: 'oidc',
  issuer: 'https://idp.example.com',
  client_id: 'cid-oidc-1',
  status: 'active',
  config_version: 2,
}

const alipayConnection: Connection = {
  id: 'conn-ali',
  name: '支付宝登录',
  provider_type: 'alipay',
  issuer: 'https://openauth.alipay.com',
  client_id: '2021000000000001',
  status: 'active',
  config_version: 2,
}

/** 仅供单元测试使用的假密钥（非真实 PEM，仅验证传输形状） */
const FAKE_PRIVATE_KEY = 'FAKE-TEST-ONLY-PRIVATE-KEY'
const FAKE_ALIPAY_PUBLIC_KEY = 'FAKE-TEST-ONLY-ALIPAY-PUBLIC-KEY'

const LIST_PATH = '/bff/apps/app-1/connections'
const CONN_PATH = `${LIST_PATH}/conn-1`
const ALIPAY_PATH = `${LIST_PATH}/conn-ali`

interface RouteOverrides {
  list?: FetchHandler
  create?: FetchHandler
  read?: FetchHandler
  patch?: FetchHandler
}

function stubRoutes(overrides: RouteOverrides = {}) {
  return stubFetch((call) => {
    const path = new URL(call.url, 'http://localhost').pathname
    if (path === CONN_PATH && call.method === 'GET') {
      return overrides.read ? overrides.read(call) : jsonResponse(oidcConnection)
    }
    if (path === CONN_PATH && call.method === 'PATCH') {
      return overrides.patch ? overrides.patch(call) : jsonResponse(oidcConnection)
    }
    if (path === LIST_PATH && call.method === 'POST') {
      return overrides.create ? overrides.create(call) : jsonResponse(oidcConnection, 201)
    }
    if (path === LIST_PATH) {
      return overrides.list
        ? overrides.list(call)
        : jsonResponse({ items: [oidcConnection], next_cursor: null })
    }
    return errorResponse(404, { detail: `未 stub 的请求：${call.method} ${path}` })
  })
}

function listReads(calls: FetchCall[]) {
  return calls.filter(
    (c) => c.method === 'GET' && new URL(c.url, 'http://localhost').pathname === LIST_PATH,
  )
}

function FakeAppLayout({ appForTest = app }: { appForTest?: App }) {
  return <Outlet context={{ app: appForTest, reloadApp: vi.fn() }} />
}

function renderConnections(session = makeSession(), appForTest: App = app) {
  renderWithSession(
    <Routes>
      <Route path="/apps/:appId" element={<FakeAppLayout appForTest={appForTest} />}>
        <Route path="connections" element={<ConnectionsTab />} />
      </Route>
    </Routes>,
    session,
    ['/apps/app-1/connections'],
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ConnectionsTab', () => {
  it('连接列表正常渲染，且不展示契约未保证返回的创建时间列', async () => {
    stubRoutes()
    renderConnections()
    expect(await screen.findByText('企业 IdP')).toBeInTheDocument()
    expect(screen.getByText('标准 OIDC')).toBeInTheDocument()
    expect(screen.queryByText('创建时间')).not.toBeInTheDocument()
  })

  it('连接列表读取失败展示错误与重试', async () => {
    stubRoutes({
      list: () => errorResponse(403, { code: 'forbidden', detail: '无权限' }),
    })
    renderConnections()
    expect(await screen.findByText('无权限')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
  })

  it('创建 OIDC 连接：默认类型，提交契约字段，成功后读回列表', async () => {
    const user = userEvent.setup()
    const { calls } = stubRoutes()
    renderConnections()
    await user.click(await screen.findByRole('button', { name: '创建连接' }))

    expect(screen.getByLabelText('类型')).toHaveValue('oidc')
    await user.type(screen.getByLabelText('名称'), '备用 IdP')
    await user.type(screen.getByLabelText('Issuer'), 'https://idp2.example.com')
    await user.type(screen.getByLabelText('Client ID'), 'cid-oidc-2')
    await user.type(screen.getByLabelText('Client Secret'), 'secret-2')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      const create = calls.find((c) => c.method === 'POST')
      expect(create?.url).toBe(LIST_PATH)
      expect(create?.body).toEqual({
        name: '备用 IdP',
        provider_type: 'oidc',
        issuer: 'https://idp2.example.com',
        client_id: 'cid-oidc-2',
        client_secret: 'secret-2',
      })
    })
    expect(await screen.findByText(/连接已创建/)).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    // 成功后读回列表
    await waitFor(() => expect(listReads(calls).length).toBeGreaterThanOrEqual(2))
  })

  it('OIDC issuer 必须是 https://（开发 localhost HTTP 也不放宽），否则不发请求', async () => {
    const user = userEvent.setup()
    const { calls } = stubRoutes()
    renderConnections()
    await user.click(await screen.findByRole('button', { name: '创建连接' }))
    await user.type(screen.getByLabelText('名称'), '本地 IdP')
    await user.type(screen.getByLabelText('Issuer'), 'http://localhost:8080')
    await user.type(screen.getByLabelText('Client ID'), 'cid-local')
    await user.type(screen.getByLabelText('Client Secret'), 'secret-local')
    await user.click(screen.getByRole('button', { name: '创建' }))
    expect(await screen.findByText('Issuer 必须是以 https:// 开头的 URL')).toBeInTheDocument()
    expect(calls.every((c) => c.method === 'GET')).toBe(true)
  })

  it('创建 GitHub 连接：Issuer 固定为服务端值，请求省略 issuer', async () => {
    const user = userEvent.setup()
    const { calls } = stubRoutes()
    renderConnections()
    await user.click(await screen.findByRole('button', { name: '创建连接' }))

    await user.selectOptions(screen.getByLabelText('类型'), 'github')
    // Issuer 不采集，展示固定值说明
    expect(screen.queryByLabelText('Issuer')).not.toBeInTheDocument()
    expect(screen.getByText(/由服务端固定/)).toBeInTheDocument()
    expect(screen.getByText('https://github.com')).toBeInTheDocument()

    await user.type(screen.getByLabelText('名称'), 'GitHub 登录')
    await user.type(screen.getByLabelText('Client ID'), 'gh-client-1')
    await user.type(screen.getByLabelText('Client Secret'), 'gh-secret-1')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      const create = calls.find((c) => c.method === 'POST')
      expect(create?.body).toEqual({
        name: 'GitHub 登录',
        provider_type: 'github',
        client_id: 'gh-client-1',
        client_secret: 'gh-secret-1',
      })
      expect(create?.body).not.toHaveProperty('issuer')
    })
    expect(await screen.findByText(/连接已创建/)).toBeInTheDocument()
  })

  it('创建微信网站扫码连接：Issuer 固定为服务端值，请求省略 issuer，提示 AppID/AppSecret', async () => {
    const user = userEvent.setup()
    const { calls } = stubRoutes()
    renderConnections()
    await user.click(await screen.findByRole('button', { name: '创建连接' }))

    await user.selectOptions(screen.getByLabelText('类型'), 'wechat')
    // Issuer 不采集，展示固定值说明
    expect(screen.queryByLabelText('Issuer')).not.toBeInTheDocument()
    expect(screen.getByText(/由服务端固定/)).toBeInTheDocument()
    expect(screen.getByText('https://open.weixin.qq.com')).toBeInTheDocument()
    // 字段标签保持 Client ID/Client Secret，提示指向网站应用的 AppID/AppSecret
    expect(screen.getByText(/网站应用的 AppID/)).toBeInTheDocument()
    expect(screen.getByText(/AppSecret/)).toBeInTheDocument()

    // 切回标准 OIDC 恢复可编辑 Issuer；再切回微信仍固定
    await user.selectOptions(screen.getByLabelText('类型'), 'oidc')
    expect(screen.getByLabelText('Issuer')).toBeInTheDocument()
    await user.selectOptions(screen.getByLabelText('类型'), 'wechat')
    expect(screen.queryByLabelText('Issuer')).not.toBeInTheDocument()

    await user.type(screen.getByLabelText('名称'), '微信扫码登录')
    await user.type(screen.getByLabelText('Client ID'), 'wx-appid-1')
    await user.type(screen.getByLabelText('Client Secret'), 'wx-appsecret-1')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      const create = calls.find((c) => c.method === 'POST')
      expect(create?.url).toBe(LIST_PATH)
      expect(create?.body).toEqual({
        name: '微信扫码登录',
        provider_type: 'wechat',
        client_id: 'wx-appid-1',
        client_secret: 'wx-appsecret-1',
      })
      expect(create?.body).not.toHaveProperty('issuer')
    })
    expect(await screen.findByText(/连接已创建/)).toBeInTheDocument()
  })

  it('创建 QQ 网站登录连接：Issuer 固定为服务端值，请求省略 issuer，提示 AppID/AppKey', async () => {
    const user = userEvent.setup()
    const { calls } = stubRoutes()
    renderConnections()
    await user.click(await screen.findByRole('button', { name: '创建连接' }))

    await user.selectOptions(screen.getByLabelText('类型'), 'qq')
    // Issuer 不采集，展示固定值说明
    expect(screen.queryByLabelText('Issuer')).not.toBeInTheDocument()
    expect(screen.getByText(/由服务端固定/)).toBeInTheDocument()
    expect(screen.getByText('https://graph.qq.com')).toBeInTheDocument()
    // 字段标签保持 Client ID/Client Secret，提示指向 QQ 互联网站应用的 AppID/AppKey
    expect(screen.getByText(/网站应用的 AppID/)).toBeInTheDocument()
    expect(screen.getByText(/AppKey/)).toBeInTheDocument()

    await user.type(screen.getByLabelText('名称'), 'QQ 登录')
    await user.type(screen.getByLabelText('Client ID'), 'qq-appid-1')
    await user.type(screen.getByLabelText('Client Secret'), 'qq-appkey-1')
    await user.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      const create = calls.find((c) => c.method === 'POST')
      expect(create?.url).toBe(LIST_PATH)
      expect(create?.body).toEqual({
        name: 'QQ 登录',
        provider_type: 'qq',
        client_id: 'qq-appid-1',
        client_secret: 'qq-appkey-1',
      })
      expect(create?.body).not.toHaveProperty('issuer')
    })
    expect(await screen.findByText(/连接已创建/)).toBeInTheDocument()
  })

  it('类型标签：创建下拉列出五种类型，列表类型列按惯用名展示', async () => {
    const user = userEvent.setup()
    stubRoutes({
      list: () =>
        jsonResponse({
          items: [
            oidcConnection,
            {
              ...oidcConnection,
              id: 'conn-gh',
              name: '代码平台',
              provider_type: 'github',
              issuer: 'https://github.com',
            },
            {
              ...oidcConnection,
              id: 'conn-wx',
              name: '扫码登录',
              provider_type: 'wechat',
              issuer: 'https://open.weixin.qq.com',
            },
            {
              ...oidcConnection,
              id: 'conn-qq',
              name: 'QQ 互联',
              provider_type: 'qq',
              issuer: 'https://graph.qq.com',
            },
            alipayConnection,
          ],
          next_cursor: null,
        }),
    })
    renderConnections()

    // 列表类型列按惯用名展示五种提供方
    expect(await screen.findByText('标准 OIDC')).toBeInTheDocument()
    expect(screen.getByText('GitHub')).toBeInTheDocument()
    expect(screen.getByText('微信网站扫码')).toBeInTheDocument()
    expect(screen.getByText('QQ 网站登录')).toBeInTheDocument()
    expect(screen.getByText('支付宝网页授权')).toBeInTheDocument()

    // 创建下拉列出全部五种类型
    await user.click(screen.getByRole('button', { name: '创建连接' }))
    expect(screen.getByRole('option', { name: '标准 OIDC' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'GitHub' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: '微信网站扫码' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'QQ 网站登录' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: '支付宝网页授权' })).toBeInTheDocument()
  })

  it('页头与空列表说明覆盖五种提供方；空列表仅保留一个创建入口', async () => {
    stubRoutes({ list: () => jsonResponse({ items: [], next_cursor: null }) })
    renderConnections()
    expect(await screen.findByText('暂无连接')).toBeInTheDocument()
    expect(screen.getByText(/创建 OIDC、GitHub、微信、QQ 或支付宝网页授权连接/)).toBeInTheDocument()
    expect(
      screen.getByText(/上游身份源：标准 OIDC、GitHub、微信、QQ、支付宝网页授权/),
    ).toBeInTheDocument()
    // 空列表：只有空状态里一个主行动，页头不重复出现
    expect(screen.getAllByRole('button', { name: '创建连接' })).toHaveLength(1)
  })

  it('编辑：仅提交变化字段，密钥留空即省略，携带 config_version 与 reason', async () => {
    const user = userEvent.setup()
    const { calls } = stubRoutes({
      patch: () => jsonResponse({ ...oidcConnection, status: 'disabled', config_version: 3 }),
    })
    renderConnections()
    await user.click(await screen.findByRole('button', { name: '编辑' }))

    // 不可变字段说明
    expect(screen.getByText(/不可修改/)).toBeInTheDocument()
    expect(screen.getByLabelText('状态')).toHaveValue('active')

    await user.selectOptions(screen.getByLabelText('状态'), 'disabled')
    await user.type(screen.getByLabelText('操作原因'), '停用旧 IdP')
    await user.click(screen.getByRole('button', { name: '保存' }))

    await waitFor(() => {
      const patch = calls.find((c) => c.method === 'PATCH')
      expect(patch?.url).toBe(CONN_PATH)
      expect(patch?.body).toEqual({
        config_version: 2,
        reason: '停用旧 IdP',
        status: 'disabled',
      })
      expect(patch?.body).not.toHaveProperty('name')
      expect(patch?.body).not.toHaveProperty('client_secret')
      expect(patch?.body).not.toHaveProperty('issuer')
      expect(patch?.body).not.toHaveProperty('provider_type')
      expect(patch?.body).not.toHaveProperty('client_id')
    })
    expect(await screen.findByText(/已更新（配置版本 3）/)).toBeInTheDocument()
  })

  it('编辑：无修改时禁止保存；操作原因必填', async () => {
    const user = userEvent.setup()
    const { calls } = stubRoutes()
    renderConnections()
    await user.click(await screen.findByRole('button', { name: '编辑' }))

    // 未修改任何内容：保存不可用
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()
    expect(screen.getByText('修改名称、状态或密钥后才能保存。')).toBeInTheDocument()

    await user.type(screen.getByLabelText('名称'), ' v2')
    expect(screen.getByRole('button', { name: '保存' })).toBeEnabled()
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText('请填写操作原因')).toBeInTheDocument()
    expect(calls.every((c) => c.method === 'GET')).toBe(true)
  })

  it('编辑：填写新密钥即轮换；关闭对话框后密钥被清空', async () => {
    const user = userEvent.setup()
    const { calls } = stubRoutes({
      patch: () => jsonResponse({ ...oidcConnection, config_version: 3 }),
    })
    renderConnections()
    await user.click(await screen.findByRole('button', { name: '编辑' }))
    await user.type(screen.getByLabelText('新 Client Secret'), 'new-secret-9')
    await user.type(screen.getByLabelText('操作原因'), '例行轮换')
    await user.click(screen.getByRole('button', { name: '保存' }))

    await waitFor(() => {
      const patch = calls.find((c) => c.method === 'PATCH')
      expect(patch?.body).toMatchObject({ client_secret: 'new-secret-9', reason: '例行轮换' })
    })
    expect(await screen.findByText(/已更新/)).toBeInTheDocument()

    // 重新打开：密钥字段已随对话框关闭清空
    await user.click(screen.getByRole('button', { name: '编辑' }))
    expect(screen.getByLabelText('新 Client Secret')).toHaveValue('')
    expect(screen.getByLabelText('操作原因')).toHaveValue('')
  })

  it('409 配置版本过期：不静默重试，须重新加载最新配置后用新版本提交', async () => {
    const user = userEvent.setup()
    let patchCalls = 0
    const { calls } = stubRoutes({
      read: () => jsonResponse({ ...oidcConnection, config_version: 5 }),
      patch: () => {
        patchCalls += 1
        return patchCalls === 1
          ? errorResponse(409, { code: 'version_conflict', detail: 'config_version 已过期' })
          : jsonResponse({ ...oidcConnection, name: '企业 IdP v2', config_version: 6 })
      },
    })
    renderConnections()
    await user.click(await screen.findByRole('button', { name: '编辑' }))
    await user.type(screen.getByLabelText('名称'), ' v2')
    await user.type(screen.getByLabelText('操作原因'), '改名')
    await user.click(screen.getByRole('button', { name: '保存' }))

    // 409：提示版本过期，保存冻结，且没有自动重试
    expect(await screen.findByText('配置版本已过期（409 冲突）')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()
    expect(calls.filter((c) => c.method === 'PATCH')).toHaveLength(1)

    // 读回最新配置后解冻，再次提交使用最新 config_version
    await user.click(screen.getByRole('button', { name: '重新加载最新配置' }))
    await waitFor(() => expect(screen.getByRole('button', { name: '保存' })).toBeDisabled())
    expect(screen.queryByText('配置版本已过期（409 冲突）')).not.toBeInTheDocument()
    await user.type(screen.getByLabelText('名称'), ' v2')
    await user.type(screen.getByLabelText('操作原因'), '核对后重新改名')
    await user.click(screen.getByRole('button', { name: '保存' }))

    await waitFor(() => {
      const patches = calls.filter((c) => c.method === 'PATCH')
      expect(patches).toHaveLength(2)
      expect(patches[1]?.body).toMatchObject({ config_version: 5, name: '企业 IdP v2' })
    })
    expect(await screen.findByText(/已更新（配置版本 6）/)).toBeInTheDocument()
  })

  it('更新结果未知：冻结提交，读回核对后解除页面级冻结', async () => {
    const user = userEvent.setup()
    let patchCalls = 0
    const { calls } = stubRoutes({
      read: () => jsonResponse({ ...oidcConnection, config_version: 3 }),
      patch: () => {
        patchCalls += 1
        return patchCalls === 1
          ? errorResponse(503, { code: 'outcome_unknown', detail: 'core 转发失败' })
          : jsonResponse({ ...oidcConnection, name: '企业 IdP v2', config_version: 4 })
      },
    })
    renderConnections()
    await user.click(await screen.findByRole('button', { name: '编辑' }))
    await user.type(screen.getByLabelText('名称'), ' v2')
    await user.type(screen.getByLabelText('操作原因'), '改名')
    await user.click(screen.getByRole('button', { name: '保存' }))

    // 冻结：结果未知提示 + 保存禁用；页面级保留未确认记录
    expect(await screen.findByText('提交结果未知：请求可能已在服务端生效')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()
    expect(screen.getByText('未确认的更新请求：连接「企业 IdP v2」')).toBeInTheDocument()
    expect(calls.filter((c) => c.method === 'PATCH')).toHaveLength(1)

    // 读回最新配置即完成核对：解冻并清除页面级记录
    await user.click(screen.getByRole('button', { name: '重新加载最新配置' }))
    await waitFor(() => expect(screen.getByRole('button', { name: '保存' })).toBeDisabled())
    expect(screen.queryByText(/未确认的更新请求/)).not.toBeInTheDocument()

    // 用户重新编辑再提交（非自动重试），使用读回的最新版本
    await user.type(screen.getByLabelText('名称'), ' v2')
    await user.type(screen.getByLabelText('操作原因'), '核对后重新改名')
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => {
      const patches = calls.filter((c) => c.method === 'PATCH')
      expect(patches).toHaveLength(2)
      expect(patches[1]?.body).toMatchObject({ config_version: 3, name: '企业 IdP v2' })
    })
  })

  it('创建结果未知：冻结并保留页面级记录，核对确认前重开表单仍冻结；关闭后密钥清空', async () => {
    const user = userEvent.setup()
    const { calls } = stubRoutes({
      create: () => errorResponse(503, { code: 'outcome_unknown', detail: 'core 转发失败' }),
    })
    renderConnections()
    await user.click(await screen.findByRole('button', { name: '创建连接' }))
    await user.type(screen.getByLabelText('名称'), '新连接')
    await user.type(screen.getByLabelText('Issuer'), 'https://idp9.example.com')
    await user.type(screen.getByLabelText('Client ID'), 'cid-9')
    await user.type(screen.getByLabelText('Client Secret'), 'secret-9')
    await user.click(screen.getByRole('button', { name: '创建' }))

    expect(await screen.findByText('提交结果未知：请求可能已在服务端生效')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()

    // 关闭对话框：页面级提示保留未确认记录
    await user.click(screen.getByRole('button', { name: '取消' }))
    expect(await screen.findByText(/未确认的创建请求：连接「新连接」/)).toBeInTheDocument()

    // 重新打开：表单冻结，不能盲目重试；且密钥字段已清空
    await user.click(screen.getByRole('button', { name: '处理未确认的创建' }))
    expect(await screen.findByText('存在结果未知的创建请求，创建已冻结')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
    expect(screen.getByLabelText('Client Secret')).toHaveValue('')
    await user.click(screen.getByRole('button', { name: '取消' }))

    // 用户在页面级提示中确认已核对：解除冻结
    await user.click(screen.getByRole('button', { name: '已核对，确认未生效' }))
    expect(screen.queryByText(/未确认的创建请求/)).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '创建连接' }))
    expect(screen.queryByText('存在结果未知的创建请求，创建已冻结')).not.toBeInTheDocument()

    // 全程只有一次 POST，未发生盲目重试
    expect(calls.filter((c) => c.method === 'POST')).toHaveLength(1)
  })

  it('审计员只读：不展示创建与编辑入口，列表与登录方式仍可读', async () => {
    stubRoutes()
    renderConnections(
      makeSession({
        operator: { id: 'op-2', email: 'audit@example.com', role: 'auditor' },
        canMutate: false,
      }),
    )
    expect(await screen.findByText('企业 IdP')).toBeInTheDocument()
    expect(
      screen.getByText(/上游身份源：标准 OIDC、GitHub、微信、QQ、支付宝网页授权/),
    ).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '创建连接' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '编辑' })).not.toBeInTheDocument()
  })
  it('请求进行中 Escape 不能卸载创建表单；结果未知后保留冻结记录', async () => {
    const user = userEvent.setup()
    let finish: ((response: Response) => void) | undefined
    stubRoutes({
      create: () =>
        new Promise<Response>((resolve) => {
          finish = resolve
        }),
    })
    renderConnections()
    await user.click(await screen.findByRole('button', { name: '创建连接' }))
    await user.selectOptions(screen.getByLabelText('类型'), 'github')
    await user.type(screen.getByLabelText('名称'), 'Pending provider')
    await user.type(screen.getByLabelText('Client ID'), 'client')
    await user.type(screen.getByLabelText('Client Secret'), 'secret-only-fixture')
    await user.click(screen.getByRole('button', { name: '创建' }))
    await user.keyboard('{Escape}')
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    finish!(errorResponse(503, { code: 'outcome_unknown', detail: 'unknown' }))
    await screen.findByText('提交结果未知：请求可能已在服务端生效')
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.getByText('未确认的创建请求：连接「Pending provider」')).toBeInTheDocument()
  })

  it('未知的密钥更新冻结其他连接；读回会清空旧密钥和输入', async () => {
    const user = userEvent.setup()
    const { calls } = stubRoutes({
      list: () =>
        jsonResponse({
          items: [oidcConnection, { ...oidcConnection, id: 'conn-2', name: 'Other provider' }],
          next_cursor: null,
        }),
      read: () => jsonResponse({ ...oidcConnection, config_version: 3 }),
      patch: () => errorResponse(503, { code: 'outcome_unknown', detail: 'unknown' }),
    })
    renderConnections()
    await user.click((await screen.findAllByRole('button', { name: '编辑' }))[0]!)
    await user.type(screen.getByLabelText('新 Client Secret'), 'possibly-applied-secret')
    await user.type(screen.getByLabelText('操作原因'), 'rotate')
    await user.click(screen.getByRole('button', { name: '保存' }))
    await screen.findByText('提交结果未知：请求可能已在服务端生效')
    expect(screen.getAllByRole('button', { name: '编辑' })[1]).toBeDisabled()
    await user.click(screen.getByRole('button', { name: '重新加载最新配置' }))
    await waitFor(() => expect(screen.getByLabelText('新 Client Secret')).toHaveValue(''))
    expect(screen.getByLabelText('操作原因')).toHaveValue('')
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()
    expect(calls.filter((c) => c.method === 'PATCH')).toHaveLength(1)
  })

  const AUTHCONFIG_PATH = '/bff/apps/app-1/authentication-config'

  function authConfigWith(federationEnabled: boolean) {
    return jsonResponse({
      password_login: true,
      email_otp_configured: true,
      phone_otp_configured: false,
      passkey_available: true,
      policy: {
        registration_enabled: true,
        password_login_enabled: true,
        email_otp_enabled: false,
        phone_otp_enabled: false,
        federation_enabled: federationEnabled,
        external_registration_enabled: true,
        passkey_enabled: false,
      },
      config_version: 2,
      provider_types: ['oidc'],
      federation_callback_url: 'https://auth.example.com/trade-web/federation/callback',
    })
  }

  function stubWithGate(federationEnabled: boolean) {
    return stubFetch((call) => {
      const path = new URL(call.url, 'http://localhost').pathname
      if (path === AUTHCONFIG_PATH) return authConfigWith(federationEnabled)
      if (path === LIST_PATH) return jsonResponse({ items: [oidcConnection], next_cursor: null })
      return errorResponse(404, { detail: `未 stub 的请求：${call.method} ${path}` })
    })
  }

  it('第三方登录总开关关闭：已启用连接标注「未生效」，顶部提示并链接到配置页', async () => {
    stubWithGate(false)
    renderConnections()

    expect(
      await screen.findByText('第三方登录总开关已关闭，已启用的连接暂不出现在登录页'),
    ).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '前往配置' })).toHaveAttribute(
      'href',
      '/apps/app-1/settings',
    )
    expect(await screen.findByText('未生效：第三方登录总开关已关闭')).toBeInTheDocument()
  })

  it('第三方登录总开关开启：已启用连接标注「登录页可见」，无误导性提示', async () => {
    stubWithGate(true)
    renderConnections()

    expect(await screen.findByText('登录页可见')).toBeInTheDocument()
    expect(screen.queryByText(/暂不出现在登录页/)).not.toBeInTheDocument()
  })

  it('策略读取失败：不编造生效状态，仅如实提示', async () => {
    stubFetch((call) => {
      const path = new URL(call.url, 'http://localhost').pathname
      if (path === AUTHCONFIG_PATH)
        return errorResponse(503, { code: 'dependency_unavailable', detail: 'core 暂不可用' })
      if (path === LIST_PATH) return jsonResponse({ items: [oidcConnection], next_cursor: null })
      return errorResponse(404, { detail: `未 stub 的请求：${call.method} ${path}` })
    })
    renderConnections()

    expect(await screen.findByText('企业 IdP')).toBeInTheDocument()
    expect(await screen.findByText(/「登录页生效」状态暂不显示/)).toBeInTheDocument()
    expect(screen.queryByText('登录页可见')).not.toBeInTheDocument()
    expect(screen.queryByText(/暂不出现在登录页/)).not.toBeInTheDocument()
  })

  it('列表刷新期间不以旧快照声明可见；禁用连接读回后保持不可见', async () => {
    const user = userEvent.setup()
    let listCalls = 0
    let finish: ((response: Response) => void) | undefined
    stubFetch((call) => {
      const path = new URL(call.url, 'http://localhost').pathname
      if (path === AUTHCONFIG_PATH) return authConfigWith(true)
      if (path === LIST_PATH) {
        if (++listCalls === 1) return jsonResponse({ items: [oidcConnection], next_cursor: null })
        return new Promise<Response>((resolve) => {
          finish = resolve
        })
      }
      return errorResponse(404)
    })
    renderConnections()
    expect(await screen.findByText('登录页可见')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '刷新列表' }))
    await waitFor(() => {
      expect(finish).toBeDefined()
      expect(screen.queryByText('登录页可见')).not.toBeInTheDocument()
    })
    finish!(jsonResponse({ items: [{ ...oidcConnection, status: 'disabled' }], next_cursor: null }))
    expect(await screen.findByText('已禁用')).toBeInTheDocument()
    expect(screen.queryByText('登录页可见')).not.toBeInTheDocument()
  })

  it('列表刷新失败：保留的旧快照标注可能过期，「登录页可见」不再展示', async () => {
    const user = userEvent.setup()
    let listCalls = 0
    stubFetch((call) => {
      const path = new URL(call.url, 'http://localhost').pathname
      if (path === AUTHCONFIG_PATH) return authConfigWith(true)
      if (path === LIST_PATH) {
        listCalls += 1
        // 首次成功读回活跃连接；刷新失败后 useAsync 保留旧数据
        return listCalls === 1
          ? jsonResponse({ items: [oidcConnection], next_cursor: null })
          : errorResponse(503, { code: 'dependency_unavailable', detail: 'core 暂不可用' })
      }
      return errorResponse(404, { detail: `未 stub 的请求：${call.method} ${path}` })
    })
    renderConnections()

    expect(await screen.findByText('登录页可见')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '刷新列表' }))

    // 过期快照不得继续派生「登录页可见」
    expect(await screen.findByText('连接列表刷新失败')).toBeInTheDocument()
    expect(screen.getByText(/最近一次成功读取的快照/)).toBeInTheDocument()
    expect(screen.queryByText('登录页可见')).not.toBeInTheDocument()
  })

  it('应用已暂停：即使总开关与连接均已启用，也不呈现「登录页可见」', async () => {
    stubWithGate(true)
    renderConnections(makeSession(), { ...app, status: 'suspended' })

    expect(await screen.findByText('应用已暂停：登录页与令牌签发不可用')).toBeInTheDocument()
    expect(await screen.findByText('未生效：应用已暂停')).toBeInTheDocument()
    expect(screen.queryByText('登录页可见')).not.toBeInTheDocument()
  })

  describe('支付宝连接', () => {
    function stubAlipayRoutes(overrides: { read?: FetchHandler; patch?: FetchHandler } = {}) {
      return stubFetch((call) => {
        const path = new URL(call.url, 'http://localhost').pathname
        if (path === ALIPAY_PATH && call.method === 'GET') {
          return overrides.read ? overrides.read(call) : jsonResponse(alipayConnection)
        }
        if (path === ALIPAY_PATH && call.method === 'PATCH') {
          return overrides.patch ? overrides.patch(call) : jsonResponse(alipayConnection)
        }
        if (path === LIST_PATH && call.method === 'POST') {
          return jsonResponse(alipayConnection, 201)
        }
        if (path === LIST_PATH) {
          return jsonResponse({ items: [alipayConnection], next_cursor: null })
        }
        return errorResponse(404, { detail: `未 stub 的请求：${call.method} ${path}` })
      })
    }

    it('创建：密钥对完整必填，提交 alipay_credentials 且绝不携带 client_secret/issuer', async () => {
      const user = userEvent.setup()
      const { calls } = stubRoutes({
        create: () => jsonResponse(alipayConnection, 201),
      })
      renderConnections()
      await user.click(await screen.findByRole('button', { name: '创建连接' }))
      await user.selectOptions(screen.getByLabelText('类型'), 'alipay')

      // 支付宝：无 Client Secret 输入，Issuer 由服务端固定
      expect(screen.queryByLabelText('Client Secret')).not.toBeInTheDocument()
      expect(screen.queryByLabelText('Issuer')).not.toBeInTheDocument()
      expect(screen.getByText('https://openauth.alipay.com')).toBeInTheDocument()

      await user.type(screen.getByLabelText('名称'), '支付宝登录')
      await user.type(screen.getByLabelText('Client ID'), '2021000000000001')
      await user.type(screen.getByLabelText('应用私钥'), FAKE_PRIVATE_KEY)
      await user.click(screen.getByRole('button', { name: '创建' }))
      // 缺支付宝公钥：密钥对不完整，不发请求
      expect(await screen.findByText('请输入支付宝公钥')).toBeInTheDocument()
      expect(calls.every((c) => c.method === 'GET')).toBe(true)

      await user.type(screen.getByLabelText('支付宝公钥'), FAKE_ALIPAY_PUBLIC_KEY)
      await user.click(screen.getByRole('button', { name: '创建' }))
      await waitFor(() => {
        const create = calls.find((c) => c.method === 'POST')
        expect(create?.body).toEqual({
          name: '支付宝登录',
          provider_type: 'alipay',
          client_id: '2021000000000001',
          alipay_credentials: {
            private_key: FAKE_PRIVATE_KEY,
            alipay_public_key: FAKE_ALIPAY_PUBLIC_KEY,
          },
        })
        expect(create?.body).not.toHaveProperty('client_secret')
        expect(create?.body).not.toHaveProperty('issuer')
      })
    })

    it('创建：AppID 为纯数字字符串（文本输入，绝不数值化），否则不发请求', async () => {
      const user = userEvent.setup()
      const { calls } = stubRoutes()
      renderConnections()
      await user.click(await screen.findByRole('button', { name: '创建连接' }))
      await user.selectOptions(screen.getByLabelText('类型'), 'alipay')

      const appIdInput = screen.getByLabelText('Client ID')
      expect(appIdInput).toHaveAttribute('inputmode', 'numeric')
      expect(appIdInput).not.toHaveAttribute('type', 'number')

      await user.type(screen.getByLabelText('名称'), '支付宝登录')
      await user.type(appIdInput, 'appid-abc')
      await user.type(screen.getByLabelText('应用私钥'), FAKE_PRIVATE_KEY)
      await user.type(screen.getByLabelText('支付宝公钥'), FAKE_ALIPAY_PUBLIC_KEY)
      await user.click(screen.getByRole('button', { name: '创建' }))
      expect(await screen.findByText('支付宝 AppID 为纯数字')).toBeInTheDocument()
      expect(calls.every((c) => c.method === 'GET')).toBe(true)
    })

    it('创建：密钥为多行输入且禁用拼写/自动填充改写', async () => {
      const user = userEvent.setup()
      stubRoutes()
      renderConnections()
      await user.click(await screen.findByRole('button', { name: '创建连接' }))
      await user.selectOptions(screen.getByLabelText('类型'), 'alipay')

      for (const label of ['应用私钥', '支付宝公钥']) {
        const field = screen.getByLabelText(label)
        expect(field.tagName).toBe('TEXTAREA')
        expect(field).toHaveAttribute('spellcheck', 'false')
        expect(field).toHaveAttribute('autocomplete', 'off')
        expect(field).toHaveAttribute('autocapitalize', 'none')
        expect(field).toHaveAttribute('autocorrect', 'off')
      }
    })

    it('创建：切换提供方清空已填写的密钥，避免密钥被提交到另一个提供方', async () => {
      const user = userEvent.setup()
      stubRoutes()
      renderConnections()
      await user.click(await screen.findByRole('button', { name: '创建连接' }))

      await user.selectOptions(screen.getByLabelText('类型'), 'wechat')
      await user.type(screen.getByLabelText('Client Secret'), 'wx-secret-draft')

      // 切到支付宝：Client Secret 草稿随字段卸载被清除，密钥对输入为空
      await user.selectOptions(screen.getByLabelText('类型'), 'alipay')
      expect(screen.queryByLabelText('Client Secret')).not.toBeInTheDocument()
      expect(screen.getByLabelText('应用私钥')).toHaveValue('')
      expect(screen.getByLabelText('支付宝公钥')).toHaveValue('')

      // 切回微信：Client Secret 为空，不残留旧草稿
      await user.selectOptions(screen.getByLabelText('类型'), 'wechat')
      expect(screen.getByLabelText('Client Secret')).toHaveValue('')
    })

    it('编辑：不轮换密钥时省略 alipay_credentials 与 client_secret', async () => {
      const user = userEvent.setup()
      const { calls } = stubAlipayRoutes({
        patch: () =>
          jsonResponse({ ...alipayConnection, name: '支付宝登录 v2', config_version: 3 }),
      })
      renderConnections()
      await user.click(await screen.findByRole('button', { name: '编辑' }))

      // 支付宝无 Client Secret 字段，展示密钥对轮换输入
      expect(screen.queryByLabelText('新 Client Secret')).not.toBeInTheDocument()
      expect(screen.getByLabelText('新应用私钥')).toBeInTheDocument()
      expect(screen.getByLabelText('新支付宝公钥')).toBeInTheDocument()

      await user.type(screen.getByLabelText('名称'), ' v2')
      await user.type(screen.getByLabelText('操作原因'), '改名')
      await user.click(screen.getByRole('button', { name: '保存' }))
      await waitFor(() => {
        const patch = calls.find((c) => c.method === 'PATCH')
        expect(patch?.body).toEqual({ config_version: 2, reason: '改名', name: '支付宝登录 v2' })
        expect(patch?.body).not.toHaveProperty('alipay_credentials')
        expect(patch?.body).not.toHaveProperty('client_secret')
      })
    })

    it('编辑：轮换须两把同时提供；完整密钥对原子提交', async () => {
      const user = userEvent.setup()
      const { calls } = stubAlipayRoutes({
        patch: () => jsonResponse({ ...alipayConnection, config_version: 3 }),
      })
      renderConnections()
      await user.click(await screen.findByRole('button', { name: '编辑' }))

      // 只填一把：阻止提交
      await user.type(screen.getByLabelText('新应用私钥'), FAKE_PRIVATE_KEY)
      await user.type(screen.getByLabelText('操作原因'), '例行轮换')
      await user.click(screen.getByRole('button', { name: '保存' }))
      expect(await screen.findByText(/轮换须同时填写应用私钥与支付宝公钥/)).toBeInTheDocument()
      expect(calls.every((c) => c.method === 'GET')).toBe(true)

      // 补齐后提交完整密钥对
      await user.type(screen.getByLabelText('新支付宝公钥'), FAKE_ALIPAY_PUBLIC_KEY)
      await user.click(screen.getByRole('button', { name: '保存' }))
      await waitFor(() => {
        const patch = calls.find((c) => c.method === 'PATCH')
        expect(patch?.body).toEqual({
          config_version: 2,
          reason: '例行轮换',
          alipay_credentials: {
            private_key: FAKE_PRIVATE_KEY,
            alipay_public_key: FAKE_ALIPAY_PUBLIC_KEY,
          },
        })
        expect(patch?.body).not.toHaveProperty('client_secret')
      })
    })

    it('编辑：409 冻结后权威读回清空两把密钥草稿', async () => {
      const user = userEvent.setup()
      let patches = 0
      stubAlipayRoutes({
        read: () => jsonResponse({ ...alipayConnection, config_version: 5 }),
        patch: () => {
          patches += 1
          return errorResponse(409, { code: 'version_conflict', detail: 'config_version 已过期' })
        },
      })
      renderConnections()
      await user.click(await screen.findByRole('button', { name: '编辑' }))
      await user.type(screen.getByLabelText('新应用私钥'), FAKE_PRIVATE_KEY)
      await user.type(screen.getByLabelText('新支付宝公钥'), FAKE_ALIPAY_PUBLIC_KEY)
      await user.type(screen.getByLabelText('操作原因'), '例行轮换')
      await user.click(screen.getByRole('button', { name: '保存' }))

      expect(await screen.findByText('配置版本已过期（409 冲突）')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()
      expect(patches).toBe(1)

      await user.click(screen.getByRole('button', { name: '重新加载最新配置' }))
      await waitFor(() =>
        expect(screen.queryByText('配置版本已过期（409 冲突）')).not.toBeInTheDocument(),
      )
      // 读回清空两把密钥与原因，版本回读为权威值
      expect(screen.getByLabelText('新应用私钥')).toHaveValue('')
      expect(screen.getByLabelText('新支付宝公钥')).toHaveValue('')
      expect(screen.getByLabelText('操作原因')).toHaveValue('')
      expect(patches).toBe(1)
    })
  })
})
