package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

type AgentCreate struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Kind         string   `json:"kind"`
	Modes        []string `json:"allowed_modes"`
	Scopes       []string `json:"allowed_scopes"`
	Resources    []string `json:"allowed_resources"`
	RedirectURIs []string `json:"redirect_uris"`
	TokenTTL     int64    `json:"token_ttl_seconds"`
	GrantTTL     int64    `json:"grant_ttl_seconds"`
}
type CreatedAgent struct {
	Agent  domain.Agent `json:"agent"`
	Secret string       `json:"client_secret,omitempty"`
}
type AgentPatch struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Status      *string `json:"status"`
	Version     int64   `json:"config_version"`
	Reason      string  `json:"reason"`
}
type AgentInstallationCreate struct {
	AgentID          string   `json:"agent_id"`
	TenantID         string   `json:"tenant_id"`
	UserID           string   `json:"user_id"`
	ResponsibleParty string   `json:"responsible_party"`
	Resource         string   `json:"resource"`
	Scopes           []string `json:"allowed_scopes"`
	Modes            []string `json:"allowed_modes"`
	Expires          int64    `json:"expires_at"`
	Reason           string   `json:"reason"`
}
type AgentVersionedReason struct {
	Version int64  `json:"config_version"`
	Reason  string `json:"reason"`
}
type AgentInstallationPatch struct {
	Status  string `json:"status"`
	Version int64  `json:"config_version"`
	Reason  string `json:"reason"`
}

