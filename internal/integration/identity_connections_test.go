package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

func TestIdentityConnections(t *testing.T) {
	configured := false
	for _, driver := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(driver) + "_DSN")
		if dsn == "" {
			continue
		}
		configured = true
		t.Run(driver, func(t *testing.T) {
			f := setup(t, driver, dsn)
			t.Run("configuration", f.connectionConfigChecks)
			t.Run("federation-binding", f.connectionBindingChecks)
			t.Run("unlink-usable-credentials", f.unlinkChecks)
			t.Run("phone-registration-login-bind", f.phoneChecks)
		})
	}
	if !configured {
		t.Skip("real PostgreSQL/MySQL required")
	}
}
func (f *fixture) managementJSON(method, path string, body any) (int, []byte) {
	f.t.Helper()
	data, _ := json.Marshal(body)
	r, _ := http.NewRequest(method, f.server.URL+"/api/v1/apps/"+f.app1.ID+path, bytes.NewReader(data))
	r.Header.Set("Authorization", "Bearer "+f.operatorToken)
	r.Header.Set("Content-Type", "application/json")
	resp, e := f.http.Do(r)
	if e != nil {
		f.t.Fatal(e)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}
func (f *fixture) createConnection(t *testing.T, name string) domain.Connection {
	status, b := f.managementJSON("POST", "/connections", map[string]string{"name": name, "provider_type": "github", "client_id": application.ID(), "client_secret": "test-only-secret"})
	if status != 201 {
		t.Fatalf("create status %d", status)
	}
	var c domain.Connection
	if json.Unmarshal(b, &c) != nil || c.ProviderType != "github" || c.Version != 1 || strings.Contains(string(b), "test-only-secret") {
		t.Fatal("connection response invalid or leaked secret")
	}
	return c
}
func (f *fixture) connectionConfigChecks(t *testing.T) {
	c := f.createConnection(t, "Github test")
	status, _ := f.managementJSON("PATCH", "/connections/"+c.ID, map[string]any{"config_version": 1, "reason": "disable test", "status": "disabled"})
	if status != 200 {
		t.Fatal(status)
	}
	for _, patch := range []any{map[string]any{"config_version": 1, "reason": "stale", "name": "stale"}, map[string]any{"config_version": 2, "reason": "identity rewrite", "issuer": "https://other.example"}, map[string]any{"config_version": 2, "reason": "empty secret", "client_secret": ""}} {
		status, _ = f.managementJSON("PATCH", "/connections/"+c.ID, patch)
		if status != 409 && status != 400 {
			t.Fatal("invalid/stale patch accepted", status)
		}
	}
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, _ := f.managementJSON("PATCH", "/connections/"+c.ID, map[string]any{"config_version": 2, "reason": "concurrent rotation", "status": "active", "client_secret": "new-test-secret"})
			results <- status
		}()
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for status := range results {
		if status == 200 {
			success++
		}
		if status == 409 {
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("lost concurrent update", success, conflict)
	}
	status, b := f.managementJSON("GET", "/connections/"+c.ID, nil)
	if status != 200 || strings.Contains(string(b), "new-test-secret") {
		t.Fatal("read leaked secret")
	}
	r, _ := http.NewRequest("GET", f.server.URL+"/api/v1/apps/"+f.app2.ID+"/connections/"+c.ID, nil)
	r.Header.Set("Authorization", "Bearer "+f.operatorToken)
	resp, e := f.http.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal("cross-app connection read accepted")
	}
	status, b = f.managementJSON("GET", "/authentication-config", nil)
	if status != 200 || !strings.Contains(string(b), `"phone_otp_configured":false`) {
		t.Fatal("deployment capability hidden")
	}
}

type identityTransport func(*http.Request) (*http.Response, error)

