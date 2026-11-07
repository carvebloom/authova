package application

import (
	"context"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
)

func (s *Service) DeletePasskey(ctx context.Context, scope domain.Scope, handle, id string, version int64, emailReady, phoneReady bool) error {
	return s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		session, user, e := s.LockedSession(t, scope, handle, true)
		if e != nil {
			return e
		}
		key, e := t.Passkey(id, true)
		if e != nil {
			return e
		}
		if key.UserID != user.ID {
			return domain.ErrForbidden
		}
		if key.Version != version {
			return domain.ErrConflict
		}
		a, e := t.App()
		if e != nil {
			return e
		}
		if a.Status != "active" {
			return domain.ErrForbidden
		}
		usable, e := s.hasUsableLogin(t, scope, a, user, loginExclusion{passkeyID: id}, emailReady, phoneReady)
		if e != nil {
			return e
		}
		if !usable {
			return domain.ErrConflict
		}
		if e = t.DeletePasskey(user.ID, id, version); e != nil {
			return e
		}
		user.Epoch++
		if e = t.PutUser(user); e != nil {
			return e
		}
		if e = t.RevokeSessions(user.ID); e != nil {
			return e
		}
		session.Epoch = user.Epoch
		session.Status = "active"
		if e = t.PutSession(session); e != nil {
			return e
		}
		if e = t.Audit(audit(scope.ID(), user.ID, "passkey.delete", id)); e != nil {
			return e
		}
		return s.notifyPasskeyChange(t, scope.ID(), user, "removed")
	})
}
