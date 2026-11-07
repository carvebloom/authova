package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

func agentAPIAs(t *testing.T, f *fixture, app, token, method, path string, input any) (int, []byte) {
	t.Helper()
	var body []byte
	if input != nil {
		var err error
		body, err = json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
	}
	r, err := http.NewRequest(method, f.server.URL+"/api/v1/apps/"+app+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "review-"+application.ID())
	response, err := f.http.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err = io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, body
}

func TestAgentRejectedRefreshDoesNotConsumeCredential(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		a, install := newAgentFixture(t, f, []string{domain.AgentDelegated})
		issued := issueDelegated(t, f, a, install)
		refresh := issued["refresh_token"].(string)
		initial := requireActive(t, f, issued["access_token"].(string), true)
		for _, rejected := range []url.Values{
			{"installation_id": {application.ID()}},
			{"resource": {"urn:authova:other"}},
		} {
			rejected.Set("grant_type", "refresh_token")
			rejected.Set("refresh_token", refresh)
			status, out := agentTokenRequest(t, f, a, rejected)
			if status != 400 || out["access_token"] != nil {
				t.Fatalf("invalid refresh parameters accepted: HTTP %d", status)
			}
		}
		status, out := agentTokenRequest(t, f, a, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
		if status != 200 {
			t.Fatalf("rejected request consumed valid refresh credential: HTTP %d", status)
		}
		current := requireActive(t, f, out["access_token"].(string), true)
		if current["grant_id"] != initial["grant_id"] || current["tenant_id"] != install.TenantID {
			t.Fatal("refresh changed authorization context")
		}
		// A genuinely replayed successful credential must still revoke its family.
		status, _ = agentTokenRequest(t, f, a, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
		if status == 200 {
			t.Fatal("successful refresh credential replayed")
		}
		requireActive(t, f, out["access_token"].(string), false)
	})
}

func TestManagementCredentialRequiresExplicitAdminScope(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		token := f.managementTokenWithScopes("openid profile offline_access")
		status, _ := agentAPIAs(t, f, f.app1.ID, token, "GET", "/agents", nil)
		if status != 403 {
			t.Fatalf("identity-only Console credential authorized management: HTTP %d", status)
		}
	})
}

func TestManagementRejectsLegacyBusinessClient(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		ctx := context.Background()
		// Model a legacy/imported row, deliberately outside current registration
		// validation. Neither public Client nor Agent provisioning permits this
		// reserved audience. Management must enforce its own credential purpose.
		f.client.Audiences = []string{application.ManagementAudience(f.app1.ID)}
		f.client.Scopes = append(f.client.Scopes, "authova.admin")
		if err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return tx.PutClient(f.client) }); err != nil {
			t.Fatal(err)
		}
		code, verifier := f.codeWithScopes("openid offline_access authova.admin")
		status, token := f.exchange(code, verifier)
		if status != 200 {
			t.Fatalf("legacy credential issuance failed: HTTP %d", status)
		}
		if err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
			keys, e := tx.Keys()
			if e != nil {
				return e
			}
			verified, e := secure.VerifyAccess(token.Access, f.app1.Issuer, keys)
			if e != nil {
				return e
			}
			session, e := tx.Session(verified.SessionID, true)
			if e != nil {
				return e
			}
			session.MFATime = time.Now().Unix()
			if e = tx.PutSession(session); e != nil {
				return e
			}
			return tx.SetAdminRole(f.user1.ID, "owner")
		}); err != nil {
			t.Fatal(err)
		}
		status, _ = agentAPIAs(t, f, f.app1.ID, token.Access, "GET", "/agents", nil)
		if status != 403 {
			t.Fatalf("legacy business token became a Console credential: HTTP %d", status)
		}
	})
}

