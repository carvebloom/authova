import type { components } from './generated/management'

// 类型与 docs/implementation/console-api-v1.md（冻结契约）一一对应。
// JSON 为 snake_case；列表统一 {items, next_cursor}；ID 为不透明字符串；时间为 RFC3339 UTC。

export interface Operator {
  id: string
  email: string
  role: string
  /** 存在即为 App 级管理员（只能管理该 App） */
  app_id?: string
}

/** 平台运营账号列表项：直接使用生成契约的 Operator schema（含 status） */
export type PlatformOperator = components['schemas']['Operator']

/** POST /bff/platform/operators 输入：创建独立的平台账号 */
export interface CreateOperatorInput {
  email: string
  password: string
  role: AdminRole
}

/** 创建响应：totp_secret 仅返回一次，绝不写入 web 存储 */
export interface CreatedOperator {
  operator: PlatformOperator
  totp_secret: string
}

/** PATCH /bff/platform/operators/{id} 输入；reason 必填 */
export interface OperatorPatch {
  role: AdminRole
  status: string
  reason: string
}

export interface OperatorUpdated {
  updated: boolean
}

/** POST /bff/login 与 GET /bff/me 的响应 */
export interface SessionInfo {
  operator: Operator
  csrf_token: string
}

/** POST /bff/app-login 的响应：跳转至真实 App OIDC + PKCE + MFA 流程 */
export interface AppLoginResponse {
  redirect_to: string
}

/** 固定 App 管理员角色；不允许自定义角色或权限 */
export type AdminRole = 'owner' | 'admin' | 'auditor'

export interface App {
  id: string
  app_key: string
  name: string
  slug: string
  status: string
  issuer: string
  created_at: string
  config_version: number
  /** 是否要求应用用户完成 MFA（契约字段） */
  require_mfa: boolean
}

export interface AppInput {
  name: string
  slug: string
}

/** PATCH /apps/{id}：名称与 MFA 策略；config_version 乐观锁，409 须读回最新版本后再提交 */
export interface AppPatch {
  name?: string
  require_mfa?: boolean
  config_version: number
}

export type ClientKind = 'public' | 'confidential'

export interface Client {
  config_version: number
  id: string
  client_id: string
  app_id: string
  name: string
  kind: string
  status: string
  /** 系统内置客户端：展示受保护标识，不提供编辑入口 */
  system?: boolean
  /** 旧密钥宽限期截止（Unix 秒）；0 表示没有仍在宽限的旧密钥 */
  previous_secret_expires_at: number
  redirect_uris: string[]
  allowed_scopes: string[]
  allowed_audiences: string[]
  allowed_introspection_audiences: string[]
  grant_types: string[]
  /** 契约以 ... 表示可能返回更多字段，创建时间如返回则展示 */
  created_at?: string
}

export interface ClientInput {
  name: string
  kind: ClientKind
  redirect_uris: string[]
  allowed_scopes: string[]
  allowed_audiences: string[]
  allowed_introspection_audiences: string[]
  grant_types: string[]
}

/** PATCH 不允许变更 kind */
export type ClientPatch = Omit<ClientInput, 'kind'> & {
  config_version: number
  status?: 'active' | 'disabled'
}

/** 创建 confidential client 时一次性返回 client_secret */
export interface CreatedClient extends Client {
  client_secret?: string
}

export interface User {
  id: string
  app_id: string
  display_name: string
  status: string
  /** 已脱敏 */
  email?: string
  /** 已脱敏 */
  phone?: string
  created_at: string
  session_epoch: number
  /** 该用户在本 App 的固定管理员角色；空串或缺省表示非管理员 */
  admin_role?: AdminRole | ''
}

export interface UserSession {
  id: string
  created_at: string
  expires_at: string
  status: string
  amr: string[]
}

export interface AuditEvent {
  id: string
  app_id: string
  actor_id: string
  action: string
  subject_id: string
  created_at: string
  /** 操作原因；契约可选，未返回时详情页不展示该行 */
  reason?: string
}

/** 上游身份源提供方：标准 OIDC、GitHub.com OAuth App、微信网站扫码、QQ 网站登录或支付宝网页授权（不包含移动 SDK） */
export type ConnectionProviderType = components['schemas']['Connection']['provider_type']

export type AlipayCredentials = components['schemas']['AlipayCredentials']

