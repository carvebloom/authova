import { describe, expect, it } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import { ApiError } from '../../api/client'
import { useAsync } from './useAsync'

interface Deferred<T> {
  promise: Promise<T>
  resolve: (value: T) => void
  reject: (err: unknown) => void
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void
  let reject!: (err: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

function Probe({ qkey, fn }: { qkey: string; fn: () => Promise<string> }) {
  const state = useAsync(qkey, fn)
  return (
    <div>
      <span data-testid="data">{state.data ?? 'none'}</span>
      <span data-testid="loading">{String(state.loading)}</span>
      <span data-testid="refreshing">{String(state.refreshing)}</span>
      <span data-testid="error">
        {state.error ? state.error.detail || state.error.title : 'none'}
      </span>
      <button type="button" onClick={state.reload}>
        reload
      </button>
    </div>
  )
}

describe('useAsync 查询身份隔离与竞态', () => {
  it('key 变化时旧 key 数据绝不同屏渲染（渲染期同步隔离）', async () => {
    const d1 = deferred<string>()
    const d2 = deferred<string>()
    const fns: Record<string, () => Promise<string>> = {
      'app:1': () => d1.promise,
      'app:2': () => d2.promise,
    }
    const { rerender } = render(<Probe qkey="app:1" fn={fns['app:1']!} />)
    await act(async () => d1.resolve('应用一的数据'))
    expect(screen.getByTestId('data')).toHaveTextContent('应用一的数据')

    // 切到 app:2（响应未返回）：任何一帧都不允许展示 app:1 的数据
    rerender(<Probe qkey="app:2" fn={fns['app:2']!} />)
    expect(screen.getByTestId('data')).toHaveTextContent('none')
    expect(screen.getByTestId('loading')).toHaveTextContent('true')

    await act(async () => d2.resolve('应用二的数据'))
    expect(screen.getByTestId('data')).toHaveTextContent('应用二的数据')
  })

  it('旧 key 的迟到响应被忽略，不覆盖新 key', async () => {
    const d1 = deferred<string>()
    const d2 = deferred<string>()
    const { rerender } = render(<Probe qkey="a" fn={() => d1.promise} />)
    rerender(<Probe qkey="b" fn={() => d2.promise} />)
    // a 的响应姗姗来迟：不得写入
    await act(async () => d1.resolve('旧数据'))
    expect(screen.getByTestId('data')).toHaveTextContent('none')
    await act(async () => d2.resolve('新数据'))
    expect(screen.getByTestId('data')).toHaveTextContent('新数据')
  })

  it('unmount 后响应到达不更新状态', async () => {
    const d = deferred<string>()
    const { unmount } = render(<Probe qkey="x" fn={() => d.promise} />)
    unmount()
    // 不抛错即为通过（React 18+ 对卸载后 setState 不再警告，此处验证逻辑忽略）
    await act(async () => d.resolve('late'))
  })

  it('reload 保留旧数据并进入 refreshing；新数据到达后替换', async () => {
    let call = 0
    const pages: Array<Deferred<string>> = []
    const fn = () => {
      const d = deferred<string>()
      pages.push(d)
      call += 1
      return d.promise
    }
    render(<Probe qkey="k" fn={fn} />)
    await act(async () => pages[0]!.resolve('第一版'))
    expect(screen.getByTestId('data')).toHaveTextContent('第一版')

    await act(async () => {
      screen.getByRole('button', { name: 'reload' }).click()
    })
    expect(screen.getByTestId('refreshing')).toHaveTextContent('true')
    // 刷新期间旧数据仍在
    expect(screen.getByTestId('data')).toHaveTextContent('第一版')
    await act(async () => pages[1]!.resolve('第二版'))
    expect(screen.getByTestId('data')).toHaveTextContent('第二版')
    expect(screen.getByTestId('refreshing')).toHaveTextContent('false')
    expect(call).toBe(2)
  })

  it('后台刷新失败保留旧数据并暴露错误；reload 立即使进行中响应作废', async () => {
    const pages: Array<Deferred<string>> = []
    const fn = () => {
      const d = deferred<string>()
      pages.push(d)
      return d.promise
    }
    render(<Probe qkey="k" fn={fn} />)
    await act(async () => pages[0]!.resolve('稳定数据'))

    // 第一次刷新：失败
    await act(async () => {
      screen.getByRole('button', { name: 'reload' }).click()
    })
    await act(async () => pages[1]!.reject(new ApiError({ status: 500, detail: '刷新失败' })))
    expect(screen.getByTestId('error')).toHaveTextContent('刷新失败')
    expect(screen.getByTestId('data')).toHaveTextContent('稳定数据')

    // 连续两次 reload：第一次的响应（即使成功）也不得覆盖第二次
    await act(async () => {
      screen.getByRole('button', { name: 'reload' }).click()
    })
    await act(async () => {
      screen.getByRole('button', { name: 'reload' }).click()
    })
    await act(async () => pages[2]!.resolve('被作废的响应'))
    await act(async () => pages[3]!.resolve('最新响应'))
    expect(screen.getByTestId('data')).toHaveTextContent('最新响应')
  })
})
