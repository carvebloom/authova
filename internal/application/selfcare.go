package application

import (
	"context"
	"errors"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/resource"

	"time"
)

func (s *Service) SelfPassword(ctx context.Context, scope domain.Scope, handle, old, new string) error {
	ctx = resource.WithApp(ctx, scope.ID())
	var user domain.User
	e := s.Store.App(ctx, scope, false, func(t ports.AppTx) error {
		_, u, e := s.AuthenticatedSession(t, scope, handle, true)
		user = u
		return e
	})
	if e != nil {
		return e
	}
	if user.PasswordHash != "" {
		if e = s.CheckPassword(ctx, old, user.PasswordHash); e != nil {
			return e
		}
	}
	hash, e := s.HashPassword(ctx, new)
	if e != nil {
		return e
	}
	return s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		_, locked, e := s.LockedSession(t, scope, handle, true)
		if e != nil {
			return e
		}
		if locked.PasswordHash != user.PasswordHash {
			return domain.ErrConflict
		}
		locked.PasswordHash = hash
		locked.Epoch++
		if e = t.PutUser(locked); e != nil {
			return e
		}
		if e = t.RevokeSessions(locked.ID); e != nil {
			return e
		}
		return t.Audit(audit(scope.ID(), locked.ID, "user.password_change", locked.ID))
	})
}
func (s *Service) BindVerifiedIdentifier(ctx context.Context, scope domain.Scope, flow string) error {
	return s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		f, e := t.Flow(flow, true)
		if e != nil {
			return e
		}
		if e = s.LiveFlow(f); e != nil {
			return e
		}
		if f.Purpose != "change_identifier" || f.Step != "bind_identifier" || f.VerifiedValue == "" || f.AuthTime < s.Now().Add(-10*time.Minute).Unix() {
			return domain.ErrForbidden
		}
		user, e := authenticatedFlowUser(t, f, true)
		if e != nil {
			return e
		}
		if _, e = s.lockedSessionForUser(t, f.SessionID, user, true); e != nil {
			return e
		}
		old := user.Email
		if f.VerifiedKind == "phone" {
			old = user.Phone
			user.Phone = f.VerifiedValue
		} else {
			user.Email = f.VerifiedValue
		}
		if e = t.ReplaceIdentifier(user.ID, f.VerifiedKind, s.Digest(scope.ID(), "identifier:"+f.VerifiedKind, old), s.Digest(scope.ID(), "identifier:"+f.VerifiedKind, f.VerifiedValue)); e != nil {
			return e
		}
		user.Epoch++
		if e = t.PutUser(user); e != nil {
			return e
		}
		if e = t.RevokeSessions(user.ID); e != nil {
			return e
		}
		if e = f.FinishCredential(domain.PurposeChangeIdentifier, domain.StepBindIdentifier); e != nil {
			return e
		}
		if e = t.PutFlow(f); e != nil {
			return e
		}
		if e = t.Audit(audit(scope.ID(), user.ID, "user.identifier_change", user.ID)); e != nil {
			return e
		}
		if old != "" {
			return t.Enqueue(domain.Message{ID: ID(), AppID: scope.ID(), Kind: f.VerifiedKind, Destination: old, Body: "Your Authova contact identifier was changed. Existing sessions were revoked.", Created: s.Now().Unix(), Available: s.Now().Unix()})
		}
		return nil
	})
}
func (s *Service) EraseSelf(ctx context.Context, scope domain.Scope, handle string) error {
	return s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		if e := t.LockOwners(); e != nil {
			return e
		}
		_, u, e := s.LockedSession(t, scope, handle, true)
		if e != nil {
			return e
		}
		role, e := t.AdminRole(u.ID)
		if e != nil && !errors.Is(e, domain.ErrNotFound) {
			return e
		}
		if e == nil && role == "owner" {
			count, e := t.CountOwners()
			if e != nil {
				return e
			}
			if count <= 1 {
				return domain.ErrConflict
			}
		}
		if e = t.EraseUserAgentGrants(u.ID); e != nil {
			return e
		}
		if e = t.EraseUserPasskeys(u.ID); e != nil {
			return e
		}
		u.Status = "deleted"
		u.Email = ""
		u.Phone = ""
		u.DisplayName = ""
		u.PasswordHash = ""
		u.TOTPSecret = ""
		u.RecoveryDigests = nil
		u.Epoch++
		if e = t.PutUser(u); e != nil {
			return e
		}
		if e = t.EraseIdentifiers(u.ID); e != nil {
			return e
		}
		if e = t.RevokeSessions(u.ID); e != nil {
			return e
		}
		if e = t.SetAdminRole(u.ID, ""); e != nil {
			return e
		}
		return t.Audit(audit(scope.ID(), u.ID, "user.erase", u.ID))
	})
}

// UnlinkExternal serializes on the user: two concurrent unlinks cannot remove
// all credentials. A profile email supplied by federation is not a verified alias.
func (s *Service) UnlinkExternal(ctx context.Context, scope domain.Scope, handle, connection string, emailConfigured, phoneConfigured bool) error {
	return s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		_, u, e := s.LockedSession(t, scope, handle, true)
		if e != nil {
			return e
		}
		bindings, e := t.ExternalBindings(u.ID)
		if e != nil {
			return e
		}
		a, e := t.App()
		if e != nil {
			return e
		}
		target := false
		for _, b := range bindings {
			if b.ConnectionID == connection && b.Count > 0 {
				target = true
			}
		}
		if !target {
			return domain.ErrNotFound
		}
		alternative, e := s.hasUsableLogin(t, scope, a, u, loginExclusion{connectionID: connection}, emailConfigured, phoneConfigured)
		if e != nil {
			return e
		}
		if !alternative {
			return domain.ErrConflict
		}
		if e = t.UnbindExternal(u.ID, connection); e != nil {
			return e
		}
		u.Epoch++
		if e = t.PutUser(u); e != nil {
			return e
		}
		if e = t.RevokeSessions(u.ID); e != nil {
			return e
		}
		return t.Audit(audit(scope.ID(), u.ID, "user.external_unlink", connection))
	})
}
