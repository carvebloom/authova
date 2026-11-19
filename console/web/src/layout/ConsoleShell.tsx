import { useState } from 'react'
import { NavLink, Outlet, useMatch, useNavigate } from 'react-router-dom'
import { ApiError } from '../api/client'
import { useSession } from '../features/session/useSession'
import { ErrorNotice } from '../shared/components/ErrorNotice'
import { Drawer } from '../shared/components/Drawer'
import { BrandMark, Icon } from '../shared/components/Icon'
import { IconButton } from '../shared/components/Button'
import { roleLabel } from '../shared/utils/format'
import { PLATFORM_NAV } from '../app/navigation'
import { getThemeChoice, setThemeChoice, type ThemeChoice } from '../layout/theme'

/**
 * 工作台外壳：稳定的全局侧栏，窄屏使用同一导航模型的顶栏 + 抽屉。
 * 账号区：邮箱/角色、主题切换、退出登录（失败显式报错且不离开当前页）。
 */
export function ConsoleShell() {
  const { operator, signOut, unknownRole, canViewPlatformOperators } = useSession()
  const navigate = useNavigate()
  const appMatch = useMatch('/apps/:appId/*')
  const currentAppId = appMatch?.params.appId ?? ''
  const [signOutError, setSignOutError] = useState<ApiError | null>(null)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [theme, setTheme] = useState<ThemeChoice>(() => getThemeChoice())

  const handleSignOut = async () => {
    setSignOutError(null)
    try {
      await signOut()
      navigate('/login', { replace: true })
    } catch (err) {
      // 退出结果未知：会话可能仍然有效，保留登录并显式展示错误，不跳转
      setSignOutError(
        err instanceof ApiError ? err : new ApiError({ status: 0, detail: String(err) }),
      )
    }
  }

  const navBody = (onNavigate?: () => void) => (
    <>
      <p className="side-section">{operator?.app_id ? '应用工作区' : '平台'}</p>
      {PLATFORM_NAV.map((item) => {
        if (item.requires && !canViewPlatformOperators) return null
        return (
          <NavLink
            key={item.to}
            to={item.to}
            end={item.end}
            className="side-link"
            onClick={onNavigate}
          >
            <Icon name={item.icon} />
            {item.to === '/apps' && operator?.app_id ? '我的应用' : item.label}
          </NavLink>
        )
      })}
    </>
  )

  const accountBlock = (
    <>
      {operator ? (
        <div className="operator">
          <span className="operator-email" title={operator.email}>
            {operator.email}
          </span>
          <span className="operator-role badge badge-muted">{roleLabel(operator.role)}</span>
        </div>
      ) : null}
      <div className="side-footer-actions">
        <label className="theme-picker">
          <span className="visually-hidden">界面主题</span>
          <select
            className="input"
            aria-label="界面主题"
            value={theme}
            onChange={(event) => {
              const choice = event.target.value as ThemeChoice
              setThemeChoice(choice)
              setTheme(choice)
            }}
          >
            <option value="system">跟随系统</option>
            <option value="light">浅色</option>
            <option value="dark">深色</option>
          </select>
        </label>
        <button
          type="button"
          className="btn btn-ghost btn-sm side-footer-signout"
          onClick={handleSignOut}
        >
          退出登录
        </button>
      </div>
    </>
  )

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">
          <BrandMark />
          <span className="brand-name">Authova</span>
          <span className="brand-sub">控制台</span>
        </div>
        <nav className="side-nav" aria-label="主导航">
          {navBody()}
        </nav>
        <div className="side-footer">{accountBlock}</div>
      </aside>

      <div className="main">
        <header className="topbar">
          <IconButton icon="menu" label="打开导航" onClick={() => setDrawerOpen(true)} />
          <span className="brand">
            <BrandMark size={18} />
            <span className="brand-name">Authova</span>
          </span>
          <span className="topbar-context">{currentAppId ? '应用管理' : '平台'}</span>
        </header>

        {unknownRole && operator ? (
          <div className="notice notice-warn role-banner" role="status">
            <div className="notice-body">
              <p className="notice-title">
                当前账号角色（{operator.role}）未识别，已按只读模式展示；写操作以服务端校验为准。
              </p>
            </div>
          </div>
        ) : null}

        <main className="content">
          {signOutError ? <ErrorNotice error={signOutError} onRetry={handleSignOut} /> : null}
          <Outlet />
        </main>
      </div>

      <Drawer open={drawerOpen} title="导航" onClose={() => setDrawerOpen(false)}>
        <nav className="side-nav" aria-label="移动端导航">
          {navBody(() => setDrawerOpen(false))}
        </nav>
        <div className="side-footer">{accountBlock}</div>
      </Drawer>
    </div>
  )
}
