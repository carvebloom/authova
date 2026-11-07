package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

func agentDatabases(t *testing.T, fn func(*testing.T, *fixture)) {
	t.Helper()
	configured := false
	for _, db := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(db) + "_DSN")
		if dsn == "" {
			continue
		}
		configured = true
		t.Run(db, func(t *testing.T) { fn(t, setup(t, db, dsn)) })
	}
	if !configured {
		t.Skip("requires real PostgreSQL/MySQL DSNs")
	}
}
func agentJSON(t *testing.T, f *fixture, method, path string, in any) (int, []byte) {
	t.Helper()
	var raw []byte
	if in != nil {
		raw, _ = json.Marshal(in)
	}
	r, e := http.NewRequest(method, f.server.URL+"/api/v1/apps/"+f.app1.ID+path, bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+f.operatorToken)
	r.Header.Set("Idempotency-Key", "agent-test-"+application.ID())
	resp, e := f.http.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}
func newAgentFixture(t *testing.T, f *fixture, modes []string) (application.CreatedAgent, domain.AgentInstallation) {
	t.Helper()
	input := application.AgentCreate{Name: "工单助手", Description: "Bounded test Agent", Kind: "confidential", Modes: modes, Scopes: []string{"api.read", "api.write"}, Resources: []string{"urn:authova:integration", "urn:authova:other"}, RedirectURIs: []string{f.server.URL + "/test-callback"}, TokenTTL: 120, GrantTTL: 3600}
	status, b := agentJSON(t, f, "POST", "/agents", input)
	if status != 201 {
		t.Fatalf("create Agent %d: %s", status, b)
	}
	var a application.CreatedAgent
	if json.Unmarshal(b, &a) != nil || a.Agent.ID == "" || a.Secret == "" {
		t.Fatal("missing creation acknowledgement")
	}
	in := application.AgentInstallationCreate{AgentID: a.Agent.ID, TenantID: "customer-a", UserID: f.user1.ID, ResponsibleParty: "tenant-provisioner", Resource: "urn:authova:integration", Scopes: []string{"api.read"}, Modes: modes, Expires: time.Now().Add(24 * time.Hour).Unix(), Reason: "customer approved installation"}
	if !contains(modes, domain.AgentDelegated) {
		in.UserID = ""
	}
	status, b = agentJSON(t, f, "POST", "/agent-installations", in)
	if status != 201 {
		t.Fatalf("install Agent %d: %s", status, b)
	}
	var i domain.AgentInstallation
	if json.Unmarshal(b, &i) != nil || i.ID == "" {
		t.Fatal("missing installation")
	}
	return a, i
}
func contains(values []string, want string) bool {
	for _, s := range values {
		if s == want {
			return true
		}
	}
	return false
}
func agentTokenRequest(t *testing.T, f *fixture, a application.CreatedAgent, form url.Values) (int, map[string]any) {
	t.Helper()
	r, _ := http.NewRequest("POST", f.app1.Issuer+"/oauth/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if a.Agent.Kind == "confidential" {
		r.SetBasicAuth(a.Agent.ClientID, a.Secret)
	} else {
		form.Set("client_id", a.Agent.ClientID)
		r.Body = io.NopCloser(strings.NewReader(form.Encode()))
		r.ContentLength = int64(len(form.Encode()))
	}
	resp, e := f.http.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if json.Unmarshal(b, &out) != nil {
		t.Fatalf("token response %d is not JSON", resp.StatusCode)
	}
	return resp.StatusCode, out
}
func decodeAgentClaims(t *testing.T, raw string) map[string]any {
	t.Helper()
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		t.Fatal("not a JWT")
	}
	b, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		t.Fatal(e)
	}
	var c map[string]any
	if json.Unmarshal(b, &c) != nil {
		t.Fatal("bad JWT payload")
	}
	return c
}
func agentIntrospect(t *testing.T, f *fixture, token string) map[string]any {
	t.Helper()
	status, b := f.post(f.app1.Issuer+"/oauth/introspect", url.Values{"token": {token}}, true)
	if status != 200 {
		t.Fatalf("introspection %d: %s", status, b)
	}
	var c map[string]any
	if json.Unmarshal(b, &c) != nil {
		t.Fatal("invalid introspection")
	}
	return c
}
func requireActive(t *testing.T, f *fixture, token string, want bool) map[string]any {
	t.Helper()
	c := agentIntrospect(t, f, token)
	if c["active"] != want {
		t.Fatalf("active=%v want=%v", c["active"], want)
	}
	return c
}
func issueDelegated(t *testing.T, f *fixture, a application.CreatedAgent, i domain.AgentInstallation) map[string]any {
	return issueDelegatedScopes(t, f, a, i, []string{"api.read"})
}

