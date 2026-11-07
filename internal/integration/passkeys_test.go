package integration

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
	"github.com/fxamacker/cbor/v2"
)

// This authenticator independently constructs COSE/CBOR and signs assertions.
// The production verifier parses and validates the real WebAuthn wire format.
type signedAuthenticator struct {
	key        *ecdsa.PrivateKey
	id, handle []byte
	origin     string
}

func newAuthenticator(t *testing.T, origin string) *signedAuthenticator {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	return &signedAuthenticator{key: key, id: []byte(secure.Random(32)), origin: origin}
}
func optionMap(t *testing.T, options any) map[string]any {
	t.Helper()
	b, e := json.Marshal(options)
	if e != nil {
		t.Fatal(e)
	}
	var m map[string]any
	if e = json.Unmarshal(b, &m); e != nil {
		t.Fatal(e)
	}
	return m["publicKey"].(map[string]any)
}
func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
func clientData(challenge, origin, kind string) []byte {
	b, _ := json.Marshal(map[string]any{"type": kind, "challenge": challenge, "origin": origin, "crossOrigin": false})
	return b
}
func (a *signedAuthenticator) register(t *testing.T, options any, origin string) []byte {
	t.Helper()
	p := optionMap(t, options)
	a.handle, _ = base64.RawURLEncoding.DecodeString(p["user"].(map[string]any)["id"].(string))
	key, e := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: a.key.X.FillBytes(make([]byte, 32)), -3: a.key.Y.FillBytes(make([]byte, 32))})
	if e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256([]byte(p["rp"].(map[string]any)["id"].(string)))
	data := append(hash[:], 0x45, 0, 0, 0, 0)
	data = append(data, make([]byte, 16)...)
	data = binary.BigEndian.AppendUint16(data, uint16(len(a.id)))
	data = append(data, a.id...)
	data = append(data, key...)
	att, e := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": data})
	if e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(map[string]any{"id": b64(a.id), "rawId": b64(a.id), "type": "public-key", "response": map[string]any{"clientDataJSON": b64(clientData(p["challenge"].(string), origin, "webauthn.create")), "attestationObject": b64(att), "transports": []string{"internal"}}, "clientExtensionResults": map[string]any{"credProps": map[string]any{"rk": true}}})
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func (a *signedAuthenticator) assert(t *testing.T, options any, counter uint32, uv bool, origin string, handle []byte) []byte {
	t.Helper()
	p := optionMap(t, options)
	hash := sha256.Sum256([]byte(p["rpId"].(string)))
	flags := byte(1)
	if uv {
		flags |= 4
	}
	data := append(hash[:], flags)
	data = binary.BigEndian.AppendUint32(data, counter)
	client := clientData(p["challenge"].(string), origin, "webauthn.get")
	ch := sha256.Sum256(client)
	signed := sha256.Sum256(append(append([]byte{}, data...), ch[:]...))
	sig, e := ecdsa.SignASN1(rand.Reader, a.key, signed[:])
	if e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(map[string]any{"id": b64(a.id), "rawId": b64(a.id), "type": "public-key", "response": map[string]any{"clientDataJSON": b64(client), "authenticatorData": b64(data), "signature": b64(sig), "userHandle": b64(handle)}, "clientExtensionResults": map[string]any{}})
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func passkeyFixture(t *testing.T, dialect, dsn string) *fixture {
	t.Helper()
	f := setup(t, dialect, dsn)
	f.service.BaseURL = strings.Replace(f.server.URL, "127.0.0.1", "localhost", 1)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		app, e := f.service.CreateApp(ctx, f.operatorToken, "Passkey App", "pk-"+strings.ReplaceAll(application.ID(), "-", "")[:12])
		if e != nil {
			t.Fatal(e)
		}
		scope, _ := domain.AppScope(app.ID)
		p := app.AuthenticationPolicy()
		p.PasskeyEnabled = true
		app.Policy = &p
		app.Version++
		user := f.user1
		user.ID = application.ID()
		user.AppID = app.ID
		if e = f.store.App(ctx, scope, true, func(tx ports.AppTx) error {
			if e := tx.UpdateAppConfig(app); e != nil {
				return e
			}
			return tx.CreateUser(user, "email", f.service.Digest(app.ID, "identifier:email", user.Email))
		}); e != nil {
			t.Fatal(e)
		}
		if i == 0 {
			f.app1, f.scope1, f.user1 = app, scope, user
		} else {
			f.app2, f.scope2, f.user2 = app, scope, user
		}
	}
	var e error
	f.client, f.secret, e = f.service.CreateClient(ctx, f.operatorToken, f.scope1, domain.Client{Name: "Passkey RP", Kind: "confidential", RedirectURIs: []string{f.server.URL + "/test-callback"}, Scopes: []string{"openid"}, Audiences: []string{"urn:authova:passkey-test"}, IntrospectionAudiences: []string{}, Grants: []string{"authorization_code"}}, "pk-"+application.ID())
	if e != nil {
		t.Fatal(e)
	}
	return f
}
func (f *fixture) passkeySession(t *testing.T) string {
	t.Helper()
	flow := f.securityFlow(t, "authorize")
	ctx := context.Background()
	if e := f.service.PasswordLogin(ctx, f.scope1, flow.ID, "email", f.user1.Email, password); e != nil {
		t.Fatal(e)
	}
	handle, e := f.service.Complete(ctx, f.scope1, flow.ID)
	if e != nil {
		t.Fatal(e)
	}
	return handle
}
func (f *fixture) setPasskeyPolicy(t *testing.T, fn func(*domain.App)) {
	t.Helper()
	if e := f.store.App(context.Background(), f.scope1, true, func(tx ports.AppTx) error {
		a, e := tx.App()
		if e != nil {
			return e
		}
		fn(&a)
		a.Version++
		return tx.UpdateAppConfig(a)
	}); e != nil {
		t.Fatal(e)
	}
}
func (f *fixture) enroll(t *testing.T, handle string, a *signedAuthenticator) domain.Passkey {
	t.Helper()
	ctx := context.Background()
	id, options, e := f.service.BeginPasskeyRegistration(ctx, f.scope1, handle, "", "My device", "")
	if e != nil {
		t.Fatal(e)
	}
	bad := a.register(t, options, "https://attacker.invalid")
	if e = f.service.FinishPasskeyRegistration(ctx, f.scope1, handle, "", id, bad); e == nil {
		t.Fatal("foreign origin accepted")
	}
	proof := a.register(t, options, a.origin)
	if e = f.service.FinishPasskeyRegistration(ctx, f.scope1, handle, "", id, proof); e != nil {
		t.Fatal(e)
	}
	if e = f.service.FinishPasskeyRegistration(ctx, f.scope1, handle, "", id, proof); e == nil {
		t.Fatal("registration replay accepted")
	}
	var keys []domain.Passkey
	if e = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error { var e error; keys, e = tx.Passkeys(f.user1.ID); return e }); e != nil || len(keys) != 1 {
		t.Fatalf("keys %d error %v", len(keys), e)
	}
	return keys[0]
}
func TestPasskeyLifecycle(t *testing.T) {
	configured := false
	for _, dialect := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(dialect) + "_DSN")
		if dsn == "" {
			continue
		}
		configured = true
		t.Run(dialect, func(t *testing.T) {
			t.Run("signed-proofs-isolation-counter-replay", func(t *testing.T) {
				f := passkeyFixture(t, dialect, dsn)
				ctx := context.Background()
				handle := f.passkeySession(t)
				auth := newAuthenticator(t, f.service.BaseURL)
				key := f.enroll(t, handle, auth)
				flow := f.securityFlow(t, "authorize")
				id, options, e := f.service.BeginPasskeyLogin(ctx, f.scope1, flow.ID)
				if e != nil {
					t.Fatal(e)
				}
				for _, proof := range [][]byte{auth.assert(t, options, 1, false, auth.origin, auth.handle), auth.assert(t, options, 1, true, "https://evil.invalid", auth.handle), auth.assert(t, options, 1, true, auth.origin, []byte("foreign-user"))} {
					if e = f.service.FinishPasskeyLogin(ctx, f.scope1, flow.ID, id, proof); e == nil {
						t.Fatal("invalid assertion accepted")
					}
				}

				// Challenge, RP hash and signature must independently match.
				wrongChallenge := optionMap(t, options)
				wrongChallenge["challenge"] = secure.Random(32)
				wrongRP := optionMap(t, options)
				wrongRP["rpId"] = "evil.invalid"
				var damaged map[string]any
				json.Unmarshal(auth.assert(t, options, 1, true, auth.origin, auth.handle), &damaged)
				response := damaged["response"].(map[string]any)
				signature, _ := base64.RawURLEncoding.DecodeString(response["signature"].(string))
				signature[len(signature)-1] ^= 1
				response["signature"] = b64(signature)
				badSignature, _ := json.Marshal(damaged)
				for _, invalid := range [][]byte{auth.assert(t, map[string]any{"publicKey": wrongChallenge}, 1, true, auth.origin, auth.handle), auth.assert(t, map[string]any{"publicKey": wrongRP}, 1, true, auth.origin, auth.handle), badSignature} {
					if e = f.service.FinishPasskeyLogin(ctx, f.scope1, flow.ID, id, invalid); e == nil {
						t.Fatal("challenge/RP/signature mismatch accepted")
					}
				}
				proof := auth.assert(t, options, 1, true, auth.origin, auth.handle)
				if e = f.service.FinishPasskeyLogin(ctx, f.scope1, flow.ID, id, proof); e != nil {
					t.Fatal(e)
				}
				if e = f.service.FinishPasskeyLogin(ctx, f.scope1, flow.ID, id, proof); e == nil {
					t.Fatal("login replay accepted")
				}
				if _, e = f.service.Complete(ctx, f.scope1, flow.ID); e != nil {
					t.Fatal(e)
				}
				if e = f.store.App(ctx, f.scope2, false, func(tx ports.AppTx) error { _, e := tx.Passkey(key.ID, false); return e }); !errors.Is(e, domain.ErrNotFound) {
					t.Fatal("cross App key lookup", e)
				}
				// A valid App 1 assertion cannot complete any ceremony in App 2.
				if e = f.service.FinishPasskeyLogin(ctx, f.scope2, flow.ID, id, proof); e == nil {
					t.Fatal("cross App assertion accepted")
				}

				foreignClient, _, e := f.service.CreateClient(ctx, f.operatorToken, f.scope2, domain.Client{Name: "Foreign RP", Kind: "confidential", RedirectURIs: []string{f.server.URL + "/test-callback"}, Scopes: []string{"openid"}, Audiences: []string{"urn:authova:foreign"}, Grants: []string{"authorization_code"}}, "foreign-"+application.ID())
				if e != nil {
					t.Fatal(e)
				}
				foreign := domain.Flow{ID: secure.Random(32), ClientID: foreignClient.ClientID, Purpose: domain.PurposeAuthorize, Step: domain.StepChoose, Expires: f.service.Now().Add(time.Minute).Unix()}
				if e = f.store.App(ctx, f.scope2, true, func(tx ports.AppTx) error { return tx.PutFlow(foreign) }); e != nil {
					t.Fatal(e)
				}
				fid, fopts, e := f.service.BeginPasskeyLogin(ctx, f.scope2, foreign.ID)
				if e != nil {
					t.Fatal(e)
				}
				if e = f.service.FinishPasskeyLogin(ctx, f.scope2, foreign.ID, fid, auth.assert(t, fopts, 2, true, auth.origin, auth.handle)); e == nil {
					t.Fatal("App1 credential resolved inside valid App2 ceremony")
				}
				clone := f.securityFlow(t, "authorize")
				cid, copts, e := f.service.BeginPasskeyLogin(ctx, f.scope1, clone.ID)
				if e != nil {
					t.Fatal(e)
				}
				if e = f.service.FinishPasskeyLogin(ctx, f.scope1, clone.ID, cid, auth.assert(t, copts, 1, true, auth.origin, auth.handle)); e == nil {
					t.Fatal("non-increasing counter accepted")
				}
				// Concurrent completion is single-use even when cryptographic checks race.
				concurrent := f.securityFlow(t, "authorize")
				rid, ropts, e := f.service.BeginPasskeyLogin(ctx, f.scope1, concurrent.ID)
				if e != nil {
					t.Fatal(e)
				}
				signed := auth.assert(t, ropts, 2, true, auth.origin, auth.handle)
				var wg sync.WaitGroup
				results := make(chan error, 2)
				for range 2 {
					wg.Go(func() { results <- f.service.FinishPasskeyLogin(ctx, f.scope1, concurrent.ID, rid, signed) })
				}
				wg.Wait()
				close(results)
				success := 0
				for e := range results {
					if e == nil {
						success++
					}
				}
				if success != 1 {
					t.Fatal("concurrent successes", success)
				}
				if e = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
					k, e := tx.Passkey(key.ID, false)
					if e == nil && k.Version != 3 {
						t.Fatalf("counter state version %d", k.Version)
					}
					return e
				}); e != nil {
					t.Fatal(e)
				}
				for _, table := range []string{"passkeys", "passkey_ceremonies"} {
					result, e := f.store.RewrapPage(ctx, f.app1.ID, table, nil, 100, true)
					if e != nil || result.Scanned == 0 {
						t.Fatalf("rewrap %s: %+v %v", table, result, e)
					}
				}
			})
			t.Run("policy-mfa-delete-revocation", func(t *testing.T) {
				f := passkeyFixture(t, dialect, dsn)
				ctx := context.Background()
				handle := f.passkeySession(t)
				other := f.passkeySession(t)
				auth := newAuthenticator(t, f.service.BaseURL)
				key := f.enroll(t, handle, auth)
				flow := f.securityFlow(t, "authorize")
				id, opts, e := f.service.BeginPasskeyLogin(ctx, f.scope1, flow.ID)
				if e != nil {
					t.Fatal(e)
				}
				proof := auth.assert(t, opts, 1, true, auth.origin, auth.handle)
				f.setPasskeyPolicy(t, func(a *domain.App) { p := a.AuthenticationPolicy(); p.PasskeyEnabled = false; a.Policy = &p })
				if e = f.service.FinishPasskeyLogin(ctx, f.scope1, flow.ID, id, proof); e == nil {
					t.Fatal("disabled policy accepted")
				}
				f.setPasskeyPolicy(t, func(a *domain.App) {
					p := a.AuthenticationPolicy()
					p.PasskeyEnabled = true
					a.Policy = &p
					a.RequireMFA = true
				})
				if e = f.service.FinishPasskeyLogin(ctx, f.scope1, flow.ID, id, proof); e != nil {
					t.Fatal(e)
				}
				if _, e = f.service.Complete(ctx, f.scope1, flow.ID); e == nil {
					t.Fatal("required MFA bypassed")
				}
				f.setPasskeyPolicy(t, func(a *domain.App) {
					a.RequireMFA = false
					p := a.AuthenticationPolicy()
					p.PasswordLoginEnabled = false
					p.EmailOTPEnabled = false
					p.PhoneOTPEnabled = false
					p.FederationEnabled = false
					a.Policy = &p
				})
				if e = f.service.DeletePasskey(ctx, f.scope1, handle, key.ID, 2, true, true); !errors.Is(e, domain.ErrConflict) {
					t.Fatal("last method deleted", e)
				}
				f.setPasskeyPolicy(t, func(a *domain.App) { p := a.AuthenticationPolicy(); p.PasswordLoginEnabled = true; a.Policy = &p })
				if e = f.service.DeletePasskey(ctx, f.scope1, handle, key.ID, 1, true, true); !errors.Is(e, domain.ErrConflict) {
					t.Fatal("stale deletion", e)
				}

				pendingID, pendingOptions, e := f.service.BeginPasskeyRegistration(ctx, f.scope1, handle, "", "Pending device", "")
				if e != nil {
					t.Fatal(e)
				}
				if e = f.service.DeletePasskey(ctx, f.scope1, handle, key.ID, 2, true, true); e != nil {
					t.Fatal(e)
				}

				another := newAuthenticator(t, auth.origin)
				if e = f.service.FinishPasskeyRegistration(ctx, f.scope1, handle, "", pendingID, another.register(t, pendingOptions, auth.origin)); e == nil {
					t.Fatal("outstanding registration survived epoch revocation")
				}
				if e = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
					_, _, e := f.service.AuthenticatedSession(tx, f.scope1, handle, false)
					return e
				}); e != nil {
					t.Fatal("current session lost", e)
				}
				if e = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
					_, _, e := f.service.AuthenticatedSession(tx, f.scope1, other, false)
					return e
				}); e == nil {
					t.Fatal("other session still active")
				}
			})
			t.Run("verified-address-passwordless-registration-and-erasure", func(t *testing.T) {
				f := passkeyFixture(t, dialect, dsn)
				ctx := context.Background()
				f.setPasskeyPolicy(t, func(a *domain.App) { p := a.AuthenticationPolicy(); p.PasswordLoginEnabled = false; a.Policy = &p })
				flow := f.securityFlow(t, "register")
				flow.Step = domain.StepSetPassword
				flow.VerifiedKind = "email"
				flow.VerifiedValue = "new-passkey@example.com"
				if e := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return tx.PutFlow(flow) }); e != nil {
					t.Fatal(e)
				}
				auth := newAuthenticator(t, f.service.BaseURL)
				id, options, e := f.service.BeginPasskeyRegistration(ctx, f.scope1, "", flow.ID, "My device", "New user")
				if e != nil {
					t.Fatal(e)
				}
				if e = f.service.FinishPasskeyRegistration(ctx, f.scope1, "", flow.ID, id, auth.register(t, options, auth.origin)); e != nil {
					t.Fatal(e)
				}
				handle, e := f.service.Complete(ctx, f.scope1, flow.ID)
				if e != nil {
					t.Fatal(e)
				}
				var user domain.User
				if e = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
					_, u, e := f.service.AuthenticatedSession(tx, f.scope1, handle, false)
					user = u
					if e == nil && u.PasswordHash != "" {
						t.Fatal("unexpected password")
					}
					return e
				}); e != nil {
					t.Fatal(e)
				}
				if e = f.service.EraseSelf(ctx, f.scope1, handle); e != nil {
					t.Fatal(e)
				}
				if e = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
					keys, e := tx.Passkeys(user.ID)
					if len(keys) != 0 {
						t.Fatal("erased credentials retained")
					}
					return e
				}); e != nil {
					t.Fatal(e)
				}
			})
		})
	}
	if !configured {
		t.Skip("real PostgreSQL/MySQL required")
	}
}
