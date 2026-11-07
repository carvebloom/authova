import { describe, expect, it } from 'vitest'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { usePagedList, type PagedListState } from './usePagedList'
import type { ListResponse } from '../../api/types'

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

interface FetchLog {
  cursor: string | undefined
  deferred: Deferred<ListResponse<string>>
}

interface ProbeCapture<T> {
  list?: PagedListState<T>
}

function Probe({
  qkey,
  enabled = true,
  log,
  onCapture,
}: {
  qkey: string
  enabled?: boolean
  log: FetchLog[]
  onCapture?: (value: PagedListState<string>) => void
}) {
  const list = usePagedList(
    qkey,
    (cursor) => {
      const d = deferred<ListResponse<string>>()
      log.push({ cursor, deferred: d })
      return d.promise
    },
    enabled,
  )
  onCapture?.(list)
  return (
    <div>
      <span data-testid="items">{list.items.join(',') || 'none'}</span>
      <span data-testid="loading">{String(list.loading)}</span>
      <span data-testid="loadingMore">{String(list.loadingMore)}</span>
      <span data-testid="error">{list.error ? list.error.title : 'none'}</span>
      <span data-testid="loadMoreError">
        {list.loadMoreError ? list.loadMoreError.title : 'none'}
      </span>
      <button type="button" onClick={list.loadMore}>
        more
      </button>
      <button type="button" onClick={list.reload}>
        reload
      </button>
    </div>
  )
}