func issueDelegatedScopes(t *testing.T, f *fixture, a application.CreatedAgent, i domain.AgentInstallation, scopes []string) map[string]any {
	t.Helper()
	verifier := secure.Random(32)
	h := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {a.Agent.ClientID}, "installation_id": {i.ID}, "resource": {i.Resource}, "redirect_uri": {f.server.URL + "/test-callback"}, "response_type": {"code"}, "scope": {"openid offline_access " + strings.Join(scopes, " ")}, "state": {"agent-state"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(h[:])}, "code_challenge_method": {"S256"}}
	resp, e := f.http.Get(f.app1.Issuer + "/authorize?" + q.Encode())
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	csrf := csrfPattern.FindSubmatch(b)
	if len(csrf) < 2 {
		t.Fatalf("Agent login failed %d: %s", resp.StatusCode, b)
	}
	flow := resp.Request.URL.String()
	status, b := f.post(flow+"/password", url.Values{"_csrf": {string(csrf[1])}, "kind": {"email"}, "identifier": {f.user1.Email}, "password": {password}}, false)
	if status != 200 {
		t.Fatalf("Agent login %d: %s", status, b)
	}
	if !bytes.Contains(b, []byte(a.Agent.Name)) || !bytes.Contains(b, []byte(i.Resource)) {
		t.Fatal("Agent consent omits identity or resource")
	}
	csrf = csrfPattern.FindSubmatch(b)
	if len(csrf) < 2 {
		t.Fatal("missing consent csrf")
	}
	r, _ := http.NewRequest("POST", flow+"/complete", strings.NewReader(url.Values{"_csrf": {string(csrf[1])}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", f.server.URL)
	resp, e = f.http.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 302 {
		t.Fatalf("consent %d: %s", resp.StatusCode, b)
	}
	location, e := url.Parse(resp.Header.Get("Location"))
	if e != nil {
		t.Fatal(e)
	}
	code := location.Query().Get("code")
	if code == "" || location.Query().Get("state") != "agent-state" {
		t.Fatal("missing authorization code/state")
	}
	status, out := agentTokenRequest(t, f, a, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {f.server.URL + "/test-callback"}})
	if status != 200 {
		t.Fatalf("delegated exchange %d: %v", status, out)
	}
	return out
}

func TestAgentAutonomousLifecycle(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		a, i := newAgentFixture(t, f, []string{domain.AgentAutonomous})
		form := url.Values{"grant_type": {"client_credentials"}, "scope": {"api.read"}, "installation_id": {i.ID}, "resource": {i.Resource}}
		status, out := agentTokenRequest(t, f, a, form)
		if status != 200 {
			t.Fatalf("machine token %d: %v", status, out)
		}
		token := out["access_token"].(string)
		c := requireActive(t, f, token, true)
		if c["agent_id"] != a.Agent.ID || c["installation_id"] != i.ID || c["tenant_id"] != i.TenantID || c["grant_id"] != i.AutonomousGrantID || c["agent_mode"] != "autonomous" {
			t.Fatalf("missing identity context: %v", c)
		}
		claims := decodeAgentClaims(t, token)
		if claims["act"] != nil || claims["sub"] != a.Agent.ClientID || claims["exp"].(float64)-claims["iat"].(float64) > 120 {
			t.Fatal("autonomous token impersonated user or exceeded TTL")
		}
		for _, change := range []struct{ name, value string }{{"installation_id", ""}, {"installation_id", application.ID()}, {"scope", "api.write"}, {"resource", "urn:authova:other"}} {
			t.Run(change.name+change.value, func(t *testing.T) {
				q := url.Values{}
				for k, v := range form {
					q[k] = append([]string{}, v...)
				}
				q.Set(change.name, change.value)
				status, out := agentTokenRequest(t, f, a, q)
				if status == 200 || out["access_token"] != nil {
					t.Fatal("unapproved Agent token issued")
				}
			})
		}
		status, b := agentJSON(t, f, "POST", "/agent-grants/"+i.AutonomousGrantID+"/revoke", application.AgentVersionedReason{Version: 1, Reason: "tenant withdrew permission"})
		if status != 200 {
			t.Fatalf("revoke %d %s", status, b)
		}
		requireActive(t, f, token, false)
		status, _ = agentTokenRequest(t, f, a, form)
		if status == 200 {
			t.Fatal("revoked grant minted token")
		}
		status, b = agentJSON(t, f, "POST", "/agent-installations/"+i.ID+"/renew", application.AgentVersionedReason{Version: i.Version, Reason: "approved replacement grant"})
		if status != 200 {
			t.Fatalf("renew %d %s", status, b)
		}
		var fresh domain.AgentInstallation
		if json.Unmarshal(b, &fresh) != nil || fresh.AutonomousGrantID == i.AutonomousGrantID || fresh.Version != i.Version+1 {
			t.Fatal("renew failed to rotate grant")
		}
		status, out = agentTokenRequest(t, f, a, form)
		if status != 200 {
			t.Fatalf("renewed mint %d %v", status, out)
		}
		replacement := out["access_token"].(string)
		requireActive(t, f, replacement, true)
		requireActive(t, f, token, false)
		status, b = agentJSON(t, f, "PATCH", "/agent-installations/"+i.ID, application.AgentInstallationPatch{Version: fresh.Version, Status: "disabled", Reason: "stop installation"})
		if status != 200 {
			t.Fatalf("disable installation %d %s", status, b)
		}
		requireActive(t, f, replacement, false)
		status, _ = agentTokenRequest(t, f, a, form)
		if status == 200 {
			t.Fatal("disabled installation minted token")
		}
	})
}

