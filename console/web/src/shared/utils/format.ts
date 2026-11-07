/** 把 RFC3339 UTC 时间格式化为本地时间；无法解析时原样展示。 */
export function formatTime(iso: string | undefined | null): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString('zh-CN', { hour12: false })
}

/** 把 Unix 秒时间戳格式化为本地时间；0 / 缺省表示未发生，展示占位符。 */
export function formatUnixSeconds(unix: number | undefined | null): string {
  if (!unix) return '—'
  const ms = unix * 1000
  const d = new Date(ms)
  if (Number.isNaN(d.getTime())) return String(unix)
  return d.toLocaleString('zh-CN', { hour12: false })
}

/** 把空格/逗号/换行分隔的输入拆成字符串数组。 */
export function splitList(text: string): string[] {
  return text
    .split(/[\s,，]+/)
    .map((s) => s.trim())
    .filter((s) => s.length > 0)
}

export function joinList(items: string[] | undefined | null): string {
  if (!items || items.length === 0) return '—'
  return items.join(' ')
}

/** 截断不透明的长 ID，保留首尾便于辨认。 */
export function shortenId(id: string, head = 8, tail = 4): string {
  if (id.length <= head + tail + 1) return id
  return `${id.slice(0, head)}…${id.slice(-tail)}`
}

export const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/
export const SLUG_RE = /^[a-z0-9][a-z0-9-]{0,62}$/
export const TOTP_RE = /^\d{6}$/
/** E.164：+ 开头，8–15 位数字 */
export const E164_RE = /^\+[1-9]\d{6,14}$/

/** 已知角色映射为中文；未知角色原样展示（服务端为最终权威）。 */
export function roleLabel(role: string): string {
  if (role === 'owner') return '拥有者'
  if (role === 'admin') return '管理员'
  if (role === 'auditor') return '审计员'
  return role
}

/** 用户的 App 管理员角色标签；空串/缺省表示非管理员。 */
export function adminRoleLabel(role: string | undefined): string {
  if (!role) return '无'
  return roleLabel(role)
}
