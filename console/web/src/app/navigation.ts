import type { IconName } from '../shared/components/Icon'

/**
 * 导航注册表：侧栏与抽屉的唯一来源。
 * 平台项面向平台上下文；App 版块在进入 /apps/:appId 后展示（label 静态，路由含 appId）。
 */

export interface PlatformNavItem {
  to: string
  label: string
  icon: IconName
  end?: boolean
  /** 需要的能力（来自 session capabilities）；缺省为登录即可见 */
  requires?: 'canViewPlatformOperators'
}

export const PLATFORM_NAV: PlatformNavItem[] = [
  { to: '/apps', label: '全部应用', icon: 'apps', end: true },
  {
    to: '/platform/operators',
    label: '平台运营账号',
    icon: 'operators',
    requires: 'canViewPlatformOperators',
  },
]

export interface AppTabItem {
  /** 相对 /apps/:appId 的路径段；'' 为概览 */
  segment: '' | 'settings' | 'clients' | 'users' | 'connections' | 'agents' | 'audit'
  label: string
  icon: IconName
}

export const APP_TABS: AppTabItem[] = [
  { segment: '', label: '概览', icon: 'overview' },
  { segment: 'settings', label: '配置', icon: 'settings' },
  { segment: 'clients', label: '客户端', icon: 'clients' },
  { segment: 'users', label: '用户', icon: 'users' },
  { segment: 'connections', label: '连接', icon: 'connections' },
  { segment: 'agents', label: 'Agent 访问', icon: 'clients' },
  { segment: 'audit', label: '审计日志', icon: 'audit' },
]

export function appTabPath(appId: string, segment: string): string {
  return segment ? `/apps/${appId}/${segment}` : `/apps/${appId}`
}
