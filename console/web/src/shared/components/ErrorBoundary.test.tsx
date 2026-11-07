import { expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ErrorBoundary } from './ErrorBoundary'

it('contains rendering failures and never renders the raw exception', () => {
  const log = vi.spyOn(console, 'error').mockImplementation(() => {})
  function Broken(): never {
    throw new Error('private-fixture-secret')
  }
  try {
    render(
      <ErrorBoundary>
        <Broken />
      </ErrorBoundary>,
    )
    expect(screen.getByRole('alert')).toHaveTextContent('页面暂时无法显示')
    expect(document.body).not.toHaveTextContent('private-fixture-secret')
    expect(screen.getByRole('button', { name: '重新加载页面' })).toBeVisible()
  } finally {
    log.mockRestore()
  }
})
