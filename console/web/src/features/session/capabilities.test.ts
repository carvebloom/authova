import { describe, expect, it } from 'vitest'
import { deriveCapabilities } from './capabilities'
import type { Operator } from '../../api/types'

const platformAdmin: Operator = { id: 'op-1', email: 'a@b.c', role: 'admin' }
const platformAuditor: Operator = { id: 'op-2', email: 'a@b.c', role: 'auditor' }
const appOwner: Operator = { id: 'op-3', email: 'a@b.c', role: 'owner', app_id: 'app-1' }
const appAdmin: Operator = { id: 'op-4', email: 'a@b.c', role: 'admin', app_id: 'app-1' }
const appAuditor: Operator = { id: 'op-5', email: 'a@b.c', role: 'auditor', app_id: 'app-1' }
const unknown: Operator = { id: 'op-6', email: 'a@b.c', role: 'support' }

describe('deriveCapabilities 固定角色能力矩阵', () => {
  it('平台级 admin：管理应用但不能授予管理员角色或变更运营账号', () => {
    const caps = deriveCapabilities(platformAdmin)
    expect(caps).toMatchObject({
      isAppScoped: false,
      canMutate: true,
      canCreateApps: true,
      canSuspendResume: true,
      canRotateKeys: true,
      canManageLoginPolicy: true,
      canManageAdministrators: false,
      canViewPlatformOperators: true,
      canManageOperators: false,
      unknownRole: false,
    })
  })

  it('平台级 owner：可创建/变更运营账号', () => {
    const caps = deriveCapabilities({ id: 'op-0', email: 'o@b.c', role: 'owner' })
    expect(caps.canViewPlatformOperators).toBe(true)
    expect(caps.canManageOperators).toBe(true)
    expect(caps.canManageAdministrators).toBe(true)
  })

  it('App 级 owner：管理本 App、轮换密钥、设置管理员，但不可见运营账号页', () => {
    const caps = deriveCapabilities(appOwner)
    expect(caps).toMatchObject({
      isAppScoped: true,
      canMutate: true,
      canCreateApps: false,
      canSuspendResume: false,
      canRotateKeys: true,
      canManageLoginPolicy: true,
      canManageAdministrators: true,
      canViewPlatformOperators: false,
      canManageOperators: false,
    })
  })

  it('App 级 admin：管理本 App 数据，但无平台操作、无密钥轮换、无登录策略写、无角色设置', () => {
    const caps = deriveCapabilities(appAdmin)
    expect(caps).toMatchObject({
      isAppScoped: true,
      canMutate: true,
      canCreateApps: false,
      canSuspendResume: false,
      canRotateKeys: false,
      canManageLoginPolicy: false,
      canManageAdministrators: false,
    })
  })

  it('auditor（两级）只读', () => {
    for (const op of [platformAuditor, appAuditor]) {
      const caps = deriveCapabilities(op)
      expect(caps.canMutate).toBe(false)
      expect(caps.canCreateApps).toBe(false)
      expect(caps.canSuspendResume).toBe(false)
      expect(caps.canRotateKeys).toBe(false)
      expect(caps.canManageLoginPolicy).toBe(false)
      expect(caps.canManageAdministrators).toBe(false)
      expect(caps.unknownRole).toBe(false)
    }
  })

  it('未知角色只读且标记 unknownRole', () => {
    const caps = deriveCapabilities(unknown)
    expect(caps.canMutate).toBe(false)
    expect(caps.unknownRole).toBe(true)
  })

  it('未登录（null operator）无任何能力', () => {
    const caps = deriveCapabilities(null)
    expect(caps.canMutate).toBe(false)
    expect(caps.isAppScoped).toBe(false)
    expect(caps.unknownRole).toBe(false)
  })
})
