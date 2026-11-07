import type { Operator } from '../../api/types'

/**
 * 固定角色能力模型（无组织或自定义业务 RBAC）：
 * - 平台级（operator 无 app_id）owner/admin：应用管理写能力，含创建应用与暂停/恢复
 * - App 级（operator.app_id 存在）owner：管理本 App 数据、轮换密钥、设置管理员角色
 * - App 级 admin：管理本 App 数据（客户端/用户/连接）
 * - auditor（两级）：只读
 * - 未知角色：只读并提示；服务端始终是权限最终裁决者
 */
export interface Capabilities {
  /** 原始角色字符串 */
  role: string | null
  /** operator.app_id 存在即 App 级管理员 */
  isAppScoped: boolean
  /** owner/admin 可执行数据写操作 */
  canMutate: boolean
  /** 创建应用：仅平台级 */
  canCreateApps: boolean
  /** 暂停/恢复应用：仅平台级 */
  canSuspendResume: boolean
  /** 轮换/吊销签名密钥：平台级 owner/admin 或 App 级 owner */
  canRotateKeys: boolean
  /** 修改登录策略（authentication-config policy）：平台级 owner/admin 或 App 级 owner */
  canManageLoginPolicy: boolean
  /** 设置 App 管理员角色：平台级或 App 级 owner */
  canManageAdministrators: boolean
  /** 查看平台运营账号页：平台级已知角色（App 级不可见） */
  canViewPlatformOperators: boolean
  /** 创建/变更平台运营账号：仅平台级 owner */
  canManageOperators: boolean
  /** 角色不在已知集合内 */
  unknownRole: boolean
}

export const KNOWN_ROLES = new Set(['owner', 'admin', 'auditor'])

export function deriveCapabilities(operator: Operator | null): Capabilities {
  const role = operator?.role ?? null
  const isAppScoped = Boolean(operator?.app_id)
  const isOwner = role === 'owner'
  const canMutate = role === 'owner' || role === 'admin'
  const unknownRole = role !== null && !KNOWN_ROLES.has(role)
  return {
    role,
    isAppScoped,
    canMutate,
    canCreateApps: canMutate && !isAppScoped,
    canSuspendResume: canMutate && !isAppScoped,
    canRotateKeys: canMutate && (!isAppScoped || isOwner),
    canManageLoginPolicy: canMutate && (!isAppScoped || isOwner),
    canManageAdministrators: isOwner,
    canViewPlatformOperators: !isAppScoped && role !== null && KNOWN_ROLES.has(role),
    canManageOperators: !isAppScoped && isOwner,
    unknownRole,
  }
}
