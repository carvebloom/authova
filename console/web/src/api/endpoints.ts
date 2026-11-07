import { apiFetch, ApiError } from './client'
import type {
  AdminRole,
  App,
  AppInput,
  AppLoginResponse,
  AppPatch,
  AuditEvent,
  AuthenticationConfig,
  AuthenticationPolicyPatch,
  Client,
  ClientInput,
  ClientPatch,
  ClientSecretRotateInput,
  ClientSecretRotated,
  Connection,
  ConnectionInput,
  ConnectionPatch,
  CreatedClient,
  CreatedOperator,
  CreateOperatorInput,
  EnableUserInput,
  KeyRevoked,
  ListResponse,
  OperatorPatch,
  OperatorUpdated,
  PlatformOperator,
  ResourceConfig,
  ResourceLimits,
  ResourcePolicyPatch,
  SessionInfo,
  SigningKeyPage,
  User,
  UserSession,
  VersionedReason,
} from './types'

// An HTTP success without the expected state acknowledgement is not evidence
// that a credential mutation failed; classify it as unknown and reconcile.
function unknownAcknowledgement(): never {
  throw new ApiError({
    status: 503,
    code: 'outcome_unknown',
    detail: '服务端状态变更响应不完整，请读取当前状态核对；系统不会自动重试。',
  })
}

// ---- 会话 ----

export function login(input: { email: string; password: string; totp: string }) {
  // 登录失败的 401 是凭据错误，不触发全局“会话失效”处理
  return apiFetch<SessionInfo>('/login', {
    method: 'POST',
    body: input,
    skipUnauthorizedHandler: true,
  })
}

/** App 管理入口：换取真实 App OIDC + PKCE + MFA 流程的重定向地址 */
export function appLogin(appKey: string) {
  return apiFetch<AppLoginResponse>('/app-login', {
    method: 'POST',
    body: { app_key: appKey },
    skipUnauthorizedHandler: true,
  })
}

export function fetchMe() {
  return apiFetch<SessionInfo>('/me')
}

export function logout() {
  // 主动登出由 signOut 自行清理本地状态，不走全局 401 处理
  return apiFetch<null>('/logout', {
    method: 'POST',
    skipUnauthorizedHandler: true,
    expectedStatus: 204,
  })
}

// ---- 应用 ----

export interface ListOptions {
  cursor?: string
  limit?: number
}

export function listApps(options: ListOptions = {}) {
  return apiFetch<ListResponse<App>>('/apps', {
    query: { cursor: options.cursor, limit: options.limit },
  })
}

/**
 * 创建应用。契约要求 Idempotency-Key：同一 payload 重试必须复用同一键（服务端去重），
 * payload 变化或创建成功后必须更换新键。若缺省，BFF 会为每次 POST 生成随机键，
 * 结果未知时的重试将产生重复应用。
 */
function appChanged<T>(value: T): T {
  window.dispatchEvent(new Event('authova:apps-changed'))
  return value
}

export function createApp(input: AppInput, idempotencyKey: string) {
  return apiFetch<App>('/apps', { method: 'POST', body: input, idempotencyKey }).then(appChanged)
}

export function getApp(appId: string) {
  return apiFetch<App>(`/apps/${encodeURIComponent(appId)}`)
}

/** 更新应用名称 / MFA 策略；携带 config_version 乐观锁，409 须读回后再提交 */
export function updateApp(appId: string, patch: AppPatch) {
  return apiFetch<App>(`/apps/${encodeURIComponent(appId)}`, { method: 'PATCH', body: patch }).then(
    appChanged,
  )
}

export function suspendApp(appId: string, reason: string) {
  return apiFetch<null>(`/apps/${encodeURIComponent(appId)}/suspend`, {
    method: 'POST',
    body: { reason },
  }).then(appChanged)
}

export function resumeApp(appId: string, reason: string) {
  return apiFetch<null>(`/apps/${encodeURIComponent(appId)}/resume`, {
    method: 'POST',
    body: { reason },
  }).then(appChanged)
}

export function rotateKeys(appId: string, reason: string) {
  return apiFetch<null>(`/apps/${encodeURIComponent(appId)}/keys/rotate`, {
    method: 'POST',
    body: { reason },
  })
}

/**
 * 签名密钥列表与密钥配置版本（409/未知结果后的权威核对入口）。
 * 时间为 Unix 秒；返回的是密钥元数据，绝不包含私钥材料。
 */
