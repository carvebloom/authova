package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/carvebloom/authova/console/internal/coreapi"
	"github.com/carvebloom/authova/console/internal/valkeyconfig"
	"github.com/valkey-io/valkey-go"
)

const testAppKey = "0123456789abcdef0123456789abcdef"

func TestStaticAssetsCannotEscapeRootIncludingSymlinks(t *testing.T) {
	parent := t.TempDir()
	assets := filepath.Join(parent, "public")
	if err := os.Mkdir(assets, 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{filepath.Join(assets, "index.html"): "public index", filepath.Join(parent, "private.key"): "outside-secret-test"} {
		if err := os.WriteFile(name, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(parent, "private.key"), filepath.Join(assets, "link.key")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(assets)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	b := &bff{staticFS: root.FS(), api: &sdk.Client{HTTP: &http.Client{Timeout: time.Second}}}
	for _, url := range []string{"https://console.example/", "https://console.example/link.key", "https://console.example/../private.key", "https://console.example/%2e%2e/private.key"} {
		response := httptest.NewRecorder()
		b.ServeHTTP(response, httptest.NewRequest("GET", url, nil))
		if strings.Contains(response.Body.String(), "outside-secret-test") {
			t.Fatal("private file escaped asset root", url)
		}
		if strings.HasSuffix(url, "/") && response.Code != 200 {
			t.Fatal("public index unavailable", response.Code)
		}
	}
}

func TestBFFAdmissionDuringBlockedCore(t *testing.T) {
	entered, release := make(chan struct{}, 2), make(chan struct{})
	b, _ := fixtureBFF(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-release:
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"items":[]}`))
		case <-r.Context().Done():
		}
	}))
	b.maxInflight = 2
	id, _ := fixtureSession(t, b, session{Token: "test-core-token", CSRF: "test-csrf", Expires: time.Now().Add(time.Hour)})
	server := httptest.NewServer(b)
	defer server.Close()
	client := server.Client()
	client.Timeout = time.Second
	fetch := func(path string) int {
		req, _ := http.NewRequest("GET", server.URL+path, nil)
		req.AddCookie(&http.Cookie{Name: "av_console", Value: id})
		resp, err := client.Do(req)
		if err != nil {
			return 0
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	done := make(chan int, 2)
	for range 2 {
		go func() { done <- fetch("/bff/apps") }()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("Core requests did not block")
		}
	}
	if status := fetch("/bff/apps"); status != 503 {
		t.Fatal("excess BFF work not rejected", status)
	}
	if status := fetch("/healthz"); status != 200 {
		t.Fatal("BFF liveness unavailable", status)
	}
	close(release)
	for range 2 {
		if status := <-done; status != 200 {
			t.Fatal("blocked BFF request failed", status)
		}
	}
	if status := fetch("/bff/apps"); status != 200 {
		t.Fatal("BFF admission did not recover", status)
	}
}

func fixtureBFF(t *testing.T, handler http.Handler) (*bff, *httptest.Server) {
	t.Helper()
	address := os.Getenv("AUTHOVA_TEST_VALKEY_ADDRESS")
	if address == "" {
		t.Skip("set AUTHOVA_TEST_VALKEY_ADDRESS for real shared-session tests")
	}
	core := httptest.NewTLSServer(handler)
	t.Cleanup(core.Close)
	api, err := sdk.New(core.URL)
	if err != nil {
		t.Fatal(err)
	}
	api.HTTP = core.Client()
	api.HTTP.Timeout = time.Second
	config := valkeyconfig.Config{Address: address}
	if file := os.Getenv("AUTHOVA_TEST_VALKEY_TLS_CONFIG"); file != "" {
		data, e := os.ReadFile(file)
		if e != nil {
			t.Fatal("cannot read TLS fixture")
		}
		var configs map[string]valkeyconfig.Config
		if json.Unmarshal(data, &configs) != nil {
			t.Fatal("invalid TLS fixture")
		}
		config = configs["console"]
	}
	options, err := config.Options()
	if err != nil {
		t.Fatal(err)
	}
	cache, err := valkey.NewClient(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cache.Close)
	return &bff{api: api, cache: cache, key: []byte(strings.Repeat("k", 32)), origin: "https://console.example.test"}, core
}

func fixtureSession(t *testing.T, b *bff, s session) (string, *http.Request) {
	t.Helper()
	id := random()
	if err := b.saveSession(context.Background(), id, s); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.cache.Do(context.Background(), b.cache.B().Del().Key("authova:console:"+id).Build()) })
	request := httptest.NewRequest("GET", "https://console.example.test/bff/apps", nil)
	request.AddCookie(&http.Cookie{Name: "av_console", Value: id})
	return id, request
}

func TestSessionDependencyAndCSRF(t *testing.T) {
	var forwarded atomic.Int64
	b, _ := fixtureBFF(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1); w.Write([]byte(`{"items":[]}`)) }))
	_, request := fixtureSession(t, b, session{Token: "test-core-token", CSRF: "test-csrf", Expires: time.Now().Add(time.Hour)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response := httptest.NewRecorder()
	b.ServeHTTP(response, request.WithContext(ctx))
	if response.Code != 503 {
		t.Fatal("cache failure was reported as unauthenticated", response.Code)
	}
	response = httptest.NewRecorder()
	write := request.Clone(context.Background())
	write.Method = "POST"
	write.Header.Set("Origin", b.origin)
	b.ServeHTTP(response, write)
	if response.Code != 403 || forwarded.Load() != 0 {
		t.Fatal("missing CSRF reached Core")
	}
}

func TestPublicRefreshIsSerializedAndNeverReplayed(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "concurrent-success", true: "uncertain-rotation"}[failed], func(t *testing.T) {
			var calls atomic.Int64
			var origin string
			b, core := fixtureBFF(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration"):
					json.NewEncoder(w).Encode(map[string]any{"issuer": origin + "/a/" + testAppKey, "authorization_endpoint": origin + "/authorize", "token_endpoint": origin + "/oauth/token", "jwks_uri": origin + "/keys"})
				case r.URL.Path == "/oauth/token":
					calls.Add(1)
					if r.Header.Get("Authorization") != "" {
						t.Error("public Client attempted Basic auth probing")
					}
					if r.ParseForm() != nil || r.Form.Get("client_id") != "test-public-client" {
						t.Error("public client_id missing")
					}
					if failed {
						w.WriteHeader(503)
						w.Write([]byte(`{"error":"server_error"}`))
						return
					}
					time.Sleep(30 * time.Millisecond)
					w.Write([]byte(`{"access_token":"new-test-access","refresh_token":"new-test-refresh","token_type":"Bearer","expires_in":300}`))
				default:
					w.Write([]byte(`{"items":[]}`))
				}
			}))
			origin = core.URL
			_, request := fixtureSession(t, b, session{Token: "old-test-access", Refresh: "old-test-refresh", TokenExpires: time.Now().Add(-time.Minute), ClientID: "test-public-client", AppKey: testAppKey, CSRF: "test-csrf", Expires: time.Now().Add(time.Hour)})
			var wait sync.WaitGroup
			for range 2 {
				wait.Add(1)
				go func() {
					defer wait.Done()
					response := httptest.NewRecorder()
					b.ServeHTTP(response, request.Clone(context.Background()))
					if !failed && response.Code != 200 {
						t.Error("concurrent refresh failed", response.Code)
					}
				}()
			}
			wait.Wait()
			if calls.Load() != 1 {
				t.Fatal("rotation was probed/replayed", calls.Load())
			}
			if failed {
				response := httptest.NewRecorder()
				b.ServeHTTP(response, request)
				if response.Code != 401 || calls.Load() != 1 {
					t.Fatal("unknown rotation was retried")
				}
			}
		})
	}
}

func TestRefreshCannotResurrectLogout(t *testing.T) {
	b, _ := fixtureBFF(t, http.NotFoundHandler())
	s := session{Token: "test-core-token", Expires: time.Now().Add(time.Hour)}
	id, _ := fixtureSession(t, b, s)
	if err := b.cache.Do(context.Background(), b.cache.B().Del().Key("authova:console:"+id).Build()).Error(); err != nil {
		t.Fatal(err)
	}
	if err := b.replaceSession(context.Background(), id, s); err != errSessionMissing {
		t.Fatal("removed session was recreated", err)
	}
}

func TestLoginJSONIsStrict(t *testing.T) {
	for _, body := range []string{`{} {}`, `{"unknown":true}`} {
		request := httptest.NewRequest("POST", "/bff/login", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if decodeJSON(request, &struct{ Email string }{}) == nil {
			t.Fatal("invalid login document accepted")
		}
	}
}
