/**
 * 主题偏好：light / dark / system。仅存 localStorage 的「authova-theme」（非敏感界面偏好），
 * 不涉及任何凭据。默认跟随系统；显式选择经 data-theme 覆盖。
 */

export type ThemeChoice = 'light' | 'dark' | 'system'

const STORAGE_KEY = 'authova-theme'

export function getThemeChoice(): ThemeChoice {
  try {
    const v = window.localStorage.getItem(STORAGE_KEY)
    if (v === 'light' || v === 'dark') return v
  } catch {
    // 存储不可用（隐私模式等）时按 system 处理
  }
  return 'system'
}

export function applyTheme(choice: ThemeChoice): void {
  const root = document.documentElement
  if (choice === 'system') {
    root.removeAttribute('data-theme')
  } else {
    root.setAttribute('data-theme', choice)
  }
}

export function setThemeChoice(choice: ThemeChoice): void {
  try {
    if (choice === 'system') window.localStorage.removeItem(STORAGE_KEY)
    else window.localStorage.setItem(STORAGE_KEY, choice)
  } catch {
    // 忽略存储失败：主题仍以会话内 data-theme 生效
  }
  applyTheme(choice)
}

/** 当前实际生效的主题（解析 system）。 */
export function resolvedTheme(choice: ThemeChoice = getThemeChoice()): 'light' | 'dark' {
  if (choice !== 'system') return choice
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

/** 启动时尽早调用，避免首屏主题闪烁。 */
export function initTheme(): void {
  applyTheme(getThemeChoice())
}
