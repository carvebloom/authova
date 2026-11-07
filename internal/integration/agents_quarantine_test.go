package integration

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"testing"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
)

func TestAgentRecoveryQuarantine(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		a, i := acceptanceAgent(t, f)
		delegated := recoveryCredentialFrom(t, f, issueDelegated(t, f, a, i))
		status, machine := agentTokenRequest(t, f, a, url.Values{"grant_type": {"client_credentials"}, "installation_id": {i.ID}, "scope": {"api.read"}})
		if status != 200 {
			t.Fatal("initial autonomous issuance failed", status)
		}
		machineToken := machine["access_token"].(string)
		requireActive(t, f, machineToken, true) // Warm the verifier before fencing.
		code, verifier := f.code()
		status, ordinary := f.exchange(code, verifier)
		if status != 200 {
			t.Fatal("ordinary control issuance failed", status)
		}
		other := *f
		other.app1, other.scope1, other.user1 = f.app2, f.scope2, f.user2
		otherAgent, _ := acceptanceAgent(t, &other)
		if _, err := f.service.QuarantineRecoveredAgents(context.Background(), ""); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal("missing recovery reason accepted", err)
		}
		report, err := f.service.QuarantineAppAgents(context.Background(), f.scope1, "actual local restore rehearsal")
		if err != nil || report.AgentsDisabled != 1 || report.InstallationsClosed != 1 || report.CredentialsCleared != 1 || report.GrantsRevoked != 2 {
			t.Fatal("incomplete quarantine", report, err)
		}
		if err = f.store.App(context.Background(), f.scope2, false, func(tx ports.AppTx) error {
			a, e := tx.Agent(otherAgent.Agent.ID, false)
			if e != nil || a.Status != "active" {
				return errors.New("App maintenance crossed the requested App boundary")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.QuarantineAppAgents(context.Background(), f.scope2, "independently quarantine the second App"); err != nil {
			t.Fatal(err)
		}
		requireActive(t, f, delegated.Access, false)
		requireActive(t, f, machineToken, false)
		if !f.active(ordinary.Access) {
			t.Fatal("Agent-only recovery invalidated unrelated human consent")
		}
		for _, form := range []url.Values{{"grant_type": {"refresh_token"}, "refresh_token": {delegated.Refresh}}, {"grant_type": {"client_credentials"}, "installation_id": {i.ID}, "scope": {"api.read"}}} {
			status, _ := agentTokenRequest(t, f, a, form)
			if status == 200 {
				t.Fatal("old credential escaped quarantine")
			}
		}
		for _, item := range []struct {
			scope domain.Scope
			id    string
		}{{f.scope1, a.Agent.ID}, {f.scope2, otherAgent.Agent.ID}} {
			if err := f.store.App(context.Background(), item.scope, false, func(tx ports.AppTx) error {
				agent, err := tx.Agent(item.id, false)
				if err != nil || agent.Status != "disabled" {
					return errors.New("another App Agent escaped quarantine")
				}
				client, err := tx.Client(agent.ClientID)
				if err != nil || client.SecretDigest != "" || client.PreviousSecretDigest != "" {
					return errors.New("restored current/previous credential was retained")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		repeated, err := f.service.QuarantineAppAgents(context.Background(), f.scope1, "repeat completed local restore quarantine")
		if err != nil || repeated.AgentsDisabled+repeated.InstallationsClosed+repeated.GrantsRevoked+repeated.CredentialsCleared != 0 {
			t.Fatal("quarantine was not idempotent", repeated, err)
		}
		// Re-enabling configuration alone must not resurrect the old client secret
		// or any prior delegation. Fresh secret rotation and consent are necessary.
		if err := f.store.App(context.Background(), f.scope1, true, func(tx ports.AppTx) error {
			agent, err := tx.Agent(a.Agent.ID, true)
			if err != nil {
				return err
			}
			agent.Status, agent.Version = "active", agent.Version+1
			if err = tx.PutAgent(agent); err != nil {
				return err
			}
			install, err := tx.AgentInstallation(i.ID, true)
			if err != nil {
				return err
			}
			install.Status, install.Version = "active", install.Version+1
			return tx.PutAgentInstallation(install)
		}); err != nil {
			t.Fatal(err)
		}
		status, _ = agentTokenRequest(t, f, a, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {delegated.Refresh}})
		if status != 401 {
			t.Fatal("re-enabled configuration accepted old secret", status)
		}
		if err := f.service.AppManage(context.Background(), f.operatorToken, f.scope1, true, true, func(tx ports.AppTx, owner domain.Operator) error {
			client, e := tx.Client(a.Agent.ClientID)
			if e != nil {
				return e
			}
			_, a.Secret, e = f.service.RotateClientSecret(tx, owner, f.app1.ID, a.Agent.ClientID, client.Version, 0, "fresh post-restore credential")
			return e
		}); err != nil {
			t.Fatal(err)
		}
		status, _ = agentTokenRequest(t, f, a, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {delegated.Refresh}})
		if status != 400 {
			t.Fatal("new client secret resurrected old delegation", status)
		}
		fresh := recoveryCredentialFrom(t, f, issueDelegated(t, f, a, i))
		if fresh.GrantID == delegated.GrantID {
			t.Fatal("fresh consent reused revoked grant")
		}
		requireActive(t, f, fresh.Access, true)
	})
}

type quarantineAuditFailureStore struct {
	ports.Store
	action string
}
type quarantineAuditFailureTx struct {
	ports.AppTx
	action string
}

func (s quarantineAuditFailureStore) App(ctx context.Context, scope domain.Scope, write bool, fn func(ports.AppTx) error) error {
	return s.Store.App(ctx, scope, write, func(tx ports.AppTx) error { return fn(quarantineAuditFailureTx{tx, s.action}) })
}
func (t quarantineAuditFailureTx) Audit(a domain.Audit) error {
	if t.action == "" || t.action == a.Action {
		return domain.ErrUnavailable
	}
	return t.AppTx.Audit(a)
}

func TestAgentRecoveryQuarantineAtomicity(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		a, i := acceptanceAgent(t, f)
		out := recoveryCredentialFrom(t, f, issueDelegated(t, f, a, i))
		f.service.Store = quarantineAuditFailureStore{Store: f.store}
		report, err := f.service.QuarantineAppAgents(context.Background(), f.scope1, "forced audit failure")
		f.service.Store = f.store
		if !errors.Is(err, domain.ErrUnavailable) || report.AgentsDisabled != 0 {
			t.Fatal("failed quarantine reported success", report, err)
		}
		requireActive(t, f, out.Access, true)
		status, _, err := agentRefreshHTTP(f, a, out.Refresh)
		if err != nil || status != 200 {
			t.Fatal("quarantine changes escaped rollback", status, err)
		}
	})
}

