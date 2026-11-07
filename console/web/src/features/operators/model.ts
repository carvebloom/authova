import type { AdminRole } from '../../api/types'
export const OPERATOR_STATUSES: Array<{ value: string; label: string }> = [
  { value: 'active', label: '正常' },
  { value: 'disabled', label: '禁用' },
]

/** 平台运营账号管理：App 级管理员（operator.app_id）不可见本页。 */
export function isAdminRole(value: string): value is AdminRole {
  return value === 'owner' || value === 'admin' || value === 'auditor'
}
