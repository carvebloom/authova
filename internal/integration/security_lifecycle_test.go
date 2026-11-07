package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/protocol"
	"github.com/carvebloom/authova/internal/secure"
	"github.com/carvebloom/authova/internal/securityevents"
	"github.com/carvebloom/authova/internal/transport"
)

func TestSecurityLifecycle(t *testing.T) {
	configured := false
	for _, dialect := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(dialect) + "_DSN")
		if dsn == "" {
			continue
		}
		configured = true
		t.Run(dialect, func(t *testing.T) {
			t.Run("app-policy-current-flows-and-scope", func(t *testing.T) { setup(t, dialect, dsn).policyLifecycle(t) })
			t.Run("policy-aware-safe-unlink", func(t *testing.T) { setup(t, dialect, dsn).policyUnlink(t) })
			t.Run("user-restore-never-revives-session", func(t *testing.T) { setup(t, dialect, dsn).enableLifecycle(t) })
			t.Run("client-secret-overlap-revocation-and-readback", func(t *testing.T) { setup(t, dialect, dsn).secretLifecycle(t) })
			t.Run("signing-key-emergency-multiple-providers", func(t *testing.T) { setup(t, dialect, dsn).keyLifecycle(t) })
			t.Run("mfa-replace-recovery-and-proof-reuse", func(t *testing.T) { setup(t, dialect, dsn).mfaLifecycle(t) })
			t.Run("failed-authentication-structured-events", func(t *testing.T) { setup(t, dialect, dsn).securityEventLifecycle(t) })
			t.Run("auditor-and-cross-app-management", func(t *testing.T) { setup(t, dialect, dsn).lifecyclePermissions(t) })
		})
	}
	if !configured {
		t.Skip("real PostgreSQL/MySQL required")
	}
}
func (f *fixture) securityFlow(t *testing.T, purpose string) domain.Flow {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"RedirectURI": f.server.URL + "/test-callback", "Scopes": f.client.Scopes, "Audiences": f.client.Audiences})
	flow := domain.Flow{ID: secure.Random(32), ClientID: f.client.ClientID, Purpose: domain.FlowPurpose(purpose), Step: "choose_method", Data: data, Expires: time.Now().Add(10 * time.Minute).Unix(), CSRF: secure.Random(32)}
	if e := f.store.App(context.Background(), f.scope1, true, func(tx ports.AppTx) error { return tx.PutFlow(flow) }); e != nil {
		t.Fatal(e)
	}
	return flow
}
func (f *fixture) policyLifecycle(t *testing.T) {
	ctx := context.Background()
	flow := f.securityFlow(t, "authorize")
	if e := f.service.PasswordLogin(ctx, f.scope1, flow.ID, "email", f.user1.Email, password); e != nil {
		t.Fatal(e)
	}
	policy := domain.DefaultAuthenticationPolicy()
	policy.PasswordLoginEnabled = false
	policy.RegistrationEnabled = false
	policy.EmailOTPEnabled = false
	policy.FederationEnabled = false
	status, b := f.managementJSON("PATCH", "/authentication-config", map[string]any{"config_version": 1, "reason": "restrict app1 methods", "policy": policy})
	if status != 200 {
		t.Fatal("policy update", status, string(b))
	}
	status, _ = f.managementJSON("PATCH", "/authentication-config", map[string]any{"config_version": 1, "reason": "stale update", "policy": policy})
	if status != 409 {
		t.Fatal("stale policy accepted", status)
	}
	status, _ = f.managementJSON("PATCH", "/authentication-config", map[string]any{"config_version": 2, "reason": "partial payload", "policy": map[string]bool{"registration_enabled": true}})
	if status != 400 {
		t.Fatal("partial policy accepted", status)
	}
	fresh := f.securityFlow(t, "authorize")
	if e := f.service.PasswordLogin(ctx, f.scope1, fresh.ID, "email", f.user1.Email, password); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("disabled password accepted", e)
	}
	if _, e := f.service.Complete(ctx, f.scope1, flow.ID); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("old authenticated flow bypassed policy", e)
	}
	if _, e := f.service.RequestChallenge(ctx, f.scope1, fresh.ID, "register", "email", "new@example.test", true); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("closed registration accepted", e)
	}
	if _, e := f.service.RequestChallenge(ctx, f.scope1, fresh.ID, "login", "email", f.user1.Email, true); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("disabled email OTP accepted", e)
	}
	if e := f.store.App(ctx, f.scope2, false, func(tx ports.AppTx) error {
		a, e := tx.App()
		if a.AuthenticationPolicy() != domain.DefaultAuthenticationPolicy() {
			t.Fatal("app2 policy changed")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
}
func (f *fixture) enableLifecycle(t *testing.T) {
	ctx := context.Background()
	handle := f.sessionFor(t, f.user1)
	status, _ := f.managementJSON("POST", "/users/"+f.user1.ID+"/disable", map[string]any{"reason": "test reversible ban"})
	if status != 200 {
		t.Fatal("disable", status)
	}
	status, _ = f.managementJSON("POST", "/users/"+f.user1.ID+"/enable", map[string]any{"reason": "stale restore", "session_epoch": f.user1.Epoch})
	if status != 409 {
		t.Fatal("stale restore accepted", status)
	}
	status, b := f.managementJSON("POST", "/users/"+f.user1.ID+"/enable", map[string]any{"reason": "restore after review", "session_epoch": f.user1.Epoch + 1})
	if status != 200 {
		t.Fatal("restore", status, string(b))
	}
	if e := f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
		_, _, e := f.service.AuthenticatedSession(tx, f.scope1, handle, false)
		return e
	}); e == nil {
		t.Fatal("restored old session")
	}
	status, _ = f.managementJSON("POST", "/users/"+f.user1.ID+"/enable", map[string]any{"reason": "duplicate restore", "session_epoch": f.user1.Epoch + 2})
	if status != 409 {
		t.Fatal("active restore accepted", status)
	}
	if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
		u, e := tx.User(f.user1.ID, true)
		if e != nil {
			return e
		}
		u.Status = "deleted"
		return tx.PutUser(u)
	}); e != nil {
		t.Fatal(e)
	}
	status, _ = f.managementJSON("POST", "/users/"+f.user1.ID+"/enable", map[string]any{"reason": "must not revive deleted", "session_epoch": f.user1.Epoch + 2})
	if status != 409 {
		t.Fatal("deleted restored", status)
	}
}
func (f *fixture) secretLifecycle(t *testing.T) {
	old := f.secret
	status, b := f.managementJSON("POST", "/clients/"+f.client.ClientID+"/secret/rotate", map[string]any{"config_version": 1, "reason": "routine credential rotation", "grace_seconds": 60})
	var rotated struct {
		Client domain.Client
		Secret string `json:"client_secret"`
	}
	if status != 200 || json.Unmarshal(b, &rotated) != nil || rotated.Secret == "" {
		t.Fatal("rotation", status)
	}
	if rotated.Client.ClientID != f.client.ClientID || rotated.Client.Version != 2 {
		t.Fatal("client identity/version changed")
	}
	status, read := f.managementJSON("GET", "/clients/"+f.client.ClientID, nil)
	if status != 200 || bytes.Contains(read, []byte(rotated.Secret)) || bytes.Contains(read, []byte(old)) {
		t.Fatal("GET leaked secret")
	}
	for _, secret := range []string{old, rotated.Secret} {
		f.secret = secret
		status, _ = f.post(f.app1.Issuer+"/oauth/token", mapForm("grant_type", "client_credentials", "scope", "api.read"), true)
		if status != 200 {
			t.Fatal("overlap authentication", status)
		}
	}
	status, _ = f.managementJSON("POST", "/clients/"+f.client.ClientID+"/secret/rotate", map[string]any{"config_version": 2, "reason": "overlap overwrite", "grace_seconds": 0})
	if status != 409 {
		t.Fatal("overlap silently overwritten", status)
	}
	status, _ = f.managementJSON("POST", "/clients/"+f.client.ClientID+"/secret/revoke-previous", map[string]any{"config_version": 2, "reason": "finish cutover"})
	if status != 200 {
		t.Fatal("previous revoke", status)
	}
	f.secret = old
	status, _ = f.post(f.app1.Issuer+"/oauth/token", mapForm("grant_type", "client_credentials", "scope", "api.read"), true)
	if status != 401 {
		t.Fatal("old secret still authenticates", status)
	}
	f.secret = rotated.Secret
	status, b = f.post(f.app1.Issuer+"/oauth/token", mapForm("grant_type", "client_credentials", "scope", "api.read"), true)
	var tok tokens
	if status != 200 || json.Unmarshal(b, &tok) != nil || !f.active(tok.Access) {
		t.Fatal("new secret failed token/introspection", status)
	}
}
func (f *fixture) keyLifecycle(t *testing.T) {
	status, b := f.post(f.app1.Issuer+"/oauth/token", mapForm("grant_type", "client_credentials", "scope", "api.read"), true)
	var tok tokens
	if status != 200 || json.Unmarshal(b, &tok) != nil || !f.active(tok.Access) {
		t.Fatal("initial token")
	}
	// Prewarm a separate provider to test cross-replica cache invalidation.
	other, e := protocol.New(f.service)
	if e != nil {
		t.Fatal(e)
	}
	handler := (&transport.Server{Service: f.service, Provider: other, Limiter: transport.NewLocalLimiter()}).Handler()
	checkOther := func() bool {
		r, _ := http.NewRequest("POST", f.app1.Issuer+"/oauth/introspect", strings.NewReader(url.Values{"token": {tok.Access}}.Encode()))
		r.SetBasicAuth(f.client.ClientID, f.secret)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		var data struct{ Active bool }
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &data) != nil {
			t.Fatal("replica check", rec.Code)
		}
		return data.Active
	}
	if !checkOther() {
		t.Fatal("replica initial token inactive")
	}
	status, b = f.managementJSON("GET", "/keys", nil)
	var listing struct {
		Items []struct {
			ID      string `json:"key_id"`
			Current bool
		}
		Version int64 `json:"config_version"`
	}
	if status != 200 || json.Unmarshal(b, &listing) != nil {
		t.Fatal("key listing")
	}
	key := ""
	for _, k := range listing.Items {
		if k.Current {
			key = k.ID
		}
	}
	status, _ = f.managementJSON("POST", "/keys/rotate", map[string]any{"reason": "normal signer rotation"})
	if status != 200 || !f.active(tok.Access) || !checkOther() {
		t.Fatal("normal rotation revoked existing token", status)
	}
	status, _ = f.managementJSON("POST", "/keys/"+key+"/revoke", map[string]any{"config_version": listing.Version, "reason": "stale emergency action"})
	if status != 409 {
		t.Fatal("stale emergency accepted", status)
	}
	status, b = f.managementJSON("POST", "/keys/"+key+"/revoke", map[string]any{"config_version": listing.Version + 1, "reason": "compromised retired key"})
	if status != 200 || f.active(tok.Access) || checkOther() {
		t.Fatal("revocation failed across warm caches", status, string(b))
	}
	resp, e := f.http.Get(f.app1.Issuer + "/keys")
	if e != nil {
		t.Fatal(e)
	}
	jwks, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if bytes.Contains(jwks, []byte(key)) {
		t.Fatal("revoked key remains in JWKS")
	}
	status, b = f.managementJSON("GET", "/keys", nil)
	if status != 200 || json.Unmarshal(b, &listing) != nil {
		t.Fatal("updated key metadata")
	}
	current := ""
	for _, k := range listing.Items {
		if k.Current {
			current = k.ID
		}
	}
	status, b = f.managementJSON("POST", "/keys/"+current+"/revoke", map[string]any{"config_version": listing.Version, "reason": "compromised current signer"})
	var replacement struct {
		New string `json:"new_key_id"`
	}
	if status != 200 || json.Unmarshal(b, &replacement) != nil || replacement.New == "" || replacement.New == current {
		t.Fatal("current signer not replaced atomically", status)
	}
	status, b = f.post(f.app1.Issuer+"/oauth/token", mapForm("grant_type", "client_credentials", "scope", "api.read"), true)
	if status != 200 || json.Unmarshal(b, &tok) != nil || !f.active(tok.Access) || !checkOther() {
		t.Fatal("replacement signer cannot issue/verify", status)
	}

}
func (f *fixture) mfaLifecycle(t *testing.T) {
	ctx := context.Background()
	seed := secure.NewTOTP()
	lastCode := "last-independent-recovery-code"
	if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
		u, e := tx.User(f.user1.ID, true)
		if e != nil {
			return e
		}
		u.TOTPSecret = seed
		u.RecoveryDigests = []string{f.service.Digest(f.app1.ID, "mfa-recovery", lastCode)}
		return tx.PutUser(u)
	}); e != nil {
		t.Fatal(e)
	}
	handle := f.sessionFor(t, f.user1)
	if _, e := f.service.BeginMFAReplacement(ctx, f.scope1, handle, "wrong-proof", secure.Random(32)); !errors.Is(e, domain.ErrUnauthenticated) {
		t.Fatal("cookie alone allowed MFA replacement", e)
	}
	flow, e := f.service.BeginMFAReplacement(ctx, f.scope1, handle, lastCode, secure.Random(32))
	if e != nil {
		t.Fatal(e)
	}
	other, e := f.service.BeginMFAReplacement(ctx, f.scope1, handle, lastCode, secure.Random(32))
	if e != nil {
		t.Fatal("setup prematurely consumed last recovery code", e)
	}
	if _, e = f.service.ConfirmMFAReplacement(ctx, f.scope1, flow.ID, "invalid-new-factor"); !errors.Is(e, domain.ErrUnauthenticated) {
		t.Fatal("unconfirmed new factor accepted", e)
	}
	codes, e := f.service.ConfirmMFAReplacement(ctx, f.scope1, flow.ID, secure.TOTP(flow.PendingTOTP, time.Now().Unix()/30))
	if e != nil || len(codes) != 10 {
		t.Fatal("replacement", e)
	}
	if _, e = f.service.ConfirmMFAReplacement(ctx, f.scope1, other.ID, secure.TOTP(other.PendingTOTP, time.Now().Unix()/30)); e == nil {
		t.Fatal("competing setup survived credential epoch change")
	}
	if e = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
		_, _, e := f.service.AuthenticatedSession(tx, f.scope1, handle, false)
		return e
	}); e == nil {
		t.Fatal("old session survived MFA replacement")
	}
	var user domain.User
	if e = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error { var e error; user, e = tx.User(f.user1.ID, false); return e }); e != nil {
		t.Fatal(e)
	}
	if user.TOTPSecret != flow.PendingTOTP || len(user.RecoveryDigests) != 10 {
		t.Fatal("factor/code state incomplete")
	}
	handle = f.sessionFor(t, user)
	if _, e = f.service.RegenerateRecoveryCodes(ctx, f.scope1, handle, secure.TOTP(user.TOTPSecret, time.Now().Unix()/30)); !errors.Is(e, domain.ErrUnauthenticated) {
		t.Fatal("TOTP proof replay accepted", e)
	}
	// The implementation accepts the standard adjacent time step. No sleep or
	// direct modification of credential state is used to bypass replay checks.
	newCodes, e := f.service.RegenerateRecoveryCodes(ctx, f.scope1, handle, secure.TOTP(user.TOTPSecret, time.Now().Unix()/30+1))
	if e != nil || len(newCodes) != 10 {
		t.Fatal("code regeneration", e)
	}
	login := f.securityFlow(t, "authorize")
	if e = f.service.PasswordLogin(ctx, f.scope1, login.ID, "email", user.Email, password); e != nil {
		t.Fatal(e)
	}
	if e = f.service.RecoveryMFA(ctx, f.scope1, login.ID, codes[0]); !errors.Is(e, domain.ErrUnauthenticated) {
		t.Fatal("old recovery set remained valid", e)
	}
	if e = f.service.RecoveryMFA(ctx, f.scope1, login.ID, newCodes[0]); e != nil {
		t.Fatal("recovery proof", e)
	}
	if _, e = f.service.Complete(ctx, f.scope1, login.ID); e == nil {
		t.Fatal("recovery bypassed new authenticator confirmation")
	}
	if e = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error { var e error; login, e = tx.Flow(login.ID, false); return e }); e != nil {
		t.Fatal(e)
	}
	if login.Step != "mfa_replace_confirm" || login.PendingTOTP == "" {
		t.Fatal("recovery has no replacement step")
	}
	recovered, e := f.service.ConfirmMFAReplacement(ctx, f.scope1, login.ID, secure.TOTP(login.PendingTOTP, time.Now().Unix()/30))
	if e != nil || len(recovered) != 10 {
		t.Fatal("recovery replacement", e)
	}
	if _, e = f.service.Complete(ctx, f.scope1, login.ID); e != nil {
		t.Fatal("recovered account cannot complete login", e)
	}
}
func (f *fixture) securityEventLifecycle(t *testing.T) {
	ctx := context.Background()
	flow := f.securityFlow(t, "authorize")
	resp, e := f.http.Get(f.app1.Issuer + "/ui/flows/" + flow.ID)
	if e != nil {
		t.Fatal(e)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	csrf := csrfPattern.FindSubmatch(body)
	if len(csrf) != 2 {
		t.Fatal("csrf")
	}
	var logs bytes.Buffer
	sink := securityevents.New(slog.New(slog.NewJSONHandler(&logs, nil)), 16)
	p, e := protocol.New(f.service)
	if e != nil {
		t.Fatal(e)
	}
	handler := (&transport.Server{Service: f.service, Provider: p, Limiter: transport.NewLocalLimiter(), SecurityEvents: sink}).Handler()
	sentinel := "never-log-this-submitted-password"
	form := url.Values{"_csrf": {string(csrf[1])}, "kind": {"email"}, "identifier": {f.user1.Email}, "password": {sentinel}}
	r, _ := http.NewRequest("POST", f.app1.Issuer+"/ui/flows/"+flow.ID+"/password", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", f.server.URL)
	r.Header.Set("X-Forwarded-For", "203.0.113.19")
	r.RemoteAddr = "127.0.0.1:24111"
	location, _ := url.Parse(f.app1.Issuer + "/ui/flows/" + flow.ID)
	for _, cookie := range f.http.Jar.Cookies(location) {
		r.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	if e = sink.Shutdown(ctx); e != nil {
		t.Fatal(e)
	}
	if rec.Code != 400 {
		t.Fatal("wrong password status", rec.Code)
	}
	var event map[string]any
	if json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &event) != nil {
		t.Fatal("no structured failure record")
	}
	if event["app_id"] != f.app1.ID || event["outcome"] != "rejected" || event["reason"] != "invalid_proof" || event["source_ref"] != f.service.Digest(f.app1.ID, "security-source", "127.0.0.1") || event["request_id"] == "" {
		t.Fatal("incomplete or untrusted event context")
	}
	for _, secret := range []string{sentinel, f.user1.Email, "203.0.113.19"} {
		if bytes.Contains(logs.Bytes(), []byte(secret)) {
			t.Fatal("security event leaked input")
		}
	}
}
func (f *fixture) lifecyclePermissions(t *testing.T) {
	ctx := context.Background()
	token := f.managementToken()
	f.operatorToken = token
	if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return tx.SetAdminRole(f.user1.ID, "auditor") }); e != nil {
		t.Fatal(e)
	}
	status, _ := f.managementJSON("GET", "/keys", nil)
	if status != 200 {
		t.Fatal("auditor key metadata read", status)
	}
	requests := []struct {
		method, path string
		body         any
	}{
		{"PATCH", "/authentication-config", map[string]any{"config_version": 1, "reason": "must reject auditor", "policy": domain.DefaultAuthenticationPolicy()}},
		{"POST", "/clients/" + f.client.ClientID + "/secret/rotate", map[string]any{"config_version": 1, "reason": "must reject auditor", "grace_seconds": 0}},
		{"POST", "/users/" + f.user1.ID + "/enable", map[string]any{"reason": "must reject auditor", "session_epoch": 2}},
	}
	for _, request := range requests {
		status, _ = f.managementJSON(request.method, request.path, request.body)
		if status != 403 {
			t.Fatal("auditor mutation accepted", request.path, status)
		}
	}
	r, _ := http.NewRequest("GET", f.server.URL+"/api/v1/apps/"+f.app2.ID+"/keys", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	resp, e := f.http.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal("cross-app manager accessed key metadata", resp.StatusCode)
	}
}