describe('usePagedList 竞态与互斥', () => {
  it('loadMore 连击只发一个请求（游标互斥去重）', async () => {
    const user = userEvent.setup()
    const log: FetchLog[] = []
    render(<Probe qkey="k" log={log} />)
    await act(async () => log[0]!.deferred.resolve({ items: ['a'], next_cursor: 'cur-1' }))
    const more = screen.getByRole('button', { name: 'more' })
    await user.click(more)
    await user.click(more)
    await user.click(more)
    expect(log.filter((l) => l.cursor === 'cur-1')).toHaveLength(1)
    await act(async () => log[1]!.deferred.resolve({ items: ['b'], next_cursor: null }))
    expect(screen.getByTestId('items')).toHaveTextContent('a,b')
    expect(screen.getByTestId('loadingMore')).toHaveTextContent('false')
  })

  it('key 变化渲染期同步清空；旧 key 进行中的翻页响应被丢弃', async () => {
    const log: FetchLog[] = []
    const { rerender } = render(<Probe qkey="app:1" log={log} />)
    await act(async () => log[0]!.deferred.resolve({ items: ['旧App-a'], next_cursor: 'old-cur' }))

    // 发起翻页后立刻切换 key
    await act(async () => {
      screen.getByRole('button', { name: 'more' }).click()
    })
    expect(log).toHaveLength(2)
    rerender(<Probe qkey="app:2" log={log} />)
    // 渲染期同步隔离：旧数据不可见
    expect(screen.getByTestId('items')).toHaveTextContent('none')

    // 旧 key 的翻页响应迟到：不得追加到新 key
    await act(async () => log[1]!.deferred.resolve({ items: ['旧App-b'], next_cursor: null }))
    expect(screen.getByTestId('items')).toHaveTextContent('none')

    await act(async () => log[2]!.deferred.resolve({ items: ['新App-a'], next_cursor: null }))
    expect(screen.getByTestId('items')).toHaveTextContent('新App-a')
    expect(screen.getByTestId('items')).not.toHaveTextContent('旧App-b')
  })

  it('reload 立即使进行中的翻页作废，重新拉第一页', async () => {
    const log: FetchLog[] = []
    render(<Probe qkey="k" log={log} />)
    await act(async () => log[0]!.deferred.resolve({ items: ['a'], next_cursor: 'c1' }))
    await act(async () => {
      screen.getByRole('button', { name: 'more' }).click()
      screen.getByRole('button', { name: 'reload' }).click()
    })
    // reload 触发新的首页请求
    expect(log).toHaveLength(3)
    // 被作废的翻页响应到达：不生效
    await act(async () => log[1]!.deferred.resolve({ items: ['STALE'], next_cursor: null }))
    await act(async () => log[2]!.deferred.resolve({ items: ['fresh'], next_cursor: null }))
    expect(screen.getByTestId('items')).toHaveTextContent('fresh')
    expect(screen.getByTestId('items')).not.toHaveTextContent('STALE')
  })

  it('翻页失败暴露 loadMoreError 且不丢已有条目；首页失败暴露 error', async () => {
    const log: FetchLog[] = []
    render(<Probe qkey="k" log={log} />)
    await act(async () => log[0]!.deferred.resolve({ items: ['a'], next_cursor: 'c1' }))
    await act(async () => {
      screen.getByRole('button', { name: 'more' }).click()
    })
    await act(async () => log[1]!.deferred.reject(new Error('boom') as unknown as never))
    await waitFor(() => expect(screen.getByTestId('loadMoreError')).not.toHaveTextContent('none'))
    expect(screen.getByTestId('items')).toHaveTextContent('a')
  })

  it('enabled=false 不发请求；启用后才加载', async () => {
    const log: FetchLog[] = []
    const { rerender } = render(<Probe qkey="k" enabled={false} log={log} />)
    expect(log).toHaveLength(0)
    expect(screen.getByTestId('loading')).toHaveTextContent('false')
    rerender(<Probe qkey="k" enabled log={log} />)
    expect(log).toHaveLength(1)
    await act(async () => log[0]!.deferred.resolve({ items: ['x'], next_cursor: null }))
    expect(screen.getByTestId('items')).toHaveTextContent('x')
  })

  it('旧 key 的迟到翻页响应不得解除新 key 的在途互斥锁', async () => {
    const log: FetchLog[] = []
    const { rerender } = render(<Probe qkey="A" log={log} />)
    await act(async () => log[0]!.deferred.resolve({ items: ['a1'], next_cursor: 'c1' }))
    // A 发起翻页（在途，未返回）
    await act(async () => {
      screen.getByRole('button', { name: 'more' }).click()
    })
    expect(log).toHaveLength(2)
    // 切到 B：首页加载
    rerender(<Probe qkey="B" log={log} />)
    await act(async () => log[2]!.deferred.resolve({ items: ['b1'], next_cursor: 'c9' }))
    // B 发起翻页（在途）
    await act(async () => {
      screen.getByRole('button', { name: 'more' }).click()
    })
    expect(log).toHaveLength(4)
    // A 的旧翻页响应迟到：不得解除 B 的锁（再次 loadMore 仍被互斥），也不得追加进 B
    await act(async () => log[1]!.deferred.resolve({ items: ['a2'], next_cursor: null }))
    await act(async () => {
      screen.getByRole('button', { name: 'more' }).click()
    })
    expect(log).toHaveLength(4)
    expect(screen.getByTestId('items')).toHaveTextContent('b1')
    // B 自己的翻页正常完成
    await act(async () => log[3]!.deferred.resolve({ items: ['b2'], next_cursor: null }))
    expect(screen.getByTestId('items')).toHaveTextContent('b1,b2')
  })

  it('A→B→A：旧 A 的请求（首页与翻页）全部作废', async () => {
    const log: FetchLog[] = []
    const { rerender } = render(<Probe qkey="A" log={log} />)
    await act(async () => log[0]!.deferred.resolve({ items: ['a1'], next_cursor: 'c1' }))
    // A 翻页在途 → 切 B → 切回 A
    await act(async () => {
      screen.getByRole('button', { name: 'more' }).click()
    })
    rerender(<Probe qkey="B" log={log} />)
    rerender(<Probe qkey="A" log={log} />)
    // 新 A 首页在途；旧 A 的翻页响应（游标恰好相同）迟到：不得追加
    await act(async () => log[1]!.deferred.resolve({ items: ['a1-stale-page'], next_cursor: null }))
    // B 的首页迟到：同样忽略
    await act(async () => log[2]!.deferred.resolve({ items: ['b-stale'], next_cursor: null }))
    await act(async () => log[3]!.deferred.resolve({ items: ['a-fresh'], next_cursor: null }))
    expect(screen.getByTestId('items')).toHaveTextContent('a-fresh')
    expect(screen.getByTestId('items')).not.toHaveTextContent('stale')
  })

  it('停用与卸载时进行中的翻页作废（不追加、不报错）', async () => {
    const log: FetchLog[] = []
    const { rerender, unmount } = render(<Probe qkey="k" log={log} />)
    await act(async () => log[0]!.deferred.resolve({ items: ['a'], next_cursor: 'c1' }))
    await act(async () => {
      screen.getByRole('button', { name: 'more' }).click()
    })
    // 停用：在途翻页作废
    rerender(<Probe qkey="k" enabled={false} log={log} />)
    await act(async () => log[1]!.deferred.resolve({ items: ['late'], next_cursor: null }))
    expect(screen.getByTestId('items')).toHaveTextContent('none')
    // 重新启用 + 翻页在途时卸载：响应到达不抛错
    rerender(<Probe qkey="k" enabled log={log} />)
    await act(async () => log[2]!.deferred.resolve({ items: ['x'], next_cursor: 'c2' }))
    await act(async () => {
      screen.getByRole('button', { name: 'more' }).click()
    })
    unmount()
    await act(async () => log[3]!.deferred.resolve({ items: ['never'], next_cursor: null }))
  })

  it('reload() 可等待：成功 true / 失败 false / 被取代 false', async () => {
    const log: FetchLog[] = []
    const capture: ProbeCapture<string> = {}
    render(
      <Probe
        qkey="k"
        log={log}
        onCapture={(value) => {
          capture.list = value
        }}
      />,
    )
    await act(async () => log[0]!.deferred.resolve({ items: ['a'], next_cursor: null }))

    // 成功 → true
    let p1!: Promise<boolean>
    act(() => {
      p1 = capture.list!.reload()
    })
    await act(async () => log[1]!.deferred.resolve({ items: ['b'], next_cursor: null }))
    await expect(p1).resolves.toBe(true)
    expect(screen.getByTestId('items')).toHaveTextContent('b')

    // 失败 → false
    let p2!: Promise<boolean>
    act(() => {
      p2 = capture.list!.reload()
    })
    await act(async () => log[2]!.deferred.reject(new Error('boom')))
    await expect(p2).resolves.toBe(false)

    // 连续 reload：前者被取代 → false，后者成功 → true
    let p3!: Promise<boolean>
    let p4!: Promise<boolean>
    act(() => {
      p3 = capture.list!.reload()
    })
    act(() => {
      p4 = capture.list!.reload()
    })
    await act(async () =>
      log[log.length - 1]!.deferred.resolve({ items: ['c'], next_cursor: null }),
    )
    await expect(p3).resolves.toBe(false)
    await expect(p4).resolves.toBe(true)
  })
})
