import type { ReactElement } from 'react'

/**
 * 内联 SVG 图标集：stroke 1.5、currentColor、24 视图框。
 * 枚举即全部可用图标；不要新增 emoji 或外链图片图标。
 */
export type IconName =
  | 'apps'
  | 'clients'
  | 'users'
  | 'connections'
  | 'audit'
  | 'operators'
  | 'overview'
  | 'settings'
  | 'logout'
  | 'plus'
  | 'refresh'
  | 'search'
  | 'close'
  | 'check'
  | 'copy'
  | 'chevron-down'
  | 'chevron-right'
  | 'menu'
  | 'sun'
  | 'moon'
  | 'eye'
  | 'eye-off'
  | 'warning'
  | 'arrow-left'
  | 'lock'
  | 'key'

const PATHS: Record<IconName, ReactElement> = {
  apps: (
    <>
      <rect x="3.5" y="3.5" width="7" height="7" rx="1.5" />
      <rect x="13.5" y="3.5" width="7" height="7" rx="1.5" />
      <rect x="3.5" y="13.5" width="7" height="7" rx="1.5" />
      <rect x="13.5" y="13.5" width="7" height="7" rx="1.5" />
    </>
  ),
  clients: (
    <>
      <circle cx="8.5" cy="12" r="4.5" />
      <path d="M13 12h7.5M17.5 12v3M20.5 12v2.25" />
    </>
  ),
  users: (
    <>
      <circle cx="9.5" cy="8.5" r="3.5" />
      <path d="M3.5 19.5c0-3.3 2.7-5.5 6-5.5s6 2.2 6 5.5" />
      <path d="M15.5 5.4a3.5 3.5 0 0 1 0 6.2M17.5 14.3c1.9.8 3 2.4 3 4.2" />
    </>
  ),
  connections: (
    <>
      <path d="M9 7.5 6.8 9.7a4.24 4.24 0 0 0 6 6l2.2-2.2" />
      <path d="m15 16.5 2.2-2.2a4.24 4.24 0 0 0-6-6L9 10.5" />
    </>
  ),
  audit: (
    <>
      <path d="M7 3.5h7.5L19 8v12.5H7z" />
      <path d="M14 3.5V8h4.5M10 12h5M10 15.5h5" />
    </>
  ),
  operators: (
    <>
      <path d="M12 3.5 5 6v5.5c0 4.3 3 7.7 7 9 4-1.3 7-4.7 7-9V6z" />
      <path d="m9.5 11.8 1.8 1.8 3.4-3.6" />
    </>
  ),
  overview: (
    <>
      <rect x="3.5" y="3.5" width="17" height="17" rx="2" />
      <path d="M3.5 9.5h17M9.5 9.5v11" />
    </>
  ),
  settings: (
    <>
      <circle cx="12" cy="12" r="3" />
      <path d="M12 2.8v2.4M12 18.8v2.4M4.9 4.9l1.7 1.7M17.4 17.4l1.7 1.7M2.8 12h2.4M18.8 12h2.4M4.9 19.1l1.7-1.7M17.4 6.6l1.7-1.7" />
    </>
  ),
  logout: (
    <>
      <path d="M14 4.5H7.5A1.5 1.5 0 0 0 6 6v12a1.5 1.5 0 0 0 1.5 1.5H14" />
      <path d="M10.5 12h10M17.5 8.5 21 12l-3.5 3.5" />
    </>
  ),
  plus: <path d="M12 5v14M5 12h14" />,
  refresh: (
    <>
      <path d="M20 12a8 8 0 1 1-2.34-5.66" />
      <path d="M20 3.5v4h-4" />
    </>
  ),
  search: (
    <>
      <circle cx="11" cy="11" r="6.5" />
      <path d="m16 16 4.5 4.5" />
    </>
  ),
  close: <path d="m6 6 12 12M18 6 6 18" />,
  check: <path d="m5 12.5 4.5 4.5L19 7.5" />,
  copy: (
    <>
      <rect x="9" y="9" width="11" height="11" rx="1.5" />
      <path d="M5 15H4.5A1.5 1.5 0 0 1 3 13.5v-9A1.5 1.5 0 0 1 4.5 3h9A1.5 1.5 0 0 1 15 4.5V5" />
    </>
  ),
  'chevron-down': <path d="m6 9.5 6 6 6-6" />,
  'chevron-right': <path d="m9.5 6 6 6-6 6" />,
  menu: <path d="M4 7h16M4 12h16M4 17h16" />,
  sun: (
    <>
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2.5v2M12 19.5v2M4.3 4.3l1.4 1.4M18.3 18.3l1.4 1.4M2.5 12h2M19.5 12h2M4.3 19.7l1.4-1.4M18.3 5.7l1.4-1.4" />
    </>
  ),
  moon: <path d="M20.5 14.5A8.5 8.5 0 0 1 9.5 3.5a8.5 8.5 0 1 0 11 11z" />,
  eye: (
    <>
      <path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12z" />
      <circle cx="12" cy="12" r="3" />
    </>
  ),
  'eye-off': (
    <>
      <path d="M4 4.5 20 19.5" />
      <path d="M10.6 6.1A9 9 0 0 1 12 5.5c6 0 9.5 6.5 9.5 6.5a17.6 17.6 0 0 1-3 3.9M6.6 7.1A16.9 16.9 0 0 0 2.5 12S6 18.5 12 18.5c1.1 0 2.2-.2 3.1-.6" />
      <path d="M9.9 10.4a3 3 0 0 0 4.2 4.2" />
    </>
  ),
  warning: (
    <>
      <path d="M12 4 2.8 19.5h18.4z" />
      <path d="M12 10v4M12 17.2v.3" />
    </>
  ),
  'arrow-left': <path d="M19 12H5M8.5 8.5 5 12l3.5 3.5" />,
  lock: (
    <>
      <rect x="5" y="10.5" width="14" height="10" rx="2" />
      <path d="M8 10.5V8a4 4 0 1 1 8 0v2.5" />
    </>
  ),
  key: (
    <>
      <circle cx="8.5" cy="14.5" r="4" />
      <path d="m11.6 11.4 8.4-8.4M17 6.5l2.5 2.5M14.5 9l2 2" />
    </>
  ),
}

export function Icon({ name, size = 16 }: { name: IconName; size?: number }) {
  return (
    <svg
      className="icon"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.5}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {PATHS[name]}
    </svg>
  )
}

/** 品牌标记：盾形 + 钥匙孔 */
export function BrandMark({ size = 22 }: { size?: number }) {
  return (
    <svg
      className="brand-mark"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      aria-hidden="true"
    >
      <path
        d="M12 2.2 4.5 5v6.2c0 4.9 3.2 8.8 7.5 10.6 4.3-1.8 7.5-5.7 7.5-10.6V5z"
        fill="currentColor"
        fillOpacity={0.12}
        stroke="currentColor"
        strokeWidth={1.6}
        strokeLinejoin="round"
      />
      <circle cx="12" cy="10.2" r="2.1" fill="currentColor" />
      <path d="M12 12.3v3.4" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" />
    </svg>
  )
}
