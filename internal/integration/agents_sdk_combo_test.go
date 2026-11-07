//go:build contractcombo

package integration

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	agentsdk "github.com/carvebloom/authova/contracts/sdk/agent"
	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
)

func TestPinnedContractCombinationAgentResourceSDK(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		a, i := newAgentFixture(t, f, []string{domain.AgentAutonomous})
		status, out := agentTokenRequest(t, f, a, url.Values{"grant_type": {"client_credentials"}, "scope": {"api.read"}, "installation_id": {i.ID}})
		if status != 200 {
			t.Fatalf("mint %d: %v", status, out)
		}
		token := out["access_token"].(string)
		verifier, e := agentsdk.NewVerifier(agentsdk.Config{Issuer: f.app1.Issuer, AppID: f.app1.ID, Resource: i.Resource, ClientID: f.client.ClientID, ClientSecret: f.secret, AllowLoopbackHTTP: true})
		if e != nil {
			t.Fatal(e)
		}
		ctx := context.Background()
		principal, e := verifier.Verify(ctx, token, i.TenantID, []string{"api.read"})
		if e != nil || principal.AgentID != a.Agent.ID {
			t.Fatalf("real SDK denied token: %v", e)
		}
		for _, tenant := range []string{"other-tenant", ""} {
			if _, e = verifier.Verify(ctx, token, tenant, []string{"api.read"}); !errors.Is(e, agentsdk.ErrInvalidToken) {
				t.Fatal("cross tenant or empty wildcard accepted")
			}
		}
		second, secondSecret, e := f.service.CreateClient(ctx, f.operatorToken, f.scope2, domain.Client{Name: "Second App resource", Kind: "confidential", Scopes: []string{"api.read"}, Audiences: []string{i.Resource}, IntrospectionAudiences: []string{i.Resource}, Grants: []string{"client_credentials"}}, "sdk-review-"+application.ID())
		if e != nil {
			t.Fatal(e)
		}
		otherApp, e := agentsdk.NewVerifier(agentsdk.Config{Issuer: f.app2.Issuer, AppID: f.app2.ID, Resource: i.Resource, ClientID: second.ClientID, ClientSecret: secondSecret, AllowLoopbackHTTP: true})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = otherApp.Verify(ctx, token, i.TenantID, []string{"api.read"}); !errors.Is(e, agentsdk.ErrInvalidToken) {
			t.Fatal("another App accepted identical tenant/resource identifiers")
		}
		if _, e = verifier.Verify(ctx, token, i.TenantID, []string{"api.write"}); !errors.Is(e, agentsdk.ErrInsufficientScope) {
			t.Fatal("scope escalation accepted")
		}
		called := false
		handler := verifier.Middleware([]string{"api.read"}, func(*http.Request) (string, error) { return i.TenantID, nil }, func(_ context.Context, p agentsdk.Principal, _ *http.Request) error {
			if p.AgentID != a.Agent.ID {
				return errors.New("not approved")
			}
			return nil
		}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := agentsdk.FromContext(r.Context()); !ok {
				t.Error("missing verified context")
			}
			called = true
			w.WriteHeader(204)
		}))
		r := httptest.NewRequest("GET", "http://resource.invalid/tickets", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 204 || !called {
			t.Fatalf("SDK middleware %d", w.Code)
		}
		status, _ = agentJSON(t, f, "POST", "/agent-grants/"+i.AutonomousGrantID+"/revoke", application.AgentVersionedReason{Version: 1, Reason: "SDK revoke check"})
		if status != 200 {
			t.Fatal("revoke failed")
		}
		called = false
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 401 || called {
			t.Fatal("SDK cached an allow after committed revocation")
		}
	})
}
