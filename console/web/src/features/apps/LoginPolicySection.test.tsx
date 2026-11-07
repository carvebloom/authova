import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AuthenticationConfig } from '../../api/types'
import { errorResponse, jsonResponse, stubFetch } from '../../test/utils'
import { LoginPolicySection } from './LoginPolicySection'

const baseConfig: AuthenticationConfig = {
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

const CONFIG_URL = '/bff/apps/app-1/authentication-config'
const CONNECTIONS_PATH = '/bff/apps/app-1/connections'

function connectionItem(id: string, status: 'active' | 'disabled') {
  return {
    id,
    name: id,
    provider_type: 'oidc',
    issuer: 'https://idp.example.com',
    client_id: `cid-${id}`,
    status,
    config_version: 1,
  }
}

/** 策略 + 连接列表读取桩；patch/read 缺省返回静态夹具 */
function stubPolicyApi(options: {
  config?: AuthenticationConfig
  connections?: Response | ((callCount: number) => Response)
  policyPatch?: (call: { body: unknown }) => Response
}) {
  let connectionCalls = 0
  const config = options.config ?? baseConfig
  return stubFetch((call) => {
    const path = new URL(call.url, 'http://localhost').pathname
    if (path === CONNECTIONS_PATH) {
      connectionCalls += 1
      const c = options.connections
      if (!c) return jsonResponse({ items: [], next_cursor: null })
      return typeof c === 'function' ? c(connectionCalls) : c
    }
    if (call.method === 'PATCH') {
      return options.policyPatch
        ? options.policyPatch(call)
        : jsonResponse({ ...config, config_version: 3 })
    }
    return jsonResponse(config)
  })
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('LoginPolicySection 登录策略', () => {
  it('更新策略：PATCH 携带完整六个开关 + config_version + reason，成功后清空原因', async () => {
    const user = userEvent.setup()
    const { calls } = stubFetch((call) => {
      if (call.method === 'PATCH') {
        return jsonResponse({
          ...baseConfig,
          policy: { ...baseConfig.policy, email_otp_enabled: true },
          config_version: 3,
        })
      }
      return jsonResponse(baseConfig)
    })
    render(<LoginPolicySection appId="app-1" canWrite />)

    const save = await screen.findByRole('button', { name: '保存登录策略' })
    expect(save).toBeDisabled()

    await user.click(screen.getByRole('checkbox', { name: /邮箱验证码登录/ }))
    await user.click(screen.getByRole('button', { name: '保存登录策略' }))
    // 原因必填
    expect(await screen.findByText('请填写操作原因')).toBeInTheDocument()
    expect(calls.filter((c) => c.method === 'PATCH')).toHaveLength(0)

    await user.type(screen.getByLabelText('操作原因'), '开通邮箱验证码登录')
    await user.click(screen.getByRole('button', { name: '保存登录策略' }))

    await waitFor(() => {
      const patch = calls.find((c) => c.method === 'PATCH')
      expect(patch?.url).toBe(CONFIG_URL)
      expect(patch?.body).toEqual({
        config_version: 2,
        reason: '开通邮箱验证码登录',
        policy: {
          registration_enabled: true,
          password_login_enabled: true,
          email_otp_enabled: true,
          phone_otp_enabled: false,
          federation_enabled: true,
          external_registration_enabled: true,
          passkey_enabled: false,
        },
      })
    })
    expect(await screen.findByText(/已更新（配置版本 3）/)).toBeInTheDocument()
    expect(screen.getByLabelText('操作原因')).toHaveValue('')
  })

  it('OTP 开关只代表登录方式：通道未配置时给出内联提示，摘要与部署就绪状态如实降级', async () => {
    stubFetch(() =>
      jsonResponse({
        ...baseConfig,
        policy: { ...baseConfig.policy, phone_otp_enabled: true },
      }),
    )
    render(<LoginPolicySection appId="app-1" canWrite />)

    // 短信 OTP 登录已启用但短信投递通道未配置：开关旁内联警示
    expect(await screen.findByText('通道未配置')).toBeInTheDocument()
    // 摘要徽章如实降级为「已开启 · 通道未配置」，不声称通道可用
    expect(screen.getAllByText('短信验证码 · 通道未配置').length).toBeGreaterThan(0)
    // 部署就绪状态独立展示（只读），不与策略混淆
    expect(screen.getByText('部署就绪状态（只读）')).toBeInTheDocument()
    expect(screen.getByText('短信投递通道（部署）')).toBeInTheDocument()
  })

  it('409 冲突：冻结表单，读回最新策略后重新编辑提交', async () => {
    const user = userEvent.setup()
    let patches = 0
    const { calls } = stubFetch((call) => {
      if (call.method === 'PATCH') {
        patches += 1
        return patches === 1
          ? errorResponse(409, { code: 'version_conflict', detail: 'config_version 已过期' })
          : jsonResponse({
              ...baseConfig,
              policy: { ...baseConfig.policy, registration_enabled: false },
              config_version: 6,
            })
      }
      // 读回：版本已被他人推进到 5
      return jsonResponse({ ...baseConfig, config_version: 5 })
    })
    render(<LoginPolicySection appId="app-1" canWrite />)

    await user.click(await screen.findByRole('checkbox', { name: /允许新用户注册/ }))
    await user.type(screen.getByLabelText('操作原因'), '暂停开放注册')
    await user.click(screen.getByRole('button', { name: '保存登录策略' }))

    expect(await screen.findByText('配置版本已过期（409 冲突）')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存登录策略' })).toBeDisabled()
    expect(calls.filter((c) => c.method === 'PATCH')).toHaveLength(1)

    await user.click(screen.getByRole('button', { name: '重新加载最新策略' }))
    await waitFor(() =>
      expect(screen.queryByText('配置版本已过期（409 冲突）')).not.toBeInTheDocument(),
    )
    // 读回后表单重置为权威状态（注册仍为开启），需重新修改
    await user.click(screen.getByRole('checkbox', { name: /允许新用户注册/ }))
    await user.type(screen.getByLabelText('操作原因'), '暂停开放注册')
    await user.click(screen.getByRole('button', { name: '保存登录策略' }))

    await waitFor(() => {
      const patches2 = calls.filter((c) => c.method === 'PATCH')
      expect(patches2).toHaveLength(2)
      expect(patches2[1]?.body).toMatchObject({
        config_version: 5,
        policy: { registration_enabled: false },
      })
    })
  })

  it('结果未知：冻结保存并引导读回，读回后解除冻结', async () => {
    const user = userEvent.setup()
    let patches = 0
    stubFetch((call) => {
      if (call.method === 'PATCH') {
        patches += 1
        return errorResponse(503, { code: 'outcome_unknown', detail: 'core 转发失败' })
      }
      return jsonResponse(baseConfig)
    })
    render(<LoginPolicySection appId="app-1" canWrite />)

    await user.click(await screen.findByRole('checkbox', { name: /^第三方登录$/ }))
    await user.type(screen.getByLabelText('操作原因'), '临时关闭第三方登录')
    await user.click(screen.getByRole('button', { name: '保存登录策略' }))

    expect(await screen.findByText('提交结果未知：请求可能已在服务端生效')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存登录策略' })).toBeDisabled()
    expect(patches).toBe(1)

    await user.click(screen.getByRole('button', { name: '重新加载最新策略' }))
    await waitFor(() =>
      expect(screen.queryByText('提交结果未知：请求可能已在服务端生效')).not.toBeInTheDocument(),
    )
    // 读回后未自动重试
    expect(patches).toBe(1)
  })

  it('读失败展示错误与重试', async () => {
    stubFetch(() => errorResponse(503, { code: 'dependency_unavailable' }))
    render(<LoginPolicySection appId="app-1" canWrite />)
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
  })

  it('只读（canWrite=false）：展示策略与就绪状态，无表单与写入口', async () => {
    const { calls } = stubFetch(() => jsonResponse(baseConfig))
    render(<LoginPolicySection appId="app-1" canWrite={false} />)

    expect(await screen.findByText(/当前账号对登录策略为只读/)).toBeInTheDocument()
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '保存登录策略' })).not.toBeInTheDocument()
    expect(calls.filter((c) => c.method !== 'GET')).toHaveLength(0)
  })
})

describe('LoginPolicySection 方式摘要与本地预设', () => {
  it('摘要展示当前配置方式与已启用连接计数；草稿变更时出现对比行', async () => {
    const user = userEvent.setup()
    stubPolicyApi({
      connections: jsonResponse({
        items: [connectionItem('c1', 'active'), connectionItem('c2', 'disabled')],
        next_cursor: null,
      }),
    })
    render(<LoginPolicySection appId="app-1" canWrite />)

    const currentRow = (await screen.findByText('当前配置')).closest('.method-summary-row')
    expect(currentRow).toHaveTextContent('密码登录')
    await waitFor(() => expect(currentRow).toHaveTextContent('第三方登录 · 1 个已启用连接'))
    expect(screen.queryByText('未保存草稿')).not.toBeInTheDocument()

    // 关闭密码登录：草稿行对比出现，且不再包含密码登录
    await user.click(screen.getByRole('checkbox', { name: /^密码登录$/ }))
    const draftRow = (await screen.findByText('未保存草稿')).closest('.method-summary-row')
    expect(draftRow).toHaveTextContent('第三方登录 · 1 个已启用连接')
    expect(draftRow).not.toHaveTextContent('密码登录')
  })

  it('「仅第三方登录」预设：只改草稿、保留注册开关、零网络请求，保存仍要求原因并完整提交六项', async () => {
    const user = userEvent.setup()
    const { calls } = stubPolicyApi({
      connections: jsonResponse({ items: [connectionItem('c1', 'active')], next_cursor: null }),
      policyPatch: () =>
        jsonResponse({
          ...baseConfig,
          policy: { ...baseConfig.policy, password_login_enabled: false },
          config_version: 3,
        }),
    })
    render(<LoginPolicySection appId="app-1" canWrite />)

    await user.click(await screen.findByRole('button', { name: '仅第三方登录' }))

    // 注册两项保持不变；登录方式按预设调整
    expect(screen.getByRole('checkbox', { name: /允许新用户注册/ })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: /第三方首次登录创建用户/ })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: /^密码登录$/ })).not.toBeChecked()
    expect(screen.getByRole('checkbox', { name: /^第三方登录$/ })).toBeChecked()
    expect(screen.getByText('未保存草稿')).toBeInTheDocument()
    // 预设不触网：至今只有 GET
    expect(calls.filter((c) => c.method !== 'GET')).toHaveLength(0)

    // 操作原因仍必填，保存才发起 PATCH
    await user.click(screen.getByRole('button', { name: '保存登录策略' }))
    expect(await screen.findByText('请填写操作原因')).toBeInTheDocument()
    expect(calls.filter((c) => c.method === 'PATCH')).toHaveLength(0)

    await user.type(screen.getByLabelText('操作原因'), '业务改为仅第三方身份源登录')
    await user.click(screen.getByRole('button', { name: '保存登录策略' }))
    await waitFor(() => {
      const patch = calls.find((c) => c.method === 'PATCH')
      expect(patch?.body).toEqual({
        config_version: 2,
        reason: '业务改为仅第三方身份源登录',
        policy: {
          registration_enabled: true,
          password_login_enabled: false,
          email_otp_enabled: false,
          phone_otp_enabled: false,
          federation_enabled: true,
          external_registration_enabled: true,
          passkey_enabled: false,
        },
      })
    })
  })

  it('全部方式关闭：给出可行动警告但不拦截维护策略保存', async () => {
    const user = userEvent.setup()
    const { calls } = stubPolicyApi({
      connections: jsonResponse({ items: [], next_cursor: null }),
      policyPatch: () =>
        jsonResponse({
          ...baseConfig,
          policy: {
            ...baseConfig.policy,
            password_login_enabled: false,
            federation_enabled: false,
          },
          config_version: 3,
        }),
    })
    render(<LoginPolicySection appId="app-1" canWrite />)

    await user.click(await screen.findByRole('checkbox', { name: /^密码登录$/ }))
    await user.click(screen.getByRole('checkbox', { name: /^第三方登录$/ }))
    expect(await screen.findByText('草稿将关闭全部登录方式')).toBeInTheDocument()

    // 重新开启任一方式：警告消失
    await user.click(screen.getByRole('checkbox', { name: /^第三方登录$/ }))
    expect(screen.queryByText('草稿将关闭全部登录方式')).not.toBeInTheDocument()

    // 再次全关仍可保存：警告不拦截、不静默修改
    await user.click(screen.getByRole('checkbox', { name: /^第三方登录$/ }))
    await user.type(screen.getByLabelText('操作原因'), '维护窗口期暂停终端登录')
    await user.click(screen.getByRole('button', { name: '保存登录策略' }))
    await waitFor(() => {
      const patch = calls.find((c) => c.method === 'PATCH')
      expect(patch?.body).toMatchObject({
        policy: {
          password_login_enabled: false,
          email_otp_enabled: false,
          phone_otp_enabled: false,
          federation_enabled: false,
        },
      })
    })
  })

  it('当前配置已全关且未修改：如实陈述当前状态，而非「草稿将关闭全部」', async () => {
    stubPolicyApi({
      config: {
        ...baseConfig,
        policy: {
          registration_enabled: false,
          password_login_enabled: false,
          email_otp_enabled: false,
          phone_otp_enabled: false,
          federation_enabled: false,
          external_registration_enabled: false,
          passkey_enabled: false,
        },
      },
      connections: jsonResponse({ items: [], next_cursor: null }),
    })
    render(<LoginPolicySection appId="app-1" canWrite />)

    expect(await screen.findByText('当前配置已关闭全部登录方式')).toBeInTheDocument()
    expect(screen.queryByText('草稿将关闭全部登录方式')).not.toBeInTheDocument()
    expect(screen.getAllByText('无可用登录方式').length).toBeGreaterThan(0)
  })

  it('开启的方式均因部署缺失不可用：显著警告并列出原因', async () => {
    stubPolicyApi({
      config: {
        ...baseConfig,
        password_login: false,
        email_otp_configured: false,
        policy: {
          ...baseConfig.policy,
          password_login_enabled: true,
          email_otp_enabled: true,
          federation_enabled: false,
        },
      },
      connections: jsonResponse({ items: [], next_cursor: null }),
    })
    render(<LoginPolicySection appId="app-1" canWrite />)

    expect(await screen.findByText('当前配置没有可用的登录方式')).toBeInTheDocument()
    expect(screen.getByText(/密码登录（部署未启用）/)).toBeInTheDocument()
    expect(screen.getByText(/邮箱验证码（邮件通道未配置）/)).toBeInTheDocument()
  })

  it('第三方登录已允许但无已启用连接：开关旁给出内联提示与连接页入口，不再占用大警告', async () => {
    stubPolicyApi({
      connections: jsonResponse({ items: [connectionItem('c1', 'disabled')], next_cursor: null }),
    })
    render(<LoginPolicySection appId="app-1" canWrite />)

    // 密码登录可用：不是显著警告，仅在第三方登录开关旁内联提示
    expect(
      await screen.findByText('当前没有已启用的连接，登录页不会展示第三方入口。'),
    ).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '管理连接' })).toHaveAttribute(
      'href',
      '#/apps/app-1/connections',
    )
    expect(screen.getAllByText('第三方登录 · 无已启用连接').length).toBeGreaterThan(0)
    expect(screen.queryByText('保存后没有可用的登录方式')).not.toBeInTheDocument()
  })

  it('第三方登录为唯一方式且无已启用连接：显著警告', async () => {
    stubPolicyApi({
      config: {
        ...baseConfig,
        policy: {
          ...baseConfig.policy,
          password_login_enabled: false,
          email_otp_enabled: false,
          phone_otp_enabled: false,
          federation_enabled: true,
        },
      },
      connections: jsonResponse({ items: [], next_cursor: null }),
    })
    render(<LoginPolicySection appId="app-1" canWrite />)

    expect(await screen.findByText('当前配置没有可用的登录方式')).toBeInTheDocument()
    expect(screen.getByText(/第三方登录（无已启用连接）/)).toBeInTheDocument()
  })

  it('连接列表读取失败：第三方登录可用性如实降级为提示，不阻断编辑，可手动重试', async () => {
    const user = userEvent.setup()
    stubPolicyApi({
      connections: (n) =>
        n === 1
          ? errorResponse(503, { code: 'dependency_unavailable', detail: 'core 暂不可用' })
          : jsonResponse({ items: [connectionItem('c1', 'active')], next_cursor: null }),
    })
    render(<LoginPolicySection appId="app-1" canWrite />)

    // 读取失败：不编造计数、不误报「无已启用连接」，仅如实提示
    expect(await screen.findByText(/连接列表暂时无法读取/)).toBeInTheDocument()
    expect(
      screen.queryByText('当前没有已启用的连接，登录页不会展示第三方入口。'),
    ).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '重试读取连接' }))
    await waitFor(() => expect(screen.queryByText(/连接列表暂时无法读取/)).not.toBeInTheDocument())
    expect(await screen.findByText('第三方登录 · 1 个已启用连接')).toBeInTheDocument()
  })

  it('409 冻结时预设与表单一并禁用', async () => {
    const user = userEvent.setup()
    stubPolicyApi({
      connections: jsonResponse({ items: [], next_cursor: null }),
      policyPatch: () =>
        errorResponse(409, { code: 'version_conflict', detail: 'config_version 已过期' }),
    })
    render(<LoginPolicySection appId="app-1" canWrite />)

    await user.click(await screen.findByRole('checkbox', { name: /^密码登录$/ }))
    await user.type(screen.getByLabelText('操作原因'), '测试冻结')
    await user.click(screen.getByRole('button', { name: '保存登录策略' }))

    expect(await screen.findByText('配置版本已过期（409 冲突）')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '仅第三方登录' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '保存登录策略' })).toBeDisabled()
  })

  it('只读：展示方式摘要，无预设入口', async () => {
    stubPolicyApi({
      connections: jsonResponse({ items: [connectionItem('c1', 'active')], next_cursor: null }),
    })
    render(<LoginPolicySection appId="app-1" canWrite={false} />)

    expect(await screen.findByText('第三方登录 · 1 个已启用连接')).toBeInTheDocument()
    expect(screen.getByText('当前配置')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '仅第三方登录' })).not.toBeInTheDocument()
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
  })
})
