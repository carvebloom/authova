import type { Client, ClientInput, ClientKind } from '../../api/types'
import { splitList } from '../../shared/utils/format'
import type { PendingCreate } from '../../shared/utils/idempotency'
export interface ClientFormState {
  status: string
  name: string
  kind: ClientKind
  redirectUris: string
  scopes: string
  audiences: string
  introspectionAudiences: string
  grantTypes: string
}

export function stateFromClient(client: Client | null): ClientFormState {
  return {
    status: client?.status ?? 'active',
    name: client?.name ?? '',
    kind: client?.kind === 'confidential' ? 'confidential' : 'public',
    redirectUris: client ? client.redirect_uris.join('\n') : '',
    scopes: client ? client.allowed_scopes.join(' ') : 'openid profile email',
    audiences: client ? client.allowed_audiences.join(' ') : '',
    introspectionAudiences: client ? client.allowed_introspection_audiences.join(' ') : '',
    grantTypes: client ? client.grant_types.join(' ') : 'authorization_code',
  }
}

export function stateFromPending(pending: PendingCreate<ClientInput>): ClientFormState {
  const p = pending.payload
  return {
    status: 'active',
    name: p.name,
    kind: p.kind,
    redirectUris: p.redirect_uris.join('\n'),
    scopes: p.allowed_scopes.join(' '),
    audiences: p.allowed_audiences.join(' '),
    introspectionAudiences: p.allowed_introspection_audiences.join(' '),
    grantTypes: p.grant_types.join(' '),
  }
}

export function payloadFromForm(form: ClientFormState): ClientInput {
  return {
    name: form.name.trim(),
    kind: form.kind,
    redirect_uris: form.redirectUris
      .split('\n')
      .map((s) => s.trim())
      .filter(Boolean),
    allowed_scopes: splitList(form.scopes),
    allowed_audiences: splitList(form.audiences),
    allowed_introspection_audiences: splitList(form.introspectionAudiences),
    grant_types: splitList(form.grantTypes),
  }
}

/** 旧客户端密钥是否仍在宽限期内（previous_secret_expires_at 为 Unix 秒，0 表示无） */
export function previousSecretLive(expiresAt: number, nowMs = Date.now()): boolean {
  return expiresAt * 1000 > nowMs
}
