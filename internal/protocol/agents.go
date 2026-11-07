package protocol

import (
	"context"
	"errors"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/zitadel/oidc/v3/pkg/oidc"
)

type agentActor struct {
	Subject  string `json:"sub"`
	ClientID string `json:"client_id"`
}

func addAgentClaims(claims map[string]any, b domain.AgentTokenBinding) {
	claims["agent_id"] = b.AgentID
	claims["installation_id"] = b.InstallationID
	claims["grant_id"] = b.GrantID
	claims["agent_mode"] = b.Mode
	if b.TenantID != "" {
		claims["tenant_id"] = b.TenantID
	}
	if b.Mode == domain.AgentDelegated {
		claims["act"] = agentActor{Subject: b.AgentID, ClientID: stringClaim(claims["client_id"])}
	}
}
func stringClaim(v any) string { s, _ := v.(string); return s }
func agentClaimsMatch(c accessClaims, b *domain.AgentTokenBinding) bool {
	if b == nil {
		return c.AgentID == "" && c.InstallationID == "" && c.GrantID == "" && c.TenantID == "" && c.AgentMode == "" && c.Actor == nil
	}
	if c.AgentID != b.AgentID || c.InstallationID != b.InstallationID || c.GrantID != b.GrantID || c.TenantID != b.TenantID || c.AgentMode != b.Mode {
		return false
	}
	if b.Mode == domain.AgentDelegated {
		return c.Actor != nil && c.Actor.Subject == b.AgentID && c.Actor.ClientID == c.ClientID && b.UserID == c.Subject
	}
	return b.Mode == domain.AgentAutonomous && b.UserID == "" && c.Actor == nil
}
func agentInstallation(b *domain.AgentTokenBinding) string {
	if b == nil {
		return ""
	}
	return b.InstallationID
}
func agentGrant(b *domain.AgentTokenBinding) string {
	if b == nil {
		return ""
	}
	return b.GrantID
}
func (s *Storage) auditAgentIssuance(ctx context.Context, tok domain.Token) error {
	b := req(ctx).AgentBinding
	return req(ctx).Tx.Audit(domain.Audit{ID: application.ID(), AppID: req(ctx).App.ID, ActorID: b.AgentID, Action: "agent.token.issue", SubjectID: b.GrantID, Reason: "token_id=" + tok.ID, CreatedAt: time.Now().UTC()})
}

func agentProtocolError(ctx context.Context, err error) error {
	if errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrExpired) || errors.Is(err, domain.ErrInvalid) {
		return oidc.ErrInvalidGrant().WithDescription("Agent authorization is not valid")
	}
	return dependency(ctx, err)
}
