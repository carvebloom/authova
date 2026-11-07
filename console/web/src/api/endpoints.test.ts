import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { setCsrfToken } from './client'
import * as api from './endpoints'
import { jsonResponse, stubFetch } from '../test/utils'

describe('endpoints 与冻结契约一致', () => {
  beforeEach(() => setCsrfToken('test-token'))
  afterEach(() => {
    vi.unstubAllGlobals()
    setCsrfToken(null)
  })

  it('login 提交 {email,password,totp} 到 /bff/login', async () => {
    const { calls } = stubFetch(() =>
      jsonResponse({
        operator: { id: 'op-1', email: 'a@b.c', role: 'admin' },
        csrf_token: 'tok',
      }),
    )
    const res = await api.login({ email: 'a@b.c', password: 'pw', totp: '123456' })
    expect(calls[0]?.url).toBe('/bff/login')
    expect(calls[0]?.method).toBe('POST')
    expect(calls[0]?.body).toEqual({ email: 'a@b.c', password: 'pw', totp: '123456' })
    expect(res.csrf_token).toBe('tok')
  })

  it('listUsers 携带 q/cursor/limit 查询参数', async () => {
    const { calls } = stubFetch(() => jsonResponse({ items: [], next_cursor: null }))
    await api.listUsers('app-1', { q: 'alice', cursor: 'cur-9', limit: 20 })
    const url = new URL(calls[0]!.url, 'http://localhost')
    expect(url.pathname).toBe('/bff/apps/app-1/users')
    expect(url.searchParams.get('q')).toBe('alice')
    expect(url.searchParams.get('cursor')).toBe('cur-9')
    expect(url.searchParams.get('limit')).toBe('20')
  })

  it('revokeUserSession 使用 DELETE 且 body 为 {reason}', async () => {
    const { calls } = stubFetch(() => jsonResponse(null, 204))
    await api.revokeUserSession('app-1', 'user-2', 'sess-3', '管理员手动撤销')
    expect(calls[0]?.url).toBe('/bff/apps/app-1/users/user-2/sessions/sess-3')
    expect(calls[0]?.method).toBe('DELETE')
    expect(calls[0]?.body).toEqual({ reason: '管理员手动撤销' })
    expect(calls[0]?.headers['X-CSRF-Token']).toBe('test-token')
  })

  it('createApp 携带契约要求的 Idempotency-Key 头', async () => {
    const { calls } = stubFetch(() =>
      jsonResponse({ id: 'app-1', name: '交易前台', slug: 'trade-web' }),
    )
    await api.createApp({ name: '交易前台', slug: 'trade-web' }, 'idem-app-1')
    expect(calls[0]?.url).toBe('/bff/apps')
    expect(calls[0]?.method).toBe('POST')
    expect(calls[0]?.headers['Idempotency-Key']).toBe('idem-app-1')
    expect(calls[0]?.body).toEqual({ name: '交易前台', slug: 'trade-web' })
  })

  it('createClient 发送契约规定的输入字段并携带 Idempotency-Key', async () => {
    const { calls } = stubFetch(() =>
      jsonResponse({ id: 'c-1', client_id: 'cid-1', app_id: 'app-1' }),
    )
    await api.createClient(
      'app-1',
      {
        name: 'Web',
        kind: 'confidential',
        redirect_uris: ['https://a.example.com/cb'],
        allowed_scopes: ['openid'],
        allowed_audiences: [],
        allowed_introspection_audiences: [],
        grant_types: ['authorization_code'],
      },
      'idem-client-1',
    )
    expect(calls[0]?.headers['Idempotency-Key']).toBe('idem-client-1')
    expect(calls[0]?.body).toEqual({
      name: 'Web',
      kind: 'confidential',
      redirect_uris: ['https://a.example.com/cb'],
      allowed_scopes: ['openid'],
      allowed_audiences: [],
      allowed_introspection_audiences: [],
      grant_types: ['authorization_code'],
    })
  })

  it('createPlatformOperator 不携带 Idempotency-Key（契约不支持幂等）', async () => {
    const { calls } = stubFetch(() =>
      jsonResponse({
        operator: { id: 'op-new', email: 'n@b.c', role: 'auditor', status: 'active' },
        totp_secret: 'JBSWY3DPEHPK3PXP',
      }),
    )
    await api.createPlatformOperator({ email: 'n@b.c', password: 'Str0ng!pass', role: 'auditor' })
    expect(calls[0]?.headers['Idempotency-Key']).toBeUndefined()
  })

  it('updateClient 走 PATCH /clients/{client_id} 且不含 kind', async () => {
    const { calls } = stubFetch(() => jsonResponse({ id: 'c-1' }))
    await api.updateClient('app-1', 'cid-1', {
      config_version: 1,
      name: '改名',
      redirect_uris: [],
      allowed_scopes: ['openid', 'email'],
      allowed_audiences: [],
      allowed_introspection_audiences: [],
      grant_types: ['refresh_token'],
    })
    expect(calls[0]?.url).toBe('/bff/apps/app-1/clients/cid-1')
    expect(calls[0]?.method).toBe('PATCH')
    expect(calls[0]?.body).not.toHaveProperty('kind')
  })

  it('suspend/resume/rotate/disable 均携带 {reason}', async () => {
    const { calls } = stubFetch(() => jsonResponse(null, 204))
    await api.suspendApp('app-1', '违规排查')
    await api.resumeApp('app-1', '排查结束')
    await api.rotateKeys('app-1', '例行轮换')
    await api.disableUser('app-1', 'user-1', '用户申请注销')
    expect(calls.map((c) => c.url)).toEqual([
      '/bff/apps/app-1/suspend',
      '/bff/apps/app-1/resume',
      '/bff/apps/app-1/keys/rotate',
      '/bff/apps/app-1/users/user-1/disable',
    ])
    for (const call of calls) {
      expect(call.method).toBe('POST')
      expect(typeof (call.body as { reason?: unknown }).reason).toBe('string')
    }
  })

  it('enableUser 走 POST /users/{id}/enable 并携带 {reason, session_epoch}', async () => {
    const { calls } = stubFetch(() =>
      jsonResponse({ id: 'user-1', app_id: 'app-1', status: 'active', session_epoch: 3 }),
    )
    await api.enableUser('app-1', 'user-1', { reason: '误禁用，恢复', session_epoch: 2 })
    expect(calls[0]?.url).toBe('/bff/apps/app-1/users/user-1/enable')
    expect(calls[0]?.method).toBe('POST')
    expect(calls[0]?.body).toEqual({ reason: '误禁用，恢复', session_epoch: 2 })
    expect(calls[0]?.headers['X-CSRF-Token']).toBe('test-token')
  })

  it('updateAuthenticationPolicy 走 PATCH authentication-config，携带完整策略 + config_version + reason', async () => {
    const { calls } = stubFetch((call) =>
      jsonResponse({ config_version: 3, policy: (call.body as { policy: unknown }).policy }),
    )
    const policy = {
      registration_enabled: true,
      password_login_enabled: true,
      email_otp_enabled: false,
      phone_otp_enabled: false,
      federation_enabled: true,
      external_registration_enabled: false,
      passkey_enabled: false,
    }
    await api.updateAuthenticationPolicy('app-1', {
      policy,
      config_version: 2,
      reason: '关闭短信登录',
    })
    expect(calls[0]?.url).toBe('/bff/apps/app-1/authentication-config')
    expect(calls[0]?.method).toBe('PATCH')
    expect(calls[0]?.body).toEqual({ policy, config_version: 2, reason: '关闭短信登录' })
  })

  it('rotateClientSecret / revokePreviousClientSecret 路径与版本化 body', async () => {
    const { calls } = stubFetch((call) =>
      jsonResponse(
        call.url.endsWith('/revoke-previous')
          ? {
              id: 'c-1',
              app_id: 'app-1',
              client_id: 'cid-1',
              kind: 'confidential',
              config_version: 9,
              previous_secret_expires_at: 0,
            }
          : {
              client: {
                id: 'c-1',
                app_id: 'app-1',
                client_id: 'cid-1',
                kind: 'confidential',
                config_version: 8,
                previous_secret_expires_at: 0,
              },
              client_secret: 's',
              previous_secret_expires_at: 0,
            },
      ),
    )
    await api.rotateClientSecret('app-1', 'cid-1', {
      config_version: 7,
      reason: '例行轮换',
      grace_seconds: 3600,
    })
    expect(calls[0]?.url).toBe('/bff/apps/app-1/clients/cid-1/secret/rotate')
    expect(calls[0]?.method).toBe('POST')
    expect(calls[0]?.body).toEqual({ config_version: 7, reason: '例行轮换', grace_seconds: 3600 })

    await api.revokePreviousClientSecret('app-1', 'cid-1', {
      config_version: 8,
      reason: '切换完成',
    })
    expect(calls[1]?.url).toBe('/bff/apps/app-1/clients/cid-1/secret/revoke-previous')
    expect(calls[1]?.body).toEqual({ config_version: 8, reason: '切换完成' })
  })

  it('listSigningKeys 走 GET /keys；revokeSigningKey 携带 {config_version, reason}', async () => {
    const { calls } = stubFetch(() =>
      jsonResponse({ items: [], config_version: 5, key_id: 'kid-abc', new_key_id: 'k2' }),
    )
    await api.listSigningKeys('app-1')
    expect(calls[0]?.url).toBe('/bff/apps/app-1/keys')
    expect(calls[0]?.method).toBe('GET')

    await api.revokeSigningKey('app-1', 'kid-abc', { config_version: 4, reason: '疑似泄露' })
    expect(calls[1]?.url).toBe('/bff/apps/app-1/keys/kid-abc/revoke')
    expect(calls[1]?.method).toBe('POST')
    expect(calls[1]?.body).toEqual({ config_version: 4, reason: '疑似泄露' })
  })

  it.each(['enable', 'policy', 'revoke-previous', 'revoke-key'])(
    '不完整的 %s 成功响应必须进入结果未知',
    async (operation) => {
      stubFetch(() => jsonResponse({}))
      const pending =
        operation === 'enable'
          ? api.enableUser('app-1', 'user-1', { session_epoch: 1, reason: 'readback required' })
          : operation === 'policy'
            ? api.updateAuthenticationPolicy('app-1', {
                config_version: 1,
                reason: 'readback required',
                policy: {
                  registration_enabled: true,
                  password_login_enabled: true,
                  email_otp_enabled: true,
                  phone_otp_enabled: true,
                  federation_enabled: true,
                  external_registration_enabled: true,
                  passkey_enabled: false,
                },
              })
            : operation === 'revoke-previous'
              ? api.revokePreviousClientSecret('app-1', 'cid-1', {
                  config_version: 1,
                  reason: 'readback required',
                })
              : api.revokeSigningKey('app-1', 'kid-1', {
                  config_version: 1,
                  reason: 'readback required',
                })
      await expect(pending).rejects.toMatchObject({ code: 'outcome_unknown' })
    },
  )

  it('路径参数做 URL 编码', async () => {
    const { calls } = stubFetch(() => jsonResponse({ items: [], next_cursor: null }))
    await api.getApp('app/with space')
    expect(calls[0]?.url).toBe('/bff/apps/app%2Fwith%20space')
  })

  it('appLogin 提交 {app_key} 到 /bff/app-login 并返回 redirect_to', async () => {
    const { calls } = stubFetch(() =>
      jsonResponse({ redirect_to: 'https://auth.example.com/oidc/authorize?client_id=x' }),
    )
    const res = await api.appLogin('ak_live_1')
    expect(calls[0]?.url).toBe('/bff/app-login')
    expect(calls[0]?.method).toBe('POST')
    expect(calls[0]?.body).toEqual({ app_key: 'ak_live_1' })
    expect(res.redirect_to).toContain('https://')
  })

  it('setAppAdministrator 走 PUT /administrators/{user_id}，空 role 即撤销', async () => {
    const { calls } = stubFetch(() => jsonResponse(null, 204))
    await api.setAppAdministrator('app-1', 'user-1', 'auditor', '任命为审计员')
    await api.setAppAdministrator('app-1', 'user-1', '', '卸任')
    expect(calls[0]?.url).toBe('/bff/apps/app-1/administrators/user-1')
    expect(calls[0]?.method).toBe('PUT')
    expect(calls[0]?.body).toEqual({ role: 'auditor', reason: '任命为审计员' })
    expect(calls[1]?.body).toEqual({ role: '', reason: '卸任' })
  })

  it('listPlatformOperators 走 GET /bff/platform/operators', async () => {
    const { calls } = stubFetch(() => jsonResponse({ items: [], next_cursor: null }))
    await api.listPlatformOperators({ limit: 20 })
    expect(calls[0]?.url).toBe('/bff/platform/operators?limit=20')
  })

  it('createPlatformOperator 提交 {email,password,role}，返回一次性 totp_secret', async () => {
    const { calls } = stubFetch(() =>
      jsonResponse({
        operator: { id: 'op-new', email: 'n@b.c', role: 'auditor', status: 'active' },
        totp_secret: 'JBSWY3DPEHPK3PXP',
      }),
    )
    const res = await api.createPlatformOperator({
      email: 'n@b.c',
      password: 'Str0ng!pass',
      role: 'auditor',
    })
    expect(calls[0]?.url).toBe('/bff/platform/operators')
    expect(calls[0]?.method).toBe('POST')
    expect(calls[0]?.body).toEqual({ email: 'n@b.c', password: 'Str0ng!pass', role: 'auditor' })
    expect(res.totp_secret).toBe('JBSWY3DPEHPK3PXP')
  })

  it('updatePlatformOperator 走 PATCH 且携带 {role,status,reason}', async () => {
    const { calls } = stubFetch(() => jsonResponse({ updated: true }))
    const res = await api.updatePlatformOperator('op-1', {
      role: 'admin',
      status: 'disabled',
      reason: '岗位调整',
    })
    expect(calls[0]?.url).toBe('/bff/platform/operators/op-1')
    expect(calls[0]?.method).toBe('PATCH')
    expect(calls[0]?.body).toEqual({ role: 'admin', status: 'disabled', reason: '岗位调整' })
    expect(res.updated).toBe(true)
  })
})
