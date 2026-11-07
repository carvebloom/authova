import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError } from '../../api/client'

export interface AsyncState<T> {
  data: T | null
  error: ApiError | null
  /** 首次加载中（当前 key 无任何数据） */
  loading: boolean
  /** 已有数据、后台刷新中（刷新失败保留旧数据并给出 error） */
  refreshing: boolean
  /**
   * 重新拉取（后台刷新语义：保留旧数据）。
   * 返回 Promise：本次拉取成功/失败/被取代时分别结算为 true/false，
   * 供「结果未知后的权威读回」流程等待结论。
   */
  reload: () => Promise<boolean>
}

interface Slot<T> {
  key: string
  data: T | null
  error: ApiError | null
  loading: boolean
  refreshing: boolean
}

function toApiError(err: unknown): ApiError {
  return err instanceof ApiError ? err : new ApiError({ status: 0, detail: String(err) })
}

/**
 * key 完整标识一次查询（如 `app:{id}:users?q=...`；fn 由调用方按 key 构造）。
 * 竞态约定：
 * - key 变化在渲染期同步重置并推进 generation，旧 key 数据绝不随新 key 渲染（跨 App 隔离）；
 * - reload() 同步推进 generation，进行中的响应立即作废，并保留旧数据展示 refreshing；
 * - effect cleanup 推进 generation + 取消标记：换 key / unmount 后迟到的响应一律忽略。
 */
export function useAsync<T>(key: string, fn: () => Promise<T>): AsyncState<T> {
  const [slot, setSlot] = useState<Slot<T>>(() => ({
    key,
    data: null,
    error: null,
    loading: true,
    refreshing: false,
  }))
  const gen = useRef(0)
  const [tick, setTick] = useState(0)
  // 每次 run（generation）对应一组等待 reload 结论的调用方
  const waiters = useRef(new Map<number, Array<(ok: boolean) => void>>())

  const settleWaiters = useCallback((generation: number, ok: boolean) => {
    const list = waiters.current.get(generation)
    if (list) {
      waiters.current.delete(generation)
      for (const resolve of list) resolve(ok)
    }
    // 更早的 run 已被取代/作废：其 waiters 一律以 false 结算，杜绝悬挂 await
    for (const [g, list2] of waiters.current) {
      if (g < generation) {
        waiters.current.delete(g)
        for (const resolve of list2) resolve(false)
      }
    }
  }, [])

  // 渲染期同步隔离 + generation 推进：React 会在提交子树前用新状态重渲染，
  // 旧 key 数据不会随新 key 出现，其进行中的响应也立即作废
  if (slot.key !== key) {
    gen.current += 1
    setSlot({ key, data: null, error: null, loading: true, refreshing: false })
  }

  // reload 先推进 generation（同步作废进行中的响应），再触发 effect 重新拉取
  const reload = useCallback((): Promise<boolean> => {
    gen.current += 1
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
    const current = gen.current
    let cancelled = false
    setSlot((prev) => {
      if (prev.key !== key) return prev
      return prev.data !== null
        ? { ...prev, error: null, refreshing: true, loading: false }
        : { ...prev, error: null, loading: true, refreshing: false }
    })
    fn()
      .then((result) => {
        if (cancelled || gen.current !== current) return
        setSlot({ key, data: result, error: null, loading: false, refreshing: false })
        settleWaiters(current, true)
      })
      .catch((err: unknown) => {
        if (cancelled || gen.current !== current) return
        setSlot((prev) => {
          if (prev.key !== key) return prev
          // 刷新失败保留旧数据；首屏失败则只有错误态
          return { ...prev, error: toApiError(err), loading: false, refreshing: false }
        })
        settleWaiters(current, false)
      })
    return () => {
      cancelled = true
      // 换 key / 重新 reload / unmount：本 run 立即作废
      if (gen.current === current) gen.current += 1
      settleWaiters(current, false)
    }
    // fn 由调用方依据 key 构造；key 已完整表达查询身份
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, tick, settleWaiters])

  return {
    data: slot.key === key ? slot.data : null,
    error: slot.key === key ? slot.error : null,
    loading: slot.key !== key || slot.loading,
    refreshing: slot.key === key && slot.refreshing,
    reload,
  }
}
