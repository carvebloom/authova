package query

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testConfig(base string) Config {
	return Config{BaseURL: base, Issuer: base + "/a/" + strings.Repeat("a", 32), AppID: "app", ClientID: "client", ClientSecret: "private-credential", InstallationID: "installation", Scopes: []string{ScopePrefix + "context", ScopePrefix + "users"}, AllowLoopbackHTTP: true}
}

func TestQueriesAreBoundedAndCredentialsAreNotExposed(t *testing.T) {
	var issues, reads atomic.Int64
	var rejected atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			issues.Add(1)
			_ = r.ParseForm()
			id, secret, ok := r.BasicAuth()
			if r.Method != "POST" || !ok || id != "client" || secret != "private-credential" || r.Form.Get("resource") != "urn:authova:query:app" || r.Form.Get("installation_id") != "installation" {
				t.Error("token request escaped approved identity")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "sensitive-bearer", "token_type": "Bearer", "expires_in": 120})
			return
		}
		reads.Add(1)
		if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/api/v1/apps/app/query/") || r.Header.Get("Authorization") != "Bearer sensitive-bearer" {
			t.Error("query escaped approved read-only resource")
		}
		if rejected.Load() {
			w.WriteHeader(403)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "private-credential", "detail": "sensitive-bearer"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer server.Close()
	c, err := New(testConfig(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for n := 0; n < 20; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := c.Query(context.Background(), Request{Operation: "users", Limit: 17})
			if err != nil {
				t.Error(err)
				return
			}
			if strings.Contains(string(res.Data), "sensitive-bearer") {
				t.Error("bearer in result")
			}
		}()
	}
	wg.Wait()
	if issues.Load() != 1 || reads.Load() != 20 {
		t.Fatal("concurrent queries repeated credential issuance", issues.Load(), reads.Load())
	}
	before := reads.Load()
	for _, q := range []Request{{Operation: "DELETE"}, {Operation: "user", ID: "../platform/operators"}, {Operation: "users", Limit: 101}, {Operation: "users", TenantID: "other"}, {Operation: "configuration"}} {
		if _, err := c.Query(context.Background(), q); err == nil {
			t.Fatal("unapproved query accepted")
		}
	}
	if reads.Load() != before {
		t.Fatal("invalid request reached upstream")
	}
	rejected.Store(true)
	_, err = c.Query(context.Background(), Request{Operation: "context"})
	var problem *Error
	if !errors.As(err, &problem) || problem.Status != 403 || strings.Contains(err.Error(), "private-credential") || strings.Contains(err.Error(), "sensitive-bearer") {
		t.Fatal("rejection did not preserve bounded safe error")
	}
}

func TestQueryConfigurationAndRedirectBoundaries(t *testing.T) {
	for _, mutate := range []func(*Config){func(c *Config) { c.BaseURL = "http://public.example" }, func(c *Config) { c.Issuer = "https://other.example/a/" + strings.Repeat("a", 32) }, func(c *Config) { c.Scopes = []string{"authova.admin"} }, func(c *Config) { c.BaseURL += "/other" }, func(c *Config) { c.AppID = "../../other" }} {
		c := testConfig("https://identity.example")
		mutate(&c)
		if _, err := New(c, nil); err == nil {
			t.Fatal("unsafe configuration accepted")
		}
	}
	var received atomic.Int64
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1); w.WriteHeader(200) }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	c, err := New(testConfig(source.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Query(context.Background(), Request{Operation: "context"}); err == nil || received.Load() != 0 {
		t.Fatal("token credential followed redirect")
	}
	path := filepath.Join(t.TempDir(), "query.json")
	raw, _ := json.Marshal(testConfig(source.URL))
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadConfig(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadConfig(path); err == nil {
		t.Fatal("shared credential file accepted")
	}
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{`{"unknown":"private"}`, string(raw) + ` {}`, strings.Repeat(" ", 65537)} {
		if err = os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = LoadConfig(path); err == nil {
			t.Fatal("invalid private config accepted")
		}
	}
}

func TestWaitingTokenCallerCanCancel(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			close(started)
			<-release
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "bearer", "token_type": "Bearer", "expires_in": 120})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer server.Close()
	c, err := New(testConfig(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := c.Query(context.Background(), Request{Operation: "context"}); done <- err }()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	returned := make(chan error, 1)
	go func() { _, err := c.Query(ctx, Request{Operation: "context"}); returned <- err }()
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled query lost context cause")
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("cancelled caller waited for someone else's network request")
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestMalformedDependencyDoesNotBecomeSuccessfulQuery(t *testing.T) {
	var body atomic.Value
	body.Store("null")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "bearer", "token_type": "Bearer", "expires_in": 120})
			return
		}
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	defer server.Close()
	c, err := New(testConfig(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"null", "[]", `"not an identity result"`, "true", "{invalid"} {
		body.Store(value)
		if _, err = c.Query(context.Background(), Request{Operation: "context"}); err == nil {
			t.Fatal("malformed dependency response reported as success")
		}
	}
}

func TestForeignErrorsAreNotPublicMessages(t *testing.T) {
	if strings.Contains(PublicError(errors.New("CANARY")), "CANARY") {
		t.Fatal("foreign public message")
	}
	if PublicError(validationError("invalid query configuration")) != "invalid query configuration" {
		t.Fatal("registered validation message changed")
	}
}
