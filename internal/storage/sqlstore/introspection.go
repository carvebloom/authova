package sqlstore

import (
	"database/sql"
	"time"

	"github.com/carvebloom/authova/internal/domain"
)

// IntrospectionState reads the authenticated resource configuration and all token
// liveness facts in one primary statement, never a cached positive decision.
func (t *tx) IntrospectionState(client, id string) (domain.Client, domain.TokenState, bool, error) {
	now := time.Now().Unix()
	return t.scanIntrospection(t.one(introspectionQuery, now, now, now, id, t.app, client))
}

type introspectionRow interface{ Scan(...any) error }

func (t *tx) scanIntrospection(row introspectionRow) (domain.Client, domain.TokenState, bool, error) {
	var requester domain.Client
	var token domain.TokenState
	var clientData, sessionData []byte
	var idValue, issuedClient, userID, sessionID sql.NullString
	var agentID, installationID, grantID, tenantID, mode, agentUser sql.NullString
	var agentVersion, installationVersion, grantVersion sql.NullInt64
	var expiry sql.NullInt64
	var clientStatus string
	var active, mfa int
	e := row.Scan(&clientData, &clientStatus, &idValue, &issuedClient, &userID, &sessionID, &expiry, &mfa, &token.KeyEpoch, &agentID, &installationID, &grantID, &tenantID, &mode, &agentUser, &agentVersion, &installationVersion, &grantVersion, &active, &sessionData)
	if e != nil {
		return requester, token, false, classify(e)
	}
	if requester, e = t.introspectionClient(clientData); e != nil {
		return requester, token, false, e
	}
	requester.Status = clientStatus
	token.ID, token.ClientID, token.UserID, token.SessionID, token.Expires = idValue.String, issuedClient.String, userID.String, sessionID.String, expiry.Int64
	token.Subject = token.UserID
	if token.Subject == "" {
		token.Subject = token.ClientID
	}
	if agentID.Valid {
		token.Agent = &domain.AgentTokenBinding{TokenID: token.ID, AgentID: agentID.String, InstallationID: installationID.String, GrantID: grantID.String, TenantID: tenantID.String, Mode: mode.String, UserID: agentUser.String, AgentVersion: agentVersion.Int64, InstallationVersion: installationVersion.Int64, GrantVersion: grantVersion.Int64}
	}
	token.RequiresMFA = mfa == 1
	if active == 1 {
		ok, err := t.tokenMFA(token.RequiresMFA, token.UserID, token.SessionID, sessionData)
		if err != nil {
			return requester, token, false, err
		}
		if !ok {
			active = 0
		}
	}
	return requester, token, active == 1, nil
}

const introspectionQuery = `SELECT c.payload,c.status,x.id,x.client_id,x.user_id,x.session_id,x.expires_at,a.require_mfa,a.key_epoch,ab.agent_id,ab.installation_id,ab.grant_id,ab.tenant_id,ab.mode,ab.user_id,ab.agent_version,ab.installation_version,ab.grant_version,
 CASE WHEN x.id IS NOT NULL AND x.revoked=0 AND x.expires_at>? AND a.status='active' AND issued.status='active'
 AND (x.user_id IS NULL OR (u.status='active' AND s.status='active' AND s.expires_at>? AND s.session_epoch=u.session_epoch AND s.user_id=u.id))
	AND (x.family_id IS NULL OR (f.revoked=0 AND f.expires_at>? AND f.client_id=x.client_id AND f.session_id=x.session_id)) AND ` + agentLiveness + ` THEN 1 ELSE 0 END,
 CASE WHEN a.require_mfa=1 AND x.user_id IS NOT NULL THEN s.payload ELSE NULL END
 FROM clients c JOIN apps a ON a.id=c.app_id
 LEFT JOIN access_tokens x ON x.app_id=c.app_id AND x.id=?
 LEFT JOIN clients issued ON issued.app_id=x.app_id AND issued.client_id=x.client_id
 LEFT JOIN users u ON u.app_id=x.app_id AND u.id=x.user_id
 LEFT JOIN sessions s ON s.app_id=x.app_id AND s.id=x.session_id
 LEFT JOIN refresh_families f ON f.app_id=x.app_id AND f.id=x.family_id
` + agentJoins + ` WHERE c.app_id=? AND c.client_id=?`

// All predicates are fixed SQL constants. Agent configuration and revocation
// share the primary statement used for ordinary token liveness, with no positive
// authorization cache. Every join repeats the App key.
const agentJoins = ` LEFT JOIN agents ag ON ag.app_id=x.app_id AND ag.client_id=x.client_id
 LEFT JOIN agent_tokens ab ON ab.app_id=x.app_id AND ab.token_id=x.id
 LEFT JOIN agent_installations ai ON ai.app_id=ab.app_id AND ai.id=ab.installation_id
 LEFT JOIN agent_grants gg ON gg.app_id=ab.app_id AND gg.id=ab.grant_id `
const agentLiveness = `(ag.id IS NULL OR (ab.token_id IS NOT NULL AND ag.status='active'
 AND ai.status='active' AND gg.status='active'
 AND ai.expires_at>=x.expires_at AND gg.expires_at>=x.expires_at
 AND ab.tenant_id=ai.tenant_id AND ab.agent_id=ag.id AND ai.agent_id=ag.id AND gg.agent_id=ag.id AND gg.installation_id=ai.id
 AND ab.agent_version=ag.config_version AND ab.installation_version=ai.config_version AND ab.grant_version=gg.config_version
 AND ((x.user_id IS NOT NULL AND gg.user_id=x.user_id AND ab.user_id=x.user_id AND ab.mode='delegated')
 OR (x.user_id IS NULL AND gg.user_id IS NULL AND ab.user_id='' AND ab.mode='autonomous'))))`

// tokenMFA is shared by full-token and projection readers. Proof bytes must come
// from the same primary statement as liveness; do not add a second snapshot read.
func (t *tx) tokenMFA(required bool, userID, sessionID string, data []byte) (bool, error) {
	if !required || userID == "" {
		return true, nil
	}
	var session domain.Session
	if err := t.open("session", data, &session); err != nil {
		return false, err
	}
	return session.ID == sessionID && session.UserID == userID && session.MFATime > 0, nil
}
