package application

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

func ManagementAudience(app string) string { return "urn:authova:management:" + app }
func (s *Service) ManagementClient(a domain.App) domain.Client {
	return domain.Client{ID: ID(), AppID: a.ID, ClientID: "avm_" + a.Key, Name: "Authova Console", Kind: "public", Status: "active", System: true, RedirectURIs: []string{s.ConsoleOrigin + "/app/callback"}, Scopes: []string{"openid", "profile", "email", "offline_access", "authova.admin"}, Audiences: []string{ManagementAudience(a.ID)}, IntrospectionAudiences: []string{}, Grants: []string{"authorization_code", "refresh_token"}, Version: 1, CreatedAt: s.Now().UTC()}
}
func (s *Service) appOperator(t ports.AppTx, a domain.App, token string, write bool) (domain.Operator, error) {
	keys, e := t.Keys()
	if e != nil {
		return domain.Operator{}, e
	}
	claims, e := secure.VerifyAccess(token, a.Issuer, keys)
	if e != nil {
		return domain.Operator{}, e
	}
	if claims.Principal != "user" || len(claims.Audience) != 1 || claims.Audience[0] != ManagementAudience(a.ID) || !slices.Contains(strings.Fields(claims.Scope), "authova.admin") {
		return domain.Operator{}, domain.ErrForbidden
	}
	// A business or delegated Agent token must never become a Console login,
	// even when a legacy/imported client carries the reserved audience. Validate
	// the credential's purpose here as well as in client provisioning.
	client, e := t.Client(claims.ClientID)
	if e != nil {
		return domain.Operator{}, e
	}
	if !client.System || client.ClientID != "avm_"+a.Key {
		return domain.Operator{}, domain.ErrForbidden
	}
	active, e := t.ActiveToken(claims.ID)
	if e != nil {
		if errors.Is(e, domain.ErrNotFound) {
			return domain.Operator{}, domain.ErrUnauthenticated
		}
		return domain.Operator{}, e
	}
	if active.Subject != claims.Subject || active.UserID != claims.Subject || active.ClientID != claims.ClientID || active.SessionID != claims.SessionID || active.Expires != claims.Expires {
		return domain.Operator{}, domain.ErrUnauthenticated
	}
	session, e := t.Session(active.SessionID, false)
	if e != nil {
		return domain.Operator{}, e
	}
	if session.MFATime == 0 || (write && session.MFATime < s.Now().Add(-10*time.Minute).Unix()) {
		return domain.Operator{}, domain.ErrUnauthenticated
	}
	if write {
		if e = t.LockOwners(); e != nil {
			return domain.Operator{}, e
		}
	}
	user, e := t.User(active.UserID, write)
	if e != nil {
		return domain.Operator{}, e
	}
	role, e := t.AdminRole(user.ID)
	if e != nil {
		return domain.Operator{}, domain.ErrForbidden
	}
	return domain.Operator{ID: user.ID, Email: secure.Mask(user.Email), AppID: a.ID, Role: role, Status: user.Status}, nil
}
func (s *Service) JWTManager(ctx context.Context, token string, write bool) (operator domain.Operator, err error) {
	issuer, e := secure.IssuerHint(token)
	if e != nil {
		return operator, domain.ErrUnauthenticated
	}
	prefix := s.BaseURL + "/a/"
	if !strings.HasPrefix(issuer, prefix) {
		return operator, domain.ErrUnauthenticated
	}
	key := strings.TrimPrefix(issuer, prefix)
	if len(key) != 32 || strings.Contains(key, "/") {
		return operator, domain.ErrUnauthenticated
	}
	a, e := s.Store.RouteApp(ctx, key)
	if e != nil {
		if errors.Is(e, domain.ErrNotFound) {
			return operator, domain.ErrUnauthenticated
		}
		return operator, e
	}
	scope, _ := domain.AppScope(a.ID)
	err = s.Store.App(ctx, scope, write, func(t ports.AppTx) error { operator, e = s.appOperator(t, a, token, write); return e })
	return
}
func (s *Service) GrantAdmin(t ports.AppTx, actor domain.Operator, app, userID, role string, reasons ...string) error {
	if actor.Role != "owner" {
		return domain.ErrForbidden
	}
	if role != "" && !slices.Contains([]string{"owner", "admin", "auditor"}, role) {
		return domain.ErrInvalid
	}
	if e := t.LockOwners(); e != nil {
		return e
	}
	u, e := t.User(userID, true)
	if e != nil {
		return e
	}
	if u.Status != "active" {
		return domain.ErrConflict
	}
	old, e := t.AdminRole(userID)
	if e != nil && !errors.Is(e, domain.ErrNotFound) {
		return e
	}
	if old == "owner" && role != "owner" {
		n, e := t.CountOwners()
		if e != nil {
			return e
		}
		if n <= 1 {
			return domain.ErrConflict
		}
	}
	if e = t.SetAdminRole(userID, role); e != nil {
		return e
	}
	return t.Audit(audit(app, actor.ID, "admin.grant", userID, reasons...))
}