func validAgentReason(reason string) bool {
	return strings.TrimSpace(reason) != "" && len(reason) <= 500
}
func agentOwner(o domain.Operator) error {
	if o.Role != "owner" {
		return domain.ErrForbidden
	}
	return nil
}
func validAgentModes(modes []string) bool {
	if len(modes) < 1 || len(modes) > 2 {
		return false
	}
	seen := map[string]bool{}
	for _, m := range modes {
		if (m != domain.AgentDelegated && m != domain.AgentAutonomous) || seen[m] {
			return false
		}
		seen[m] = true
	}
	return true
}
func validAgentScopes(scopes []string) bool {
	if len(scopes) < 1 || len(scopes) > 27 {
		return false
	}
	seen := map[string]bool{}
	for _, s := range scopes {
		if len(s) < 1 || len(s) > 128 || seen[s] || slices.Contains([]string{"openid", "profile", "email", "phone", "offline_access"}, s) {
			return false
		}
		for _, c := range s {
			if c < 0x21 || c > 0x7e || c == '"' || c == '\\' {
				return false
			}
		}
		seen[s] = true
	}
	return true
}
func subset(values, ceiling []string) bool {
	for _, v := range values {
		if !slices.Contains(ceiling, v) {
			return false
		}
	}
	return true
}
func validAgentIdentifier(id string) bool {
	if len(id) > 128 || strings.TrimSpace(id) != id {
		return false
	}
	for _, c := range id {
		if c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}
func (s *Service) validAgentResource(raw string) bool {
	if len(raw) > 2048 {
		return false
	}
	u, e := url.Parse(raw)
	if e != nil || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return false
	}
	return (u.Scheme == "urn" && u.Opaque != "") || (u.Host != "" && (u.Scheme == "https" || (s.Insecure && u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))))
}
func (s *Service) CreateAgent(ctx context.Context, token string, scope domain.Scope, in AgentCreate, key string) (out CreatedAgent, err error) {
	if !ValidAgentOperationKey(key) || strings.TrimSpace(in.Name) == "" || len(in.Name) > 200 || len(in.Description) > 2000 || !validAgentModes(in.Modes) || !validAgentScopes(in.Scopes) || len(in.Resources) < 1 || len(in.Resources) > 16 || in.TokenTTL < 30 || in.TokenTTL > 300 || in.GrantTTL < 300 || in.GrantTTL > 2592000 {
		return out, domain.ErrInvalid
	}
	for i, r := range in.Resources {
		if !s.validAgentResource(r) || slices.Contains(in.Resources[:i], r) {
			return out, domain.ErrInvalid
		}
	}
	grants := []string{}
	scopes := append([]string{}, in.Scopes...)
	if slices.Contains(in.Modes, domain.AgentDelegated) {
		if len(in.RedirectURIs) == 0 {
			return out, domain.ErrInvalid
		}
		grants = append(grants, "authorization_code", "refresh_token")
		scopes = append(scopes, "openid", "offline_access")
	}
	if slices.Contains(in.Modes, domain.AgentAutonomous) {
		if in.Kind != "confidential" {
			return out, domain.ErrInvalid
		}
		grants = append(grants, "client_credentials")
	}
	c := domain.Client{ID: ID(), AppID: scope.ID(), ClientID: "ava_" + strings.ReplaceAll(ID(), "-", ""), Name: in.Name, Kind: in.Kind, Status: "active", RedirectURIs: in.RedirectURIs, Scopes: scopes, Audiences: in.Resources, IntrospectionAudiences: []string{}, Grants: grants, Version: 1, CreatedAt: s.Now().UTC()}
	if c.RedirectURIs == nil {
		c.RedirectURIs = []string{}
	}
	if !validClient(c, s.Insecure) {
		return out, domain.ErrInvalid
	}
	if c.Kind == "confidential" {
		out.Secret = secure.Random(32)
		c.SecretDigest = s.Digest(scope.ID(), "client-secret", out.Secret)
	}
	out.Agent = domain.Agent{ID: ID(), AppID: scope.ID(), ClientID: c.ClientID, Name: in.Name, Description: in.Description, Kind: in.Kind, Status: "active", Modes: in.Modes, Scopes: in.Scopes, Resources: in.Resources, TokenTTL: in.TokenTTL, GrantTTL: in.GrantTTL, Version: 1, CreatedAt: s.Now().UTC()}
	fp := s.Digest(scope.ID(), "idem-agent", mustJSON(in))
	err = s.AppManage(ctx, token, scope, true, false, func(t ports.AppTx, o domain.Operator) error {
		if e := agentOwner(o); e != nil {
			return e
		}
		idem := s.Digest(scope.ID(), "idempotency-actor", o.ID+":"+key)
		raw, e := t.Idempotency(idem, fp)
		if e == nil {
			return json.Unmarshal(raw, &out)
		}
		if !errors.Is(e, domain.ErrNotFound) {
			return e
		}
		if e = t.PutClient(c); e != nil {
			return e
		}
		if e = t.PutAgent(out.Agent); e != nil {
			return e
		}
		if e = t.Audit(audit(scope.ID(), o.ID, "agent.create", out.Agent.ID)); e != nil {
			return e
		}
		raw, e = json.Marshal(out)
		if e != nil {
			return e
		}
		return t.PutIdempotency(idem, fp, raw, s.Now().Add(10*time.Minute).Unix())
	})
	if err != nil {
		out.Secret = ""
	}
	return
}
func (s *Service) UpdateAgent(t ports.AppTx, o domain.Operator, id string, in AgentPatch) (domain.Agent, error) {
	if e := agentOwner(o); e != nil {
		return domain.Agent{}, e
	}
	if !validAgentReason(in.Reason) {
		return domain.Agent{}, domain.ErrInvalid
	}
	a, e := t.Agent(id, true)
	if e != nil {
		return a, e
	}
	if in.Version != a.Version {
		return a, domain.ErrConflict
	}
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" || len(*in.Name) > 200 {
			return a, domain.ErrInvalid
		}
		a.Name = *in.Name
	}
	if in.Description != nil {
		if len(*in.Description) > 2000 {
			return a, domain.ErrInvalid
		}
		a.Description = *in.Description
	}
	if in.Status != nil {
		if *in.Status != "active" && *in.Status != "disabled" {
			return a, domain.ErrInvalid
		}
		a.Status = *in.Status
	}
	a.Version++
	if e = t.PutAgent(a); e != nil {
		return a, e
	}
	return a, t.Audit(audit(a.AppID, o.ID, "agent.update", a.ID, in.Reason))
}
func (s *Service) CreateAgentInstallation(ctx context.Context, token string, scope domain.Scope, in AgentInstallationCreate, key string) (out domain.AgentInstallation, err error) {
	if !ValidAgentOperationKey(key) || !validAgentReason(in.Reason) || !validAgentModes(in.Modes) || !validAgentScopes(in.Scopes) || !validAgentIdentifier(in.TenantID) || strings.TrimSpace(in.ResponsibleParty) == "" || len(in.ResponsibleParty) > 200 || in.Expires < s.Now().Add(5*time.Minute).Unix() || in.Expires > s.Now().Add(365*24*time.Hour).Unix() {
		return out, domain.ErrInvalid
	}
	// No self-reported membership: the trusted provisioner pins a user for each
	// tenant-scoped delegated installation. App-wide consent may omit a user.
	if in.TenantID != "" && slices.Contains(in.Modes, domain.AgentDelegated) && in.UserID == "" {
		return out, domain.ErrInvalid
	}
	fp := s.Digest(scope.ID(), "idem-agent-installation", mustJSON(in))
	err = s.AppManage(ctx, token, scope, true, true, func(t ports.AppTx, o domain.Operator) error {
		if e := agentOwner(o); e != nil {
			return e
		}
		idem := s.Digest(scope.ID(), "idempotency-actor", o.ID+":"+key)
		raw, e := t.Idempotency(idem, fp)
		if e == nil {
			return json.Unmarshal(raw, &out)
		}
		if !errors.Is(e, domain.ErrNotFound) {
			return e
		}
		// Match delegated issuance's User -> Agent lock order. The installation's
		// composite User FK also takes a row lock; taking Agent first can deadlock.
		if in.UserID != "" {
			u, e := t.User(in.UserID, true)
			if e != nil {
				return e
			}
			if u.Status != "active" {
				return domain.ErrForbidden
			}
		}
		a, e := t.SharedAgent(in.AgentID)
		if e != nil {
			return e
		}
		if a.Status != "active" || !subset(in.Modes, a.Modes) || !subset(in.Scopes, a.Scopes) || !slices.Contains(a.Resources, in.Resource) {
			return domain.ErrForbidden
		}
		if in.UserID != "" {
			u, e := t.User(in.UserID, false)
			if e != nil {
				return e
			}
			if u.Status != "active" {
				return domain.ErrForbidden
			}
		}
		out = domain.AgentInstallation{ID: ID(), AppID: scope.ID(), AgentID: a.ID, TenantID: in.TenantID, UserID: in.UserID, ResponsibleParty: in.ResponsibleParty, Resource: in.Resource, Scopes: in.Scopes, Modes: in.Modes, Status: "active", Version: 1, CreatedAt: s.Now().UTC(), Expires: in.Expires}
		if slices.Contains(in.Modes, domain.AgentAutonomous) {
			out.AutonomousGrantID = ID()
		}
		if e = t.PutAgentInstallation(out); e != nil {
			return e
		}
		if out.AutonomousGrantID != "" {
			g := s.newAgentGrant(a, out, domain.AgentAutonomous, "", "", out.AutonomousGrantID)
			if e = t.PutAgentGrant(g); e != nil {
				return e
			}
			if e = t.Audit(agentGrantAudit(g, o.ID, "agent.grant.create", in.Reason)); e != nil {
				return e
			}
		}
		if e = t.Audit(audit(scope.ID(), o.ID, "agent.installation.create", out.ID, in.Reason)); e != nil {
			return e
		}
		raw, e = json.Marshal(out)
		if e != nil {
			return e
		}
		return t.PutIdempotency(idem, fp, raw, s.Now().Add(10*time.Minute).Unix())
	})
	return
}
func (s *Service) newAgentGrant(a domain.Agent, i domain.AgentInstallation, mode, user, session, id string) domain.AgentGrant {
	return domain.AgentGrant{ID: id, AppID: i.AppID, AgentID: a.ID, InstallationID: i.ID, ClientID: a.ClientID, UserID: user, TenantID: i.TenantID, Mode: mode, Resource: i.Resource, Scopes: append([]string{}, i.Scopes...), Status: "active", Version: 1, CreatedAt: s.Now().UTC(), Expires: min(i.Expires, s.Now().Unix()+a.GrantTTL), SessionID: session}
}
func agentGrantAudit(g domain.AgentGrant, actor, action, reason string) domain.Audit {
	// These are identifiers and consent metadata, never tokens or proof material.
	return audit(g.AppID, actor, action, g.ID, mustJSON(struct {
		AgentID        string `json:"agent_id"`
		InstallationID string `json:"installation_id"`
		UserID         string `json:"user_id"`
		TenantID       string `json:"tenant_id"`
		Mode           string `json:"mode"`
		Reason         string `json:"reason"`
	}{g.AgentID, g.InstallationID, g.UserID, g.TenantID, g.Mode, reason}))
}
func (s *Service) UpdateAgentInstallation(t ports.AppTx, o domain.Operator, id string, in AgentInstallationPatch) (domain.AgentInstallation, error) {
	if e := agentOwner(o); e != nil {
		return domain.AgentInstallation{}, e
	}
	if !validAgentReason(in.Reason) || (in.Status != "active" && in.Status != "disabled") {
		return domain.AgentInstallation{}, domain.ErrInvalid
	}
	current, e := t.AgentInstallation(id, false)
	if e != nil {
		return current, e
	}
	if _, e = t.SharedAgent(current.AgentID); e != nil {
		return current, e
	}
	current, e = t.AgentInstallation(id, true)
	if e != nil {
		return current, e
	}
	if current.Version != in.Version {
		return current, domain.ErrConflict
	}
	current.Status = in.Status
	current.Version++
	if e = t.PutAgentInstallation(current); e != nil {
		return current, e
	}
	return current, t.Audit(audit(current.AppID, o.ID, "agent.installation.update", id, in.Reason))
}
func (s *Service) RenewAutonomousAgent(t ports.AppTx, o domain.Operator, id string, in AgentVersionedReason) (domain.AgentInstallation, error) {
	if e := agentOwner(o); e != nil {
		return domain.AgentInstallation{}, e
	}
	if !validAgentReason(in.Reason) {
		return domain.AgentInstallation{}, domain.ErrInvalid
	}
	i, e := t.AgentInstallation(id, false)
	if e != nil {
		return i, e
	}
	a, e := t.SharedAgent(i.AgentID)
	if e != nil {
		return i, e
	}
	i, e = t.AgentInstallation(id, true)
	if e != nil {
		return i, e
	}
	if i.Version != in.Version {
		return i, domain.ErrConflict
	}
	if a.Status != "active" || i.Status != "active" || i.Expires <= s.Now().Unix() || !slices.Contains(i.Modes, domain.AgentAutonomous) {
		return i, domain.ErrForbidden
	}
	if i.AutonomousGrantID != "" {
		g, e := t.AgentGrant(i.AutonomousGrantID, true)
		if e != nil {
			return i, e
		}
		if g.Status == "active" {
			g.Status = "revoked"
			g.RevokedAt = s.Now().Unix()
			g.Version++
			if e = t.PutAgentGrant(g); e != nil {
				return i, e
			}
			if e = t.Audit(agentGrantAudit(g, o.ID, "agent.grant.revoke", in.Reason)); e != nil {
				return i, e
			}
		}
	}
	i.AutonomousGrantID = ID()
	i.Version++
	if e = t.PutAgentInstallation(i); e != nil {
		return i, e
	}
	g := s.newAgentGrant(a, i, domain.AgentAutonomous, "", "", i.AutonomousGrantID)
	if e = t.PutAgentGrant(g); e != nil {
		return i, e
	}
	return i, t.Audit(agentGrantAudit(g, o.ID, "agent.grant.create", in.Reason))
}
func (s *Service) RevokeAgentGrant(t ports.AppTx, actor, id string, in AgentVersionedReason, user string) (domain.AgentGrant, error) {
	if !validAgentReason(in.Reason) {
		return domain.AgentGrant{}, domain.ErrInvalid
	}
	g, e := t.AgentGrant(id, false)
	if e != nil {
		return g, e
	}
	if user != "" && g.UserID != user {
		return domain.AgentGrant{}, domain.ErrNotFound
	}
	if _, e = t.SharedAgent(g.AgentID); e != nil {
		return g, e
	}
	if _, e = t.SharedAgentInstallation(g.InstallationID); e != nil {
		return g, e
	}
	g, e = t.AgentGrant(id, true)
	if e != nil {
		return g, e
	}
	if g.Version != in.Version {
		return g, domain.ErrConflict
	}
	if g.Status != "active" {
		return g, domain.ErrConflict
	}
	g.Status = "revoked"
	g.Version++
	g.RevokedAt = s.Now().Unix()
	if e = t.PutAgentGrant(g); e != nil {
		return g, e
	}
	return g, t.Audit(agentGrantAudit(g, actor, "agent.grant.revoke", in.Reason))
}

