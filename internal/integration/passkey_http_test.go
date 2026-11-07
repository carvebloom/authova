package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
)

// Exercise actual Kratos-hosted ceremony HTTP with independently signed virtual
// authenticator proofs; no browser credential API or production verifier is mocked.
func TestPasskeyHTTPCeremonies(t *testing.T) {
	configured := false
	for _, dialect := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(dialect) + "_DSN")
		if dsn == "" {
			continue
		}
		configured = true
		t.Run(dialect, func(t *testing.T) {
			f := setupChannelsHostname(t, dialect, dsn, false, false, "localhost")
			f.setPasskeyPolicy(t, func(a *domain.App) { p := a.AuthenticationPolicy(); p.PasskeyEnabled = true; a.Policy = &p })
			handle := f.passkeySession(t)
			endpoint, _ := url.Parse(f.app1.Issuer)
			f.http.Jar.SetCookies(endpoint, []*http.Cookie{{Name: "av_session_" + f.app1.Key, Value: handle, Path: "/a/" + f.app1.Key, HttpOnly: true}})
			csrf := f.service.Digest(f.app1.ID, "account-csrf", handle)
			call := func(path, token string, input any) (int, map[string]any) {
				t.Helper()
				body, e := json.Marshal(input)
				if e != nil {
					t.Fatal(e)
				}
				req, e := http.NewRequest("POST", f.app1.Issuer+path, bytes.NewReader(body))
				if e != nil {
					t.Fatal(e)
				}
				req.Header.Set("Origin", f.server.URL)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-CSRF-Token", token)
				response, e := f.http.Do(req)
				if e != nil {
					t.Fatal(e)
				}
				defer response.Body.Close()
				data, e := io.ReadAll(response.Body)
				if e != nil {
					t.Fatal(e)
				}
				out := map[string]any{}
				if e = json.Unmarshal(data, &out); e != nil {
					t.Fatalf("HTTP %d: invalid JSON", response.StatusCode)
				}
				return response.StatusCode, out
			}
			if status, _ := call("/ui/account/passkeys/register/begin", "missing", map[string]any{"name": "Virtual device"}); status != 403 {
				t.Fatal("missing CSRF accepted", status)
			}
			status, begin := call("/ui/account/passkeys/register/begin", csrf, map[string]any{"name": "Virtual device"})
			if status != 200 {
				t.Fatal("register begin", status, begin)
			}
			auth := newAuthenticator(t, f.server.URL)
			proof := auth.register(t, begin["options"], f.server.URL)
			finish := map[string]any{"ceremony_id": begin["ceremony_id"], "credential": json.RawMessage(proof)}
			if status, _ = call("/ui/account/passkeys/register/finish", csrf, finish); status != 200 {
				t.Fatal("register finish", status)
			}
			if status, _ = call("/ui/account/passkeys/register/finish", csrf, finish); status == 200 {
				t.Fatal("registration replay accepted")
			}
			flow := f.securityFlow(t, "authorize")
			response, e := f.http.Get(f.app1.Issuer + "/ui/flows/" + flow.ID)
			if e != nil {
				t.Fatal(e)
			}
			data, _ := io.ReadAll(response.Body)
			response.Body.Close()
			match := csrfPattern.FindSubmatch(data)
			if len(match) != 2 {
				t.Fatal("flow cookie/CSRF not established")
			}
			flowCSRF := string(match[1])
			base := "/ui/flows/" + flow.ID + "/passkey/login"
			status, begin = call(base+"/begin", flowCSRF, map[string]any{})
			if status != 200 {
				t.Fatal("login begin", status, begin)
			}
			finish = map[string]any{"ceremony_id": begin["ceremony_id"], "credential": json.RawMessage(auth.assert(t, begin["options"], 1, true, f.server.URL, auth.handle))}
			if status, _ = call(base+"/finish", flowCSRF, finish); status != 200 {
				t.Fatal("login finish", status)
			}
			if status, _ = call(base+"/finish", flowCSRF, finish); status == 200 {
				t.Fatal("assertion replay accepted")
			}
			if e = f.store.App(context.Background(), f.scope1, false, func(tx ports.AppTx) error {
				v, e := tx.Flow(flow.ID, false)
				if e == nil && v.UserID != f.user1.ID {
					t.Fatal("wrong authenticated subject")
				}
				return e
			}); e != nil {
				t.Fatal(e)
			}
		})
	}
	if !configured {
		t.Skip("real PostgreSQL/MySQL required")
	}
}
