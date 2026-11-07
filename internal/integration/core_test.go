// Package integration exercises the same semantics on real PostgreSQL and MySQL.
package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/protocol"
	"github.com/carvebloom/authova/internal/runtimehttp"
	"github.com/carvebloom/authova/internal/secure"
	"github.com/carvebloom/authova/internal/storage/sqlstore"
	"github.com/carvebloom/authova/internal/transport"
	coreoidc "github.com/coreos/go-oidc/v3/oidc"
)

const password = "authova integration test password"

type fixture struct {
	t              *testing.T
	store          *sqlstore.Store
	service        *application.Service
	server         *httptest.Server
	operatorToken  string
	operatorEmail  string
	operatorTOTP   string
	operatorAuthAt time.Time
	operatorReauth int
	app1, app2     domain.App
	scope1, scope2 domain.Scope
	client         domain.Client
	secret         string
	user1, user2   domain.User
	http           *http.Client
}

func setup(t *testing.T, dialect, dsn string) *fixture { return setupTransport(t, dialect, dsn, false) }

func setupTransport(t *testing.T, dialect, dsn string, production bool) *fixture {
	return setupChannels(t, dialect, dsn, production, false)
}
func setupChannels(t *testing.T, dialect, dsn string, production, phone bool, wrap ...func(http.Handler) http.Handler) *fixture {
	return setupChannelsHostname(t, dialect, dsn, production, phone, "", wrap...)
}

