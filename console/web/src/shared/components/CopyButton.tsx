import { useEffect, useRef, useState } from 'react'
import { Icon } from './Icon'

async function copyText(value: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value)
      return true
    }
  } catch {
    // 继续走回退方案
  }
  try {
    const ta = document.createElement('textarea')
    ta.value = value
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    return ok
  } catch {
    return false
  }
}

/** 复制按钮：结果反馈（已复制/失败）经 aria-live 播报，不止依赖视觉变化。 */
export function CopyButton({ value, label = '复制' }: { value: string; label?: string }) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle')
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current)
    },
    [],
  )

  const handleCopy = async () => {
    const ok = await copyText(value)
    setState(ok ? 'copied' : 'failed')
    if (timer.current) clearTimeout(timer.current)
    timer.current = setTimeout(() => setState('idle'), 2000)
  }

  const text = state === 'copied' ? '已复制' : state === 'failed' ? '复制失败' : label
  return (
    <button
      type="button"
      className={`copy-btn${state === 'copied' ? ' is-copied' : ''}`}
      onClick={handleCopy}
      aria-live="polite"
    >
      <Icon name={state === 'copied' ? 'check' : 'copy'} size={13} />
      {text}
    </button>
  )
}
