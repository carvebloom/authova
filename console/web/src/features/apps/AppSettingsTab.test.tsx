import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Outlet, Route, Routes } from 'react-router-dom'
import type { App, AuthenticationConfig } from '../../api/types'
import {
  errorResponse,
  jsonResponse,
  makeSession,
  renderWithSession,
  stubFetch,
  type FetchCall,
} from '../../test/utils'
import { AppSettingsTab } from './AppSettingsTab'

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

const authConfig: AuthenticationConfig = {
  password_login: true,
  email_otp_configured: true,
  phone_otp_configured: false,
  passkey_available: true,
  policy: {
    registration_enabled: true,
    password_login_enabled: true,
    email_otp_enabled: false,
    phone_otp_enabled: false,
    federation_enabled: true,
    external_registration_enabled: true,
    passkey_enabled: false,
  },
  config_version: 2,
  provider_types: ['oidc'],
  federation_callback_url: 'https://auth.example.com/trade-web/federation/callback',
}

const APP_PATH = '/bff/apps/app-1'

/** 应用配置 + 登录策略读取桩；patch/read handler 缺省时返回静态夹具 */
function stubSettingsApi(options: {
  appPatch?: (call: FetchCall) => Response
  appRead?: () => Response
  policyPatch?: (call: FetchCall) => Response
  policyRead?: () => Response
}) {
  return stubFetch((call) => {
    if (call.url === `${APP_PATH}/authentication-config`) {
      if (call.method === 'PATCH') {
        return options.policyPatch ? options.policyPatch(call) : jsonResponse(authConfig)
      }
      return options.policyRead ? options.policyRead() : jsonResponse(authConfig)
    }
    if (call.method === 'PATCH') {
      return options.appPatch ? options.appPatch(call) : jsonResponse(app)
    }
    return options.appRead ? options.appRead() : jsonResponse(app)
  })
}

function FakeAppLayout() {
  return <Outlet context={{ app, reloadApp: vi.fn() }} />
}

function renderSettings(session = makeSession()) {
  renderWithSession(
    <Routes>
      <Route path="/apps/:appId" element={<FakeAppLayout />}>
        <Route path="settings" element={<AppSettingsTab />} />
      </Route>
    </Routes>,
    session,
    ['/apps/app-1/settings'],
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('AppSettingsTab 应用配置', () => {
  it('修改名称与 MFA：PATCH 仅携带变化字段与 config_version，成功后提示', async () => {
    const user = userEvent.setup()
    const { calls } = stubSettingsApi({
      appPatch: () =>
        jsonResponse({ ...app, name: '交易前台 v2', require_mfa: true, config_version: 4 }),
    })
    renderSettings()

    // 未修改时保存不可用
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()

    await user.clear(screen.getByLabelText('应用名称'))
    await user.type(screen.getByLabelText('应用名称'), '交易前台 v2')
    await user.click(screen.getByRole('checkbox', { name: /要求用户 MFA/ }))
    await user.click(screen.getByRole('button', { name: '保存' }))

    await waitFor(() => {
      const patch = calls.find((c) => c.method === 'PATCH')
      expect(patch?.url).toBe(APP_PATH)
      expect(patch?.body).toEqual({ config_version: 3, name: '交易前台 v2', require_mfa: true })
    })
    expect(await screen.findByText(/已更新（配置版本 4）/)).toBeInTheDocument()
  })

  it('409 版本冲突：冻结保存，读回最新配置后用新版本重新提交', async () => {
    const user = userEvent.setup()
    let patchCalls = 0
    const { calls } = stubSettingsApi({
      appPatch: () => {
        patchCalls += 1
        return patchCalls === 1
          ? errorResponse(409, { code: 'version_conflict', detail: 'config_version 已过期' })
          : jsonResponse({ ...app, name: '改名成功', config_version: 6 })
      },
      // 读回最新配置
      appRead: () => jsonResponse({ ...app, config_version: 5 }),
    })
    renderSettings()
    await user.type(screen.getByLabelText('应用名称'), ' v2')
    await user.click(screen.getByRole('button', { name: '保存' }))

    expect(await screen.findByText('配置版本已过期（409 冲突）')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()
    expect(calls.filter((c) => c.method === 'PATCH')).toHaveLength(1)

    await user.click(screen.getByRole('button', { name: '重新加载最新配置' }))
    await waitFor(() =>
      expect(screen.queryByText('配置版本已过期（409 冲突）')).not.toBeInTheDocument(),
    )
    // 读回后输入被重置为权威状态，保存重新可用需再次修改
    expect(screen.getByLabelText('应用名称')).toHaveValue('交易前台')
    await user.clear(screen.getByLabelText('应用名称'))
    await user.type(screen.getByLabelText('应用名称'), '改名成功')
    await user.click(screen.getByRole('button', { name: '保存' }))

    await waitFor(() => {
      const patches = calls.filter((c) => c.method === 'PATCH')
      expect(patches).toHaveLength(2)
      expect(patches[1]?.body).toMatchObject({ config_version: 5, name: '改名成功' })
    })
  })

  it('结果未知：冻结提交，读回权威状态后解除冻结', async () => {
    const user = userEvent.setup()
    let patchCalls = 0
    const { calls } = stubSettingsApi({
      appPatch: () => {
        patchCalls += 1
        return patchCalls === 1
          ? errorResponse(503, { code: 'outcome_unknown', detail: 'core 转发失败' })
          : jsonResponse({ ...app, name: '交易前台 v2', config_version: 4 })
      },
      appRead: () => jsonResponse({ ...app, name: '交易前台 v2', config_version: 4 }),
    })
    renderSettings()
    await user.type(screen.getByLabelText('应用名称'), ' v2')
    await user.click(screen.getByRole('button', { name: '保存' }))

    expect(await screen.findByText('提交结果未知：请求可能已在服务端生效')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()
    expect(calls.filter((c) => c.method === 'PATCH')).toHaveLength(1)

    await user.click(screen.getByRole('button', { name: '重新加载最新配置' }))
    await waitFor(() =>
      expect(screen.queryByText('提交结果未知：请求可能已在服务端生效')).not.toBeInTheDocument(),
    )
    // 读回显示实际已生效（名称已为 v2），不再重复提交
    expect(screen.getByLabelText('应用名称')).toHaveValue('交易前台 v2')
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()
    expect(calls.filter((c) => c.method === 'PATCH')).toHaveLength(1)
  })

  it('只读角色：不展示表单与保存入口，也不发起任何写请求', async () => {
    const { calls } = stubSettingsApi({})
    renderSettings(
      makeSession({
        operator: { id: 'op-2', email: 'audit@example.com', role: 'auditor' },
        canMutate: false,
      }),
    )
    expect(screen.queryByLabelText('应用名称')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '保存' })).not.toBeInTheDocument()
    expect(screen.getByText('当前账号为只读角色，不能修改配置。')).toBeInTheDocument()
    // 登录策略版块以只读方式加载（GET），绝不发起写请求
    expect(await screen.findByText(/当前账号对登录策略为只读/)).toBeInTheDocument()
    expect(calls.filter((c) => c.method !== 'GET')).toHaveLength(0)
  })
})