func TestAgentCredentialAndInstallationCannotBeSubstituted(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		one, first := newAgentFixture(t, f, []string{domain.AgentAutonomous})
		two, second := newAgentFixture(t, f, []string{domain.AgentAutonomous})
		for _, pair := range []struct {
			agent   application.CreatedAgent
			install domain.AgentInstallation
		}{{one, second}, {two, first}} {
			status, result := agentTokenRequest(t, f, pair.agent, url.Values{"grant_type": {"client_credentials"}, "installation_id": {pair.install.ID}, "scope": {"api.read"}})
			if status != 400 || result["access_token"] != nil {
				t.Fatalf("another Agent's installation accepted: HTTP %d", status)
			}
		}
		status, result := agentTokenRequest(t, f, one, url.Values{"grant_type": {"client_credentials"}, "installation_id": {first.ID}, "scope": {"api.read"}, "user_id": {f.user1.ID}, "tenant_id": {"forged-tenant"}, "agent_mode": {domain.AgentDelegated}})
		if status != 200 {
			t.Fatalf("control credential request: HTTP %d", status)
		}
		token := result["access_token"].(string)
		claims := requireActive(t, f, token, true)
		if claims["sub"] != one.Agent.ClientID || claims["act"] != nil || claims["tenant_id"] != first.TenantID || claims["agent_mode"] != domain.AgentAutonomous {
			t.Fatal("caller-supplied fields changed autonomous identity")
		}
		r, _ := http.NewRequest("POST", f.app1.Issuer+"/oauth/introspect", bytes.NewBufferString(url.Values{"token": {token}}.Encode()))
		r.SetBasicAuth(one.Agent.ClientID, one.Secret)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response, err := f.http.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != 403 {
			t.Fatalf("Agent credentials were accepted as resource credentials: HTTP %d", response.StatusCode)
		}
		status, _ = agentJSON(t, f, "PATCH", "/clients/"+one.Agent.ClientID, map[string]any{"config_version": 1, "allowed_introspection_audiences": []string{first.Resource}})
		if status != 403 {
			t.Fatalf("legacy client edit granted resource introspection: HTTP %d", status)
		}
	})
}

func TestAgentCrossAppHTTPBoundaries(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		a, install := newAgentFixture(t, f, []string{domain.AgentAutonomous})
		for _, path := range []string{"/agents/" + a.Agent.ID, "/agent-installations/" + install.ID, "/agent-grants/" + install.AutonomousGrantID} {
			status, _ := agentAPIAs(t, f, f.app2.ID, f.operatorToken, "GET", path, nil)
			if status != 404 {
				t.Fatalf("cross-App object read %s: HTTP %d", path, status)
			}
		}
		status, _ := agentAPIAs(t, f, f.app2.ID, f.operatorToken, "POST", "/agent-grants/"+install.AutonomousGrantID+"/revoke", application.AgentVersionedReason{Version: 1, Reason: "cross App attempt"})
		if status != 404 {
			t.Fatalf("cross-App grant mutation: HTTP %d", status)
		}
		form := url.Values{"grant_type": {"client_credentials"}, "scope": {"api.read"}, "installation_id": {install.ID}}
		r, _ := http.NewRequest("POST", f.app2.Issuer+"/oauth/token", bytes.NewBufferString(form.Encode()))
		r.SetBasicAuth(a.Agent.ClientID, a.Secret)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response, err := f.http.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatalf("foreign App accepted Agent credentials: HTTP %d", response.StatusCode)
		}
		manager := f.managementToken()
		status, _ = agentAPIAs(t, f, f.app2.ID, manager, "GET", "/agents", nil)
		if status != 403 && status != 401 && status != 404 {
			t.Fatalf("App-scoped manager read another App: HTTP %d", status)
		}
	})
}

