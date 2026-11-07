import { useEffect, useRef, useState } from 'react'

const FOCUSABLE =
  'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])'

export interface ModalA11yOptions {
  open: boolean
  onClose: () => void
  /** false 时 Esc / 遮罩不关闭（提交中禁止取消与离开）；默认 true */
  dismissible?: boolean
}

/**
 * 模态栈：同一时间只应存在一层模态；并发时 Esc / Tab 陷阱只作用于最顶层。
 * 顶层按 DOM 顺序判定（后挂载的 portal 在 document.body 中更靠后、视觉上在最上），
 * 与 effect 注册顺序无关（子组件 effect 先于父组件执行，不能直接按注册序）。
 */
const modalStack: Array<{ id: number; getEl: () => HTMLElement | null }> = []
let nextModalId = 0
let originalOverflow = ''

/**
 * 模态层共用行为：焦点陷阱（Tab/Shift+Tab 循环，含焦点停在面板自身的时刻）、
 * Esc（仅最顶层、受 dismissible 门控）、背景 inert、滚动锁、打开聚焦、关闭后焦点恢复。
 * onClose/dismissible 经 ref 读取，父级内联函数不会导致效果重跑抢焦点。
 */
export function useModalA11y<T extends HTMLElement = HTMLDivElement>({
  open,
  onClose,
  dismissible = true,
}: ModalA11yOptions) {
  const panelRef = useRef<T>(null)
  const [activation, setActivation] = useState(() => ({ open, trigger: document.activeElement }))
  if (activation.open !== open)
    setActivation({ open, trigger: open ? document.activeElement : activation.trigger })
  const modalIdRef = useRef(0)
  const onCloseRef = useRef(onClose)
  const dismissibleRef = useRef(dismissible)

  // 同步最新 props 到 ref（在 effect 中写入，避免渲染期读 ref）；
  // 该 effect 先于下方 keydown 监听注册，保证 handler 读到最新值
  useEffect(() => {
    onCloseRef.current = onClose
    dismissibleRef.current = dismissible
  })

  useEffect(() => {
    if (!open) return
    const panel = panelRef.current
    modalIdRef.current = ++nextModalId
    const entry = { id: modalIdRef.current, getEl: () => panelRef.current }
    if (modalStack.length === 0) originalOverflow = document.body.style.overflow
    modalStack.push(entry)

    // 仅在焦点不在面板内时接管：表单 autoFocus 已定位时不抢焦点
    if (panel && !panel.contains(document.activeElement)) panel.focus()

    const root = document.getElementById('root')
    root?.setAttribute('inert', '')
    document.body.style.overflow = 'hidden'

    return () => {
      const idx = modalStack.indexOf(entry)
      if (idx >= 0) modalStack.splice(idx, 1)
      if (modalStack.length === 0) {
        root?.removeAttribute('inert')
        document.body.style.overflow = originalOverflow
      }
      const prev = activation.trigger
      if (prev instanceof HTMLElement && document.contains(prev)) prev.focus()
    }
  }, [open, activation.trigger])

  useEffect(() => {
    if (!open) return
    const isTopmost = () => {
      const me = panelRef.current
      if (!me) return false
      for (const other of modalStack) {
        if (other.id === modalIdRef.current) continue
        const el = other.getEl()
        // 其他模态在 DOM 中位于我之后 → 它才是视觉顶层
        if (el?.isConnected && me.compareDocumentPosition(el) & Node.DOCUMENT_POSITION_FOLLOWING) {
          return false
        }
      }
      return true
    }
    const onKeyDown = (e: KeyboardEvent) => {
      // 仅最顶层模态响应；下层模态（不应存在）不响应 Esc/Tab
      if (!isTopmost()) return
      if (e.key === 'Escape') {
        e.stopPropagation()
        if (dismissibleRef.current) onCloseRef.current()
        return
      }
      if (e.key !== 'Tab') return
      const panel = panelRef.current
      if (!panel) return
      const focusable = Array.from(panel.querySelectorAll<HTMLElement>(FOCUSABLE)).filter((el) => {
        if (
          el.tabIndex < 0 ||
          el.matches('input[type="hidden"], :disabled') ||
          el.closest('[hidden], [aria-hidden="true"], [inert]')
        )
          return false
        const closed = el.closest('details:not([open])')
        if (closed && !closed.querySelector('summary')?.contains(el)) return false
        for (
          let node: HTMLElement | null = el;
          node && panel.contains(node);
          node = node.parentElement
        ) {
          const style = window.getComputedStyle(node)
          if (style.display === 'none' || style.visibility === 'hidden') return false
        }
        return true
      })
      const active = document.activeElement
      if (focusable.length === 0) {
        e.preventDefault()
        panel.focus()
        return
      }
      const first = focusable[0]!
      const last = focusable[focusable.length - 1]!
      // 焦点停在面板自身（初始 panel.focus 后）时，两个方向都必须陷在模态内
      const atStart = active === first || active === panel || !panel.contains(active)
      const atEnd = active === last || active === panel || !panel.contains(active)
      if (e.shiftKey) {
        if (atStart) {
          e.preventDefault()
          last.focus()
        }
      } else if (atEnd) {
        e.preventDefault()
        first.focus()
      }
    }
    document.addEventListener('keydown', onKeyDown, true)
    return () => document.removeEventListener('keydown', onKeyDown, true)
  }, [open])

  return panelRef
}
