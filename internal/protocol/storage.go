package protocol

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/secure"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

type Storage struct{ Service *application.Service }

var _ op.Storage = (*Storage)(nil)

func (s *Storage) GetClientByClientID(ctx context.Context, id string) (op.Client, error) {
	c, e := req(ctx).Tx.Client(id)
	if e != nil {
		if !errors.Is(e, domain.ErrNotFound) {
			dependency(ctx, e)
		}
		return nil, e
	}
	a, e := req(ctx).Tx.App()
	if e != nil {
		return nil, dependency(ctx, e)
	}
	if c.Status != "active" || a.Status != "active" {
		return nil, domain.ErrForbidden
	}
	return &client{c, a, s.Service.Insecure}, nil
}
func (s *Storage) AuthorizeClientIDSecret(ctx context.Context, id, secret string) error {
	c, e := s.GetClientByClientID(ctx, id)
	if e != nil {
		return e
	}
	local := c.(*client)
	if !s.Service.ClientSecretMatches(req(ctx).App.ID, local.Client, secret) {
		return domain.ErrUnauthenticated
	}
	return nil
}
func (s *Storage) CreateAuthRequest(ctx context.Context, a *oidc.AuthRequest, user string) (op.AuthRequest, error) {
	c, e := req(ctx).Tx.Client(a.ClientID)
	if e != nil {
		return nil, e
	}
	if a.CodeChallenge == "" || a.CodeChallengeMethod != oidc.CodeChallengeMethodS256 || len(a.CodeChallenge) != 43 {
		return nil, oidc.ErrInvalidRequest().WithDescription("S256 PKCE required")
	}
	if len(a.State) > 512 || len(a.Nonce) > 512 {
		return nil, oidc.ErrInvalidRequest()
	}
	for _, sc := range a.Scopes {
		if !slices.Contains(c.Scopes, sc) {
			return nil, oidc.ErrInvalidScope()
		}
	}
	if slices.Contains(a.Prompt, oidc.PromptNone) {
		return nil, oidc.ErrLoginRequired()
	}
	if a.ResponseMode != "" && a.ResponseMode != oidc.ResponseModeQuery {
		return nil, oidc.ErrInvalidRequest()
	}
	aInfo, install, isAgent, agentErr := s.Service.AgentContext(req(ctx).Tx, a.ClientID, req(ctx).InstallationID, domain.AgentDelegated, "", a.Scopes, false)
	if agentErr != nil {
		if !errors.Is(agentErr, domain.ErrForbidden) && !errors.Is(agentErr, domain.ErrNotFound) {
			dependency(ctx, agentErr)
		}
		return nil, oidc.ErrInvalidRequest().WithDescription("Agent installation is not authorized")
	}
	if req(ctx).Resource != "" && (!isAgent || req(ctx).Resource != install.Resource) {
		return nil, oidc.ErrInvalidRequest().WithDescription("invalid target resource")
	}
	id := secure.Random(32)
	r := &Request{ID: id, ClientID: a.ClientID, RedirectURI: a.RedirectURI, State: a.State, Nonce: a.Nonce, Scopes: a.Scopes, Audiences: c.Audiences, CodeChallenge: a.CodeChallenge, CodeMethod: a.CodeChallengeMethod, ResponseMode: a.ResponseMode}
	if isAgent {
		r.AgentInstallationID = install.ID
		r.AgentGrantExpires = min(install.Expires, s.Service.Now().Unix()+aInfo.GrantTTL)
		r.Audiences = []string{install.Resource}
	}
	b, _ := json.Marshal(r)
	f := domain.Flow{ID: id, ClientID: a.ClientID, Data: b, Step: domain.StepChoose, Purpose: "authorize", Expires: time.Now().Add(10 * time.Minute).Unix()}
	if e = req(ctx).Tx.PutFlow(f); e != nil {
		return nil, dependency(ctx, e)
	}
	return r, nil
}
func (s *Storage) AuthRequestByID(ctx context.Context, id string) (op.AuthRequest, error) {
	f, e := req(ctx).Tx.Flow(id, false)
	if e != nil {
		return nil, e
	}
	if f.Expires <= time.Now().Unix() {
		return nil, domain.ErrExpired
	}
	if f.Completed && (f.Step != "complete" || f.Purpose != "authorize" || f.UserID == "" || f.SessionID == "") {
		return nil, domain.ErrForbidden
	}
	r := &Request{}
	if e = json.Unmarshal(f.Data, r); e != nil {
		return nil, dependency(ctx, e)
	}
	r.UserID, r.SessionID, r.Ready, r.AuthTime, r.MFATime, r.AMR = f.UserID, f.SessionID, f.Completed, f.AuthTime, f.MFATime, f.AMR
	return r, nil
}
func (s *Storage) AuthRequestByCode(ctx context.Context, code string) (op.AuthRequest, error) {
	c, e := req(ctx).Tx.Code(s.Service.Digest(req(ctx).App.ID, "code", code), true)
	if e != nil {
		return nil, e
	}
	if c.Used || c.Expires <= time.Now().Unix() {
		return nil, domain.ErrUnauthenticated
	}
	return s.AuthRequestByID(ctx, c.FlowID)
}
func (s *Storage) SaveAuthCode(ctx context.Context, id, code string) error {
	f, e := req(ctx).Tx.Flow(id, true)
	if e != nil {
		return e
	}
	if !f.Completed || f.Step != "complete" || f.Purpose != "authorize" || f.UserID == "" || f.SessionID == "" || f.Expires <= time.Now().Unix() {
		return domain.ErrForbidden
	}
	if !secure.Equal(f.CookieDigest, s.Service.Digest(req(ctx).App.ID, "flow-cookie", req(ctx).Cookie)) {
		return domain.ErrForbidden
	}
	return dependency(ctx, req(ctx).Tx.PutCode(domain.Code{Digest: s.Service.Digest(req(ctx).App.ID, "code", code), FlowID: id, Expires: time.Now().Add(time.Minute).Unix()}))
}
func (s *Storage) DeleteAuthRequest(ctx context.Context, id string) error {
	f, e := req(ctx).Tx.Flow(id, true)
	if e != nil {
		return e
	}
	f.Expires = time.Now().Unix()
	return dependency(ctx, req(ctx).Tx.PutFlow(f))
}
func (s *Storage) requestToken(ctx context.Context, r op.TokenRequest, family string) (domain.Token, error) {
	info := req(ctx)
	a, e := info.Tx.App()
	if e != nil {
		return domain.Token{}, e
	}
	if a.Status != "active" {
		return domain.Token{}, domain.ErrForbidden
	}
	tok := domain.Token{ID: application.ID(), Subject: r.GetSubject(), Scopes: r.GetScopes(), Audiences: r.GetAudience(), Created: time.Now().Unix(), Expires: time.Now().Add(5 * time.Minute).Unix(), FamilyID: family}
	switch v := r.(type) {
	case *Request:
		if v.UserID == "" || v.SessionID == "" {
			return tok, domain.ErrUnauthenticated
		}
		tok.ClientID = v.ClientID
		tok.UserID = v.UserID
		tok.SessionID = v.SessionID
		tok.AuthTime = v.AuthTime
		tok.AMR = v.AMR
	case *RefreshRequest:
		if v.Value.UserID == "" || v.Value.SessionID == "" {
			return tok, domain.ErrUnauthenticated
		}
		tok.ClientID = v.Value.ClientID
		tok.UserID = v.Value.UserID
		tok.SessionID = v.Value.SessionID
		tok.AuthTime = v.Value.AuthTime
		tok.AMR = v.Value.AMR
	case *machineRequest:
		tok.ClientID = v.client
	default:
		return tok, domain.ErrForbidden
	}
	c, e := info.Tx.Client(tok.ClientID)
	if e != nil {
		return tok, e
	}
	if c.Status != "active" {
		return tok, domain.ErrForbidden
	}
	for _, value := range tok.Scopes {
		if !slices.Contains(c.Scopes, value) {
			return tok, domain.ErrForbidden
		}
	}
	for _, value := range tok.Audiences {
		if !slices.Contains(c.Audiences, value) {
			return tok, domain.ErrForbidden
		}
	}

	if c.System {
		if tok.UserID == "" {
			return tok, domain.ErrForbidden
		}
		role, e := info.Tx.AdminRole(tok.UserID)
		if e != nil {
			return tok, dependency(ctx, e)
		}
		if role == "" {
			return tok, domain.ErrForbidden
		}
		session, e := info.Tx.Session(tok.SessionID, false)
		if e != nil {
			return tok, e
		}
		if session.MFATime == 0 {
			return tok, domain.ErrForbidden
		}
	}

	if tok.UserID != "" {
		u, e := info.Tx.User(tok.UserID, true)
		if e != nil {
			return tok, e
		}
		session, e := info.Tx.Session(tok.SessionID, false)
		if e != nil {
			return tok, e
		}
		if a.RequireMFA && session.MFATime == 0 {
			return tok, domain.ErrUnauthenticated
		}
		if tok.Subject != u.ID || session.UserID != u.ID || u.Status != "active" || session.Status != "active" || session.Epoch != u.Epoch || session.ExpiresAt.Unix() <= time.Now().Unix() {
			return tok, domain.ErrUnauthenticated
		}
		tok.Epoch = u.Epoch
	}
	installation, grantID, mode := "", "", domain.AgentAutonomous
	switch v := r.(type) {
	case *Request:
		installation, grantID, mode = v.AgentInstallationID, v.AgentGrantID, domain.AgentDelegated
	case *RefreshRequest:
		installation, grantID, mode = v.Value.AgentInstallationID, v.Value.AgentGrantID, domain.AgentDelegated
	case *machineRequest:
		installation = req(ctx).InstallationID
	}
	if info.InstallationID != "" && installation != info.InstallationID {
		return tok, oidc.ErrInvalidGrant().WithDescription("Agent installation mismatch")
	}
	binding, e := s.Service.AgentIssuance(info.Tx, &tok, installation, grantID, mode)
	if e != nil {
		return tok, agentProtocolError(ctx, e)
	}
	info.AgentBinding = binding
	if binding != nil && info.Resource != "" && (len(tok.Audiences) != 1 || tok.Audiences[0] != info.Resource) {
		return tok, oidc.ErrInvalidGrant().WithDescription("invalid Agent target resource")
	}
	return tok, nil
}
func (s *Storage) CreateAccessToken(ctx context.Context, r op.TokenRequest) (id string, exp time.Time, err error) {
	tok, e := s.requestToken(ctx, r, "")
	if e != nil {
		return "", exp, e
	}
	e = req(ctx).Tx.PutToken(tok)
	if e == nil && req(ctx).AgentBinding != nil {
		e = req(ctx).Tx.PutAgentToken(*req(ctx).AgentBinding)
		if e == nil {
			e = s.auditAgentIssuance(ctx, tok)
		}
	}
	return tok.ID, time.Unix(tok.Expires, 0), dependency(ctx, e)
}
func (s *Storage) CreateAccessAndRefreshTokens(ctx context.Context, r op.TokenRequest, current string) (id, newToken string, exp time.Time, err error) {
	t := req(ctx).Tx
	family := ""
	generation := int64(0)
	expires := time.Now().Add(30 * 24 * time.Hour).Unix()
	var previous *domain.Refresh
	if current != "" {
		old, e := t.Refresh(s.Service.Digest(req(ctx).App.ID, "refresh", current), true)
		if e != nil {
			return "", "", exp, e
		}
		if old.Used {
			return "", "", exp, domain.ErrUnauthenticated
		}
		family = old.FamilyID
		generation = old.Generation + 1
		expires = old.Expires
		previous = &old
	} else {
		family = application.ID()
	}
	tok, e := s.requestToken(ctx, r, family)
	if e != nil {
		return "", "", exp, e
	}
	// Parameter/authorization rejection must not consume a still-valid refresh
	// token. Actual reuse is handled separately by TokenRequestByRefreshToken.
	if previous != nil {
		previous.Used = true
		if e = t.PutRefresh(*previous); e != nil {
			return "", "", exp, dependency(ctx, e)
		}
	}
	if current == "" {
		if e = t.CreateFamily(family, tok.ClientID, tok.SessionID, expires); e != nil {
			return "", "", exp, dependency(ctx, e)
		}
	}
	if e = t.PutToken(tok); e != nil {
		return "", "", exp, dependency(ctx, e)
	}
	if req(ctx).AgentBinding != nil {
		if e = t.PutAgentToken(*req(ctx).AgentBinding); e != nil {
			return "", "", exp, dependency(ctx, e)
		}
		if e = s.auditAgentIssuance(ctx, tok); e != nil {
			return "", "", exp, dependency(ctx, e)
		}
	}
	newToken = secure.Random(32)
	data, _ := json.Marshal(&Request{AgentInstallationID: agentInstallation(req(ctx).AgentBinding), AgentGrantID: agentGrant(req(ctx).AgentBinding), ClientID: tok.ClientID, UserID: tok.UserID, SessionID: tok.SessionID, FamilyID: family, Scopes: tok.Scopes, Audiences: tok.Audiences, AMR: tok.AMR, AuthTime: tok.AuthTime, Epoch: tok.Epoch})
	refresh := domain.Refresh{Digest: s.Service.Digest(req(ctx).App.ID, "refresh", newToken), FamilyID: family, ClientID: tok.ClientID, Generation: generation, Expires: expires, Data: data}
	if e = t.PutRefresh(refresh); e != nil {
		return "", "", exp, dependency(ctx, e)
	}
	return tok.ID, newToken, time.Unix(tok.Expires, 0), nil
}
func (s *Storage) TokenRequestByRefreshToken(ctx context.Context, token string) (op.RefreshTokenRequest, error) {
	t := req(ctx).Tx
	digest := s.Service.Digest(req(ctx).App.ID, "refresh", token)
	r, e := t.Refresh(digest, false)
	if e != nil {
		return nil, e
	}
	if r.ClientID != req(ctx).ClientID {
		return nil, domain.ErrUnauthenticated
	}
	revoked, e := t.LockFamily(r.FamilyID)
	if e != nil {
		return nil, dependency(ctx, e)
	}
	r, e = t.Refresh(digest, true)
	if e != nil {
		return nil, e
	}
	if r.Used {
		if e = t.RevokeFamily(r.FamilyID); e != nil {
			return nil, dependency(ctx, e)
		}
		if e = t.Audit(domain.Audit{ID: application.ID(), AppID: req(ctx).App.ID, ActorID: r.ClientID, Action: "refresh.reuse", SubjectID: r.FamilyID, CreatedAt: time.Now().UTC()}); e != nil {
			return nil, dependency(ctx, e)
		}
		return nil, domain.ErrUnauthenticated
	}
	if revoked || r.Expires <= time.Now().Unix() {
		return nil, domain.ErrUnauthenticated
	}
	out := &RefreshRequest{}
	if e = json.Unmarshal(r.Data, &out.Value); e != nil {
		return nil, dependency(ctx, e)
	}
	return out, nil
}
func (s *Storage) TerminateSession(ctx context.Context, user, client string) error {
	return domain.ErrForbidden /* browser logout is CSRF protected by Authova */
}
func (s *Storage) RevokeToken(ctx context.Context, token, user, clientID string) *oidc.Error {
	t := req(ctx).Tx
	if user != "" {
		tok, e := t.Token(token)
		if errors.Is(e, domain.ErrNotFound) {
			return nil
		}
		if e != nil {
			return oidc.ErrServerError().WithParent(dependency(ctx, e))
		}
		if tok.ClientID != clientID {
			return nil
		}
		tok.Revoked = true
		if e = t.PutToken(tok); e != nil {
			return oidc.ErrServerError().WithParent(dependency(ctx, e))
		}
		return nil
	}
	r, e := t.Refresh(s.Service.Digest(req(ctx).App.ID, "refresh", token), false)
	if errors.Is(e, domain.ErrNotFound) {
		return nil
	}
	if e != nil {
		return oidc.ErrServerError().WithParent(dependency(ctx, e))
	}
	if r.ClientID != clientID {
		return nil
	}
	if e = t.RevokeFamily(r.FamilyID); e != nil {
		return oidc.ErrServerError().WithParent(dependency(ctx, e))
	}
	return nil
}
func (s *Storage) GetRefreshTokenInfo(ctx context.Context, clientID, token string) (string, string, error) {
	r, e := req(ctx).Tx.Refresh(s.Service.Digest(req(ctx).App.ID, "refresh", token), false)
	if e != nil && !errors.Is(e, domain.ErrNotFound) {
		return "", "", dependency(ctx, e)
	}
	if e != nil || r.ClientID != clientID {
		return "", "", op.ErrInvalidRefreshToken
	}
	request := Request{}
	if e = json.Unmarshal(r.Data, &request); e != nil {
		return "", "", e
	}
	return request.UserID, r.FamilyID, nil
}
func (s *Storage) SigningKey(ctx context.Context) (op.SigningKey, error) {
	keys, e := req(ctx).Tx.Keys()
	if e != nil {
		return nil, dependency(ctx, e)
	}
	for _, k := range keys {
		if k.Current && k.RevokedAt == 0 {
			priv, e := x509.ParsePKCS1PrivateKey(k.Private)
			if e != nil {
				return nil, e
			}
			return key{id: k.ID, private: priv}, nil
		}
	}
	return nil, dependency(ctx, domain.ErrUnavailable)
}
func (s *Storage) SignatureAlgorithms(context.Context) ([]jose.SignatureAlgorithm, error) {
	return []jose.SignatureAlgorithm{jose.RS256}, nil
}
func (s *Storage) KeySet(ctx context.Context) ([]op.Key, error) {
	keys, e := req(ctx).Tx.Keys()
	if e != nil {
		return nil, dependency(ctx, e)
	}
	out := []op.Key{}
	for _, k := range keys {
		if k.RevokedAt != 0 || (!k.Current && k.Retire < time.Now().Unix()) {
			continue
		}
		p, e := x509.ParsePKCS1PrivateKey(k.Private)
		if e != nil {
			return nil, e
		}
		out = append(out, key{id: k.ID, public: &p.PublicKey})
	}
	return out, nil
}
func (s *Storage) SetUserinfoFromScopes(context.Context, *oidc.UserInfo, string, string, []string) error {
	return nil
}
func fillUser(info *oidc.UserInfo, u domain.User, scopes []string) {
	info.Subject = u.ID
	if slices.Contains(scopes, "profile") {
		info.Name = u.DisplayName
	}
	if slices.Contains(scopes, "email") && u.Email != "" {
		info.Email = u.Email
		info.EmailVerified = true
	}
	if slices.Contains(scopes, "phone") && u.Phone != "" {
		info.PhoneNumber = u.Phone
		info.PhoneNumberVerified = true
	}
}
func (s *Storage) SetUserinfoFromRequest(ctx context.Context, info *oidc.UserInfo, r op.IDTokenRequest, scopes []string) error {
	u, e := req(ctx).Tx.User(r.GetSubject(), false)
	if e != nil {
		return dependency(ctx, e)
	}
	fillUser(info, u, scopes)
	return nil
}
func (s *Storage) SetUserinfoFromToken(ctx context.Context, info *oidc.UserInfo, id, subject, origin string) error {
	tok, e := req(ctx).Tx.ActiveToken(id)
	if e != nil {
		return e
	}
	if tok.UserID == "" || tok.Subject != subject || !slices.Contains(tok.Scopes, "openid") {
		return domain.ErrForbidden
	}
	u, e := req(ctx).Tx.User(tok.UserID, false)
	if e != nil {
		return e
	}
	fillUser(info, u, tok.Scopes)
	return nil
}
func (s *Storage) SetIntrospectionFromToken(context.Context, *oidc.IntrospectionResponse, string, string, string) error {
	return domain.ErrForbidden /* handled by Authova to preserve dependency errors */
}
func (s *Storage) GetPrivateClaimsFromScopes(context.Context, string, string, []string) (map[string]any, error) {
	return nil, nil
}
func (s *Storage) GetPrivateClaimsFromRequest(ctx context.Context, r op.TokenRequest, scopes []string) (map[string]any, error) {
	claims := map[string]any{"app_id": req(ctx).App.ID, "scope": strings.Join(r.GetScopes(), " "), "principal_type": "client"}
	switch v := r.(type) {
	case *Request:
		claims["sid"] = v.SessionID
		claims["principal_type"] = "user"
	case *RefreshRequest:
		claims["sid"] = v.Value.SessionID
		claims["principal_type"] = "user"
	}
	return claims, nil
}
func (s *Storage) GetKeyByIDAndClientID(context.Context, string, string) (*jose.JSONWebKey, error) {
	return nil, domain.ErrForbidden
}
func (s *Storage) ValidateJWTProfileScopes(context.Context, string, []string) ([]string, error) {
	return nil, domain.ErrForbidden
}
func (s *Storage) Health(ctx context.Context) error { return s.Service.Store.Health(ctx) }
func (s *Storage) ClientCredentials(ctx context.Context, id, secret string) (op.Client, error) {
	if e := s.AuthorizeClientIDSecret(ctx, id, secret); e != nil {
		return nil, e
	}
	return s.GetClientByClientID(ctx, id)
}
func (s *Storage) ClientCredentialsTokenRequest(ctx context.Context, id string, scopes []string) (op.TokenRequest, error) {
	c, e := req(ctx).Tx.Client(id)
	if e != nil {
		return nil, e
	}
	for _, scope := range scopes {
		if !slices.Contains(c.Scopes, scope) || scope == "openid" || scope == "offline_access" {
			return nil, oidc.ErrInvalidScope()
		}
	}
	if len(scopes) == 0 {
		return nil, oidc.ErrInvalidScope()
	}
	return &machineRequest{client: id, scopes: scopes, audiences: c.Audiences}, nil
}
