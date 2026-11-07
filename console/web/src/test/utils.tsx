import type { ReactElement } from 'react'
import { render } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { vi } from 'vitest'
import { deriveCapabilities } from '../features/session/capabilities'
import { SessionContext, type SessionValue } from '../features/session/context'

export function makeSession(overrides: Partial<SessionValue> = {}): SessionValue {
  const operator =
    overrides.operator === undefined
      ? { id: 'op-1', email: 'admin@example.com', role: 'admin' }
      : overrides.operator
  return {
    status: 'ready',
    operator,
    sessionExpired: false,
    bootstrapError: null,
    retryBootstrap: vi.fn(),
    signIn: vi.fn(async () => {}),
    signOut: vi.fn(async () => {}),
    // 能力默认从 operator 推导，与 SessionProvider 保持一致；可用 overrides 显式覆盖
    ...deriveCapabilities(operator),
    ...overrides,
  }
}

export function renderWithSession(
  ui: ReactElement,
  session: SessionValue,
  initialEntries: string[] = ['/'],
) {
  return render(
    <MemoryRouter initialEntries={initialEntries}>
      <SessionContext.Provider value={session}>{ui}</SessionContext.Provider>
    </MemoryRouter>,
  )
}

export interface FetchCall {
  url: string
  method: string
  headers: Record<string, string>
  body: unknown
}

export type FetchHandler = (call: FetchCall) => Response | Promise<Response>

export function stubFetch(handler: FetchHandler) {
  const calls: FetchCall[] = []
  const mock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url =
      typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
    const headers: Record<string, string> = {}
    if (init?.headers) {
      for (const [k, v] of Object.entries(init.headers as Record<string, string>)) {
        headers[k] = v
      }
    }
    let body: unknown = null
    if (typeof init?.body === 'string') {
      try {
        body = JSON.parse(init.body)
      } catch {
        body = init.body
      }
    }
    const call: FetchCall = { url, method: init?.method ?? 'GET', headers, body }
    calls.push(call)
    return handler(call)
  })
  vi.stubGlobal('fetch', mock)
  return { calls, mock }
}

export function jsonResponse(body: unknown, status = 200): Response {
  if (status === 204 || status === 205 || status === 304) {
    return new Response(null, { status })
  }
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

export function errorResponse(
  status: number,
  overrides: Partial<{
    type: string
    title: string
    detail: string
    code: string
    request_id: string
  }> = {},
): Response {
  return jsonResponse(
    {
      type: overrides.type ?? 'about:blank',
      title: overrides.title ?? 'Error',
      status,
      detail: overrides.detail ?? '',
      code: overrides.code ?? 'error',
      request_id: overrides.request_id ?? 'req-test-1',
    },
    status,
  )
}
