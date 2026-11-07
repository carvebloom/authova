package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

func TestAgentCreationReceiptIsScopedAndSecretFree(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		in := application.AgentCreate{Name: "receipt Agent", Description: "synthetic", Kind: "confidential", Modes: []string{domain.AgentAutonomous}, Scopes: []string{"api.read"}, Resources: []string{"urn:authova:integration"}, RedirectURIs: []string{}, TokenTTL: 120, GrantTTL: 3600}
		key := "receipt-12345678"
		ctx := context.Background()
		first, e := f.service.CreateAgent(ctx, f.operatorToken, f.scope1, in, key)
		if e != nil {
			t.Fatal(e)
		}
		second, e := f.service.CreateAgent(ctx, f.operatorToken, f.scope1, in, key)
		if e != nil || first.Agent.ID != second.Agent.ID || first.Secret != second.Secret {
			t.Fatal("creator replay is not stable")
		}
		status, b := agentJSON(t, f, "GET", "/agent-operations/"+key, nil)
		if status != 200 {
			t.Fatalf("receipt %d", status)
		}
		var receipt map[string]any
		if json.Unmarshal(b, &receipt) != nil || receipt["resource_id"] != first.Agent.ID || receipt["kind"] != "agent" || len(receipt) != 2 {
			t.Fatal("receipt disclosed secret or lost identity")
		}
		otherOwner := f.managementToken()
		status, _ = agentAPIAs(t, f, f.app1.ID, otherOwner, "GET", "/agent-operations/"+key, nil)
		if status != 404 {
			t.Fatalf("another owner read creator-scoped receipt: HTTP %d", status)
		}
		status, _ = agentAPIAs(t, f, f.app2.ID, f.operatorToken, "GET", "/agent-operations/"+key, nil)
		if status != 404 {
			t.Fatalf("receipt crossed App scope: HTTP %d", status)
		}
		in.Description = "different"
		if _, e = f.service.CreateAgent(ctx, f.operatorToken, f.scope1, in, key); !errors.Is(e, domain.ErrConflict) {
			t.Fatal("idempotency key accepted changed payload")
		}
	})
}
func TestAgentCurrentGrantPolicyAndUserBinding(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		a, i := newAgentFixture(t, f, []string{domain.AgentDelegated, domain.AgentAutonomous})
		ctx := context.Background()
		if e := f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
			_, _, _, e := f.service.AgentContext(tx, a.Agent.ClientID, i.ID, domain.AgentDelegated, "wrong-user", []string{"api.read"}, false)
			return e
		}); !errors.Is(e, domain.ErrForbidden) {
			t.Fatal("wrong delegated user accepted")
		}
		status, out := agentTokenRequest(t, f, a, url.Values{"grant_type": {"client_credentials"}, "installation_id": {i.ID}, "scope": {"api.read"}})
		if status != 200 {
			t.Fatalf("issue %d %v", status, out)
		}
		token := out["access_token"].(string)
		requireActive(t, f, token, true)
		if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
			g, e := tx.AgentGrant(i.AutonomousGrantID, true)
			if e != nil {
				return e
			}
			g.Expires = time.Now().Add(-time.Second).Unix()
			g.Version++
			return tx.PutAgentGrant(g)
		}); e != nil {
			t.Fatal(e)
		}
		requireActive(t, f, token, false)
		status, out = agentTokenRequest(t, f, a, url.Values{"grant_type": {"client_credentials"}, "installation_id": {i.ID}, "scope": {"api.read"}})
		if status != 400 || out["error"] != "invalid_grant" {
			t.Fatalf("expired grant should be OAuth rejection, got %d %v", status, out)
		}
		// Management operator roles cannot be borrowed from an Agent business token.
		status, _ = agentJSON(t, f, "GET", "/agents", nil)
		if status != 200 {
			t.Fatal("control read failed")
		}
	})
}

func TestAgentPublicClientAndManagementRoleBoundary(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		ctx := context.Background()
		a, e := f.service.CreateAgent(ctx, f.operatorToken, f.scope1, application.AgentCreate{Name: "Public Agent", Description: "PKCE client", Kind: "public", Modes: []string{domain.AgentDelegated}, Scopes: []string{"api.read"}, Resources: []string{"urn:authova:integration"}, RedirectURIs: []string{f.server.URL + "/test-callback"}, TokenTTL: 30, GrantTTL: 3600}, "public-agent-"+application.ID())
		if e != nil || a.Secret != "" {
			t.Fatalf("public registration: %v", e)
		}
		i, e := f.service.CreateAgentInstallation(ctx, f.operatorToken, f.scope1, application.AgentInstallationCreate{AgentID: a.Agent.ID, ResponsibleParty: "owner", Resource: "urn:authova:integration", Scopes: []string{"api.read"}, Modes: []string{domain.AgentDelegated}, Expires: time.Now().Add(time.Hour).Unix(), Reason: "public Agent consent"}, "public-install-"+application.ID())
		if e != nil {
			t.Fatal(e)
		}
		out := issueDelegated(t, f, a, i)
		token := out["access_token"].(string)
		c := requireActive(t, f, token, true)
		if c["agent_id"] != a.Agent.ID {
			t.Fatal("public PKCE client lost Agent identity")
		}
		status, _ := agentJSON(t, f, "PATCH", "/clients/"+a.Agent.ClientID, map[string]any{"config_version": 1, "allowed_scopes": []string{"openid", "offline_access", "api.read", "api.write"}})
		if status != 403 {
			t.Fatal("legacy client patch widened Agent ceiling")
		}
		owner := f.operatorToken
		for _, role := range []string{"admin", "auditor"} {
			secret := secure.NewTOTP()
			op := domain.Operator{ID: application.ID(), Email: application.ID() + "@example.test", Role: role, Status: "active", Password: f.user1.PasswordHash, TOTP: secret}
			if e = f.store.Platform(ctx, true, func(tx ports.PlatformTx) error { return tx.PutOperator(op) }); e != nil {
				t.Fatal(e)
			}
			f.operatorToken, _, _, e = f.service.LoginOperator(ctx, op.Email, password, secure.TOTP(secret, time.Now().Unix()/30))
			if e != nil {
				t.Fatal(e)
			}
			status, _ := agentJSON(t, f, "GET", "/agents", nil)
			if status != 200 {
				t.Fatalf("%s cannot read Agent registry", role)
			}
			status, _ = agentJSON(t, f, "GET", "/agent-grants", nil)
			if (role == "auditor" && status != 403) || (role == "admin" && status != 200) {
				t.Fatalf("%s private grant read boundary: HTTP %d", role, status)
			}
			status, _ = agentJSON(t, f, "PATCH", "/agents/"+a.Agent.ID, map[string]any{"status": "disabled", "config_version": 1, "reason": "unauthorized role attempt"})
			if status != 403 {
				t.Fatalf("%s can mutate Agent registry: %d", role, status)
			}
			status, _ = agentJSON(t, f, "PATCH", "/clients/"+a.Agent.ClientID, map[string]any{"config_version": 1, "name": "unauthorized rename"})
			if status != 403 {
				t.Fatalf("%s bypassed Agent owner gate through Client API", role)
			}

		}
		f.operatorToken = owner
	})
}
