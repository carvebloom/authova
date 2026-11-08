import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  ApiError,
  advanceAuthGeneration,
  apiFetch,
  getCsrfToken,
  setCsrfToken,
  setUnauthorizedHandler,
} from './client'
import { errorResponse, jsonResponse, stubFetch } from '../test/utils'

describe('apiFetch', () => {
  beforeEach(() => {
    setCsrfToken(null)
    setUnauthorizedHandler(null)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    setCsrfToken(null)
    setUnauthorizedHandler(null)
  })

  it('GET 请求不带 CSRF 头，写操作携带 X-CSRF-Token', async () => {
    const { calls } = stubFetch(() => jsonResponse({ items: [], next_cursor: null }))
    setCsrfToken('csrf-abc')

    await apiFetch('/apps')
    await apiFetch('/apps', { method: 'POST', body: { name: 'x', slug: 'x' } })

    expect(calls[0]?.headers['X-CSRF-Token']).toBeUndefined()
    expect(calls[1]?.headers['X-CSRF-Token']).toBe('csrf-abc')
    expect(calls[1]?.headers['Content-Type']).toBe('application/json')
    expect(getCsrfToken()).toBe('csrf-abc')
  })

  it('未设置 token 时写请求也不伪造 CSRF 头', async () => {
    const { calls } = stubFetch(() => jsonResponse(null, 204))
    await apiFetch('/logout', { method: 'POST' })
    expect(calls[0]?.headers['X-CSRF-Token']).toBeUndefined()
  })

  it('解析服务端错误体 {title,detail,code,request_id}', async () => {
    stubFetch(() =>
      errorResponse(403, {
        title: 'Forbidden',
        detail: '审计员不可执行写操作',
        code: 'forbidden',
        request_id: 'req-123',
      }),
    )
    const err = await apiFetch('/apps', { method: 'POST', body: {} }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    const apiErr = err as ApiError
    expect(apiErr.status).toBe(403)
    expect(apiErr.title).toBe('Forbidden')
    expect(apiErr.detail).toBe('审计员不可执行写操作')
    expect(apiErr.code).toBe('forbidden')
    expect(apiErr.requestId).toBe('req-123')
    expect(apiErr.isForbidden).toBe(true)
  })

  it('非 JSON 错误响应回退为带状态码的标题', async () => {
    stubFetch(() => new Response('<html>bad gateway</html>', { status: 502 }))
    const err = await apiFetch('/apps').catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(502)
    expect((err as ApiError).title).toContain('502')
  })

  it('网络异常转换为 status 0 的 ApiError', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('fetch failed')
      }),
    )
    const err = await apiFetch('/apps').catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(0)
    expect((err as ApiError).title).toBe('网络错误')
  })

  it('成功写入的响应体中断也保留结果未知语义', async () => {
    const response = new Response('{}', { status: 201 })
    vi.spyOn(response, 'text').mockRejectedValue(new TypeError('connection reset'))
    stubFetch(() => response)
    const err = await apiFetch('/apps', { method: 'POST', body: {} }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).isOutcomeUnknown).toBe(true)
  })

  it.each(['<html>gateway</html>', '', 'null'])(
    '成功响应的无效 JSON 不伪装成创建成功: %s',
    async (body) => {
      stubFetch(() => new Response(body, { status: 201 }))
      const err = await apiFetch('/apps', { method: 'POST', body: {} }).catch((e: unknown) => e)
      expect(err).toBeInstanceOf(ApiError)
      expect((err as ApiError).isOutcomeUnknown).toBe(true)
    },
  )

  it('登出只有契约规定的 204 才作为成功返回', async () => {
    stubFetch(() => jsonResponse({ unexpected: true }, 200))
    await expect(
      apiFetch('/logout', { method: 'POST', expectedStatus: 204 }),
    ).rejects.toMatchObject({ code: 'outcome_unknown' })
  })

  it('401 触发未授权回调', async () => {
    stubFetch(() => errorResponse(401, { code: 'unauthorized' }))
    const onUnauthorized = vi.fn()
    setUnauthorizedHandler(onUnauthorized)
    await expect(apiFetch('/apps')).rejects.toBeInstanceOf(ApiError)
    expect(onUnauthorized).toHaveBeenCalledTimes(1)
  })

  it('旧认证 generation 启动的请求，其迟到 401 不清除新会话（不触发全局登出）', async () => {
    let respond: ((r: Response) => void) | undefined
    vi.stubGlobal(
      'fetch',
      vi.fn(
        () =>
          new Promise<Response>((res) => {
            respond = res
          }),
      ),
    )
    const onUnauthorized = vi.fn()
    setUnauthorizedHandler(onUnauthorized)
    // 请求在 generation N 启动；响应到达前 generation 已推进（新登录/登出发生）
    const pending = apiFetch('/apps').catch((e: unknown) => e)
    advanceAuthGeneration()
    respond!(errorResponse(401, { code: 'unauthorized' }))
    await pending
    expect(onUnauthorized).not.toHaveBeenCalled()
  })

  it('同代请求的 401 仍正常触发全局处理', async () => {
    let respond: ((r: Response) => void) | undefined
    vi.stubGlobal(
      'fetch',
      vi.fn(
        () =>
          new Promise<Response>((res) => {
            respond = res
          }),
      ),
    )
    const onUnauthorized = vi.fn()
    setUnauthorizedHandler(onUnauthorized)
    const pending = apiFetch('/apps').catch((e: unknown) => e)
    respond!(errorResponse(401, { code: 'unauthorized' }))
    await pending
    expect(onUnauthorized).toHaveBeenCalledTimes(1)
  })

  it('skipUnauthorizedHandler 的 401（如登录凭据错误）不触发全局登出', async () => {
    stubFetch(() => errorResponse(401, { code: 'invalid_credentials' }))
    const onUnauthorized = vi.fn()
    setUnauthorizedHandler(onUnauthorized)
    await expect(
      apiFetch('/login', { method: 'POST', body: {}, skipUnauthorizedHandler: true }),
    ).rejects.toBeInstanceOf(ApiError)
    expect(onUnauthorized).not.toHaveBeenCalled()
  })

  it('仅在显式提供时发送 Idempotency-Key 头', async () => {
    const { calls } = stubFetch(() => jsonResponse(null, 204))
    await apiFetch('/apps', { method: 'POST', body: { name: 'x', slug: 'x' } })
    await apiFetch('/apps', {
      method: 'POST',
      body: { name: 'x', slug: 'x' },
      idempotencyKey: 'idem-42',
    })
    expect(calls[0]?.headers['Idempotency-Key']).toBeUndefined()
    expect(calls[1]?.headers['Idempotency-Key']).toBe('idem-42')
  })

  it('isOutcomeUnknown 仅覆盖网络错误与 outcome_unknown，不含契约内的明确 503 失败', async () => {
    expect(new ApiError({ status: 0, title: '网络错误' }).isOutcomeUnknown).toBe(true)
    expect(
      new ApiError({ status: 503, code: 'outcome_unknown', title: 'x' }).isOutcomeUnknown,
    ).toBe(true)
    expect(
      new ApiError({ status: 503, code: 'dependency_unavailable', title: 'x' }).isOutcomeUnknown,
    ).toBe(false)
    expect(new ApiError({ status: 409, code: 'conflict', title: 'x' }).isOutcomeUnknown).toBe(false)
    expect(new ApiError({ status: 400, title: 'x' }).isOutcomeUnknown).toBe(false)
  })

  it('拼接查询串并跳过空值', async () => {
    const { calls } = stubFetch(() => jsonResponse({ items: [], next_cursor: null }))
    await apiFetch('/apps/app-1/users', {
      query: { q: 'alice', cursor: undefined, limit: 20 },
    })
    expect(calls[0]?.url).toBe('/bff/apps/app-1/users?q=alice&limit=20')
  })
})

describe('response header correlation', () => {
  afterEach(() => vi.unstubAllGlobals())
  it('keeps the header request ID when a response body is truncated', async () => {
    const response = new Response('', {
      status: 200,
      headers: { 'X-Request-ID': 'core-request-123' },
    })
    vi.spyOn(response, 'text').mockRejectedValue(new Error('private body transport'))
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response))
    await expect(apiFetch('/apps', { method: 'POST' })).rejects.toMatchObject({
      status: 0,
      requestId: 'core-request-123',
    })
  })
})
