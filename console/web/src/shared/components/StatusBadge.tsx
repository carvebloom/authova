const KNOWN: Record<string, { label: string; tone: 'ok' | 'warn' | 'bad' | 'muted' }> = {
  active: { label: '活跃', tone: 'ok' },
  quarantined: { label: '恢复隔离', tone: 'warn' },
  suspended: { label: '已暂停', tone: 'warn' },
  disabled: { label: '已禁用', tone: 'bad' },
  revoked: { label: '已撤销', tone: 'muted' },
  expired: { label: '已过期', tone: 'muted' },
}

/** 状态为不透明字符串：已知状态映射为中文标签与色彩，未知状态原样展示。 */
export function StatusBadge({ status }: { status: string }) {
  const known = KNOWN[status]
  const label = known ? known.label : status
  const tone = known ? known.tone : 'muted'
  return (
    <span className={`badge badge-${tone}`} title={status}>
      <span className="badge-dot" aria-hidden="true" />
      {label}
    </span>
  )
}
