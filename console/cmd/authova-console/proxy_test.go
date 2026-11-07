package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	sdk "github.com/carvebloom/authova/console/internal/coreapi"
)

func TestConsoleHeadersAndLocalProblemCorrelation(t *testing.T) {
	for _, development := range []bool{true, false} {
		b := &bff{development: development, api: &sdk.Client{HTTP: &http.Client{}}}
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "https://console.example/bff/login", nil)
		r.Header.Set("Origin", "https://other.example")
		r.Header.Set("X-Request-ID", "attacker-controlled-id")
		b.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("cross-origin request accepted", w.Code)
		}
		var p sdk.Problem
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(p.RequestID, "console_") || p.RequestID != w.Header().Get("X-Request-ID") {
			t.Fatal("local error correlation missing or user-controlled")
		}
		if (w.Header().Get("Strict-Transport-Security") == "") != development {
			t.Fatal("HSTS mode mismatch")
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" || w.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
			t.Fatal("sensitive response protection missing")
		}
		csp := w.Header().Get("Content-Security-Policy")
		for _, required := range []string{"object-src 'none'", "frame-ancestors 'none'", "script-src 'self'"} {
			if !strings.Contains(csp, required) {
				t.Fatal("CSP protection missing", required)
			}
		}
		if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
			t.Fatal("CSP permits unsafe execution")
		}
	}
}

func TestProxyClassifiesUnreadableResponsesWithoutRetry(t *testing.T) {
	for _, upstream := range []string{"broken-json", "html", "redirect", "disconnect"} {
		for _, method := range []string{"GET", "PATCH"} {
			t.Run(upstream+"/"+method, func(t *testing.T) {
				var calls atomic.Int32
				core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					switch upstream {
					case "disconnect":
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						conn.Close()
					case "redirect":
						http.Redirect(w, r, "/other", http.StatusFound)
					case "html":
						w.Header().Set("Content-Type", "text/html")
						w.Write([]byte("<html>proxy failed</html>"))
					default:
						w.Header().Set("Content-Type", "application/json")
						w.Write([]byte(`{"items":`))
					}
				}))
				defer core.Close()
				api, err := sdk.New(core.URL)
				if err != nil {
					t.Fatal(err)
				}
				b := &bff{api: api}
				w := httptest.NewRecorder()
				b.securityHeaders(w)
				r := httptest.NewRequest(method, "https://console.example/bff/apps/app", strings.NewReader(`{"name":"updated"}`))
				b.proxyManagement(w, r, session{Token: "fixture-token"}, "unused-fixture-id")
				var p sdk.Problem
				if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
					t.Fatal(err)
				}
				want := "outcome_unknown"
				if method == "GET" {
					want = "dependency_unavailable"
				}
				if w.Code != 503 || p.Code != want || calls.Load() != 1 {
					t.Fatal("unexpected outcome classification or retry", w.Code, p.Code, calls.Load())
				}
				if strings.Contains(w.Body.String(), "fixture-token") || p.RequestID == "" {
					t.Fatal("error leaked a credential or lacks correlation")
				}
			})
		}
	}
}

func TestProxyBoundsRoutesAndPreservesAuthority(t *testing.T) {
	var calls atomic.Int32
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Idempotency-Key") != "stable-input-key" || r.Header.Get("Authorization") != "Bearer fixture-token" || r.URL.Path != "/api/v1/apps/app" {
			t.Error("upstream credentials, route or idempotency changed")
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.Header().Set("X-Request-ID", "core-request-id")
		w.WriteHeader(409)
		w.Write([]byte(`{"status":409,"code":"conflict","request_id":"core-request-id"}`))
	}))
	defer core.Close()
	api, err := sdk.New(core.URL)
	if err != nil {
		t.Fatal(err)
	}
	b := &bff{api: api}
	for _, path := range []string{"/bff/apps-unsafe", "/bff/platform/operators-unsafe", "/bff/platform/sessions"} {
		w := httptest.NewRecorder()
		b.proxyManagement(w, httptest.NewRequest("GET", "https://console.example"+path, nil), session{}, "unused-fixture-id")
		if w.Code != 404 || calls.Load() != 0 {
			t.Fatal("unbounded proxy route", path)
		}
	}
	w := httptest.NewRecorder()
	b.proxyManagement(w, httptest.NewRequest("TRACE", "https://console.example/bff/apps", nil), session{}, "unused-fixture-id")
	if w.Code != 405 || calls.Load() != 0 {
		t.Fatal("unsupported method forwarded")
	}
	r := httptest.NewRequest("PATCH", "https://console.example/bff/apps/app", strings.NewReader(`{}`))
	r.Header.Set("Idempotency-Key", "stable-input-key")
	w = httptest.NewRecorder()
	b.proxyManagement(w, r, session{Token: "fixture-token"}, "unused-fixture-id")
	if w.Code != 409 || w.Header().Get("X-Request-ID") != "core-request-id" || !strings.Contains(w.Body.String(), `"code":"conflict"`) || calls.Load() != 1 {
		t.Fatal("Core rejection was rewritten or retried")
	}
}
