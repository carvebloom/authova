package application

import (
	"errors"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
)

type loginExclusion struct{ passkeyID, connectionID string }

// hasUsableLogin is shared by credential removal operations. Caller holds the
// user lock, serializing removals. Profile contacts and password hashes alone
// are never treated as a login alias. System administrator login is preserved.
func (s *Service) hasUsableLogin(t ports.AppTx, scope domain.Scope, a domain.App, u domain.User, excluded loginExclusion, emailReady, phoneReady bool) (bool, error) {
	if a.Status != "active" || u.Status != "active" {
		return false, domain.ErrForbidden
	}
	policy := a.AuthenticationPolicy()
	passwordAvailable := policy.PasswordLoginEnabled
	if !passwordAvailable && u.PasswordHash != "" {
		role, err := t.AdminRole(u.ID)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return false, err
		}
		if role == "owner" || role == "admin" || role == "auditor" {
			system, err := t.Client("avm_" + a.Key)
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				return false, err
			}
			passwordAvailable = err == nil && system.System && system.Status == "active"
		}
	}
	for _, alias := range []struct {
		kind, value string
		delivery    bool
	}{
		{"email", u.Email, emailReady && policy.EmailOTPEnabled},
		{"phone", u.Phone, phoneReady && policy.PhoneOTPEnabled},
	} {
		if alias.value == "" {
			continue
		}
		owner, err := t.FindUser(alias.kind, s.Digest(scope.ID(), "identifier:"+alias.kind, alias.value))
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return false, err
		}
		if err == nil && owner.ID == u.ID && ((passwordAvailable && u.PasswordHash != "") || alias.delivery) {
			return true, nil
		}
	}
	if policy.PasskeyEnabled {
		keys, err := t.Passkeys(u.ID)
		if err != nil {
			return false, err
		}
		for _, key := range keys {
			if key.ID != excluded.passkeyID {
				return true, nil
			}
		}
	}
	if policy.FederationEnabled {
		bindings, err := t.ExternalBindings(u.ID)
		if err != nil {
			return false, err
		}
		for _, binding := range bindings {
			if binding.Count < 1 || binding.ConnectionID == excluded.connectionID {
				continue
			}
			connection, err := t.Connection(binding.ConnectionID)
			if err != nil {
				return false, err
			}
			if connection.Status == "active" {
				return true, nil
			}
		}
	}
	return false, nil
}
