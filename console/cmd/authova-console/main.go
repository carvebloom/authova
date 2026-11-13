// Authova Console keeps management credentials in a shared server-side session store.
package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/carvebloom/authova/console/internal/diagnostic"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	sdk "github.com/carvebloom/authova/console/internal/coreapi"
	"github.com/carvebloom/authova/console/internal/valkeyconfig"
	"github.com/valkey-io/valkey-go"
)

type session struct {
	Refresh, AppKey, ClientID string
	TokenExpires              time.Time
	Token, CSRF               string
	Operator                  sdk.Operator
	Expires                   time.Time
	RefreshPending            bool `json:"refresh_pending,omitempty"`
}

var errSessionMissing = errors.New("authentication required")

type bff struct {
	staticFS    fs.FS
	maxInflight int64
	inflight    atomic.Int64
	api         *sdk.Client
	cache       valkey.Client
	key         []byte
	origin      string
	development bool
}

func random() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func (b *bff) crypt(data []byte, seal bool) ([]byte, error) {
	block, e := aes.NewCipher(b.key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	if seal {
		n := make([]byte, g.NonceSize())
		if _, e = rand.Read(n); e != nil {
			return nil, e
		}
		return g.Seal(n, n, data, []byte("authova-console-session:v1")), nil
	}
	if len(data) < g.NonceSize() {
		return nil, errors.New("invalid session")
	}
	return g.Open(nil, data[:g.NonceSize()], data[g.NonceSize():], []byte("authova-console-session:v1"))
}
func (b *bff) load(r *http.Request) (session, string, error) {
	var s session
	c, e := r.Cookie("av_console")
	if e != nil || len(c.Value) != 43 {
		return s, "", errSessionMissing
	}
	raw, e := b.cache.Do(r.Context(), b.cache.B().Get().Key("authova:console:"+c.Value).Build()).AsBytes()
	if e != nil {
		if valkey.IsValkeyNil(e) {
			return s, c.Value, errSessionMissing
		}
		return s, c.Value, diagnostic.Wrap(e, "console", "decode")
	}
	data, e := b.crypt(raw, false)
	if e != nil {
		return s, c.Value, diagnostic.Wrap(e, "console", "decode")
	}
	e = json.Unmarshal(data, &s)
	if s.Expires.Before(time.Now()) {
		return s, c.Value, errSessionMissing
	}
	return s, c.Value, diagnostic.Wrap(e, "console", "decode")
}
func (b *bff) cookie(w http.ResponseWriter, id string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: "av_console", Value: id, Path: "/", HttpOnly: true, Secure: !b.development, SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}
func response(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(sdk.Problem{Type: "urn:authova:problem:" + code, Status: status, Title: http.StatusText(status), Code: code, RequestID: w.Header().Get("X-Request-ID")})
}
func (b *bff) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if v := recover(); v != nil {
			if v == http.ErrAbortHandler {
				panic(http.ErrAbortHandler)
			}
			diagnostic.Panic(r.Context(), "console", v)
			panic(http.ErrAbortHandler)
		}
	}()
	concurrency := b.inflight.Add(1)
	defer b.inflight.Add(-1)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	r = r.WithContext(b.upstreamContext(ctx))
	b.securityHeaders(w)
	r = r.WithContext(diagnostic.WithRequest(r.Context(), w.Header().Get("X-Request-ID")))

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	maximum := b.maxInflight
	if maximum <= 0 {
		maximum = 256
	}
	if r.URL.Path != "/healthz" && concurrency > maximum {
		w.Header().Set("Retry-After", "1")
		problem(w, 503, "dependency_unavailable")
		return
	}
	if r.URL.Path == "/app/callback" && r.Method == "GET" {
		b.appCallback(w, r)
		return
	}
	if r.URL.Path == "/bff/app-login" {
		b.appLogin(w, r)
		return
	}
	if r.URL.Path == "/healthz" {
		response(w, 200, map[string]string{"status": "ok"})
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/bff/") {
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		if b.staticFS == nil {
			problem(w, 503, "static_unavailable")
			return
		}
		file := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if file == "" {
			file = "index.html"
		}
		if info, e := fs.Stat(b.staticFS, file); e != nil || info.IsDir() {
			file = "index.html"
		}
		asset, e := b.staticFS.Open(file)
		if e != nil {
			problem(w, 503, "static_unavailable")
			return
		}
		defer asset.Close()
		info, e := asset.Stat()
		seeker, ok := asset.(io.ReadSeeker)
		if e != nil || !ok {
			problem(w, 503, "static_unavailable")
			return
		}
		http.ServeContent(w, r, file, info.ModTime(), seeker)
		return
	}
	if r.Method != "GET" && r.Header.Get("Origin") != b.origin {
		problem(w, 403, "forbidden")
		return
	}
	if r.URL.Path == "/bff/login" && r.Method == "POST" {
		var in struct{ Email, Password, TOTP string }
		if decodeJSON(r, &in) != nil {
			problem(w, 400, "invalid_request")
			return
		}
		login, e := b.api.Login(r.Context(), in.Email, in.Password, in.TOTP)
		if e != nil {
			var p *sdk.Problem
			if errors.As(e, &p) {
				if p.Status >= 500 {
					diagnostic.Log(diagnostic.WithUpstream(r.Context(), p.RequestID), "console", "upstream", diagnostic.Wrap(e, "console", "upstream"))
				}
				if p.RequestID != "" {
					w.Header().Set("X-Request-ID", diagnostic.ID(p.RequestID))
				}
				response(w, p.Status, p)
			} else {
				diagnostic.Log(r.Context(), "console", "upstream", e)
				problem(w, 503, "dependency_unavailable")
			}
			return
		}
		s := session{Token: login.Token, CSRF: random(), Operator: login.Operator, Expires: login.ExpiresAt}
		raw, _ := json.Marshal(s)
		encrypted, e := b.crypt(raw, true)
		if e != nil {
			diagnostic.Log(r.Context(), "console", "upstream", e)
			problem(w, 503, "dependency_unavailable")
			return
		}
		id := random()
		if e = b.cache.Do(r.Context(), b.cache.B().Set().Key("authova:console:"+id).Value(string(encrypted)).ExSeconds(int64(time.Until(s.Expires).Seconds())).Build()).Error(); e != nil {
			diagnostic.Log(r.Context(), "console", "query", diagnostic.Wrap(e, "console", "query"))
			problem(w, 503, "dependency_unavailable")
			return
		}
		b.cookie(w, id, 8*3600)
		response(w, 200, map[string]any{"operator": s.Operator, "csrf_token": s.CSRF})
		return
	}
	s, id, e := b.load(r)
	if e != nil {
		if errors.Is(e, errSessionMissing) {
			problem(w, 401, "authentication_required")
		} else {
			diagnostic.Log(r.Context(), "console", "query", e)
			problem(w, 503, "dependency_unavailable")
		}
		return
	}
	if r.URL.Path != "/bff/logout" {
		s, e = b.refreshSession(r, id, s)
	}
	if e != nil {
		if errors.Is(e, errSessionMissing) {
			problem(w, 401, "authentication_required")
		} else {
			diagnostic.Log(r.Context(), "console", "upstream", e)
			problem(w, 503, "authentication_refresh_failed")
		}
		return
	}
	if r.Method != "GET" && r.Header.Get("X-CSRF-Token") != s.CSRF {
		problem(w, 403, "forbidden")
		return
	}
	if r.URL.Path == "/bff/me" && r.Method == "GET" {
		resp, e := b.api.Request(r.Context(), "GET", "/api/v1/platform/me", s.Token, "", nil)
		if e != nil {
			diagnostic.Log(r.Context(), "console", "upstream", e)
			problem(w, 503, "dependency_unavailable")
			return
		}
		if resp.Status != 200 {
			if resp.Status == http.StatusUnauthorized {
				// Authoritative rejection also invalidates the BFF copy so a page
				// reload cannot restore a session that requires fresh authentication.
				_ = b.invalidateRejectedSession(r.Context(), id, s)
			}
			w.Header().Set("Content-Type", "application/problem+json")
			if resp.RequestID != "" {
				w.Header().Set("X-Request-ID", diagnostic.ID(resp.RequestID))
			}
			if resp.Status >= 500 {
				diagnostic.Log(diagnostic.WithUpstream(r.Context(), resp.RequestID), "console", "upstream", diagnostic.Wrap(errors.New("upstream unavailable"), "console", "upstream"))
			}
			w.WriteHeader(resp.Status)
			_ = json.NewEncoder(w).Encode(sdk.ProjectProblem(resp.Status, resp.Body, resp.RequestID))
			return
		}
		var current struct {
			Operator sdk.Operator `json:"operator"`
		}
		if decodeErr := json.Unmarshal(resp.Body, &current); decodeErr != nil {
			diagnostic.Log(diagnostic.WithUpstream(r.Context(), resp.RequestID), "console", "decode", diagnostic.Wrap(decodeErr, "console", "decode"))
			problem(w, 503, "invalid_response")
			return
		}
		response(w, 200, map[string]any{"operator": current.Operator, "csrf_token": s.CSRF})
		return
	}
	if r.URL.Path == "/bff/logout" && r.Method == "POST" {
		var resp sdk.Response
		if s.AppKey != "" {
			e = b.revokeAppSession(r.Context(), s)
			resp.Status = 204
		} else {
			resp, e = b.api.Request(r.Context(), "DELETE", "/api/v1/platform/sessions", s.Token, "", nil)
		}
		if e != nil || resp.Status != 204 {
			diagnostic.Log(diagnostic.WithUpstream(r.Context(), resp.RequestID), "console", "upstream", e)
			problem(w, 503, "logout_failed")
			return
		}
		if e = b.cache.Do(r.Context(), b.cache.B().Del().Key("authova:console:"+id).Build()).Error(); e != nil {
			diagnostic.Log(r.Context(), "console", "cleanup", diagnostic.Combine(nil, e, true))
			problem(w, 503, "logout_failed")
			return
		}
		b.cookie(w, "", -1)
		w.WriteHeader(204)
		return
	}
	b.proxyManagement(w, r, s, id)
}
func main() {
	defer func() {
		if v := recover(); v != nil {
			diagnostic.Panic(context.Background(), "console", v)
			os.Exit(1)
		}
	}()
	slog.SetDefault(slog.New(diagnostic.Handler{Next: slog.NewJSONHandler(os.Stderr, nil)}))
	if e := run(); e != nil {
		diagnostic.Log(context.Background(), "console", "startup", e)
		os.Exit(1)
	}
}
func run() error {
	origin := os.Getenv("AUTHOVA_CONSOLE_URL")
	u, e := url.Parse(origin)
	dev := os.Getenv("AUTHOVA_MODE") == "development"
	if e != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil || (!dev && u.Scheme != "https") || (dev && u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("canonical console HTTPS origin required")
	}
	key, e := os.ReadFile(os.Getenv("AUTHOVA_CONSOLE_KEY_FILE"))
	if e != nil || len(key) != 32 {
		return errors.New("console session key file must contain 32 bytes")
	}
	api, e := sdk.New(os.Getenv("AUTHOVA_BASE_URL"))
	if e != nil {
		return e
	}
	if !dev && !strings.HasPrefix(api.BaseURL, "https://") {
		return errors.New("production core origin requires HTTPS")
	}
	vc, e := valkeyconfig.FromEnv(dev)
	if e != nil {
		return e
	}
	options, e := vc.Options()
	if e != nil {
		return e
	}
	cache, e := valkey.NewClient(options)
	if e != nil {
		return diagnostic.Wrap(e, "console", "connect")
	}
	defer cache.Close()
	assets := os.Getenv("AUTHOVA_CONSOLE_ASSETS")
	if assets == "" {
		assets = "console/web/dist"
	}
	if _, e = os.Stat(filepath.Join(assets, "index.html")); e != nil {
		return diagnostic.Wrap(e, "console", "startup")
	}
	assetRoot, e := os.OpenRoot(assets)
	if e != nil {
		return diagnostic.Wrap(e, "console", "startup")
	}
	defer assetRoot.Close()
	listen := os.Getenv("AUTHOVA_CONSOLE_LISTEN")
	if listen == "" {
		listen = ":8090"
	}
	server := &http.Server{Addr: listen, Handler: &bff{api: api, cache: cache, key: key, origin: origin, staticFS: assetRoot.FS(), development: dev}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	slog.Info("console listening", "address", listen)
	return serveHTTP(ctx, server)
}

func decodeJSON(r *http.Request, target any) error {
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || kind != "application/json" {
		return errors.New("JSON content type required")
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("one JSON value required")
	}
	return nil
}
