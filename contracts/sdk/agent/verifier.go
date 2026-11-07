// Package agent validates Authova Agent credentials using fresh OAuth token
// introspection. Business authorization remains owned by the resource server.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

var (
	ErrInvalidToken      = errors.New("invalid Agent token")
	ErrInsufficientScope = errors.New("insufficient Agent permission")
	ErrUnavailable       = errors.New("identity service unavailable")
)

type Config struct {
	Issuer       string
	AppID        string
	Resource     string
	ClientID     string
	ClientSecret string
	HTTP         *http.Client
	// Development only: HTTP is accepted exclusively on localhost/loopback.
	AllowLoopbackHTTP bool
}
type Verifier struct {
	config Config
	http   *http.Client
}
type Actor struct {
	Subject  string `json:"sub"`
	ClientID string `json:"client_id"`
}
type Principal struct {
	Active         bool     `json:"active"`
	Issuer         string   `json:"iss"`
	AppID          string   `json:"app_id"`
	Subject        string   `json:"sub"`
	ClientID       string   `json:"client_id"`
	AgentID        string   `json:"agent_id"`
	InstallationID string   `json:"installation_id"`
	GrantID        string   `json:"grant_id"`
	TenantID       string   `json:"tenant_id"`
	Mode           string   `json:"agent_mode"`
	PrincipalType  string   `json:"principal_type"`
	Audience       []string `json:"aud"`
	Scope          string   `json:"scope"`
	Expires        int64    `json:"exp"`
	Issued         int64    `json:"iat"`
	TokenID        string   `json:"jti"`
	Actor          *Actor   `json:"act"`
}

func NewVerifier(c Config) (*Verifier, error) {
	u, e := url.Parse(c.Issuer)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.HasSuffix(c.Issuer, "/") || (u.Scheme != "https" && !(c.AllowLoopbackHTTP && u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) || c.AppID == "" || c.Resource == "" || c.ClientID == "" || c.ClientSecret == "" {
		return nil, errors.New("invalid Agent verifier configuration")
	}
	target, e := url.Parse(c.Resource)
	if e != nil || target.Scheme == "" || target.Fragment != "" || strings.ContainsAny(c.Resource, "\r\n\"") {
		return nil, errors.New("invalid resource URI")
	}
	client := http.Client{Timeout: 5 * time.Second}
	if c.HTTP != nil {
		client = *c.HTTP
	}
	if client.Timeout <= 0 || client.Timeout > 5*time.Second {
		client.Timeout = 5 * time.Second
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Verifier{config: c, http: &client}, nil
}

// Verify does not cache a positive authorization decision. tenant must come
// from the resource server's trusted request/resource context. Empty means an
// App-wide request and explicitly rejects tokens scoped to another tenant.
func (v *Verifier) Verify(ctx context.Context, token, tenant string, requiredScopes []string) (Principal, error) {
	var p Principal
	if len(token) == 0 || len(token) > 16384 || strings.ContainsAny(token, " \t\r\n") {
		return p, ErrInvalidToken
	}
	form := url.Values{"token": {token}, "token_type_hint": {"access_token"}}
	r, e := http.NewRequestWithContext(ctx, "POST", v.config.Issuer+"/oauth/introspect", strings.NewReader(form.Encode()))
	if e != nil {
		return p, ErrUnavailable
	}
	r.SetBasicAuth(url.QueryEscape(v.config.ClientID), url.QueryEscape(v.config.ClientSecret))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "application/json")
	resp, e := v.http.Do(r)
	if e != nil {
		return p, ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return p, ErrUnavailable
	}
	mediaType, _, mediaErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		return p, ErrUnavailable
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 32769))
	if e != nil || len(raw) > 32768 || json.Unmarshal(raw, &p) != nil {
		return p, ErrUnavailable
	}
	now := time.Now().Unix()
	if !p.Active || p.Issuer != v.config.Issuer || p.AppID != v.config.AppID || p.AgentID == "" || p.InstallationID == "" || p.GrantID == "" || p.Subject == "" || p.ClientID == "" || p.TokenID == "" || p.Issued <= 0 || p.Expires <= p.Issued || p.Expires <= now || p.Expires-p.Issued > 300 || p.Issued > now+30 || !slices.Contains(p.Audience, v.config.Resource) || len(p.Audience) != 1 || p.TenantID != tenant {
		return Principal{}, ErrInvalidToken
	}
	switch p.Mode {
	case "delegated":
		if p.PrincipalType != "user" || p.Actor == nil || p.Actor.Subject != p.AgentID || p.Actor.ClientID != p.ClientID {
			return Principal{}, ErrInvalidToken
		}
	case "autonomous":
		if p.PrincipalType != "client" || p.Actor != nil || p.Subject != p.ClientID {
			return Principal{}, ErrInvalidToken
		}
	default:
		return Principal{}, ErrInvalidToken
	}
	scopes := strings.Fields(p.Scope)
	for _, required := range requiredScopes {
		if required == "" || !slices.Contains(scopes, required) {
			return Principal{}, ErrInsufficientScope
		}
	}
	return p, nil
}

type principalKey struct{}

func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// Middleware requires an explicit business authorization callback. It can check
// current user permissions and row ownership before the handler receives data.
// tenantResolver must derive tenant from trusted routing/business context, never
// accept an arbitrary header as proof of tenant membership.
func (v *Verifier) Middleware(scopes []string, tenantResolver func(*http.Request) (string, error), authorize func(context.Context, Principal, *http.Request) error, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if tenantResolver == nil || authorize == nil || next == nil {
			http.Error(w, "authorization is not configured", http.StatusServiceUnavailable)
			return
		}
		headers := r.Header.Values("Authorization")
		if len(headers) != 1 || !strings.HasPrefix(headers[0], "Bearer ") {
			challenge(w, "")
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		tenant, e := tenantResolver(r)
		if e != nil {
			http.Error(w, "tenant access denied", http.StatusForbidden)
			return
		}
		p, e := v.Verify(r.Context(), strings.TrimPrefix(headers[0], "Bearer "), tenant, scopes)
		if e != nil {
			if errors.Is(e, ErrUnavailable) {
				http.Error(w, "identity service unavailable", http.StatusServiceUnavailable)
			} else if errors.Is(e, ErrInsufficientScope) {
				challenge(w, "insufficient_scope")
				http.Error(w, "permission denied", http.StatusForbidden)
			} else {
				challenge(w, "invalid_token")
				http.Error(w, "invalid credential", http.StatusUnauthorized)
			}
			return
		}
		if e = authorize(r.Context(), p, r); e != nil {
			http.Error(w, "business permission denied", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	})
}

// ProtectedResourceMetadata exposes the OAuth discovery document for an HTTP
// MCP resource. The MCP transport and per-tool authorization are separate.
func (v *Verifier) ProtectedResourceMetadata(scopes []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"resource": v.config.Resource, "authorization_servers": []string{v.config.Issuer}, "scopes_supported": scopes, "bearer_methods_supported": []string{"header"}})
	})
}

func challenge(w http.ResponseWriter, code string) {
	value := w.Header().Get("WWW-Authenticate")
	if value == "" {
		value = "Bearer"
	}
	if code != "" {
		if value == "Bearer" {
			value += " "
		} else {
			value += ", "
		}
		value += "error=\"" + code + "\""
	}
	w.Header().Set("WWW-Authenticate", value)
}
