package agent

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVerifierFailClosed(t *testing.T) {
	var issuer string
	p := Principal{Active: true, AppID: "app", Subject: "user", ClientID: "agent-client", AgentID: "agent", InstallationID: "installation", GrantID: "grant", TenantID: "tenant", Mode: "delegated", PrincipalType: "user", Audience: []string{"urn:example:resource"}, Scope: "tickets:read", Issued: time.Now().Unix(), Expires: time.Now().Add(time.Minute).Unix(), TokenID: "token", Actor: &Actor{Subject: "agent", ClientID: "agent-client"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, secret, ok := r.BasicAuth()
		if !ok || id != "resource-client" || secret != "resource-secret" {
			t.Error("resource credentials missing")
		}
		if r.URL.Path != "/a/key/oauth/introspect" {
			t.Error("wrong introspection endpoint")
		}
		w.Header().Set("Content-Type", "application/json")
		out := p
		if out.Issuer == "" {
			out.Issuer = issuer
		}
		json.NewEncoder(w).Encode(out)
	}))
	defer server.Close()
	issuer = server.URL + "/a/key"
	v, e := NewVerifier(Config{Issuer: issuer, AppID: "app", Resource: "urn:example:resource", ClientID: "resource-client", ClientSecret: "resource-secret", AllowLoopbackHTTP: true})
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if _, e = v.Verify(ctx, "synthetic-token", "tenant", []string{"tickets:read"}); e != nil {
		t.Fatal(e)
	}
	if _, e = v.Verify(ctx, "synthetic-token", "tenant", []string{"tickets:write"}); !errors.Is(e, ErrInsufficientScope) {
		t.Fatal("missing scope accepted")
	}
	for _, change := range []struct {
		name   string
		mutate func(*Principal)
	}{
		{"inactive", func(p *Principal) { p.Active = false }},
		{"wrong-issuer", func(p *Principal) { p.Issuer = "https://other.example/a/key" }},
		{"wrong-app", func(p *Principal) { p.AppID = "other" }},
		{"wrong-tenant", func(p *Principal) { p.TenantID = "other" }},
		{"wrong-actor", func(p *Principal) { p.Actor = &Actor{Subject: "other", ClientID: "agent-client"} }},
		{"wrong-actor-client", func(p *Principal) { p.Actor = &Actor{Subject: "agent", ClientID: "other"} }},
		{"wrong-resource", func(p *Principal) { p.Audience = []string{"urn:other"} }},
		{"extra-audience", func(p *Principal) { p.Audience = append(p.Audience, "urn:other") }},
		{"expired", func(p *Principal) { p.Expires = time.Now().Add(-time.Second).Unix() }},
		{"unbounded-ttl", func(p *Principal) { p.Expires = p.Issued + 301 }},
		{"overflowing-ttl", func(p *Principal) { p.Issued = math.MinInt64 }},
		{"nonpositive-lifetime", func(p *Principal) { p.Expires = time.Now().Unix() + 10; p.Issued = p.Expires + 5 }},
		{"wrong-principal-type", func(p *Principal) { p.PrincipalType = "client" }},
		{"missing-principal-type", func(p *Principal) { p.PrincipalType = "" }},
		{"missing-grant", func(p *Principal) { p.GrantID = "" }},
		{"missing-installation", func(p *Principal) { p.InstallationID = "" }},
		{"missing-agent", func(p *Principal) { p.AgentID = "" }},
		{"unexpected-mode", func(p *Principal) { p.Mode = "other" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			before := p
			change.mutate(&p)
			if _, e = v.Verify(ctx, "synthetic-token", "tenant", []string{"tickets:read"}); !errors.Is(e, ErrInvalidToken) {
				t.Fatalf("accepted invalid principal: %v", e)
			}
			p = before
		})
	}
	p.Mode, p.PrincipalType, p.Subject, p.Actor = "autonomous", "client", p.ClientID, nil
	if _, e = v.Verify(ctx, "synthetic-token", "tenant", []string{"tickets:read"}); e != nil {
		t.Fatalf("valid autonomous identity rejected: %v", e)
	}
	p.PrincipalType = "user"
	if _, e = v.Verify(ctx, "synthetic-token", "tenant", []string{"tickets:read"}); !errors.Is(e, ErrInvalidToken) {
		t.Fatal("autonomous identity accepted a user principal")
	}
}
func TestVerifierDoesNotFollowRedirectOrAllowMissingBusinessPolicy(t *testing.T) {
	targetCalled := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalled = true; w.WriteHeader(200) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	v, e := NewVerifier(Config{Issuer: source.URL, AppID: "app", Resource: "urn:example:resource", ClientID: "resource", ClientSecret: "secret", AllowLoopbackHTTP: true})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = v.Verify(context.Background(), "token", "", nil); !errors.Is(e, ErrUnavailable) || targetCalled {
		t.Fatal("redirect followed or not fail closed")
	}
	h := v.Middleware(nil, nil, nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unconfigured business policy reached handler") }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 503 {
		t.Fatal("missing policy not rejected")
	}
}
func TestVerifierRejectsUnsafeConfiguration(t *testing.T) {
	for _, issuer := range []string{"http://example.com/a/key", "https://user:pass@example.com/a/key", "https://example.com/a/key?token=x", "https://example.com/a/key/"} {
		if _, e := NewVerifier(Config{Issuer: issuer, AppID: "app", Resource: "urn:resource", ClientID: "resource", ClientSecret: "secret", AllowLoopbackHTTP: true}); e == nil {
			t.Errorf("unsafe issuer accepted: %s", issuer)
		}
	}
}
