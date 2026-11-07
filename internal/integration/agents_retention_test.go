package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
)

func TestAgentConsentRetentionAndScopedCleanup(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		a, i := newAgentFixture(t, f, []string{domain.AgentAutonomous})
		ctx := context.Background()
		old := time.Now().Add(-120 * 24 * time.Hour).Unix()
		if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
			g, e := tx.AgentGrant(i.AutonomousGrantID, true)
			if e != nil {
				return e
			}
			g.Expires = old
			g.Version++
			return tx.PutAgentGrant(g)
		}); e != nil {
			t.Fatal(e)
		}
		if _, e := f.store.Cleanup(ctx, 100, 90); e != nil {
			t.Fatal(e)
		}
		if e := f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error { _, e := tx.AgentGrant(i.AutonomousGrantID, false); return e }); e != nil {
			t.Fatal("current autonomous receipt removed while installation can renew", e)
		}
		if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
			current, e := tx.AgentInstallation(i.ID, true)
			if e != nil {
				return e
			}
			current.Expires = old
			current.Version++
			return tx.PutAgentInstallation(current)
		}); e != nil {
			t.Fatal(e)
		}
		if _, e := f.store.Cleanup(ctx, 100, 90); e != nil {
			t.Fatal(e)
		}
		if e := f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error { _, e := tx.AgentGrant(i.AutonomousGrantID, false); return e }); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal("expired grant retained after retention")
		}
		if e := f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error { _, e := tx.AgentInstallation(i.ID, false); return e }); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal("expired installation retained after retention")
		}
		if e := f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error { _, e := tx.Agent(a.Agent.ID, false); return e }); e != nil {
			t.Fatal("cleanup deleted durable Agent identity")
		}
	})
}