// AgentContext validates the preregistered installation at every boundary.
// The bool distinguishes ordinary OAuth clients from Agent clients; failures
// must never fall back to issuing an ordinary unrestricted token.
func (s *Service) AgentContext(t ports.AppTx, client, installation, mode, user string, scopes []string, lock bool) (a domain.Agent, i domain.AgentInstallation, isAgent bool, err error) {
	a, err = t.AgentByClient(client)
	if errors.Is(err, domain.ErrNotFound) {
		if installation != "" {
			err = domain.ErrForbidden
		} else {
			err = nil
		}
		return
	}
	if err != nil {
		return a, i, true, err
	}
	isAgent = true
	if lock {
		a, err = t.SharedAgent(a.ID)
		if err != nil {
			return
		}
	}
	if a.Status != "active" || !slices.Contains(a.Modes, mode) || installation == "" {
		err = domain.ErrForbidden
		return
	}
	if lock {
		i, err = t.SharedAgentInstallation(installation)
	} else {
		i, err = t.AgentInstallation(installation, false)
	}
	if err != nil {
		return
	}
	if i.AgentID != a.ID || i.Status != "active" || i.Expires <= s.Now().Unix() || !slices.Contains(i.Modes, mode) {
		err = domain.ErrForbidden
		return
	}
	if user != "" && i.UserID != "" && i.UserID != user {
		err = domain.ErrForbidden
		return
	}
	business := AgentBusinessScopes(scopes)
	if !validAgentScopes(business) || !subset(business, a.Scopes) || !subset(business, i.Scopes) {
		err = domain.ErrForbidden
		return
	}
	return
}
func AgentBusinessScopes(scopes []string) []string {
	out := []string{}
	for _, v := range scopes {
		if !slices.Contains([]string{"openid", "profile", "email", "phone", "offline_access"}, v) {
			out = append(out, v)
		}
	}
	return out
}

