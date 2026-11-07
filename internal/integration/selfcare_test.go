package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

func (f *fixture) selfcareChecks(t *testing.T) {
	ctx := context.Background()
	hash, e := f.service.HashPassword(ctx, password)
	if e != nil {
		t.Fatal(e)
	}
	user := domain.User{ID: application.ID(), AppID: f.app1.ID, Status: "active", Email: "selfcare-" + application.ID() + "@example.test", PasswordHash: hash, Epoch: 1, CreatedAt: time.Now().UTC()}
	handle := secure.Random(32)
	session := domain.Session{ID: application.ID(), ClientID: f.client.ClientID, UserID: user.ID, Status: "active", Epoch: 1, AuthTime: time.Now().Unix(), CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Hour), HandleDigest: f.service.Digest(f.app1.ID, "session-handle", handle)}
	e = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
		if e := tx.CreateUser(user, "email", f.service.Digest(f.app1.ID, "identifier:email", user.Email)); e != nil {
			return e
		}
		return tx.PutSession(session)
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = f.service.SelfPassword(ctx, f.scope1, handle, "wrong password", "a new valid selfcare password phrase"); !errors.Is(e, domain.ErrUnauthenticated) {
		t.Fatal("wrong old password accepted")
	}
	if e = f.service.SelfPassword(ctx, f.scope1, handle, password, "a new valid selfcare password phrase"); e != nil {
		t.Fatal(e)
	}
	e = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
		_, _, e := f.service.AuthenticatedSession(tx, f.scope1, handle, false)
		return e
	})
	if !errors.Is(e, domain.ErrUnauthenticated) {
		t.Fatal("old session survived password update")
	}
	// Re-authenticate as a fixture to validate irreversible user-fact erasure and alias release.
	e = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
		u, e := tx.User(user.ID, true)
		if e != nil {
			return e
		}
		handle = secure.Random(32)
		session.HandleDigest = f.service.Digest(f.app1.ID, "session-handle", handle)
		session.ID = application.ID()
		session.Status = "active"
		session.Epoch = u.Epoch
		return tx.PutSession(session)
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = f.service.EraseSelf(ctx, f.scope1, handle); e != nil {
		t.Fatal(e)
	}
	e = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
		u, e := tx.User(user.ID, false)
		if e != nil {
			return e
		}
		if u.Status != "deleted" || u.Email != "" || u.PasswordHash != "" {
			t.Fatal("user private facts retained after erasure")
		}
		_, e = tx.FindUser("email", f.service.Digest(f.app1.ID, "identifier:email", user.Email))
		if !errors.Is(e, domain.ErrNotFound) {
			t.Fatal("erased identifier remains active")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
