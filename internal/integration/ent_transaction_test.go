package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
)

// Exercise generated reads/writes and native SQL through the real App boundary.
// Duplicate IDs deliberately prove that Ent's single-ID helpers cannot cross App.
func TestEntBoundTransactions(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		ctx := context.Background()
		id := application.ID()
		for _, scope := range []domain.Scope{f.scope1, f.scope2} {
			u := f.user1
			u.ID = id
			u.AppID = scope.ID()
			if err := f.store.App(ctx, scope, true, func(tx ports.AppTx) error { return tx.CreateUser(u, "", "") }); err != nil {
				t.Fatal(err)
			}
		}
		abort := errors.New("deliberate transaction abort")
		err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
			u, e := tx.User(id, true)
			if e != nil {
				return e
			}
			u.Status = "disabled"
			if e = tx.PutUser(u); e != nil {
				return e
			}
			read, e := tx.User(id, false)
			if e != nil {
				return e
			}
			if read.Status != "disabled" {
				t.Fatal("Ent did not read its uncommitted write")
			}
			a, e := tx.App()
			if e != nil {
				return e
			}
			a.Version++
			a.Name = "rolled back config"
			if e = tx.UpdateAppConfig(a); e != nil {
				return e
			}
			return abort
		})
		if !errors.Is(err, abort) {
			t.Fatal(err)
		}
		for _, scope := range []domain.Scope{f.scope1, f.scope2} {
			if e := f.store.App(ctx, scope, false, func(tx ports.AppTx) error {
				u, e := tx.User(id, false)
				if e != nil {
					return e
				}
				if u.Status != "active" || u.AppID != scope.ID() {
					t.Fatal("rollback or App isolation failed")
				}
				if e = tx.PutUser(u); !errors.Is(e, domain.ErrForbidden) {
					t.Fatal("read transaction permitted generated mutation", e)
				}
				a, e := tx.App()
				if e != nil {
					return e
				}
				if a.Name == "rolled back config" {
					t.Fatal("config escaped rollback")
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		}
	})
}

// MySQL resolves duplicate keys against any unique index. A duplicate public
// client_id must never overwrite the payload of a different internal identity.
func TestEntUpsertSecondaryUniqueCollision(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		ctx := context.Background()
		candidate := f.client
		candidate.ID = application.ID()
		candidate.Name = "must not replace original"
		err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return tx.PutClient(candidate) })
		if !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("secondary unique collision: %v", err)
		}
		if err := f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
			original, err := tx.Client(f.client.ClientID)
			if err != nil {
				return err
			}
			if original.ID != f.client.ID || original.Name != f.client.Name {
				t.Fatal("secondary collision changed existing client")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		candidate = f.client
		candidate.Name = "same identity update"
		if err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return tx.PutClient(candidate) }); err != nil {
			t.Fatal(err)
		}
		if err := f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
			updated, err := tx.Client(f.client.ClientID)
			if err == nil && updated.Name != candidate.Name {
				t.Fatal("same identity update did not persist")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	})
}