// CompleteAgentConsent runs inside the locked login transaction after MFA and
// current authentication policy checks. A delegated grant cannot be created by
// a management API or by the Agent itself.
func (s *Service) CompleteAgentConsent(t ports.AppTx, scope domain.Scope, f *domain.Flow, session domain.Session) error {
	var request struct {
		AgentInstallationID string
		AgentGrantID        string
		AgentGrantExpires   int64
		Scopes              []string
	}
	if json.Unmarshal(f.Data, &request) != nil {
		return domain.ErrUnavailable
	}
	a, i, agent, e := s.AgentContext(t, f.ClientID, request.AgentInstallationID, domain.AgentDelegated, f.UserID, request.Scopes, true)
	if e != nil {
		return e
	}
	if !agent {
		return nil
	}
	g := s.newAgentGrant(a, i, domain.AgentDelegated, f.UserID, session.ID, ID())
	g.Scopes = AgentBusinessScopes(request.Scopes)
	if request.AgentGrantExpires <= s.Now().Unix() {
		return domain.ErrExpired
	}
	g.Expires = min(g.Expires, request.AgentGrantExpires)
	if e = t.PutAgentGrant(g); e != nil {
		return e
	}
	var data map[string]json.RawMessage
	if json.Unmarshal(f.Data, &data) != nil {
		return domain.ErrUnavailable
	}
	data["AgentGrantID"], _ = json.Marshal(g.ID)
	f.Data, e = json.Marshal(data)
	if e != nil {
		return e
	}
	return t.Audit(agentGrantAudit(g, f.UserID, "agent.grant.consent", "explicit_browser_consent"))
}

