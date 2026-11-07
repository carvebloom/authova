package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/protocol"
	"github.com/carvebloom/authova/internal/transport"
)

func queryScopes() []string {
	return []string{"authova.query.context", "authova.query.configuration", "authova.query.users", "authova.query.sessions", "authova.query.agents", "authova.query.installations", "authova.query.grants", "authova.query.audit"}
}

func newQueryIdentity(t *testing.T, f *fixture, tenant string, scopes []string) (application.CreatedAgent, domain.AgentInstallation, string) {
	t.Helper()
	a, err := f.service.CreateAgent(context.Background(), f.operatorToken, f.scope1, application.AgentCreate{Name: "Read-only diagnostics", Kind: "confidential", Modes: []string{domain.AgentAutonomous}, Scopes: scopes, Resources: []string{domain.QueryAudience(f.app1.ID)}, TokenTTL: 120, GrantTTL: 3600}, "query-agent-"+application.ID())
	if err != nil {
		t.Fatal(err)
	}
	i, err := f.service.CreateAgentInstallation(context.Background(), f.operatorToken, f.scope1, application.AgentInstallationCreate{AgentID: a.Agent.ID, TenantID: tenant, ResponsibleParty: "approved diagnostics", Resource: domain.QueryAudience(f.app1.ID), Scopes: scopes, Modes: []string{domain.AgentAutonomous}, Expires: time.Now().Add(time.Hour).Unix(), Reason: "Explicitly approved read-only query access"}, "query-install-"+application.ID())
	if err != nil {
		t.Fatal(err)
	}
	status, result := agentTokenRequest(t, f, a, url.Values{"grant_type": {"client_credentials"}, "installation_id": {i.ID}, "resource": {i.Resource}, "scope": {strings.Join(scopes, " ")}})
	if status != 200 {
		t.Fatalf("query token issuance HTTP %d", status)
	}
	token, ok := result["access_token"].(string)
	if !ok || token == "" {
		t.Fatal("missing query token")
	}
	return a, i, token
}

func queryHTTP(t *testing.T, f *fixture, method, app, token, path string) (int, []byte) {
	t.Helper()
	r, err := http.NewRequest(method, f.server.URL+"/api/v1/apps/"+app+"/query/"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+token)
	res, err := f.http.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("query response is cacheable")
	}
	return res.StatusCode, body
}

