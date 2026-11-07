import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError } from '../../api/client'
import type { ListResponse } from '../../api/types'

export interface PagedListState<T> {
  items: T[]
  nextCursor: string | null
  error: ApiError | null
  loadMoreError: ApiError | null
  loading: boolean
  loadingMore: boolean
  /** 重新加载第一页；返回 Promise 结算本次加载成败，供权威读回流程等待 */
  reload: () => Promise<boolean>
  loadMore: () => void
}

interface Slot<T> {
  key: string
  items: T[]
  nextCursor: string | null
  error: ApiError | null
  loadMoreError: ApiError | null
  loading: boolean
  loadingMore: boolean
}

function idleSlot<T>(key: string, loading: boolean): Slot<T> {
  return {
    key,
    items: [],
    nextCursor: null,
    error: null,
    loadMoreError: null,
    loading,
    loadingMore: false,
  }
}

function toApiError(err: unknown): ApiError {
  return err instanceof ApiError ? err : new ApiError({ status: 0, detail: String(err) })
}

/**
 * 契约列表统一为 {items, next_cursor} 游标分页。首页读取与翻页共享同一查询 generation：
 * - key 变化 / 停用在渲染期同步重置并推进 generation，旧 key 列表绝不随新 key 渲染；
 * - reload() 同步推进 generation：进行中的首页/翻页响应全部作废；
 * - effect cleanup 推进 generation：换 key / unmount 后迟到响应忽略；
 * - loadMore 以 {generation, cursor} 为锁：连击去重，旧 run 的响应既不能追加进新 key，
 *   也不能解除新 key 的在途锁（A→B→A 安全）。
 */
export function usePagedList<T>(
  key: string,
  fetchPage: (cursor: string | undefined) => Promise<ListResponse<T>>,
  enabled = true,
): PagedListState<T> {
  const [slot, setSlot] = useState<Slot<T>>(() => idleSlot(key, enabled))
  const gen = useRef(0)
  const pageInFlight = useRef<{ gen: number; cursor: string } | null>(null)
  const slotRef = useRef(slot)
  const fnRef = useRef(fetchPage)
  const [tick, setTick] = useState(0)
  const waiters = useRef(new Map<number, Array<(ok: boolean) => void>>())

  const settleWaiters = useCallback((generation: number, ok: boolean) => {
    const list = waiters.current.get(generation)
    if (list) {
      waiters.current.delete(generation)
      for (const resolve of list) resolve(ok)
    }
    for (const [g, list2] of waiters.current) {
      if (g < generation) {
        waiters.current.delete(g)
        for (const resolve of list2) resolve(false)
      }
    }
  }, [])

  // ref 同步放入 effect，避免渲染期写 ref
  useEffect(() => {
    slotRef.current = slot
  }, [slot])
  useEffect(() => {
    fnRef.current = fetchPage
  })

  // 渲染期同步隔离 + generation 推进：key 变化或停用时立即丢弃旧数据、作废在途请求
  if (slot.key !== key) {
    gen.current += 1
    pageInFlight.current = null
    setSlot(idleSlot(key, enabled))
  } else if (!enabled && (slot.items.length > 0 || slot.loading || slot.loadingMore)) {
    gen.current += 1
    pageInFlight.current = null
    setSlot(idleSlot(key, false))
  }

  const reload = useCallback((): Promise<boolean> => {
    gen.current += 1
    pageInFlight.current = null
    const target = gen.current
    const p = new Promise<boolean>((resolve) => {
      const list = waiters.current.get(target) ?? []
      list.push(resolve)
      waiters.current.set(target, list)
    })
    setTick((t) => t + 1)
    return p
  }, [])

  useEffect(() => {
    if (!enabled) return
    const current = gen.current
    let cancelled = false
    setSlot((prev) => (prev.key === key ? { ...idleSlot(key, true) } : prev))
    fnRef
      .current(undefined)
      .then((res) => {
        if (cancelled || gen.current !== current) return
        setSlot({ ...idleSlot(key, false), items: res.items, nextCursor: res.next_cursor })
        settleWaiters(current, true)
      })
      .catch((err: unknown) => {
        if (cancelled || gen.current !== current) return
        setSlot((prev) =>
          prev.key === key ? { ...prev, loading: false, error: toApiError(err) } : prev,
        )
        settleWaiters(current, false)
      })
    return () => {
      cancelled = true
      if (gen.current === current) gen.current += 1
      settleWaiters(current, false)
    }
    // fetchPage 经 fnRef 取最新；key 已完整表达查询身份
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, tick, enabled, settleWaiters])

  const loadMore = useCallback(() => {
    const cur = slotRef.current
    if (!enabled || cur.key !== key || cur.loading) return
    const cursor = cur.nextCursor
    // 互斥 + 去重：本 key 任何在途翻页都阻塞新翻页（锁绑定 generation）
    if (!cursor || pageInFlight.current !== null) return
    const current = gen.current
    pageInFlight.current = { gen: current, cursor }
    // 只能解除属于自己 generation 的锁
    const release = () => {
      if (pageInFlight.current?.gen === current) pageInFlight.current = null
    }
    setSlot((prev) =>
      prev.key === key && gen.current === current
        ? { ...prev, loadingMore: true, loadMoreError: null }
        : prev,
    )
    fnRef
      .current(cursor)
      .then((res) => {
        release()
        if (gen.current !== current) return
        setSlot((prev) => {
          // 游标已漂移（如 reload 抢先重置）则丢弃此次响应
          if (prev.key !== key || prev.nextCursor !== cursor) return prev
          return {
            ...prev,
            items: [...prev.items, ...res.items],
            nextCursor: res.next_cursor,
            loadingMore: false,
          }
        })
      })
      .catch((err: unknown) => {
        release()
        if (gen.current !== current) return
        setSlot((prev) =>
          prev.key === key ? { ...prev, loadingMore: false, loadMoreError: toApiError(err) } : prev,
        )
      })
  }, [enabled, key])

  const valid = slot.key === key
  return {
    items: valid ? slot.items : [],
    nextCursor: valid ? slot.nextCursor : null,
    error: valid ? slot.error : null,
    loadMoreError: valid ? slot.loadMoreError : null,
    loading: !valid || slot.loading,
    loadingMore: valid && slot.loadingMore,
    reload,
    loadMore,
  }
}