export function listSigningKeys(appId: string) {
  return apiFetch<SigningKeyPage>(`/apps/${encodeURIComponent(appId)}/keys`)
}

/**
 * 紧急吊销指定签名密钥：立即从 JWKS 移除；吊销当前签名密钥时服务端同时生成继任者。
 * config_version 来自最近一次 GET /keys；409 或结果未知须重新读取后再决定。
 */
export async function revokeSigningKey(appId: string, keyId: string, input: VersionedReason) {
  const value = await apiFetch<unknown>(
    `/apps/${encodeURIComponent(appId)}/keys/${encodeURIComponent(keyId)}/revoke`,
    { method: 'POST', body: input },
  )
  const data = value as KeyRevoked | null
  if (
    !data ||
    data.key_id !== keyId ||
    typeof data.new_key_id !== 'string' ||
    data.new_key_id === keyId ||
    data.config_version !== input.config_version + 1
  )
    unknownAcknowledgement()
  return data
}

// ---- 客户端 ----

export function listClients(appId: string, options: ListOptions = {}) {
  return apiFetch<ListResponse<Client>>(`/apps/${encodeURIComponent(appId)}/clients`, {
    query: { cursor: options.cursor, limit: options.limit },
  })
}

/** 创建客户端；幂等键规则同 createApp。confidential 类型的 client_secret 仅此响应返回一次。 */
export function createClient(appId: string, input: ClientInput, idempotencyKey: string) {
  return apiFetch<CreatedClient>(`/apps/${encodeURIComponent(appId)}/clients`, {
    method: 'POST',
    body: input,
    idempotencyKey,
  })
}

export function getClient(appId: string, clientId: string) {
  return apiFetch<Client>(
    `/apps/${encodeURIComponent(appId)}/clients/${encodeURIComponent(clientId)}`,
  )
}

export function updateClient(appId: string, clientId: string, patch: ClientPatch) {
  return apiFetch<Client>(
    `/apps/${encodeURIComponent(appId)}/clients/${encodeURIComponent(clientId)}`,
    { method: 'PATCH', body: patch },
  )
}

/**
 * 轮换机密客户端密钥：响应中的 client_secret 仅此一次返回，GET 永不返回密钥。
 * grace_seconds=0 表示旧密钥立即失效；>0 表示旧密钥在宽限期内继续可用。
 * 无幂等回放：409 或结果未知时必须 GET 读回客户端，版本号上升即视为可能已生效。
 */
export async function rotateClientSecret(
  appId: string,
  clientId: string,
  input: ClientSecretRotateInput,
) {
  const value = await apiFetch<unknown>(
    `/apps/${encodeURIComponent(appId)}/clients/${encodeURIComponent(clientId)}/secret/rotate`,
    { method: 'POST', body: input },
  )
  const data = value as Partial<ClientSecretRotated> | null
  const current = data?.client
  if (
    !data ||
    !current ||
    current.app_id !== appId ||
    current.client_id !== clientId ||
    current.kind !== 'confidential' ||
    current.system === true ||
    current.config_version !== input.config_version + 1 ||
    typeof data.client_secret !== 'string' ||
    data.client_secret.length === 0 ||
    data.client_secret.length > 4096 ||
    typeof data.previous_secret_expires_at !== 'number' ||
    !Number.isSafeInteger(data.previous_secret_expires_at) ||
    data.previous_secret_expires_at < 0 ||
    current.previous_secret_expires_at !== data.previous_secret_expires_at
  ) {
    // The mutation may have committed. Never render a fabricated secret or
    // mistake an invalid acknowledgement for a definite failure to retry.
    throw new ApiError({
      status: 503,
      code: 'outcome_unknown',
      detail: '密钥轮换响应不完整，请读取客户端当前状态核对；系统不会自动重试。',
    })
  }
  return data as ClientSecretRotated
}

/** 显式撤销仍在宽限期内的旧客户端密钥；宽限期内服务端拒绝新的轮换，须先撤销或等待过期 */
export async function revokePreviousClientSecret(
  appId: string,
  clientId: string,
  input: VersionedReason,
) {
  const value = await apiFetch<unknown>(
    `/apps/${encodeURIComponent(appId)}/clients/${encodeURIComponent(clientId)}/secret/revoke-previous`,
    { method: 'POST', body: input },
  )
  const data = value as Client | null
  if (
    !data ||
    data.app_id !== appId ||
    data.client_id !== clientId ||
    data.kind !== 'confidential' ||
    data.config_version !== input.config_version + 1 ||
    data.previous_secret_expires_at !== 0
  )
    unknownAcknowledgement()
  return data
}