func (f *fixture) policyUnlink(t *testing.T) {
	ctx := context.Background()
	c := f.createConnection(t, "remaining provider")
	handle := f.sessionFor(t, f.user1)
	if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return tx.BindExternal(c.ID, "bound-provider-subject", f.user1.ID) }); e != nil {
		t.Fatal(e)
	}
	policy := domain.DefaultAuthenticationPolicy()
	policy.PasswordLoginEnabled = false
	policy.EmailOTPEnabled = false
	policy.PhoneOTPEnabled = false
	status, _ := f.managementJSON("PATCH", "/authentication-config", map[string]any{"config_version": 1, "reason": "only federation can authenticate", "policy": policy})
	if status != 200 {
		t.Fatal(status)
	}
	if e := f.service.UnlinkExternal(ctx, f.scope1, handle, c.ID, true, true); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("disabled local methods counted as a usable alternative", e)
	}
	policy.EmailOTPEnabled = true
	status, _ = f.managementJSON("PATCH", "/authentication-config", map[string]any{"config_version": 2, "reason": "enable verified email login", "policy": policy})
	if status != 200 {
		t.Fatal(status)
	}
	if e := f.service.UnlinkExternal(ctx, f.scope1, handle, c.ID, true, false); e != nil {
		t.Fatal("usable allowed alias rejected", e)
	}
}
