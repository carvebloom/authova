package protocol

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
)

type corsStore struct {
	ports.Store
	clients []domain.Client
	err     error
	app     string
}

func (s *corsStore) App(_ context.Context, scope domain.Scope, write bool, f func(ports.AppTx) error) error {
	if write {
		panic("CORS must be read only")
	}
	s.app = scope.ID()
	if s.err != nil {
		return s.err
	}
	return f(corsTx{clients: s.clients})
}

type corsTx struct {
	ports.AppTx
	clients []domain.Client
}

func (t corsTx) Clients(after string, n int) ([]domain.Client, error) { return t.clients, nil }

func TestAppBoundBrowserCORS(t *testing.T) {
	for _, tc := range []struct {
		name, origin, path, method, preflight, headers, kind, status string
		deny, unavailable                                            bool
	}{
		{name: "discovery", origin: "https://spa.example", path: "/.well-known/openid-configuration", method: "GET"},
		{name: "jwks", origin: "https://spa.example", path: "/keys", method: "GET"},
		{name: "token", origin: "https://spa.example", path: "/oauth/token", method: "POST"},
		{name: "revoke", origin: "https://spa.example", path: "/revoke", method: "POST"},
		{name: "userinfo", origin: "https://spa.example", path: "/userinfo", method: "GET"},
		{name: "preflight", origin: "https://spa.example", path: "/oauth/token", method: "OPTIONS", preflight: "POST", headers: "content-type, authorization"},
		{name: "foreign", origin: "https://other.example", path: "/keys", method: "GET", deny: true},
		{name: "null", origin: "null", path: "/keys", method: "GET", deny: true},
		{name: "suffix", origin: "https://spa.example.evil", path: "/keys", method: "GET", deny: true},
		{name: "userinfo-preflight", origin: "https://spa.example", path: "/userinfo", method: "OPTIONS", preflight: "GET"},
		{name: "header", origin: "https://spa.example", path: "/oauth/token", method: "OPTIONS", preflight: "POST", headers: "x-unsafe", deny: true},
		{name: "verb", origin: "https://spa.example", path: "/oauth/token", method: "OPTIONS", preflight: "DELETE", deny: true},
		{name: "confidential", origin: "https://spa.example", path: "/keys", method: "GET", kind: "confidential", deny: true},
		{name: "disabled", origin: "https://spa.example", path: "/keys", method: "GET", status: "disabled", deny: true},
		{name: "unavailable", origin: "https://spa.example", path: "/keys", method: "GET", unavailable: true},
		{name: "server", path: "/oauth/token", method: "POST"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kind, status := tc.kind, tc.status
			if kind == "" {
				kind = "public"
			}
			if status == "" {
				status = "active"
			}
			store := &corsStore{clients: []domain.Client{{Kind: kind, Status: status, RedirectURIs: []string{"https://spa.example/callback"}}}}
			if tc.unavailable {
				store.err = errors.New("unavailable")
			}
			p := &Provider{service: &application.Service{BaseURL: "https://auth.example", Store: store}}
			r := httptest.NewRequest(tc.method, "https://auth.example/a/key"+tc.path, nil)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Access-Control-Request-Method", tc.preflight)
			r.Header.Set("Access-Control-Request-Headers", tc.headers)
			w := httptest.NewRecorder()
			handled := p.browserCORS(w, r, domain.App{ID: "app-a"}, tc.path)
			if tc.unavailable {
				if !handled || w.Code != 503 {
					t.Fatal("outage did not fail closed")
				}
				return
			}
			if tc.deny {
				if !handled || w.Code != 403 || w.Header().Get("Access-Control-Allow-Origin") != "" {
					t.Fatal("unexpected CORS authorization")
				}
				return
			}
			if tc.origin != "" {
				if store.app != "app-a" || w.Header().Get("Access-Control-Allow-Origin") != tc.origin {
					t.Fatal("wrong app/origin")
				}
			}
			if w.Header().Get("Access-Control-Allow-Credentials") != "" {
				t.Fatal("credentialed CORS enabled")
			}
			if tc.method == "OPTIONS" && (!handled || w.Code != 204) {
				t.Fatal("preflight failed")
			}
		})
	}
}