func TestReadOnlyAgentQueries(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		ctx := context.Background()
		a, i, token := newQueryIdentity(t, f, "", queryScopes())
		t.Run("metadata-projections-and-contact-redaction", func(t *testing.T) {
			secret := "never-export-query-secret"
			if err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
				if err := tx.PutConnection(domain.Connection{ID: application.ID(), Name: "Test provider", ProviderType: "github", ClientID: "private-upstream-client", Secret: secret, Status: "active", Version: 1}); err != nil {
					return err
				}
				return tx.Audit(domain.Audit{ID: application.ID(), AppID: f.app1.ID, ActorID: "fixture", Action: "query.fixture", SubjectID: f.user1.ID, Reason: secret, CreatedAt: time.Now()})
			}); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"context", "configuration", "users", "users/" + f.user1.ID, "users/" + f.user1.ID + "/sessions", "agents", "agents/" + a.Agent.ID, "installations", "installations/" + i.ID, "grants", "grants/" + i.AutonomousGrantID, "audit"} {
				status, body := queryHTTP(t, f, "GET", f.app1.ID, token, path)
				if status != 200 {
					t.Fatalf("%s HTTP %d", path, status)
				}
				for _, forbidden := range []string{secret, a.Secret, f.user1.PasswordHash, "private-upstream-client", `"password_hash"`, `"totp_secret"`, `"client_secret"`, `"handle_digest"`} {
					if strings.Contains(string(body), forbidden) {
						t.Fatalf("private material in %s", path)
					}
				}
				if strings.HasPrefix(path, "users") && strings.Contains(string(body), f.user1.Email) {
					t.Fatal("unapproved full contact disclosed")
				}
			}
			_, _, contactToken := newQueryIdentity(t, f, "", []string{"authova.query.users", "authova.query.contacts"})
			status, body := queryHTTP(t, f, "GET", f.app1.ID, contactToken, "users/"+f.user1.ID)
			if status != 200 || !strings.Contains(string(body), f.user1.Email) {
				t.Fatal("explicit contact capability not honored")
			}
		})
		t.Run("bounded-pagination-and-other-app-user", func(t *testing.T) {
			want := map[string]bool{f.user1.ID: true}
			if err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
				for n := 0; n < 121; n++ {
					u := domain.User{ID: application.ID(), AppID: f.app1.ID, Status: "active", DisplayName: "query fixture", Epoch: 1, CreatedAt: time.Now()}
					want[u.ID] = true
					if err := tx.CreateUser(u, "email", f.service.Digest(f.app1.ID, "query-fixture", u.ID)); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			cursor := ""
			for n := 0; n < 20; n++ {
				status, body := queryHTTP(t, f, "GET", f.app1.ID, token, "users?limit=17&cursor="+url.QueryEscape(cursor))
				if status != 200 {
					t.Fatal(status)
				}
				var page domain.Page[application.QueryUser]
				if json.Unmarshal(body, &page) != nil || len(page.Items) > 17 {
					t.Fatal("invalid bounded page")
				}
				for _, u := range page.Items {
					if seen[u.ID] || !want[u.ID] || u.AppID != f.app1.ID {
						t.Fatal("duplicate or foreign user")
					}
					seen[u.ID] = true
				}
				if page.NextCursor == nil {
					break
				}
				cursor = *page.NextCursor
			}
			if len(seen) != len(want) {
				t.Fatal("query pagination silently truncated")
			}
			status, _ := queryHTTP(t, f, "GET", f.app1.ID, token, "users/"+f.user2.ID)
			if status != 404 {
				t.Fatal("foreign App user was visible", status)
			}
		})
		t.Run("credential-purpose-and-scope", func(t *testing.T) {
			_, _, narrow := newQueryIdentity(t, f, "", []string{"authova.query.context"})
			if status, _ := queryHTTP(t, f, "GET", f.app1.ID, narrow, "users"); status != 403 {
				t.Fatal("scope escalation", status)
			}
			if status, _ := queryHTTP(t, f, "GET", f.app2.ID, token, "context"); status != 401 {
				t.Fatal("cross App query accepted", status)
			}
			if status, _ := queryHTTP(t, f, "GET", f.app1.ID, f.operatorToken, "context"); status != 401 {
				t.Fatal("management credential accepted as query Agent", status)
			}
			ordinary, installation := newAgentFixture(t, f, []string{domain.AgentAutonomous})
			status, out := agentTokenRequest(t, f, ordinary, url.Values{"grant_type": {"client_credentials"}, "scope": {"api.read"}, "installation_id": {installation.ID}})
			if status != 200 {
				t.Fatal(status)
			}
			if status, _ = queryHTTP(t, f, "GET", f.app1.ID, out["access_token"].(string), "context"); status != 403 {
				t.Fatal("business audience accepted for query", status)
			}
			parts := strings.Split(token, ".")
			var claims map[string]any
			raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
			_ = json.Unmarshal(raw, &claims)
			claims["scope"] = "authova.admin"
			raw, _ = json.Marshal(claims)
			parts[1] = base64.RawURLEncoding.EncodeToString(raw)
			if status, _ = queryHTTP(t, f, "GET", f.app1.ID, strings.Join(parts, "."), "context"); status != 401 {
				t.Fatal("tampered query proof accepted", status)
			}
		})
		t.Run("configuration-connections-page-without-secrets", func(t *testing.T) {
			ids := map[string]bool{}
			if err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
				for n := 0; n < 111; n++ {
					id := application.ID()
					ids[id] = true
					if err := tx.PutConnection(domain.Connection{ID: id, Name: "Paged provider", ProviderType: "github", ClientID: "upstream-id-not-for-agents", Secret: "hidden-provider-secret", Status: "active", Version: 1}); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			cursor := ""
			for n := 0; n < 20; n++ {
				status, body := queryHTTP(t, f, "GET", f.app1.ID, token, "configuration?limit=17&cursor="+url.QueryEscape(cursor))
				if status != 200 {
					t.Fatal(status)
				}
				var result struct {
					Connections []struct {
						ID string `json:"id"`
					} `json:"connections"`
					Next string `json:"connections_next_cursor"`
				}
				if json.Unmarshal(body, &result) != nil || len(result.Connections) > 17 || strings.Contains(string(body), "hidden-provider-secret") || strings.Contains(string(body), "upstream-id-not-for-agents") {
					t.Fatal("invalid configuration projection")
				}
				for _, c := range result.Connections {
					if seen[c.ID] {
						t.Fatal("repeated connection")
					}
					seen[c.ID] = true
				}
				if result.Next == "" {
					break
				}
				cursor = result.Next
			}
			for id := range ids {
				if !seen[id] {
					t.Fatal("configuration silently truncated connections")
				}
			}
		})
		t.Run("read-only-and-strict-input", func(t *testing.T) {
			for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
				if status, _ := queryHTTP(t, f, method, f.app1.ID, token, "users/"+f.user1.ID); status != 405 {
					t.Fatal("query accepted mutation method", method, status)
				}
			}
			for _, path := range []string{"users?limit=101", "users?limit=1&limit=2", "context?app_id=" + f.app2.ID, "users?url=https://example.test", "grants?tenant_id=a&tenant_id=b", "users?limit=%ZZ"} {
				if status, _ := queryHTTP(t, f, "GET", f.app1.ID, token, path); status != 400 {
					t.Fatal("ambiguous or arbitrary query accepted", path, status)
				}
			}
			r, _ := http.NewRequest("POST", f.server.URL+"/api/v1/apps/"+f.app1.ID+"/users/"+f.user1.ID+"/disable", strings.NewReader(`{"reason":"attempted escalation"}`))
			r.Header.Set("Authorization", "Bearer "+token)
			r.Header.Set("Content-Type", "application/json")
			res, err := f.http.Do(r)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != 403 {
				t.Fatal("query token became a management operator", res.StatusCode)
			}
		})
		t.Run("query-audience-is-not-an-implicit-management-role", func(t *testing.T) {
			c, secret, err := f.service.CreateClient(ctx, f.operatorToken, f.scope1, domain.Client{Name: "Ordinary machine", Kind: "confidential", Scopes: []string{"authova.query.context"}, Audiences: []string{domain.QueryAudience(f.app1.ID)}, Grants: []string{"client_credentials"}}, "plain-query-"+application.ID())
			if err != nil {
				t.Fatal(err)
			}
			status, result := agentTokenRequest(t, f, application.CreatedAgent{Agent: domain.Agent{ClientID: c.ClientID, Kind: c.Kind}, Secret: secret}, url.Values{"grant_type": {"client_credentials"}, "scope": {"authova.query.context"}})
			if status != 200 {
				t.Fatal(status)
			}
			if status, _ = queryHTTP(t, f, "GET", f.app1.ID, result["access_token"].(string), "context"); status != 403 {
				t.Fatal("ordinary machine became query identity", status)
			}
			delegated, err := f.service.CreateAgent(ctx, f.operatorToken, f.scope1, application.AgentCreate{Name: "Delegated owner", Kind: "confidential", Modes: []string{domain.AgentDelegated}, Scopes: []string{"authova.query.context"}, Resources: []string{domain.QueryAudience(f.app1.ID)}, RedirectURIs: []string{f.server.URL + "/test-callback"}, TokenTTL: 120, GrantTTL: 3600}, "delegated-query-"+application.ID())
			if err != nil {
				t.Fatal(err)
			}
			installation, err := f.service.CreateAgentInstallation(ctx, f.operatorToken, f.scope1, application.AgentInstallationCreate{AgentID: delegated.Agent.ID, UserID: f.user1.ID, Resource: domain.QueryAudience(f.app1.ID), Scopes: []string{"authova.query.context"}, Modes: []string{domain.AgentDelegated}, ResponsibleParty: "owner", Reason: "mode isolation regression", Expires: time.Now().Add(time.Hour).Unix()}, "delegate-install-"+application.ID())
			if err != nil {
				t.Fatal(err)
			}
			if err = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return tx.SetAdminRole(f.user1.ID, "owner") }); err != nil {
				t.Fatal(err)
			}
			out := issueDelegatedScopes(t, f, delegated, installation, []string{"authova.query.context"})
			if status, _ = queryHTTP(t, f, "GET", f.app1.ID, out["access_token"].(string), "context"); status != 403 {
				t.Fatal("delegated user's owner role became query authority", status)
			}
		})
		t.Run("tenant-facts-cannot-become-global-access", func(t *testing.T) {
			_, first, tenantToken := newQueryIdentity(t, f, "tenant-one", queryScopes())
			_, other, _ := newQueryIdentity(t, f, "tenant-two", queryScopes())
			for _, path := range []string{"configuration", "users", "users/" + f.user1.ID, "users/" + f.user1.ID + "/sessions", "agents", "audit", "grants?tenant_id=tenant-two", "installations?tenant_id=tenant-two"} {
				if status, _ := queryHTTP(t, f, "GET", f.app1.ID, tenantToken, path); status != 403 {
					t.Fatal("tenant credential widened", path, status)
				}
			}
			for _, path := range []string{"installations/" + other.ID, "grants/" + other.AutonomousGrantID} {
				if status, _ := queryHTTP(t, f, "GET", f.app1.ID, tenantToken, path); status != 404 {
					t.Fatal("cross-tenant object visible", path, status)
				}
			}
			for _, path := range []string{"installations", "grants"} {
				status, body := queryHTTP(t, f, "GET", f.app1.ID, tenantToken, path)
				if status != 200 || strings.Contains(string(body), "tenant-two") || !strings.Contains(string(body), first.AgentID) {
					t.Fatal("tenant page leaked or lost facts", status)
				}
			}
		})
		t.Run("committed-revocation-and-policy", func(t *testing.T) {
			_, _, fresh := newQueryIdentity(t, f, "", []string{"authova.query.context"})
			if err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
				app, err := tx.App()
				if err != nil {
					return err
				}
				app.Status = "suspended"
				app.Version++
				return tx.UpdateAppConfig(app)
			}); err != nil {
				t.Fatal(err)
			}
			if status, _ := queryHTTP(t, f, "GET", f.app1.ID, fresh, "context"); status != 401 {
				t.Fatal("suspended App remained queryable", status)
			}
			if err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
				app, err := tx.App()
				if err != nil {
					return err
				}
				app.Status = "active"
				app.Version++
				return tx.UpdateAppConfig(app)
			}); err != nil {
				t.Fatal(err)
			}
			status, _ := agentJSON(t, f, "POST", "/agent-grants/"+i.AutonomousGrantID+"/revoke", application.AgentVersionedReason{Version: 1, Reason: "query read access removed"})
			if status != 200 {
				t.Fatal(status)
			}
			for n := 0; n < 3; n++ {
				if status, _ := queryHTTP(t, f, "GET", f.app1.ID, token, "context"); status != 401 {
					t.Fatal("cached authorization survived revoke", status)
				}
			}
		})
	})
}

type queryUnavailableStore struct{ ports.Store }

func (queryUnavailableStore) App(context.Context, domain.Scope, bool, func(ports.AppTx) error) error {
	return domain.ErrUnavailable
}

func TestAgentQueryDependencyFailure(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		_, _, token := newQueryIdentity(t, f, "", []string{"authova.query.context"})
		service := *f.service
		service.Store = queryUnavailableStore{Store: f.store}
		provider, err := protocol.New(&service)
		if err != nil {
			t.Fatal(err)
		}
		h := (&transport.Server{Service: &service, Provider: provider, Limiter: transport.NewLocalLimiter()}).Handler()
		r := httptest.NewRequest("GET", "/api/v1/apps/"+f.app1.ID+"/query/context", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 503 {
			t.Fatal("dependency failure became authorization result", w.Code)
		}
	})
}