// AgentIssuance bounds every new access/refresh issuance by a current grant.
func (s *Service) AgentIssuance(t ports.AppTx, tok *domain.Token, installation, grantID, mode string) (*domain.AgentTokenBinding, error) {
	a, i, agent, e := s.AgentContext(t, tok.ClientID, installation, mode, tok.UserID, tok.Scopes, true)
	if e != nil {
		return nil, e
	}
	if !agent {
		return nil, nil
	}
	if mode == domain.AgentAutonomous {
		grantID = i.AutonomousGrantID
	}
	if grantID == "" {
		return nil, domain.ErrForbidden
	}
	g, e := t.SharedAgentGrant(grantID)
	if e != nil {
		return nil, e
	}
	if g.Status != "active" || g.Expires <= s.Now().Unix() || g.AgentID != a.ID || g.InstallationID != i.ID || g.ClientID != tok.ClientID || g.UserID != tok.UserID || g.Mode != mode || g.TenantID != i.TenantID || g.Resource != i.Resource || (mode == domain.AgentDelegated && (g.SessionID != tok.SessionID || g.UserID == "")) || !subset(AgentBusinessScopes(tok.Scopes), g.Scopes) {
		return nil, domain.ErrForbidden
	}
	tok.Expires = min(tok.Expires, s.Now().Unix()+a.TokenTTL, g.Expires, i.Expires)
	tok.Audiences = []string{i.Resource}
	return &domain.AgentTokenBinding{TokenID: tok.ID, AgentID: a.ID, InstallationID: i.ID, GrantID: g.ID, TenantID: i.TenantID, Mode: mode, UserID: g.UserID, AgentVersion: a.Version, InstallationVersion: i.Version, GrantVersion: g.Version}, nil
}