func TestAgentRecoveryQuarantineInterruptedDrain(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		a, i := acceptanceAgent(t, f)
		before := recoveryCredentialFrom(t, f, issueDelegated(t, f, a, i))
		f.service.Store = quarantineAuditFailureStore{Store: f.store, action: "agent.installation.update"}
		partial, err := f.service.QuarantineAppAgents(context.Background(), f.scope1, "interrupt after committed fence")
		f.service.Store = f.store
		if !errors.Is(err, domain.ErrUnavailable) || partial.AgentsDisabled != 1 || partial.CredentialsCleared != 1 || partial.InstallationsClosed != 0 || partial.GrantsRevoked != 0 {
			t.Fatal("interrupted drain did not preserve its committed fence", partial, err)
		}
		requireActive(t, f, before.Access, false)
		status, _ := agentTokenRequest(t, f, a, url.Values{"grant_type": {"client_credentials"}, "installation_id": {i.ID}, "scope": {"api.read"}})
		if status != 401 {
			t.Fatal("interrupted drain left old credential usable", status)
		}
		finished, err := f.service.QuarantineAppAgents(context.Background(), f.scope1, "resume bounded drain after reconciliation")
		if err != nil || finished.AgentsDisabled != 0 || finished.CredentialsCleared != 0 || finished.InstallationsClosed != 1 || finished.GrantsRevoked != 2 {
			t.Fatal("fenced drain could not resume", finished, err)
		}
	})
}

func TestAgentRecoveryQuarantinePagination(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		a, _ := acceptanceAgent(t, f)
		for n := 0; n < 101; n++ {
			_, err := f.service.CreateAgentInstallation(context.Background(), f.operatorToken, f.scope1, application.AgentInstallationCreate{
				AgentID: a.Agent.ID, TenantID: fmt.Sprintf("pagination-tenant-%d", n), UserID: f.user1.ID, ResponsibleParty: "trusted provisioner",
				Resource: "urn:authova:integration", Scopes: []string{"api.read"}, Modes: []string{domain.AgentAutonomous}, Expires: f.service.Now().Unix() + 86400, Reason: "pagination fixture",
			}, "pagination-"+application.ID())
			if err != nil {
				t.Fatal(err)
			}
		}
		report, err := f.service.QuarantineAppAgents(context.Background(), f.scope1, "complete more than one installation/grant page")
		if err != nil || report.InstallationsClosed != 102 || report.GrantsRevoked != 102 {
			t.Fatal("quarantine missed a page", report, err)
		}
		if err = f.store.App(context.Background(), f.scope1, false, func(tx ports.AppTx) error {
			installations, e := tx.AgentInstallations(domain.AgentFilter{AgentID: a.Agent.ID, Limit: 200})
			if e != nil || len(installations) != 102 {
				return errors.New("unexpected installation denominator")
			}
			grants, e := tx.AgentGrants(domain.AgentFilter{AgentID: a.Agent.ID, Limit: 200})
			if e != nil || len(grants) != 102 {
				return errors.New("unexpected grant denominator")
			}
			for _, i := range installations {
				if i.Status != "disabled" {
					return errors.New("installation page remained active")
				}
			}
			for _, g := range grants {
				if g.Status != "revoked" {
					return errors.New("grant page remained active")
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
}