/** 读取响应绝不含 client_secret */
export interface Connection {
  id: string
  name: string
  provider_type: ConnectionProviderType
  issuer: string
  client_id: string
  status: 'active' | 'disabled'
  config_version: number
  /** 契约标注可选；未返回时界面不展示创建时间 */
  created_at?: string
}

/** provider_type 缺省为 oidc；issuer 仅 OIDC 需要，GitHub/微信/QQ/支付宝由服务端固定 issuer，创建时省略；支付宝使用 alipay_credentials 两把密钥，其他类型使用 client_secret */
export interface ConnectionInput {
  name: string
  provider_type: ConnectionProviderType
  issuer?: string
  client_id: string
  client_secret?: string
  alipay_credentials?: AlipayCredentials
}

/**
 * provider_type / issuer / client_id 不可变。
 * client_secret 缺省表示保留原密钥（显式空串无效）；config_version 过期返回 409，须重新读取后再提交。
 */
export interface ConnectionPatch {
  config_version: number
  reason: string
  name?: string
  status?: 'active' | 'disabled'
  client_secret?: string
  alipay_credentials?: AlipayCredentials
}

/**
 * GET /apps/{app}/authentication-config：部署就绪状态、联邦回调地址与当前登录策略。
 * 就绪字段（*_configured / password_login / provider_types）只表示部署配置存在；
 * policy 为服务端强制的登录策略，PATCH 走乐观锁（config_version）。
 */
export type AuthenticationConfig = components['schemas']['AuthenticationConfig']

/** 登录策略七个开关（含 passkey_enabled）；PATCH 必须完整提交全部七项 */
export type AuthenticationPolicy = components['schemas']['AuthenticationPolicy']

/** PATCH /apps/{app}/authentication-config 输入：完整策略 + config_version + reason */
export type AuthenticationPolicyPatch = components['schemas']['AuthenticationPolicyPatch']

/** 应用资源限额六项（生成契约 ResourceLimits；各项取值上下界见契约 schema） */
export type ResourceLimits = components['schemas']['ResourceLimits']

/**
 * GET /apps/{app}/resource-policy 响应：配置限额与当前生效限额。
 * 并发上限按进程生效（concurrency_scope=per_process），服务端可因预留全局容量下调生效值；
 * 频率计数为部署级共享限流器（counter_scope=shared_limiter），按固定窗口计数；
 * 提交后当前进程立即生效，其余副本在 propagation_seconds 内生效。
 */
export type ResourceConfig = components['schemas']['ResourceConfig']

/** PATCH /apps/{app}/resource-policy 输入：完整六项限额 + config_version + reason */
export type ResourcePolicyPatch = components['schemas']['ResourcePolicyPatch']

/** POST /apps/{app}/users/{user}/enable 输入：reason + 当前 session_epoch 乐观校验 */
export type EnableUserInput = components['schemas']['EnableUser']

/** POST /apps/{app}/clients/{client}/secret/rotate 输入；grace_seconds ∈ [0, 86400]，0 为立即切换 */
export type ClientSecretRotateInput = components['schemas']['ClientSecretRotate']

/** 轮换响应：client_secret 仅此一次返回，GET 永不返回密钥 */
export type ClientSecretRotated = components['schemas']['ClientSecretRotated']

/** secret/revoke-previous 与 keys/{key_id}/revoke 共用的版本化原因输入 */
export type VersionedReason = components['schemas']['VersionedReason']

/** GET /apps/{app}/keys 列表项；时间均为 Unix 秒，0 表示未发生 */
export type SigningKey = components['schemas']['SigningKey']
export type SigningKeyPage = components['schemas']['SigningKeyPage']

/** POST /apps/{app}/keys/{key_id}/revoke 响应；吊销当前签名密钥时 new_key_id 为继任者 */
export type KeyRevoked = components['schemas']['KeyRevoked']

export interface ListResponse<T> {
  items: T[]
  next_cursor: string | null
}

export type Agent = components['schemas']['Agent']
export type AgentCreate = components['schemas']['AgentCreate']
export type CreatedAgent = components['schemas']['CreatedAgent']
export type AgentPatch = components['schemas']['AgentPatch']
export type AgentInstallation = components['schemas']['AgentInstallation']
export type AgentInstallationCreate = components['schemas']['AgentInstallationCreate']
export type AgentInstallationPatch = components['schemas']['AgentInstallationPatch']
export type AgentGrant = components['schemas']['AgentGrant']
