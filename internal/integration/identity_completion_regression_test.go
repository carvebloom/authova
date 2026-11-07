package integration

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/carvebloom/authova/internal/protocol"
	"github.com/carvebloom/authova/internal/transport"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

func TestIdentityCompletionRegression(t *testing.T) {
	configured := false
	for _, driver := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(driver) + "_DSN")
		if dsn == "" {
			continue
		}
		configured = true
		t.Run(driver, func(t *testing.T) {
			for _, kind := range []string{"email", "phone"} {
				t.Run("otp-only-registration/"+kind, func(t *testing.T) {
					f := setupChannelsHostname(t, driver, dsn, false, true, "localhost")
					if !f.service.PasskeyAvailable(f.app1) {
						t.Fatal("fixture must support WebAuthn independently of App policy")
					}
					ctx := context.Background()
					if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
						a, e := tx.App()
						if e != nil {
							return e
						}
						p := domain.AuthenticationPolicy{RegistrationEnabled: true}
						if kind == "email" {
							p.EmailOTPEnabled = true
						} else {
							p.PhoneOTPEnabled = true
						}
						a.Policy = &p
						a.Version++
						return tx.UpdateAppConfig(a)
					}); e != nil {
						t.Fatal(e)
					}
					var message domain.Message
					f.service.Store = challengeCaptureStore{Store: f.store, message: &message}
					// Exercise the actual browser transport, cookies, CSRF and Code/PKCE path.
					// Phone delivery uses a captured encrypted outbox, not a real SMS provider.
					verifier := secure.Random(32)
					hash := sha256.Sum256([]byte(verifier))
					q := url.Values{"client_id": {f.client.ClientID}, "redirect_uri": {f.server.URL + "/test-callback"}, "response_type": {"code"}, "scope": {"openid email profile offline_access"}, "state": {"otp-registration"}, "nonce": {"otp-nonce"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "code_challenge_method": {"S256"}}
					res, e := f.http.Get(f.app1.Issuer + "/authorize?" + q.Encode())
					if e != nil {
						t.Fatal(e)
					}
					body, _ := io.ReadAll(res.Body)
					res.Body.Close()
					path := res.Request.URL.String()
					if res.StatusCode != 200 || !strings.Contains(string(body), `value="register"`) {
						t.Fatal("OTP-only registration unavailable", res.StatusCode)
					}
					if !strings.Contains(string(body), `data-otp-only-registration="true"`) {
						t.Fatal("deployment WebAuthn capability overrode disabled App Passkey policy")
					}
					if kind == "phone" && !strings.Contains(string(body), `<option value="email" disabled>`) {
						t.Fatal("phone-only registration offers the disabled email channel")
					}
					if kind == "phone" && !strings.Contains(string(body), `<option value="phone" selected>`) {
						t.Fatal("phone-only registration did not default to phone")
					}
					if kind == "email" && !strings.Contains(string(body), `<option value="phone" disabled>`) {
						t.Fatal("email-only registration offers the disabled phone channel")
					}
					submit := func(action string, form url.Values) *http.Response {
						t.Helper()
						csrf := csrfPattern.FindSubmatch(body)
						if len(csrf) != 2 {
							t.Fatal("CSRF missing")
						}
						form.Set("_csrf", string(csrf[1]))
						req, _ := http.NewRequest("POST", path+"/"+action, strings.NewReader(form.Encode()))
						req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
						req.Header.Set("Origin", f.server.URL)
						r, e := f.http.Do(req)
						if e != nil {
							t.Fatal(e)
						}
						body, _ = io.ReadAll(r.Body)
						r.Body.Close()
						if r.StatusCode >= 400 {
							t.Fatalf("%s rejected %d %s", action, r.StatusCode, string(body))
						}
						return r
					}
					destination := "new-otp@example.test"
					if kind == "phone" {
						destination = "+8613800138010"
					}
					submit("challenges", url.Values{"purpose": {"register"}, "channel": {kind}, "destination": {destination}})
					code := strings.Split(strings.TrimPrefix(message.Body, "Authova verification code: "), ".")[0]
					challenge := regexp.MustCompile(`name="challenge_id" value="([^"]+)"`).FindSubmatch(body)
					if len(challenge) != 2 {
						t.Fatal("challenge missing")
					}
					submit("verify", url.Values{"challenge_id": {string(challenge[1])}, "code": {code}})
					if !strings.Contains(string(body), `action="`+res.Request.URL.Path+`/register"`) {
						t.Fatal("verified OTP-only registration has no account-creation action")
					}
					if strings.Contains(string(body), `name="password"`) {
						t.Fatal("OTP-only registration demanded password")
					}
					submit("register", url.Values{"display_name": {"OTP User"}})
					r := submit("complete", url.Values{})
					location, e := r.Location()
					if e != nil || location.Query().Get("state") != "otp-registration" || location.Query().Get("code") == "" {
						t.Fatal("authorization code missing", e)
					}
					status, tok := f.exchange(location.Query().Get("code"), verifier)
					if status != 200 || !f.active(tok.Access) {
						t.Fatal("OTP user code/PKCE/introspection failed", status)
					}
					if e = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
						u, e := tx.FindUser(kind, f.service.Digest(f.app1.ID, "identifier:"+kind, destination))
						if e == nil && u.PasswordHash != "" {
							t.Fatal("OTP-only account got a password")
						}
						return e
					}); e != nil {
						t.Fatal(e)
					}
					// Current policy is rechecked for already verified registration proofs.
					flow := f.securityFlow(t, "authorize")
					id, e := f.service.RequestChallenge(ctx, f.scope1, flow.ID, "register", kind, "other-otp@example.test", true)
					if kind == "phone" { // Email text above is intentionally invalid as a phone.
						if !errors.Is(e, domain.ErrInvalid) {
							t.Fatal(e)
						}
						id, e = f.service.RequestChallenge(ctx, f.scope1, flow.ID, "register", kind, "+8613800138011", true)
					}
					if e != nil {
						t.Fatal(e)
					}
					proof := strings.Split(strings.TrimPrefix(message.Body, "Authova verification code: "), ".")[0]
					if e = f.service.VerifyChallenge(ctx, f.scope1, flow.ID, id, proof); e != nil {
						t.Fatal(e)
					}
					if e = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
						a, e := tx.App()
						if e != nil {
							return e
						}
						p := a.AuthenticationPolicy()
						p.RegistrationEnabled = false
						a.Policy = &p
						a.Version++
						return tx.UpdateAppConfig(a)
					}); e != nil {
						t.Fatal(e)
					}
					if e = f.service.Register(ctx, f.scope1, flow.ID, "", "must not be created"); !errors.Is(e, domain.ErrForbidden) {
						t.Fatal("stale registration policy bypass", e)
					}
				})
			}
			t.Run("invalid-token-key-load-bounded", func(t *testing.T) {
				f := setup(t, driver, dsn)
				code, v := f.code()
				status, tok := f.exchange(code, v)
				if status != 200 {
					t.Fatal(status)
				}
				var loads atomic.Int64
				s := *f.service
				s.Store = verificationCountingStore{Store: f.store, loads: &loads}
				p, e := protocol.New(&s)
				if e != nil {
					t.Fatal(e)
				}
				handler := (&transport.Server{Service: &s, Provider: p, Limiter: transport.NewLocalLimiter()}).Handler()
				introspect := func(token string) bool {
					t.Helper()
					r := httptest.NewRequest("POST", "/a/"+f.app1.Key+"/oauth/introspect", strings.NewReader(url.Values{"token": {token}}.Encode()))
					r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					r.SetBasicAuth(f.client.ClientID, f.secret)
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, r)
					if w.Code != 200 {
						t.Fatal("introspection failed", w.Code, w.Body.String())
					}
					var result struct{ Active bool }
					if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
						t.Fatal(e)
					}
					return result.Active
				}
				parts := strings.Split(tok.Access, ".")
				headerBytes, _ := base64.RawURLEncoding.DecodeString(parts[0])
				payloadBytes, _ := base64.RawURLEncoding.DecodeString(parts[1])
				var h, claims map[string]any
				json.Unmarshal(headerBytes, &h)
				json.Unmarshal(payloadBytes, &claims)
				originalID := claims["jti"]
				for i := 0; i < 30; i++ {
					h["kid"] = application.ID()
					claims["jti"] = application.ID()
					hb, _ := json.Marshal(h)
					pb, _ := json.Marshal(claims)
					raw := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(pb) + "." + parts[2]
					if introspect(raw) {
						t.Fatal("invalid token active")
					}
				}
				if loads.Load() != 0 {
					t.Fatal("unknown token caused key load", loads.Load())
				}
				claims["jti"] = originalID
				for i := 0; i < 30; i++ {
					h["kid"] = application.ID()
					hb, _ := json.Marshal(h)
					pb, _ := json.Marshal(claims)
					raw := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(pb) + "." + parts[2]
					if introspect(raw) {
						t.Fatal("unknown kid active")
					}
				}
				if loads.Load() != 1 {
					t.Fatal("random kids repeatedly loaded key set", loads.Load())
				}
				if !introspect(tok.Access) || loads.Load() != 1 {
					t.Fatal("known key did not reuse complete set")
				}
			})
			t.Run("session-pagination-and-isolation", func(t *testing.T) {
				f := setup(t, driver, dsn)
				ctx := context.Background()
				ids := map[string]bool{}
				oldest := ""
				if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
					for i := 0; i < 155; i++ {
						id := application.ID()
						if oldest == "" {
							oldest = id
						}
						ids[id] = true
						state := "active"
						if i%2 == 1 {
							state = "revoked"
						}
						if e := tx.PutSession(domain.Session{ID: id, UserID: f.user1.ID, ClientID: f.client.ClientID, HandleDigest: f.service.Digest(f.app1.ID, "test-session", id), Epoch: f.user1.Epoch, Status: state, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}); e != nil {
							return e
						}
					}
					return nil
				}); e != nil {
					t.Fatal(e)
				}
				seen := map[string]bool{}
				after := ""
				for page := 0; page < 20; page++ {
					status, b := f.managementJSON("GET", "/users/"+f.user1.ID+"/sessions?limit=17&cursor="+url.QueryEscape(after), nil)
					if status != 200 {
						t.Fatal(status, string(b))
					}
					var result domain.Page[domain.Session]
					if e := json.Unmarshal(b, &result); e != nil {
						t.Fatal(e)
					}
					if len(result.Items) > 17 {
						t.Fatal("limit ignored")
					}
					for _, s := range result.Items {
						if !ids[s.ID] || seen[s.ID] || s.UserID != f.user1.ID {
							t.Fatal("invalid or duplicate session")
						}
						seen[s.ID] = true
					}
					if result.NextCursor == nil {
						break
					}
					after = *result.NextCursor
				}
				if len(seen) != 155 {
					t.Fatal("incomplete enumeration", len(seen))
				}
				status, _ := f.managementJSON("DELETE", "/users/"+f.user1.ID+"/sessions/"+oldest, map[string]any{"reason": "oldest active session can be revoked"})
				if status != 204 {
					t.Fatal(status)
				}
				if e := f.store.App(ctx, f.scope2, false, func(tx ports.AppTx) error {
					page, e := tx.Sessions(f.user1.ID, "", 100)
					if e == nil && len(page) != 0 {
						t.Fatal("cross-App sessions leaked")
					}
					return e
				}); e != nil {
					t.Fatal(e)
				}
				status, _ = f.managementJSON("GET", "/users/"+f.user2.ID+"/sessions?limit=17&cursor="+url.QueryEscape(oldest), nil)
				if status != 404 {
					t.Fatal("cross-App user lookup accepted", status)
				}
			})
		})
	}
	if !configured {
		t.Skip("real PG/MySQL required")
	}
}

type verificationCountingStore struct {
	ports.Store
	loads *atomic.Int64
}

func (s verificationCountingStore) App(ctx context.Context, scope domain.Scope, write bool, fn func(ports.AppTx) error) error {
	return s.Store.App(ctx, scope, write, func(tx ports.AppTx) error { return fn(verificationCountingTx{AppTx: tx, loads: s.loads}) })
}

type verificationCountingTx struct {
	ports.AppTx
	loads *atomic.Int64
}

func (t verificationCountingTx) VerificationKeys() ([]domain.SigningKey, error) {
	t.loads.Add(1)
	return t.AppTx.VerificationKeys()
}
