import { apiFetch, ApiError } from './client'
import type {
  Agent,
  AgentCreate,
  AgentPatch,
  CreatedAgent,
  AgentInstallation,
  AgentInstallationCreate,
  AgentInstallationPatch,
  AgentGrant,
  ListResponse,
  VersionedReason,
} from './types'

export interface AgentQuery {
  cursor?: string
  limit?: number
  agent_id?: string
  installation_id?: string
  user_id?: string
  tenant_id?: string
}
const base = (appId: string) => `/apps/${encodeURIComponent(appId)}`
const segment = (id: string) => encodeURIComponent(id)
function unknown(): never {
  throw new ApiError({
    status: 503,
    code: 'outcome_unknown',
    detail: '响应缺少状态确认，请重新读取后核对；不会自动重试。',
  })
}
function versioned<T extends { id: string; config_version: number }>(value: T, id?: string): T {
  if (
    !value ||
    typeof value.id !== 'string' ||
    !value.id ||
    !Number.isSafeInteger(value.config_version) ||
    value.config_version < 1 ||
    (id !== undefined && value.id !== id)
  )
    unknown()
  return value
}
export function listAgents(appId: string, query: AgentQuery = {}) {
  return apiFetch<ListResponse<Agent>>(`${base(appId)}/agents`, { query: { ...query } })
}
export function getAgent(appId: string, id: string) {
  return apiFetch<Agent>(`${base(appId)}/agents/${segment(id)}`).then((value) =>
    versioned(value, id),
  )
}
export async function createAgent(appId: string, input: AgentCreate, idempotencyKey: string) {
  const out = await apiFetch<CreatedAgent>(`${base(appId)}/agents`, {
    method: 'POST',
    body: input,
    idempotencyKey,
  })
  if (
    !out?.agent ||
    (input.kind === 'confidential' && (typeof out.client_secret !== 'string' || !out.client_secret))
  )
    unknown()
  versioned(out.agent)
  if (out.agent.app_id !== appId || !out.agent.client_id) unknown()
  return out
}
export async function updateAgent(appId: string, id: string, patch: AgentPatch) {
  const out = versioned(
    await apiFetch<Agent>(`${base(appId)}/agents/${segment(id)}`, { method: 'PATCH', body: patch }),
    id,
  )
  if (
    out.config_version !== patch.config_version + 1 ||
    (patch.status !== undefined && out.status !== patch.status)
  )
    unknown()
  return out
}
export function listAgentInstallations(appId: string, query: AgentQuery = {}) {
  return apiFetch<ListResponse<AgentInstallation>>(`${base(appId)}/agent-installations`, {
    query: { ...query },
  })
}
export function getAgentInstallation(appId: string, id: string) {
  return apiFetch<AgentInstallation>(`${base(appId)}/agent-installations/${segment(id)}`).then(
    (value) => versioned(value, id),
  )
}
export async function createAgentInstallation(
  appId: string,
  input: AgentInstallationCreate,
  idempotencyKey: string,
) {
  const out = versioned(
    await apiFetch<AgentInstallation>(`${base(appId)}/agent-installations`, {
      method: 'POST',
      body: input,
      idempotencyKey,
    }),
  )
  if (
    out.app_id !== appId ||
    out.agent_id !== input.agent_id ||
    out.resource !== input.resource ||
    out.tenant_id !== input.tenant_id ||
    out.user_id !== input.user_id ||
    out.status !== 'active'
  )
    unknown()
  return out
}
export async function updateAgentInstallation(
  appId: string,
  id: string,
  patch: AgentInstallationPatch,
) {
  const out = versioned(
    await apiFetch<AgentInstallation>(`${base(appId)}/agent-installations/${segment(id)}`, {
      method: 'PATCH',
      body: patch,
    }),
    id,
  )
  if (out.config_version !== patch.config_version + 1 || out.status !== patch.status) unknown()
  return out
}
export async function renewAutonomousAgent(appId: string, id: string, patch: VersionedReason) {
  const out = versioned(
    await apiFetch<AgentInstallation>(`${base(appId)}/agent-installations/${segment(id)}/renew`, {
      method: 'POST',
      body: patch,
    }),
    id,
  )
  if (out.config_version !== patch.config_version + 1 || !out.autonomous_grant_id) unknown()
  return out
}
export function listAgentGrants(appId: string, query: AgentQuery = {}) {
  return apiFetch<ListResponse<AgentGrant>>(`${base(appId)}/agent-grants`, { query: { ...query } })
}
export function getAgentGrant(appId: string, id: string) {
  return apiFetch<AgentGrant>(`${base(appId)}/agent-grants/${segment(id)}`).then((value) =>
    versioned(value, id),
  )
}
export async function revokeAgentGrant(appId: string, id: string, patch: VersionedReason) {
  const out = versioned(
    await apiFetch<AgentGrant>(`${base(appId)}/agent-grants/${segment(id)}/revoke`, {
      method: 'POST',
      body: patch,
    }),
    id,
  )
  if (
    out.config_version !== patch.config_version + 1 ||
    out.status !== 'revoked' ||
    out.revoked_at <= 0
  )
    unknown()
  return out
}
export function getAgentOperation(appId: string, key: string) {
  return apiFetch<{ kind: 'agent' | 'installation'; resource_id: string }>(
    `${base(appId)}/agent-operations/${segment(key)}`,
  )
}
