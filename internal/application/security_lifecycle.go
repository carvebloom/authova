package application

import (
	"strings"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

func ValidReason(reason string) bool { return strings.TrimSpace(reason) != "" && len(reason) <= 500 }

func (s *Service) EnableUser(t ports.AppTx, actor, app, id string, epoch int64, reason string) (domain.User, error) {
	if !ValidReason(reason) {
		return domain.User{}, domain.ErrInvalid
	}
	if e := t.LockOwners(); e != nil {
		return domain.User{}, e
	}
	u, e := t.User(id, true)
	if e != nil {
		return u, e
	}
	if u.Status != "disabled" || u.Epoch != epoch {
		return u, domain.ErrConflict
	}
	u.Status = "active"
	u.Epoch++
	if e = t.PutUser(u); e != nil {
		return u, e
	}
	if e = t.RevokeSessions(u.ID); e != nil {
		return u, e
	}
	return u, t.Audit(audit(app, actor, "user.enable", id, reason))
}

// ClientSecretMatches is shared by token, revoke and introspection endpoints.
// Rotation changes credential acceptance, not already-issued token lifetimes.
func (s *Service) ClientSecretMatches(app string, c domain.Client, secret string) bool {
	if secret == "" || len(secret) > 4096 || c.Kind != "confidential" {
		return false
	}
	digest := s.Digest(app, "client-secret", secret)
	current := secure.Equal(c.SecretDigest, digest)
	previous := secure.Equal(c.PreviousSecretDigest, digest)
	return current || (previous && c.PreviousSecretDigest != "" && c.PreviousSecretExpires > s.Now().Unix())
}
func (s *Service) RotateClientSecret(t ports.AppTx, actor domain.Operator, app, id string, version, grace int64, reason string) (domain.Client, string, error) {
	if !ValidReason(reason) || grace < 0 || grace > 86400 {
		return domain.Client{}, "", domain.ErrInvalid
	}
	if _, e := agentClientManagement(t, actor, id); e != nil {
		return domain.Client{}, "", e
	}
	c, e := t.LockClient(id)
	if e != nil {
		return c, "", e
	}
	if c.System || c.Kind != "confidential" || c.Status != "active" {
		return c, "", domain.ErrForbidden
	}
	if c.Version != version || c.PreviousSecretExpires > s.Now().Unix() {
		return c, "", domain.ErrConflict
	}
	secret := secure.Random(32)
	c.PreviousSecretDigest = ""
	c.PreviousSecretExpires = 0
	if grace > 0 {
		c.PreviousSecretDigest = c.SecretDigest
		c.PreviousSecretExpires = s.Now().Add(time.Duration(grace) * time.Second).Unix()
	}
	c.SecretDigest = s.Digest(app, "client-secret", secret)
	c.Version++
	if e = t.PutClient(c); e != nil {
		return c, "", e
	}
	if e = t.Audit(audit(app, actor.ID, "client.secret_rotate", id, reason)); e != nil {
		return c, "", e
	}
	return c, secret, nil
}
func (s *Service) RevokePreviousClientSecret(t ports.AppTx, actor domain.Operator, app, id string, version int64, reason string) (domain.Client, error) {
	if !ValidReason(reason) {
		return domain.Client{}, domain.ErrInvalid
	}
	if _, e := agentClientManagement(t, actor, id); e != nil {
		return domain.Client{}, e
	}
	c, e := t.LockClient(id)
	if e != nil {
		return c, e
	}
	if c.System || c.Kind != "confidential" {
		return c, domain.ErrForbidden
	}
	if c.Version != version {
		return c, domain.ErrConflict
	}
	c.PreviousSecretDigest = ""
	c.PreviousSecretExpires = 0
	c.Version++
	if e = t.PutClient(c); e != nil {
		return c, e
	}
	return c, t.Audit(audit(app, actor.ID, "client.previous_secret_revoke", id, reason))
}

// RevokeSigningKey changes the verification generation in the same transaction.
// Cached verifiers use a generation from the primary liveness statement, never
// an App routing cache; other replicas observe revocation without a pub/sub race.
func (s *Service) RevokeSigningKey(t ports.AppTx, actor, app, id string, version int64, replacement domain.SigningKey, reason string) (string, int64, error) {
	if !ValidReason(reason) {
		return "", 0, domain.ErrInvalid
	}
	if e := t.LockOwners(); e != nil {
		return "", 0, e
	}
	a, e := t.App()
	if e != nil {
		return "", 0, e
	}
	if a.Status == domain.Quarantined {
		return "", 0, domain.ErrConflict
	}
	if a.Version != version {
		return "", 0, domain.ErrConflict
	}
	keys, e := t.Keys()
	if e != nil {
		return "", 0, e
	}
	var found *domain.SigningKey
	for i := range keys {
		if keys[i].ID == id {
			found = &keys[i]
			break
		}
	}
	if found == nil {
		return "", 0, domain.ErrNotFound
	}
	if found.RevokedAt != 0 {
		return "", 0, domain.ErrConflict
	}
	newID := ""
	if found.Current {
		newID = replacement.ID
		if newID == "" || replacement.AppID != app {
			return "", 0, domain.ErrInvalid
		}
	}
	found.Current = false
	found.RevokedAt = s.Now().Unix()
	found.Retire = found.RevokedAt
	if e = t.PutKey(*found); e != nil {
		return "", 0, e
	}
	if newID != "" {
		if e = t.PutKey(replacement); e != nil {
			return "", 0, e
		}
	}
	a.KeyEpoch++
	a.Version++
	if e = t.UpdateAppConfig(a); e != nil {
		return "", 0, e
	}
	if e = t.Audit(audit(app, actor, "key.revoke", id, reason)); e != nil {
		return "", 0, e
	}
	return newID, a.Version, nil
}

// CheckAuthenticationPolicy is evaluated against current transactional App
// state at proof acceptance and completion, including already-started flows.
func CheckAuthenticationPolicy(t ports.AppTx, f domain.Flow, method, purpose string) error {
	a, e := t.App()
	if e != nil {
		return e
	}
	if a.Status != "active" {
		return domain.ErrForbidden
	}
	p := a.AuthenticationPolicy()
	c, e := t.Client(f.ClientID)
	if e != nil {
		return e
	}
	if c.Status != "active" {
		return domain.ErrForbidden
	}
	if purpose == "register" && !p.RegistrationEnabled {
		return domain.ErrForbidden
	}
	if f.Registered && !p.RegistrationEnabled {
		return domain.ErrForbidden
	}
	// A fixed system Client retains password + enforced MFA for administrators;
	// it does not bypass registration restrictions or grant management authority.
	switch method {
	case "passkey":
		if !p.PasskeyEnabled || c.System {
			return domain.ErrForbidden
		}
	case "password":
		if !p.PasswordLoginEnabled && !c.System {
			return domain.ErrForbidden
		}
	case "email_otp":
		if !p.EmailOTPEnabled {
			return domain.ErrForbidden
		}
	case "phone_otp":
		if !p.PhoneOTPEnabled {
			return domain.ErrForbidden
		}
	case "federated":
		if !p.FederationEnabled {
			return domain.ErrForbidden
		}
		if f.Registered && !p.ExternalRegistrationEnabled {
			return domain.ErrForbidden
		}
	}
	return nil
}
func challengePolicy(t ports.AppTx, f domain.Flow, kind, purpose string) error {
	method := "password"
	if purpose == "login" {
		method = kind + "_otp"
	}
	if purpose == "change_identifier" || purpose == "register" {
		method = ""
	}
	return CheckAuthenticationPolicy(t, f, method, purpose)
}

// registrationMethod preserves password enrollment and permits no-password
// enrollment only for an enabled, verified OTP method. System clients always
// retain password enrollment; this creates no administrator role or authority.
func (s *Service) registrationMethod(t ports.AppTx, f domain.Flow, password string) (string, error) {
	if err := s.LiveFlow(f); err != nil {
		return "", err
	}
	if f.Purpose != "register" || f.Step != domain.StepSetPassword || f.VerifiedValue == "" || (f.VerifiedKind != "email" && f.VerifiedKind != "phone") {
		return "", domain.ErrForbidden
	}
	a, err := t.App()
	if err != nil {
		return "", err
	}
	c, err := t.Client(f.ClientID)
	if err != nil {
		return "", err
	}
	method := "password"
	if !a.AuthenticationPolicy().PasswordLoginEnabled && !c.System {
		if password != "" {
			return "", domain.ErrForbidden
		}
		method = f.VerifiedKind + "_otp"
	}
	if err = CheckAuthenticationPolicy(t, f, method, "register"); err != nil {
		return "", err
	}
	return method, nil
}