// ---- 用户 ----

export interface ListUsersOptions extends ListOptions {
  q?: string
}

export function listUsers(appId: string, options: ListUsersOptions = {}) {
  return apiFetch<ListResponse<User>>(`/apps/${encodeURIComponent(appId)}/users`, {
    query: { cursor: options.cursor, limit: options.limit, q: options.q },
  })
}

export function getUser(appId: string, userId: string) {
  return apiFetch<User>(`/apps/${encodeURIComponent(appId)}/users/${encodeURIComponent(userId)}`)
}

export function disableUser(appId: string, userId: string, reason: string) {
  return apiFetch<null>(
    `/apps/${encodeURIComponent(appId)}/users/${encodeURIComponent(userId)}/disable`,
    { method: 'POST', body: { reason } },
  )
}

/**
 * 恢复已禁用用户（已删除用户不可恢复）。携带读取到的 session_epoch 做并发校验；
 * 恢复后旧会话保持撤销，用户需重新登录。409/结果未知须 GET 读回后再决定。
 */
export async function enableUser(appId: string, userId: string, input: EnableUserInput) {
  const value = await apiFetch<unknown>(
    `/apps/${encodeURIComponent(appId)}/users/${encodeURIComponent(userId)}/enable`,
    { method: 'POST', body: input },
  )
  const data = value as User | null
  if (
    !data ||
    data.app_id !== appId ||
    data.id !== userId ||
    data.status !== 'active' ||
    data.session_epoch !== input.session_epoch + 1
  )
    unknownAcknowledgement()
  return data
}

/** 设置/撤销用户的固定 App 管理员角色（role 为空串即撤销）；仅 owner 可调用 */
export function setAppAdministrator(
  appId: string,
  userId: string,
  role: AdminRole | '',
  reason: string,
) {
  return apiFetch<null>(
    `/apps/${encodeURIComponent(appId)}/administrators/${encodeURIComponent(userId)}`,
    { method: 'PUT', body: { role, reason } },
  )
}

export function listUserSessions(appId: string, userId: string, options: ListOptions = {}) {
  return apiFetch<ListResponse<UserSession>>(
    `/apps/${encodeURIComponent(appId)}/users/${encodeURIComponent(userId)}/sessions`,
    { query: { cursor: options.cursor, limit: options.limit } },
  )
}

export function revokeUserSession(
  appId: string,
  userId: string,
  sessionId: string,
  reason: string,
) {
  return apiFetch<null>(
    `/apps/${encodeURIComponent(appId)}/users/${encodeURIComponent(userId)}/sessions/${encodeURIComponent(sessionId)}`,
    { method: 'DELETE', body: { reason } },
  )
}

// ---- 连接 ----

export function listConnections(appId: string, options: ListOptions = {}) {
  return apiFetch<ListResponse<Connection>>(`/apps/${encodeURIComponent(appId)}/connections`, {
    query: { cursor: options.cursor, limit: options.limit },
  })
}

/** 创建连接（201）。无幂等支持：结果未知时不允许盲目重试，须先核对列表。 */
export function createConnection(appId: string, input: ConnectionInput) {
  return apiFetch<Connection>(`/apps/${encodeURIComponent(appId)}/connections`, {
    method: 'POST',
    body: input,
  })
}

export function getConnection(appId: string, connectionId: string) {
  return apiFetch<Connection>(
    `/apps/${encodeURIComponent(appId)}/connections/${encodeURIComponent(connectionId)}`,
  )
}

/** 更新名称/状态或轮换密钥；config_version 过期返回 409，须重新读取最新配置后再提交 */
export function updateConnection(appId: string, connectionId: string, patch: ConnectionPatch) {
  return apiFetch<Connection>(
    `/apps/${encodeURIComponent(appId)}/connections/${encodeURIComponent(connectionId)}`,
    { method: 'PATCH', body: patch },
  )
}

/** 登录方式部署就绪状态与联邦回调地址；部署级密钥（如 AUTHOVA_SMS_URL/TOKEN）不经由控制台收集 */
export function getAuthenticationConfig(appId: string) {
  return apiFetch<AuthenticationConfig>(`/apps/${encodeURIComponent(appId)}/authentication-config`)
}

