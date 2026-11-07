package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMiddlewareRejectsUntrustedTenantAndBusinessAccess(t *testing.T) {
	var issuer string
	var requests atomic.Int32
	var upstreamStatus atomic.Int32
	upstreamStatus.Store(200)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if status := int(upstreamStatus.Load()); status != 200 {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(Principal{Active: true, Issuer: issuer, AppID: "app-a", Subject: "user-a", ClientID: "agent-client", AgentID: "agent", InstallationID: "installation-a", GrantID: "grant-a", TenantID: "tenant-a", Mode: "delegated", PrincipalType: "user", Audience: []string{"urn:resource:a"}, Scope: "tickets:read", Issued: time.Now().Unix(), Expires: time.Now().Add(time.Minute).Unix(), TokenID: "token-a", Actor: &Actor{Subject: "agent", ClientID: "agent-client"}})
	}))
	defer server.Close()
	issuer = server.URL
	v, err := NewVerifier(Config{Issuer: issuer, AppID: "app-a", Resource: "urn:resource:a", ClientID: "resource", ClientSecret: "secret", AllowLoopbackHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name             string
		headers          []string
		resourceTenant   string
		permission       bool
		scope            string
		dependencyStatus int
		want             int
	}{
		{"no-credential", nil, "tenant-a", true, "tickets:read", 200, 401},
		{"ambiguous-credential", []string{"Bearer one", "Bearer two"}, "tenant-a", true, "tickets:read", 200, 401},
		{"cross-tenant", []string{"Bearer token"}, "tenant-b", true, "tickets:read", 200, 401},
		{"empty-tenant-is-not-wildcard", []string{"Bearer token"}, "", true, "tickets:read", 200, 401},
		{"missing-scope", []string{"Bearer token"}, "tenant-a", true, "tickets:write", 200, 403},
		{"business-object-denied", []string{"Bearer token"}, "tenant-a", false, "tickets:read", 200, 403},
		{"dependency-down", []string{"Bearer token"}, "tenant-a", true, "tickets:read", 503, 503},
		{"approved", []string{"Bearer token"}, "tenant-a", true, "tickets:read", 200, 204},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			upstreamStatus.Store(int32(scenario.dependencyStatus))
			called := false
			h := v.Middleware([]string{scenario.scope}, func(*http.Request) (string, error) {
				// Trusted routing/business context, independent of the spoofed header.
				return scenario.resourceTenant, nil
			}, func(context.Context, Principal, *http.Request) error {
				if !scenario.permission {
					return errors.New("object belongs to another user")
				}
				return nil
			}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				p, ok := FromContext(r.Context())
				if !ok || p.TenantID != "tenant-a" || p.Subject != "user-a" {
					t.Error("unverified identity reached resource")
				}
				called = true
				w.WriteHeader(204)
			}))
			r := httptest.NewRequest("GET", "/tickets/private", nil)
			for _, header := range scenario.headers {
				r.Header.Add("Authorization", header)
			}
			r.Header.Set("X-Tenant-ID", "tenant-a")
			before := requests.Load()
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != scenario.want || called != (scenario.want == 204) {
				t.Fatalf("resource boundary: status=%d handler=%v", w.Code, called)
			}
			if len(scenario.headers) != 1 && requests.Load() != before {
				t.Fatal("ambiguous credential reached introspection")
			}
			if scenario.name == "missing-scope" && !strings.Contains(w.Header().Get("WWW-Authenticate"), "insufficient_scope") {
				t.Fatal("scope denial missing OAuth challenge")
			}
		})
	}
}
