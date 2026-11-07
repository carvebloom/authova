package integration

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
)

func TestAgentSharedIssuanceAndExclusiveRevocation(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		a, i := newAgentFixture(t, f, []string{domain.AgentAutonomous})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		held := make(chan struct{})
		release := make(chan struct{})
		finished := make(chan error, 1)
		go func() {
			finished <- f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
				if _, e := tx.SharedAgent(a.Agent.ID); e != nil {
					return e
				}
				if _, e := tx.SharedAgentInstallation(i.ID); e != nil {
					return e
				}
				if _, e := tx.SharedAgentGrant(i.AutonomousGrantID); e != nil {
					return e
				}
				close(held)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
		}()
		select {
		case <-held:
		case e := <-finished:
			t.Fatalf("shared state failed: %v", e)
		case <-ctx.Done():
			t.Fatal("configuration lock timed out")
		}
		// A second issuer on the SAME Agent/installation/grant must complete while
		// the first transaction still holds all three configuration shared locks.
		type issued struct {
			token  string
			status int
		}
		result := make(chan issued, 1)
		go func() {
			status, out := agentTokenRequest(t, f, a, url.Values{"grant_type": {"client_credentials"}, "installation_id": {i.ID}, "scope": {"api.read"}})
			token, _ := out["access_token"].(string)
			result <- issued{token, status}
		}()
		var token string
		select {
		case out := <-result:
			if out.status != 200 {
				t.Errorf("concurrent issuer status %d", out.status)
			}
			token = out.token
		case <-time.After(3 * time.Second):
			close(release)
			<-finished
			t.Fatal("issuance serialized behind a shared configuration reader")
		}
		revoked := make(chan error, 1)
		go func() {
			revoked <- f.service.AppManage(ctx, f.operatorToken, f.scope1, true, true, func(tx ports.AppTx, o domain.Operator) error {
				_, e := f.service.RevokeAgentGrant(tx, o.ID, i.AutonomousGrantID, application.AgentVersionedReason{Version: 1, Reason: "concurrent revocation test"}, "")
				return e
			})
		}()
		select {
		case e := <-revoked:
			t.Fatalf("revocation bypassed shared issuance lock: %v", e)
		case <-time.After(100 * time.Millisecond):
		}
		close(release)
		if e := <-finished; e != nil {
			t.Fatal(e)
		}
		if e := <-revoked; e != nil {
			t.Fatal(e)
		}
		requireActive(t, f, token, false)
	})
}
