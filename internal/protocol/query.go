package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	jose "github.com/go-jose/go-jose/v4"
)

// tokenStateMatches is shared by introspection and read-only query access.
// The verified signature and current primary facts must describe the same
// principal, session, grant and expiration; a routing hint is never authority.
func tokenStateMatches(c accessClaims, t domain.TokenState) bool {
	return agentClaimsMatch(c, t.Agent) && t.ID == c.ID && t.Subject == c.Subject &&
		t.ClientID == c.ClientID && t.SessionID == c.SessionID && t.Expires == c.Expires &&
		((t.UserID != "" && c.Principal == "user") ||
			(t.UserID == "" && c.Principal == "client" && c.Subject == c.ClientID))
}

// ReadQuery authorizes a purpose-bound autonomous Agent, then reads data in
// the same App-scoped read transaction. No Console role or positive allow cache
// participates. Revocation completed before this request is observed on primary.
func (p *Provider) ReadQuery(ctx context.Context, scope domain.Scope, raw, kind string, read func(ports.AppTx, domain.QueryPrincipal) error) error {
	required, tenantAllowed := domain.QueryCapability(kind)
	if required == "" || read == nil {
		return domain.ErrInvalid
	}
	if len(raw) == 0 || len(raw) > 16384 || strings.ContainsAny(raw, " \t\r\n") {
		return domain.ErrUnauthenticated
	}
	signed, err := jose.ParseSigned(raw, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil || len(signed.Signatures) != 1 {
		return domain.ErrUnauthenticated
	}
	var hint accessClaims
	if json.Unmarshal(signed.UnsafePayloadWithoutVerification(), &hint) != nil || hint.ID == "" || hint.ClientID == "" || len(hint.ID) > 128 || len(hint.ClientID) > 128 {
		return domain.ErrUnauthenticated
	}
	return p.service.Store.App(ctx, scope, false, func(t ports.AppTx) error {
		a, err := t.App()
		if err != nil {
			return queryCredentialError(err)
		}
		client, token, live, err := t.IntrospectionState(hint.ClientID, hint.ID)
		if err != nil {
			return queryCredentialError(err)
		}
		if !live || a.ID != scope.ID() || client.AppID != a.ID || client.ClientID != hint.ClientID || client.Status != "active" || client.Kind != "confidential" {
			return domain.ErrUnauthenticated
		}
		request := WithTransaction(ctx, t, a, client.ClientID, "")
		claims, err := p.cachedVerify(request, signed, token.KeyEpoch)
		if err != nil {
			return err
		}
		if !tokenStateMatches(claims, token) || token.Agent == nil ||
			claims.AgentMode != domain.AgentAutonomous || token.Agent.UserID != "" ||
			claims.Principal != "client" || claims.Issued <= 0 || claims.Expires <= claims.Issued || claims.Expires-claims.Issued > 300 {
			return domain.ErrForbidden
		}
		if len(claims.Audience) != 1 || claims.Audience[0] != domain.QueryAudience(a.ID) {
			return domain.ErrForbidden
		}
		scopes := strings.Fields(claims.Scope)
		if !slices.Contains(scopes, required) || (claims.TenantID != "" && !tenantAllowed) {
			return domain.ErrForbidden
		}
		return read(t, domain.QueryPrincipal{AppID: a.ID, ClientID: claims.ClientID, AgentID: claims.AgentID, InstallationID: claims.InstallationID, GrantID: claims.GrantID, TenantID: claims.TenantID, Scopes: scopes, Expires: claims.Expires})
	})
}

func queryCredentialError(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return domain.ErrUnauthenticated
	}
	return err
}