func TestAgentDelegationConsentRefreshAndUserRevocation(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		a, i := newAgentFixture(t, f, []string{domain.AgentDelegated})
		out := issueDelegated(t, f, a, i)
		token := out["access_token"].(string)
		refresh := out["refresh_token"].(string)
		c := requireActive(t, f, token, true)
		grantID := c["grant_id"].(string)
		if c["sub"] != f.user1.ID || c["agent_mode"] != "delegated" || c["act"].(map[string]any)["sub"] != a.Agent.ID {
			t.Fatal("subject and actor attribution lost")
		}
		status, out := agentTokenRequest(t, f, a, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
		if status != 200 {
			t.Fatalf("refresh %d: %v", status, out)
		}
		refresh = out["refresh_token"].(string)
		newToken := out["access_token"].(string)
		c = requireActive(t, f, newToken, true)
		if c["grant_id"] != grantID {
			t.Fatal("refresh changed consent")
		}
		pageURL := f.app1.Issuer + "/ui/account/agents"
		resp, e := f.http.Get(pageURL)
		if e != nil {
			t.Fatal(e)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !bytes.Contains(b, []byte(grantID)) || !bytes.Contains(b, []byte(a.Agent.Name)) {
			t.Fatalf("account grant list %d", resp.StatusCode)
		}
		csrf := csrfPattern.FindSubmatch(b)
		if len(csrf) < 2 {
			t.Fatal("account csrf missing")
		}
		status, _ = f.post(pageURL+"/"+grantID+"/revoke", url.Values{"_csrf": {"forged"}, "config_version": {"1"}, "confirm": {"revoke"}}, false)
		if status != 403 {
			t.Fatal("forged csrf accepted")
		}
		requireActive(t, f, newToken, true)
		status, b = f.post(pageURL+"/"+grantID+"/revoke", url.Values{"_csrf": {string(csrf[1])}, "config_version": {"1"}, "confirm": {"revoke"}}, false)
		if status != 200 {
			t.Fatalf("self revoke %d %s", status, b)
		}
		requireActive(t, f, token, false)
		requireActive(t, f, newToken, false)
		status, _ = agentTokenRequest(t, f, a, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
		if status == 200 {
			t.Fatal("refresh resurrected withdrawn consent")
		}
		status, b = agentJSON(t, f, "GET", "/agent-grants?user_id="+url.QueryEscape(f.user1.ID), nil)
		if status != 200 {
			t.Fatal("management grant list failed")
		}
		var grants domain.Page[domain.AgentGrant]
		if json.Unmarshal(b, &grants) != nil || len(grants.Items) != 1 || grants.Items[0].Status != "revoked" {
			t.Fatal("revocation not authoritative")
		}
		// Normal RP authorization still works and is not labelled as an Agent.
		code, verifier := f.code()
		status, b = f.post(f.app1.Issuer+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {f.server.URL + "/test-callback"}}, true)
		if status != 200 {
			t.Fatalf("ordinary client regressed: %d", status)
		}
		var normal map[string]any
		json.Unmarshal(b, &normal)
		c = requireActive(t, f, normal["access_token"].(string), true)
		if c["agent_id"] != nil {
			t.Fatal("ordinary client became Agent")
		}
	})
}