// setupChannelsHostname configures the canonical origin before creating Apps or
// serving requests. localhost exercises WebAuthn eligibility without changing
// any running fixture's origin or weakening its Origin/CSRF checks.
func setupChannelsHostname(t *testing.T, dialect, dsn string, production, phone bool, hostname string, wrap ...func(http.Handler) http.Handler) *fixture {
	t.Helper()
	ctx := context.Background()
	key, e := os.ReadFile(os.Getenv("AUTHOVA_MASTER_KEY_FILE"))
	if e != nil {
		key = bytes.Repeat([]byte{3}, 32)
	}
	v, e := secure.NewVault(key)
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewUnstartedServer(nil)
	scheme := "http://"
	if production {
		scheme = "https://"
	}
	address := server.Listener.Addr().String()
	if hostname != "" {
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			t.Fatal(err)
		}
		address = net.JoinHostPort(hostname, port)
	}
	base := scheme + address
	store, e := sqlstore.Open(ctx, dialect, dsn, base, v, 32)
	if e != nil {
		t.Fatal(e)
	}
	if e = store.CheckRuntime(ctx); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.Close() })
	service, e := application.New(store, v, base, !production, 2)
	if e != nil {
		t.Fatal(e)
	}
	secret := secure.NewTOTP()
	email := "test-" + application.ID() + "@example.com"
	hash, e := service.HashPassword(ctx, password)
	if e != nil {
		t.Fatal(e)
	}
	operator := domain.Operator{ID: application.ID(), Email: email, Role: "owner", Status: "active", Password: hash, TOTP: secret}
	if e = store.Platform(ctx, true, func(p ports.PlatformTx) error { return p.PutOperator(operator) }); e != nil {
		t.Fatal(e)
	}
	token, _, _, e := service.LoginOperator(ctx, email, password, secure.TOTP(secret, time.Now().Unix()/30))
	if e != nil {
		t.Fatal(e)
	}
	f := &fixture{t: t, store: store, service: service, server: server, operatorToken: token, operatorEmail: email, operatorTOTP: secret, operatorAuthAt: time.Now()}
	for i := 0; i < 2; i++ {
		app, e := service.CreateApp(ctx, token, "Test App", "test-"+strings.ReplaceAll(application.ID(), "-", "")[:12])
		if e != nil {
			t.Fatal(e)
		}
		scope, _ := domain.AppScope(app.ID)
		u := domain.User{ID: application.ID(), AppID: app.ID, DisplayName: "Integration User", Status: "active", Email: "same@example.com", Epoch: 1, CreatedAt: time.Now().UTC(), PasswordHash: hash}
		if e = store.App(ctx, scope, true, func(tx ports.AppTx) error {
			return tx.CreateUser(u, "email", service.Digest(app.ID, "identifier:email", u.Email))
		}); e != nil {
			t.Fatal(e)
		}
		if i == 0 {
			f.app1, f.scope1, f.user1 = app, scope, u
		} else {
			f.app2, f.scope2, f.user2 = app, scope, u
		}
	}
	c := domain.Client{Name: "Integration RP", Kind: "confidential", RedirectURIs: []string{base + "/test-callback"}, Scopes: []string{"openid", "email", "profile", "offline_access", "api.read"}, Audiences: []string{"urn:authova:integration"}, IntrospectionAudiences: []string{"urn:authova:integration"}, Grants: []string{"authorization_code", "refresh_token", "client_credentials"}}
	f.client, f.secret, e = service.CreateClient(ctx, token, f.scope1, c, "idem-"+application.ID())
	if e != nil {
		t.Fatal(e)
	}
	provider, e := protocol.New(service)
	if e != nil {
		t.Fatal(e)
	}
	limiter := transport.NewLocalLimiter()
	service.ChallengeLimiter = limiter
	api := &transport.Server{Service: service, Provider: provider, Limiter: limiter, EmailConfigured: true, PhoneConfigured: phone}
	server.Config.Handler = runtimehttp.NewServer(&http.Server{Handler: api.Handler()})
	for _, wrapper := range wrap {
		server.Config.Handler = wrapper(server.Config.Handler)
	}
	if production {
		server.StartTLS()
	} else {
		server.Start()
	}
	server.URL = base
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	f.http = server.Client()
	f.http.Jar = jar
	f.http.Timeout = 10 * time.Second
	f.http.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if r.URL.Path == "/test-callback" {
			return http.ErrUseLastResponse
		}
		return nil
	}
	return f
}
func (f *fixture) post(path string, form url.Values, basic bool) (int, []byte) {
	f.t.Helper()
	r, e := http.NewRequest("POST", path, strings.NewReader(form.Encode()))
	if e != nil {
		f.t.Fatal(e)
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", f.server.URL)
	if basic {
		r.SetBasicAuth(f.client.ClientID, f.secret)
	}
	resp, e := f.http.Do(r)
	if e != nil {
		f.t.Fatal(e)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

var csrfPattern = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

func (f *fixture) code() (string, string) {
	return f.codeWithScopes("openid email profile offline_access")
}

func (f *fixture) codeWithScopes(scopes string) (string, string) {
	f.t.Helper()
	verifier := secure.Random(32)
	hash := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {f.client.ClientID}, "redirect_uri": {f.server.URL + "/test-callback"}, "response_type": {"code"}, "scope": {scopes}, "state": {"test-state"}, "nonce": {"test-nonce"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "code_challenge_method": {"S256"}}
	resp, e := f.http.Get(f.app1.Issuer + "/authorize?" + q.Encode())
	if e != nil {
		f.t.Fatal(e)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	csrf := csrfPattern.FindSubmatch(b)
	if len(csrf) < 2 {
		f.t.Fatalf("login UI did not provide CSRF (status %d)", resp.StatusCode)
	}
	flow := resp.Request.URL.String()
	status, b := f.post(flow+"/password", url.Values{"_csrf": {string(csrf[1])}, "kind": {"email"}, "identifier": {"same@example.com"}, "password": {password}}, false)
	if status != 200 {
		f.t.Fatalf("password login status %d", status)
	}
	csrf = csrfPattern.FindSubmatch(b)
	if len(csrf) < 2 {
		f.t.Fatal("consent CSRF missing")
	}
	r, _ := http.NewRequest("POST", flow+"/complete", strings.NewReader(url.Values{"_csrf": {string(csrf[1])}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", f.server.URL)
	resp, e = f.http.Do(r)
	if e != nil {
		f.t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 302 {
		f.t.Fatalf("authorize callback failed with %d", resp.StatusCode)
	}
	location, e := url.Parse(resp.Header.Get("Location"))
	if e != nil {
		f.t.Fatal(e)
	}
	code := location.Query().Get("code")
	if code == "" || location.Query().Get("state") != "test-state" {
		f.t.Fatal("code/state missing")
	}
	return code, verifier
}

type tokens struct {
	Access  string `json:"access_token"`
	ID      string `json:"id_token"`
	Refresh string `json:"refresh_token"`
}

func (f *fixture) exchange(code, verifier string) (int, tokens) {
	status, b := f.post(f.app1.Issuer+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {f.server.URL + "/test-callback"}, "code_verifier": {verifier}}, true)
	var tok tokens
	json.Unmarshal(b, &tok)
	return status, tok
}
func (f *fixture) active(token string) bool {
	status, b := f.post(f.app1.Issuer+"/oauth/introspect", url.Values{"token": {token}}, true)
	if status != 200 {
		f.t.Fatalf("introspection status %d", status)
	}
	var response struct{ Active bool }
	if e := json.Unmarshal(b, &response); e != nil {
		f.t.Fatal(e)
	}
	return response.Active
}
func TestDatabases(t *testing.T) {
	configured := false
	for _, dialect := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(dialect) + "_DSN")
		if dsn == "" {
			continue
		}
		configured = true
		t.Run(dialect, func(t *testing.T) {
			f := setup(t, dialect, dsn)
			ctx := context.Background()
			t.Run("scope-and-atomicity", func(t *testing.T) {
				if f.user1.ID == f.user2.ID {
					t.Fatal("shared identity")
				}
				if e := f.store.App(ctx, domain.Scope{}, false, func(ports.AppTx) error { return nil }); !errors.Is(e, domain.ErrForbidden) {
					t.Fatal("empty scope accepted")
				}
				e := f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error { _, e := tx.User(f.user2.ID, false); return e })
				if !errors.Is(e, domain.ErrNotFound) {
					t.Fatal("cross-App read accepted")
				}
				e = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
					return tx.PutSession(domain.Session{ID: application.ID(), UserID: f.user2.ID, Status: "active", Epoch: 1, HandleDigest: secure.Random(32), ExpiresAt: time.Now().Add(time.Hour)})
				})
				if !errors.Is(e, domain.ErrInvalid) {
					t.Fatal("cross-App foreign key accepted")
				}
				e = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
					if e := tx.CreateUser(f.user1, "email", "different-digest"); e != nil {
						return e
					}
					return nil
				})
				if !errors.Is(e, domain.ErrConflict) {
					t.Fatal("duplicate primary key accepted")
				}
				driver := "pgx"
				if dialect == "mysql" {
					driver = "mysql"
				}
				db, e := sql.Open(driver, dsn)
				if e != nil {
					t.Fatal(e)
				}
				defer db.Close()
				if dialect == "postgres" {
					var n int
					if e = db.QueryRow("SELECT COUNT(*) FROM users").Scan(&n); e != nil || n != 0 {
						t.Fatal("unset PG scope bypassed RLS")
					}
				}
			})
			t.Run("OIDC-interoperability-and-revocation", func(t *testing.T) {
				code, verifier := f.code()
				status, tok := f.exchange(code, verifier)
				if status != 200 || tok.Access == "" || tok.ID == "" || tok.Refresh == "" {
					t.Fatalf("token exchange status %d", status)
				}
				rp, e := coreoidc.NewProvider(ctx, f.app1.Issuer)
				if e != nil {
					t.Fatal(e)
				}
				identity, e := rp.Verifier(&coreoidc.Config{ClientID: f.client.ClientID, SupportedSigningAlgs: []string{"RS256"}}).Verify(ctx, tok.ID)
				if e != nil || identity.Nonce != "test-nonce" {
					t.Fatal("ID token interoperability failed", e)
				}
				if e = identity.VerifyAccessToken(tok.Access); e != nil {
					t.Fatal("at_hash mismatch", e)
				}
				if !f.active(tok.Access) || f.active(tok.ID) {
					t.Fatal("access/ID token type separation")
				}
				if status, _ = f.exchange(code, verifier); status == 200 {
					t.Fatal("code replay accepted")
				}
				r, _ := http.NewRequest("GET", f.app1.Issuer+"/userinfo", nil)
				r.Header.Set("Authorization", "Bearer "+tok.Access)
				resp, e := f.http.Do(r)
				if e != nil {
					t.Fatal(e)
				}
				resp.Body.Close()
				if resp.StatusCode != 200 {
					t.Fatal("userinfo failed")
				}
				if e = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return f.service.DisableUser(tx, "test", f.app1.ID, f.user1.ID) }); e != nil {
					t.Fatal(e)
				}
				if f.active(tok.Access) {
					t.Fatal("disabled user remains active")
				}
				if e = f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
					u, e := tx.User(f.user1.ID, true)
					if e != nil {
						return e
					}
					u.Status = "active"
					return tx.PutUser(u)
				}); e != nil {
					t.Fatal(e)
				}
			})
			t.Run("code-concurrency-and-refresh-reuse", func(t *testing.T) {
				code, verifier := f.code()
				var statuses [2]int
				var granted [2]tokens
				var wg sync.WaitGroup
				for i := 0; i < 2; i++ {
					wg.Add(1)
					go func(i int) { defer wg.Done(); statuses[i], granted[i] = f.exchange(code, verifier) }(i)
				}
				wg.Wait()
				winner := -1
				for i, status := range statuses {
					if status == 200 {
						if winner != -1 {
							t.Fatal("code issued twice")
						}
						winner = i
					}
				}
				if winner < 0 {
					t.Fatalf("both code requests rejected: %v", statuses)
				}
				old := granted[winner]
				var refreshed [2]tokens
				for i := 0; i < 2; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						status, b := f.post(f.app1.Issuer+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {old.Refresh}}, true)
						statuses[i] = status
						json.Unmarshal(b, &refreshed[i])
					}(i)
				}
				wg.Wait()
				success := 0
				for i, status := range statuses {
					if status == 200 {
						success++
						if f.active(refreshed[i].Access) {
							t.Fatal("refresh reuse did not persist family revocation")
						}
					}
				}
				if success != 1 {
					t.Fatalf("refresh concurrency: %v", statuses)
				}
			})
			t.Run("last-owner-and-OTP-single-use", func(t *testing.T) {
				if e := f.store.App(ctx, f.scope2, true, func(tx ports.AppTx) error { return tx.SetAdminRole(f.user2.ID, "owner") }); e != nil {
					t.Fatal(e)
				}
				if e := f.store.App(ctx, f.scope2, true, func(tx ports.AppTx) error { return f.service.DisableUser(tx, "test", f.app2.ID, f.user2.ID) }); !errors.Is(e, domain.ErrConflict) {
					t.Fatal("last owner disabled")
				}
				flow := domain.Flow{ID: secure.Random(32), ClientID: f.client.ClientID, Step: "choose_method", Purpose: "authorize", Expires: time.Now().Add(time.Minute).Unix()}
				if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return tx.PutFlow(flow) }); e != nil {
					t.Fatal(e)
				}
				destination := "new-" + application.ID() + "@example.com"
				id, e := f.service.RequestChallenge(ctx, f.scope1, flow.ID, "register", "email", destination, true)
				if e != nil {
					t.Fatal(e)
				}
				code := ""
				// The outbox is shared across App fixtures. A worker lease can
				// contain notifications or OTPs for unrelated users; only this
				// App and destination are authoritative for this challenge.
				for batch := 0; batch < 100 && code == ""; batch++ {
					messages, err := f.store.LeaseMessages(ctx, ports.DeliveryLeaseRequest{Limit: 100, AppLimit: 100})
					if err != nil {
						t.Fatal(err)
					}
					if len(messages) == 0 {
						break
					}
					for _, m := range messages {
						if m.AppID != f.app1.ID || m.Destination != destination {
							continue
						}
						match := regexp.MustCompile(`code: ([0-9]{6})`).FindStringSubmatch(m.Body)
						if len(match) == 2 {
							code = match[1]
							break
						}
					}
				}
				if code == "" {
					t.Fatal("OTP not enqueued")
				}
				if e = f.service.VerifyChallenge(ctx, f.scope1, flow.ID, id, code); e != nil {
					t.Fatal(e)
				}
				if e = f.service.VerifyChallenge(ctx, f.scope1, flow.ID, id, code); e == nil {
					t.Fatal("OTP reused")
				}
				if e = f.service.Register(ctx, f.scope1, flow.ID, password, "Verified User"); e != nil {
					t.Fatal(e)
				}
			})
			t.Run("dependency-failure-and-persistent-OTP-budget", func(t *testing.T) { f.faultChecks(t) })
			t.Run("selfcare-password-and-erasure", func(t *testing.T) { f.selfcareChecks(t) })
			t.Run("app-management-MFA-and-live-role", func(t *testing.T) { f.managementChecks(t) })

		})
	}
	if !configured {
		t.Skip("set AUTHOVA_TEST_POSTGRES_DSN and AUTHOVA_TEST_MYSQL_DSN for real database tests")
	}
}