func (f identityTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func (f *fixture) sessionFor(t *testing.T, u domain.User) string {
	t.Helper()
	handle := secure.Random(32)
	err := f.store.App(context.Background(), f.scope1, true, func(tx ports.AppTx) error {
		return tx.PutSession(domain.Session{ID: application.ID(), ClientID: f.client.ClientID, UserID: u.ID, Epoch: u.Epoch, Status: "active", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), AuthTime: time.Now().Unix(), HandleDigest: f.service.Digest(f.app1.ID, "session-handle", handle)})
	})
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse(f.server.URL)
	f.http.Jar.SetCookies(base, []*http.Cookie{{Name: "av_session_" + f.app1.Key, Value: handle, Path: "/a/" + f.app1.Key}})
	return handle
}
func (f *fixture) linkConnection(t *testing.T, c domain.Connection) (string, string) {
	t.Helper()
	resp, e := f.http.Get(f.app1.Issuer + "/ui/account")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	csrf := csrfPattern.FindSubmatch(b)
	if len(csrf) != 2 {
		t.Fatal("account csrf absent")
	}
	status, b := f.post(f.app1.Issuer+"/ui/account/link", url.Values{"_csrf": {string(csrf[1])}, "connection_id": {c.ID}}, false)
	if status != 200 {
		t.Fatalf("link start %d", status)
	}
	link, found := identityProviderURL(b)
	if !found {
		t.Fatal("upstream link absent")
	}
	u, _ := url.Parse(link)
	return u.Query().Get("state"), u.Query().Get("code_challenge")
}
func (f *fixture) connectionBindingChecks(t *testing.T) {
	old := f.service.UpstreamClient
	defer func() { f.service.UpstreamClient = old }()
	f.service.UpstreamClient = &http.Client{Transport: identityTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"id":123,"login":"provider-user"}`
		if r.URL.Host == "github.com" {
			r.ParseForm()
			if r.Form.Get("code_verifier") == "" {
				t.Error("PKCE missing")
			}
			body = `{"access_token":"test-transient","token_type":"bearer"}`
		} else if r.URL.Path == "/user/emails" {
			body = `[{"email":"same@example.com","verified":true,"primary":true}]`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	f.sessionFor(t, f.user1)
	c1, c2 := f.createConnection(t, "Provider one"), f.createConnection(t, "Provider two")
	for _, c := range []domain.Connection{c1, c2} {
		state, pkce := f.linkConnection(t, c)
		if pkce == "" {
			t.Fatal("PKCE challenge absent")
		}
		resp, e := f.http.Get(f.app1.Issuer + "/ui/federation/callback?state=" + url.QueryEscape(state) + "&code=test")
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatal("binding callback", resp.StatusCode)
		}
	}
	err := f.store.App(context.Background(), f.scope1, false, func(tx ports.AppTx) error {
		b, e := tx.ExternalBindings(f.user1.ID)
		if e == nil && len(b) != 2 {
			t.Fatal("multi-provider binding absent")
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	// Test the occupied identity using a different real App1 user instead.
	other := domain.User{ID: application.ID(), AppID: f.app1.ID, Status: "active", Epoch: 1, CreatedAt: time.Now()}
	err = f.store.App(context.Background(), f.scope1, true, func(tx ports.AppTx) error { return tx.CreateUser(other, "", "") })
	if err != nil {
		t.Fatal(err)
	}
	f.sessionFor(t, other)
	state, _ := f.linkConnection(t, c1)
	resp, e := f.http.Get(f.app1.Issuer + "/ui/federation/callback?state=" + url.QueryEscape(state) + "&code=test")
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 409 {
		t.Fatal("occupied identity stolen", resp.StatusCode)
	}
	f.sessionFor(t, f.user1)
	state, _ = f.linkConnection(t, c2)
	status, _ := f.managementJSON("PATCH", "/connections/"+c2.ID, map[string]any{"config_version": 1, "reason": "change during callback", "status": "disabled"})
	if status != 200 {
		t.Fatal(status)
	}
	resp, e = f.http.Get(f.app1.Issuer + "/ui/federation/callback?state=" + url.QueryEscape(state) + "&code=test")
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("changed connection flow survived", resp.StatusCode)
	}
	// A new provider account with an existing local email remains a distinct user.
	c3 := f.createConnection(t, "Provider login")
	flow := domain.Flow{ID: secure.Random(32), ClientID: f.client.ClientID, Step: "choose_method", Purpose: "authorize", Expires: time.Now().Add(time.Minute).Unix(), CSRF: secure.Random(32)}
	handle := secure.Random(32)
	flow.CookieDigest = f.service.Digest(f.app1.ID, "flow-cookie", handle)
	if err = f.store.App(context.Background(), f.scope1, true, func(tx ports.AppTx) error { return tx.PutFlow(flow) }); err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse(f.server.URL)
	f.http.Jar.SetCookies(base, []*http.Cookie{{Name: "av_flow_" + flow.ID, Value: handle, Path: "/a/" + f.app1.Key}})
	status, b := f.post(f.app1.Issuer+"/ui/flows/"+flow.ID+"/federation", url.Values{"_csrf": {flow.CSRF}, "connection_id": {c3.ID}}, false)
	if status != 200 {
		t.Fatal("federation login start", status)
	}
	link, found := identityProviderURL(b)
	if !found {
		t.Fatal("federation login URL absent")
	}
	destination, _ := url.Parse(link)
	callback := f.app1.Issuer + "/ui/federation/callback?state=" + url.QueryEscape(destination.Query().Get("state")) + "&code=test"
	resp, e = f.http.Get(callback)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("federation login callback", resp.StatusCode)
	}
	if err = f.store.App(context.Background(), f.scope1, false, func(tx ports.AppTx) error {
		current, e := tx.Flow(flow.ID, false)
		if e != nil {
			return e
		}
		if current.UserID == "" || current.UserID == f.user1.ID || current.Step != "consent" {
			t.Fatal("automatic email merge or failed provision")
		}
		alias, e := tx.FindUser("email", f.service.Digest(f.app1.ID, "identifier:email", "same@example.com"))
		if e == nil && alias.ID != f.user1.ID {
			t.Fatal("provider profile overwrote local alias")
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	resp, e = f.http.Get(callback)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("federation state replay accepted", resp.StatusCode)
	}

}
func (f *fixture) unlinkChecks(t *testing.T) {
	ctx := context.Background()
	u := domain.User{ID: application.ID(), AppID: f.app1.ID, Email: "profile-only@example.test", PasswordHash: "unusable-without-verified-alias", Status: "active", Epoch: 1, CreatedAt: time.Now()}
	c1, c2 := f.createConnection(t, "Unlink A"), f.createConnection(t, "Unlink B")
	e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
		if e := tx.CreateUser(u, "", ""); e != nil {
			return e
		}
		if e := tx.BindExternal(c1.ID, "subject-one", u.ID); e != nil {
			return e
		}
		return tx.BindExternal(c1.ID, "subject-two", u.ID)
	})
	if e != nil {
		t.Fatal(e)
	}
	aliasUser := domain.User{ID: application.ID(), AppID: f.app1.ID, Email: "same-profile@example.test", Status: "active", Epoch: 1, CreatedAt: time.Now()}
	e = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
		if e := tx.CreateUser(aliasUser, "", ""); e != nil {
			return e
		}
		digest := f.service.Digest(f.app1.ID, "identifier:email", aliasUser.Email)
		if e := tx.ReplaceIdentifier(aliasUser.ID, "email", digest, digest); e != nil {
			return e
		}
		owner, e := tx.FindUser("email", digest)
		if e == nil && owner.ID != aliasUser.ID {
			t.Fatal("verified profile alias owned by another user")
		}
		return e
	})
	if e != nil {
		t.Fatal("same-value profile failed to become login alias", e)
	}

	handle := f.sessionFor(t, u)
	if e = f.service.UnlinkExternal(ctx, f.scope1, handle, c1.ID, false, false); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("same-connection count or profile email allowed lockout", e)
	}
	e = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return tx.BindExternal(c2.ID, "subject-three", u.ID) })
	if e != nil {
		t.Fatal(e)
	}
	status, _ := f.managementJSON("PATCH", "/connections/"+c2.ID, map[string]any{"config_version": 1, "reason": "disabled alternative", "status": "disabled"})
	if status != 200 {
		t.Fatal(status)
	}
	if e = f.service.UnlinkExternal(ctx, f.scope1, handle, c1.ID, false, false); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("disabled provider counted as usable", e)
	}
	status, _ = f.managementJSON("PATCH", "/connections/"+c2.ID, map[string]any{"config_version": 2, "reason": "restore alternative", "status": "active"})
	if status != 200 {
		t.Fatal(status)
	}

	if e = f.service.UnlinkExternal(ctx, f.scope1, handle, c1.ID, false, false); e != nil {
		t.Fatal(e)
	}
	e = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
		_, _, e := f.service.AuthenticatedSession(tx, f.scope1, handle, false)
		return e
	})
	if !errors.Is(e, domain.ErrUnauthenticated) {
		t.Fatal("unlink did not revoke sessions")
	}
	u.Epoch++
	handle = f.sessionFor(t, u)
	if e = f.service.UnlinkExternal(ctx, f.scope1, handle, c2.ID, false, false); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("last binding removed", e)
	}
	if e = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return tx.BindExternal(c1.ID, "parallel-subject", u.ID) }); e != nil {
		t.Fatal(e)
	}
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, connection := range []string{c1.ID, c2.ID} {
		group.Add(1)
		go func() {
			defer group.Done()
			results <- f.service.UnlinkExternal(ctx, f.scope1, handle, connection, false, false)
		}()
	}
	group.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, domain.ErrUnauthenticated) && !errors.Is(err, domain.ErrConflict) {
			t.Fatal("unexpected concurrent unlink outcome", err)
		}
	}
	if success != 1 {
		t.Fatal("concurrent unlink removed all methods or did not progress", success)
	}
	if e = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
		bindings, e := tx.ExternalBindings(u.ID)
		if e == nil && len(bindings) != 1 {
			t.Fatal("concurrent unlink left wrong binding count")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}

}
func (f *fixture) phoneChecks(t *testing.T) {
	ctx := context.Background()
	phone := "+8613800138000"
	flow := domain.Flow{ID: secure.Random(32), Step: "choose_method", Purpose: "authorize", ClientID: f.client.ClientID, Expires: time.Now().Add(time.Minute).Unix()}
	e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return tx.PutFlow(flow) })
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.service.RequestChallenge(ctx, f.scope1, flow.ID, "register", "phone", phone, false); !errors.Is(e, domain.ErrUnavailable) {
		t.Fatal("unconfigured SMS accepted")
	}
	// Use a valid deterministic E.164 number; challenge proof is injected in the fixture, no real SMS is claimed.
	id, e := f.service.RequestChallenge(ctx, f.scope1, flow.ID, "register", "phone", phone, true)
	if e != nil {
		t.Fatal(e)
	}
	e = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
		c, e := tx.Challenge(id, true)
		if e != nil {
			return e
		}
		c.Digest = f.service.Digest(f.app1.ID, "otp:"+id, "123456")
		return tx.PutChallenge(c)
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = f.service.VerifyChallenge(ctx, f.scope1, flow.ID, id, "123456"); e != nil {
		t.Fatal(e)
	}
	if e = f.service.Register(ctx, f.scope1, flow.ID, password, "Phone user"); e != nil {
		t.Fatal(e)
	}
	login := domain.Flow{ID: secure.Random(32), Step: "choose_method", Purpose: "authorize", ClientID: f.client.ClientID, Expires: time.Now().Add(time.Minute).Unix()}
	e = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return tx.PutFlow(login) })
	if e != nil {
		t.Fatal(e)
	}
	if e = f.service.PasswordLogin(ctx, f.scope1, login.ID, "phone", phone, password); e != nil {
		t.Fatal(e)
	}
	otpFlow := domain.Flow{ID: secure.Random(32), Step: "choose_method", Purpose: "authorize", ClientID: f.client.ClientID, Expires: time.Now().Add(time.Minute).Unix()}
	if e = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return tx.PutFlow(otpFlow) }); e != nil {
		t.Fatal(e)
	}
	id, e = f.service.RequestChallenge(ctx, f.scope1, otpFlow.ID, "login", "phone", phone, true)
	if e != nil {
		t.Fatal(e)
	}
	f.setChallengeCode(t, otpFlow.ID, id)
	if e = f.service.VerifyChallenge(ctx, f.scope1, otpFlow.ID, id, "123456"); e != nil {
		t.Fatal(e)
	}
	if e = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
		flow, e := tx.Flow(otpFlow.ID, false)
		if e == nil && (flow.UserID == "" || flow.Step != "consent" || flow.AMR[0] != "otp") {
			t.Fatal("SMS login did not authenticate")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	handle := f.sessionFor(t, f.user1)
	var session domain.Session
	if e = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
		var e error
		session, e = tx.SessionByHandle(f.service.Digest(f.app1.ID, "session-handle", handle))
		return e
	}); e != nil {
		t.Fatal(e)
	}
	bind := domain.Flow{ID: secure.Random(32), Step: "choose_method", Purpose: "change_identifier", ClientID: f.client.ClientID, SessionID: session.ID, UserID: f.user1.ID, AuthEpoch: f.user1.Epoch, AuthTime: session.AuthTime, Expires: time.Now().Add(time.Minute).Unix()}
	if e = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return tx.PutFlow(bind) }); e != nil {
		t.Fatal(e)
	}
	id, e = f.service.RequestChallenge(ctx, f.scope1, bind.ID, "change_identifier", "phone", "+8613800138001", true)
	if e != nil {
		t.Fatal(e)
	}
	f.setChallengeCode(t, bind.ID, id)
	if e = f.service.VerifyChallenge(ctx, f.scope1, bind.ID, id, "123456"); e != nil {
		t.Fatal(e)
	}
	if e = f.service.BindVerifiedIdentifier(ctx, f.scope1, bind.ID); e != nil {
		t.Fatal(e)
	}
	if e = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
		u, e := tx.FindUser("phone", f.service.Digest(f.app1.ID, "identifier:phone", "+8613800138001"))
		if e == nil && (u.ID != f.user1.ID || u.Phone != "+8613800138001") {
			t.Fatal("phone binding lost account")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}

	e = f.store.App(ctx, f.scope2, true, func(tx ports.AppTx) error {
		return tx.CreateUser(domain.User{ID: application.ID(), AppID: f.app2.ID, Phone: phone, Status: "active", Epoch: 1, CreatedAt: time.Now()}, "phone", f.service.Digest(f.app2.ID, "identifier:phone", phone))
	})
	if e != nil {
		t.Fatal("phone cannot be reused in independent App", e)
	}
}

func (f *fixture) setChallengeCode(t *testing.T, flow, id string) {
	t.Helper()
	if e := f.store.App(context.Background(), f.scope1, true, func(tx ports.AppTx) error {
		c, e := tx.Challenge(id, true)
		if e != nil {
			return e
		}
		if c.FlowID != flow {
			return domain.ErrInvalid
		}
		c.Digest = f.service.Digest(f.app1.ID, "otp:"+id, "123456")
		return tx.PutChallenge(c)
	}); e != nil {
		t.Fatal(e)
	}
}
