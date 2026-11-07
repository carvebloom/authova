import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router-dom'
import type { App } from '../api/types'
import {
  jsonResponse,
  makeSession,
  renderWithSession,
  stubFetch,
  type FetchCall,
} from '../test/utils'
import { AppSwitcher } from './AppSwitcher'

function makeApp(i: number): App {
  return {
    id: `app-${i}`,
    app_key: `ak_${i}`,
    name: `应用 ${i}`,
    slug: `app-${i}`,
    status: 'active',
    issuer: `https://auth.example.com/app-${i}`,
    created_at: '2026-09-01T08:00:00Z',
    config_version: 1,
    require_mfa: false,
  }
}

function renderSwitcher() {
  return renderWithSession(
    <Routes>
      <Route path="/apps/:appId" element={<AppSwitcher open onClose={() => {}} />} />
      <Route path="*" element={<AppSwitcher open onClose={() => {}} />} />
    </Routes>,
    makeSession(),
    ['/apps/app-1'],
  )
}

function isAppsList(call: FetchCall) {
  return call.method === 'GET' && call.url.startsWith('/bff/apps')
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('AppSwitcher 应用选择器', () => {
  it('游标分页：第二页的应用也能找到并进入（不永久限于第一页）', async () => {
    const user = userEvent.setup()
    const page1 = Array.from({ length: 3 }, (_, i) => makeApp(i + 1))
    const page2 = [makeApp(4), makeApp(5)]
    const { calls } = stubFetch((call) => {
      const url = new URL(call.url, 'http://localhost')
      if (url.searchParams.get('cursor') === 'page-2') {
        return jsonResponse({ items: page2, next_cursor: null })
      }
      return jsonResponse({ items: page1, next_cursor: 'page-2' })
    })
    renderSwitcher()

    expect(await screen.findByText('应用 1')).toBeInTheDocument()
    expect(screen.queryByText('应用 5')).not.toBeInTheDocument()

    // 筛选只作用于已加载条目：第二页的应用需先加载
    await user.type(screen.getByLabelText('筛选已加载的应用'), '应用 5')
    expect(screen.getByText('已加载条目中无匹配')).toBeInTheDocument()

    await user.clear(screen.getByLabelText('筛选已加载的应用'))
    await user.click(screen.getByRole('button', { name: '加载更多' }))
    expect(await screen.findByText('应用 5')).toBeInTheDocument()

    // 全程两个列表请求（两页）
    await waitFor(() => expect(calls.filter(isAppsList)).toHaveLength(2))
  })

  it('筛选已加载条目并按名称/Slug 匹配', async () => {
    const user = userEvent.setup()
    stubFetch(() =>
      jsonResponse({
        items: [makeApp(1), { ...makeApp(2), name: '支付网关', slug: 'pay-gw' }],
        next_cursor: null,
      }),
    )
    renderSwitcher()
    await screen.findByText('应用 1')
    await user.type(screen.getByLabelText('筛选已加载的应用'), 'pay-gw')
    expect(screen.queryByText('应用 1')).not.toBeInTheDocument()
    expect(screen.getByText('支付网关')).toBeInTheDocument()
  })
})
