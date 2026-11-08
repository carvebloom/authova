// 同源 BFF fetch 封装：Cookie 由浏览器携带，写操作附 X-CSRF-Token，
// token 只保存在内存中，绝不写入 localStorage / sessionStorage。

export interface ApiErrorInit {
  status: number
  title?: string
  detail?: string
  code?: string
  requestId?: string | null
}

export class ApiError extends Error {
  readonly status: number
  readonly title: string
  readonly detail: string
  readonly code: string | null
  readonly requestId: string | null

  constructor(init: ApiErrorInit) {
    super(init.detail ?? init.title ?? `请求失败（${init.status}）`)
    this.name = 'ApiError'
    this.status = init.status
    this.title = init.title ?? (init.status === 0 ? '网络错误' : `请求失败（${init.status}）`)
    this.detail = init.detail ?? ''
    this.code = init.code ?? null
    this.requestId = init.requestId ?? null
  }

  get isUnauthorized(): boolean {
    return this.status === 401
  }

  get isForbidden(): boolean {
    return this.status === 403
  }

  /**
   * 提交结果未知：网络异常（status 0，请求可能已到达服务端）或 BFF 转发失败
   * （503 + outcome_unknown）。此时直接重试非幂等写操作可能造成重复写入。
   * 契约内其他 503（如核心服务依赖故障）是明确失败，不属于此类。
   */
  get isOutcomeUnknown(): boolean {
    return this.status === 0 || this.code === 'outcome_unknown'
  }
}

let csrfToken: string | null = null

export function setCsrfToken(token: string | null): void {
  csrfToken = token
}

export function getCsrfToken(): string | null {
  return csrfToken
}

/**
 * 认证 generation：登录、登出、会话被终止都会推进。
 * 请求启动时捕获 generation；迟到的 401 仅在 generation 未变时触发全局“会话失效”，
 * 避免旧会话的迟到响应清除新建立的登录会话。
 */
let authGeneration = 0

export function advanceAuthGeneration(): number {
  return ++authGeneration
}

export function getAuthGeneration(): number {
  return authGeneration
}

type UnauthorizedHandler = () => void
let unauthorizedHandler: UnauthorizedHandler | null = null

export function setUnauthorizedHandler(handler: UnauthorizedHandler | null): void {
  unauthorizedHandler = handler
}

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
  body?: unknown
  query?: Record<string, string | number | undefined>
  /** 仅契约支持幂等的创建操作（创建 App / 客户端）使用；由调用方按 payload 稳定复用 */
  idempotencyKey?: string
  /** 登录/登出类端点的 401 是凭据或会话状态语义，不代表“已有会话失效”，不触发全局登出 */
  skipUnauthorizedHandler?: boolean
  expectedStatus?: number
}

function buildUrl(path: string, query?: RequestOptions['query']): string {
  const url = `/bff${path}`
  if (!query) return url
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === '') continue
    params.set(key, String(value))
  }
  const qs = params.toString()
  return qs ? `${url}?${qs}` : url
}

interface ErrorBody {
  type?: unknown
  title?: unknown
  status?: unknown
  detail?: unknown
  code?: unknown
  request_id?: unknown
}

function toApiError(status: number, body: unknown, headerId: string | null = null): ApiError {
  const fallback = `请求失败（HTTP ${status}）`
  if (body && typeof body === 'object') {
    const b = body as ErrorBody
    const detail = typeof b.detail === 'string' ? b.detail : ''
    const title = typeof b.title === 'string' && b.title ? b.title : fallback
    const code = typeof b.code === 'string' && b.code ? b.code : null
    const requestId = typeof b.request_id === 'string' && b.request_id ? b.request_id : null
    return new ApiError({
      status,
      title,
      detail,
      code: code ?? undefined,
      requestId: requestId ?? headerId,
    })
  }
  return new ApiError({ status, title: fallback, requestId: headerId })
}

export async function apiFetch<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const method = options.method ?? 'GET'
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (options.body !== undefined) headers['Content-Type'] = 'application/json'
  if (method !== 'GET' && csrfToken) headers['X-CSRF-Token'] = csrfToken
  if (options.idempotencyKey) headers['Idempotency-Key'] = options.idempotencyKey
  // 请求启动时的认证 generation：响应期的 401 只能终止“启动时同一代”的会话
  const authGen = authGeneration

  let res: Response
  try {
    res = await fetch(buildUrl(path, options.query), {
      method,
      headers,
      body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
      credentials: 'same-origin',
    })
  } catch {
    throw new ApiError({
      status: 0,
      title: '网络错误',
      detail: '无法连接服务器，请检查网络后重试。',
    })
  }

  if (res.status === 401 && !options.skipUnauthorizedHandler && authGeneration === authGen) {
    unauthorizedHandler?.()
  }
  const headerId = res.headers.get('X-Request-ID')
  let text: string
  try {
    text = await res.text()
  } catch {
    // Headers can arrive after a write committed; a lost body is still unknown.
    if (!res.ok && res.status < 500) throw toApiError(res.status, null, headerId)
    throw new ApiError({
      status: 0,
      detail: '响应中断，提交结果未知，请核实后重试。',
      requestId: headerId,
    })
  }
  let data: unknown = null
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      if (res.ok) {
        throw new ApiError({
          status: res.status,
          code: 'outcome_unknown',
          requestId: headerId,
          detail: '响应格式异常，提交结果未知。',
        })
      }
      data = null
    }
  }

  if (!res.ok) {
    throw toApiError(res.status, data, headerId)
  }
  if (
    (options.expectedStatus !== undefined && res.status !== options.expectedStatus) ||
    (res.status !== 204 && data === null)
  ) {
    throw new ApiError({
      status: res.status,
      code: 'outcome_unknown',
      requestId: headerId,
      detail: '响应不符合接口契约，请核实提交结果。',
    })
  }
  return data as T
}