// agentClientManagement closes the legacy Client API route around Agent owner
// controls. The caller is the current operator verified by AppManage.
func agentClientManagement(t interface {
	AgentByClient(string) (domain.Agent, error)
}, actor domain.Operator, clientID string) (*domain.Agent, error) {
	a, e := t.AgentByClient(clientID)
	if errors.Is(e, domain.ErrNotFound) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if actor.Role != "owner" {
		return nil, domain.ErrForbidden
	}
	return &a, nil
}
func agentClientWithinCeiling(a domain.Agent, c domain.Client) bool {
	allowedScopes := append(append([]string{}, a.Scopes...), "openid", "offline_access")
	allowedGrants := []string{}
	if slices.Contains(a.Modes, domain.AgentDelegated) {
		allowedGrants = append(allowedGrants, "authorization_code", "refresh_token")
	}
	if slices.Contains(a.Modes, domain.AgentAutonomous) {
		allowedGrants = append(allowedGrants, "client_credentials")
	}
	return len(c.IntrospectionAudiences) == 0 && subset(c.Scopes, allowedScopes) && subset(c.Audiences, a.Resources) && subset(c.Grants, allowedGrants)
}

// ValidAgentOperationKey keeps the create/readback identifier URI-safe.
func ValidAgentOperationKey(key string) bool {
	if len(key) < 8 || len(key) > 128 {
		return false
	}
	for _, c := range key {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~') {
			return false
		}
	}
	return true
}