func TestAgentTenantAndUserGrantIsolation(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		a, first := newAgentFixture(t, f, []string{domain.AgentDelegated})
		other := domain.User{ID: application.ID(), AppID: f.app1.ID, Email: "other-review@example.test", DisplayName: "Other tenant user", Status: "active", Epoch: 1, PasswordHash: f.user1.PasswordHash, CreatedAt: time.Now().UTC()}
		ctx := context.Background()
		if err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
			return tx.CreateUser(other, "email", f.service.Digest(f.app1.ID, "identifier:email", other.Email))
		}); err != nil {
			t.Fatal(err)
		}
		second, err := f.service.CreateAgentInstallation(ctx, f.operatorToken, f.scope1, application.AgentInstallationCreate{AgentID: a.Agent.ID, TenantID: "customer-b", UserID: other.ID, ResponsibleParty: "trusted tenant provisioner", Resource: first.Resource, Modes: first.Modes, Scopes: first.Scopes, Expires: first.Expires, Reason: "separate explicit tenant approval"}, "review-"+application.ID())
		if err != nil {
			t.Fatal(err)
		}
		one := issueDelegated(t, f, a, first)
		grantOne := requireActive(t, f, one["access_token"].(string), true)["grant_id"].(string)
		jar, _ := cookiejar.New(nil)
		secondUser := *f
		secondUser.user1 = other
		secondUser.http = &http.Client{Jar: jar, CheckRedirect: f.http.CheckRedirect, Timeout: 15 * time.Second}
		two := issueDelegated(t, &secondUser, a, second)
		grantTwo := requireActive(t, f, two["access_token"].(string), true)["grant_id"].(string)
		// The same Agent may hold independent grants; a refresh cannot choose the
		// other approved installation to borrow that tenant or user's authority.
		status, out := agentTokenRequest(t, f, a, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {one["refresh_token"].(string)}, "installation_id": {second.ID}})
		if status != 400 || out["access_token"] != nil {
			t.Fatal("refresh switched tenant installation")
		}
		status, out = agentTokenRequest(t, f, a, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {one["refresh_token"].(string)}, "tenant_id": {second.TenantID}, "user_id": {other.ID}})
		if status != 200 {
			t.Fatalf("valid refresh failed after switch rejection: HTTP %d", status)
		}
		claims := requireActive(t, f, out["access_token"].(string), true)
		if claims["tenant_id"] != first.TenantID || claims["sub"] != f.user1.ID || claims["grant_id"] != grantOne {
			t.Fatal("untrusted request parameters changed bound identity")
		}
		page := f.app1.Issuer + "/ui/account/agents"
		if err := f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
			grants, err := tx.AgentGrants(domain.AgentFilter{UserID: f.user1.ID, Limit: 100})
			if err != nil {
				return err
			}
			if len(grants) != 1 || grants[0].ID != grantOne {
				t.Fatalf("bound user grant query returned %d grants", len(grants))
			}
			return nil
		}); err != nil {
			t.Fatalf("fixture grant query: %v (cause: %v)", err, errors.Unwrap(err))
		}
		response, err := f.http.Get(page + "?user_id=" + other.ID + "&tenant_id=" + second.TenantID)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || !bytes.Contains(body, []byte(grantOne)) || bytes.Contains(body, []byte(grantTwo)) {
			t.Fatal("user grant list trusted caller-supplied subject/tenant")
		}
		csrf := csrfPattern.FindSubmatch(body)
		if len(csrf) != 2 {
			t.Fatal("missing account CSRF binding")
		}
		status, _ = f.post(page+"/"+grantTwo+"/revoke", url.Values{"_csrf": {string(csrf[1])}, "confirm": {"revoke"}, "config_version": {"1"}}, false)
		if status != 404 {
			t.Fatalf("user revoked another tenant/user grant: HTTP %d", status)
		}
		requireActive(t, f, two["access_token"].(string), true)
		status, _ = f.post(page+"/"+grantOne+"/revoke", url.Values{"_csrf": {string(csrf[1])}, "confirm": {"revoke"}, "config_version": {"1"}}, false)
		if status != 200 {
			t.Fatal("own grant revocation failed")
		}
		requireActive(t, f, out["access_token"].(string), false)
		requireActive(t, f, two["access_token"].(string), true)
		if err = f.store.App(ctx, f.scope2, true, func(tx ports.AppTx) error { return tx.PutAgentGrant(domain.AgentGrant{AppID: f.app1.ID}) }); !errors.Is(err, domain.ErrForbidden) {
			t.Fatal("repository accepted foreign App facts")
		}
	})
}