func TestAgentIsolationValidationAndAudit(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		a, i := newAgentFixture(t, f, []string{domain.AgentDelegated, domain.AgentAutonomous})
		ctx := context.Background()
		if e := f.store.App(ctx, f.scope2, false, func(tx ports.AppTx) error { _, e := tx.Agent(a.Agent.ID, false); return e }); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal("Agent leaked across App")
		}
		if e := f.store.App(ctx, f.scope2, false, func(tx ports.AppTx) error { _, e := tx.AgentInstallation(i.ID, false); return e }); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal("installation leaked across App")
		}
		in := application.AgentInstallationCreate{AgentID: a.Agent.ID, TenantID: "other-tenant", ResponsibleParty: "owner", Resource: i.Resource, Scopes: []string{"api.read"}, Modes: []string{domain.AgentDelegated}, Expires: time.Now().Add(time.Hour).Unix(), Reason: "test untrusted tenant"}
		status, _ := agentJSON(t, f, "POST", "/agent-installations", in)
		if status != 400 {
			t.Fatal("tenant-scoped delegation accepted without pinned user")
		}
		in.UserID = f.user2.ID
		status, _ = agentJSON(t, f, "POST", "/agent-installations", in)
		if status != 404 {
			t.Fatal("cross App user binding accepted")
		}
		in.UserID = f.user1.ID
		in.Scopes = []string{"not.allowed"}
		status, _ = agentJSON(t, f, "POST", "/agent-installations", in)
		if status != 403 {
			t.Fatal("installation widened ceiling")
		}
		form := url.Values{"grant_type": {"client_credentials"}, "scope": {"api.read"}, "installation_id": {i.ID}}
		status, out := agentTokenRequest(t, f, a, form)
		if status != 200 {
			t.Fatalf("token %d %v", status, out)
		}
		token := out["access_token"].(string)
		status, b := agentJSON(t, f, "PATCH", "/agents/"+a.Agent.ID, map[string]any{"config_version": a.Agent.Version, "status": "disabled", "reason": "owner disabled Agent"})
		if status != 200 {
			t.Fatalf("Agent disable %d %s", status, b)
		}
		requireActive(t, f, token, false)
		status, _ = agentJSON(t, f, "PATCH", "/agents/"+a.Agent.ID, map[string]any{"config_version": a.Agent.Version, "status": "active", "reason": "stale edit"})
		if status != 409 {
			t.Fatal("Agent lost-update accepted")
		}
		err := f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
			events, e := tx.Audits("", 100)
			if e != nil {
				return e
			}
			seen := map[string]bool{}
			for _, v := range events {
				seen[v.Action] = true
			}
			for _, name := range []string{"agent.create", "agent.installation.create", "agent.grant.create", "agent.token.issue", "agent.update"} {
				if !seen[name] {
					t.Errorf("missing audit %s", name)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}