/**
 * 更新登录策略（完整七个开关 + config_version 乐观锁 + reason）。
 * 策略由服务端强制执行，本控制台不提供任何绕过；409/结果未知须读回权威状态后重新编辑。
 */
export async function updateAuthenticationPolicy(appId: string, patch: AuthenticationPolicyPatch) {
  const value = await apiFetch<unknown>(
    `/apps/${encodeURIComponent(appId)}/authentication-config`,
    { method: 'PATCH', body: patch },
  )
  const data = value as AuthenticationConfig | null
  if (
    !data ||
    data.config_version !== patch.config_version + 1 ||
    !data.policy ||
    Object.entries(patch.policy).some(
      ([key, value]) => data.policy?.[key as keyof typeof data.policy] !== value,
    )
  )
    unknownAcknowledgement()
  return data
}

// ---- 应用资源限额 ----

/** 应用资源限额（配置值与当前生效值）；生效并发可因服务端预留全局容量低于配置值 */
const RESOURCE_KEYS: (keyof ResourceLimits)[] = [
  'http_concurrency',
  'kdf_concurrency',
  'db_concurrency',
  'requests_per_second',
  'email_challenges_per_hour',
  'sms_challenges_per_hour',
]
function validResourceConfig(data: ResourceConfig | null): data is ResourceConfig {
  return (
    !!data &&
    Number.isSafeInteger(data.config_version) &&
    data.config_version > 0 &&
    data.concurrency_scope === 'per_process' &&
    data.counter_scope === 'shared_limiter' &&
    data.propagation_seconds === 5 &&
    !!data.limits &&
    !!data.effective_limits &&
    RESOURCE_KEYS.every(
      (key) =>
        Number.isSafeInteger(data.limits[key]) &&
        data.limits[key] >= 1 &&
        Number.isSafeInteger(data.effective_limits[key]) &&
        data.effective_limits[key] >= 1 &&
        data.effective_limits[key] <= data.limits[key],
    )
  )
}
export async function getResourcePolicy(appId: string) {
  const data = await apiFetch<ResourceConfig>(`/apps/${encodeURIComponent(appId)}/resource-policy`)
  if (!validResourceConfig(data))
    throw new ApiError({
      status: 502,
      code: 'invalid_response',
      detail: '资源预算响应不完整，请重新读取。',
    })
  return data
}

/**
 * 更新资源限额（完整六项 + config_version 乐观锁 + reason）。
 * 响应缺少关键确认字段视为结果未知：冻结并读回核对，绝不自动重放。
 */
export async function updateResourcePolicy(appId: string, patch: ResourcePolicyPatch) {
  const value = await apiFetch<unknown>(`/apps/${encodeURIComponent(appId)}/resource-policy`, {
    method: 'PATCH',
    body: patch,
  })
  const data = value as ResourceConfig | null
  const limitsOk =
    data?.limits &&
    Object.entries(patch.limits).every(
      ([key, value]) => data.limits[key as keyof typeof data.limits] === value,
    )
  const effectiveOk =
    data?.effective_limits &&
    Object.keys(patch.limits).every((key) => {
      const v = data.effective_limits[key as keyof ResourceLimits]
      return Number.isSafeInteger(v) && v >= 1
    })
  if (
    !validResourceConfig(data) ||
    data.config_version !== patch.config_version + 1 ||
    !limitsOk ||
    !effectiveOk ||
    data.concurrency_scope !== 'per_process' ||
    data.counter_scope !== 'shared_limiter' ||
    data.propagation_seconds !== 5
  )
    unknownAcknowledgement()
  return data
}

// ---- 审计 ----

export function listAudit(appId: string, options: ListOptions = {}) {
  return apiFetch<ListResponse<AuditEvent>>(`/apps/${encodeURIComponent(appId)}/audit`, {
    query: { cursor: options.cursor, limit: options.limit },
  })
}

// ---- 平台运营账号（App 级管理员不可见） ----

export function listPlatformOperators(options: ListOptions = {}) {
  return apiFetch<ListResponse<PlatformOperator>>('/platform/operators', {
    query: { cursor: options.cursor, limit: options.limit },
  })
}

export function createPlatformOperator(input: CreateOperatorInput) {
  return apiFetch<CreatedOperator>('/platform/operators', { method: 'POST', body: input })
}

export function updatePlatformOperator(operatorId: string, patch: OperatorPatch) {
  return apiFetch<OperatorUpdated>(`/platform/operators/${encodeURIComponent(operatorId)}`, {
    method: 'PATCH',
    body: patch,
  })
}
