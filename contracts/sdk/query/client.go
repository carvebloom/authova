// Package query is a purpose-bound, read-only Authova control-plane client.
// It never accepts arbitrary paths, management tokens or mutation methods.
package query

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

const ScopePrefix = "authova.query."

type Config struct {
	BaseURL           string   `json:"base_url"`
	Issuer            string   `json:"issuer"`
	AppID             string   `json:"app_id"`
	ClientID          string   `json:"client_id"`
	ClientSecret      string   `json:"client_secret"`
	InstallationID    string   `json:"installation_id"`
	Scopes            []string `json:"scopes"`
	AllowLoopbackHTTP bool     `json:"allow_loopback_http,omitempty"`
}

type Operation struct{ Name, Scope, Description string }

// Operations returns the fixed tool catalog; returned slices are not shared.
func Operations() []Operation {
	return []Operation{
		{"context", ScopePrefix + "context", "Read the current query identity, App, tenant and granted scopes"},
		{"configuration", ScopePrefix + "configuration", "Read App authentication policy, delivery readiness, resource limits and redacted provider configuration"},
		{"users", ScopePrefix + "users", "List users in the approved App; contacts are masked unless separately approved"},
		{"user", ScopePrefix + "users", "Read one App user; contacts are masked unless separately approved"},
		{"sessions", ScopePrefix + "sessions", "List one user's sessions without cookie handles or credentials"},
		{"agents", ScopePrefix + "agents", "List registered Agent metadata in the approved App"},
		{"agent", ScopePrefix + "agents", "Read one registered Agent without credentials"},
		{"installations", ScopePrefix + "installations", "List approved Agent installations; tenant-bound credentials cannot cross tenant"},
		{"installation", ScopePrefix + "installations", "Read one approved Agent installation within the credential boundary"},
		{"grants", ScopePrefix + "grants", "List Agent grants and revocation status within the credential boundary"},
		{"grant", ScopePrefix + "grants", "Read one Agent grant within the credential boundary"},
		{"audit", ScopePrefix + "audit", "List App audit metadata; free-text reasons are excluded"},
	}
}

type Request struct {
	Operation      string `json:"operation"`
	ID             string `json:"id,omitempty"`
	UserID         string `json:"user_id,omitempty"`
	Cursor         string `json:"cursor,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	InstallationID string `json:"installation_id,omitempty"`
	TenantID       string `json:"tenant_id,omitempty"`
}

type Response struct {
	Data      json.RawMessage `json:"data"`
	RequestID string          `json:"request_id,omitempty"`
}

// Error contains only HTTP status, a bounded machine code and request ID.
// Raw upstream response bodies and credential-bearing errors are never exposed.
type Error struct {
	Status          int
	Code, RequestID string
}

func (e *Error) Error() string {
	return fmt.Sprintf("Authova query rejected (HTTP %d, %s, request_id=%s)", e.Status, e.Code, e.RequestID)
}

type dependencyError struct {
	message string
	cause   error
	line    int
	id      string
}

func (e *dependencyError) Error() string {
	if e.id != "" {
		return e.message + " (request_id=" + safeCode(e.id) + ")"
	}
	return e.message
}
func (e *dependencyError) DiagnosticRequestID() string { return safeCode(e.id) }
func (e *dependencyError) Unwrap() error               { return e.cause }

type Client struct {
	config  Config
	http    *http.Client
	mu      sync.Mutex
	token   string
	expires time.Time
	loading *tokenLoad
}

type tokenLoad struct {
	done  chan struct{}
	token string
	err   error
}

func LoadConfig(path string) (Config, error) {
	var c Config
	f, err := os.Open(path)
	if err != nil {
		return c, validationError("cannot open query configuration")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return c, validationError("query configuration must be a private regular file (0600)")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(raw) > 65536 {
		return c, validationError("query configuration exceeds limit")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil {
		return Config{}, validationError("invalid query configuration")
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		return Config{}, validationError("query configuration must contain one JSON object")
	}
	return c, nil
}

func New(c Config, client *http.Client) (*Client, error) {
	base, e := url.Parse(c.BaseURL)
	if e != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || base.Path != "" ||
		(base.Scheme != "https" && !(c.AllowLoopbackHTTP && base.Scheme == "http" && slices.Contains([]string{"localhost", "127.0.0.1", "::1"}, base.Hostname()))) {
		return nil, validationError("query origin must use HTTPS (or explicitly approved loopback HTTP)")
	}
	issuer, e := url.Parse(c.Issuer)
	if e != nil || issuer.Scheme != base.Scheme || issuer.Host != base.Host || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" ||
		!strings.HasPrefix(issuer.Path, "/a/") || len(strings.TrimPrefix(issuer.Path, "/a/")) != 32 || strings.Contains(strings.TrimPrefix(issuer.Path, "/a/"), "/") ||
		!reference(c.AppID) || !reference(c.ClientID) || !reference(c.InstallationID) || len(c.ClientSecret) == 0 || len(c.ClientSecret) > 4096 || len(c.Scopes) == 0 || len(c.Scopes) > 9 {
		return nil, validationError("invalid App-bound query identity configuration")
	}
	seen := map[string]bool{}
	for _, scope := range c.Scopes {
		valid := scope == ScopePrefix+"contacts"
		for _, op := range Operations() {
			valid = valid || scope == op.Scope
		}
		if !valid || seen[scope] {
			return nil, validationError("query scopes must be unique read-only capabilities")
		}
		seen[scope] = true
	}
	c.Scopes = append([]string(nil), c.Scopes...)
	h := http.Client{Timeout: 5 * time.Second}
	if client != nil {
		h = *client
	}
	if h.Timeout <= 0 || h.Timeout > 10*time.Second {
		h.Timeout = 5 * time.Second
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{config: c, http: &h}, nil
}

func (c *Client) Scopes() []string { return append([]string(nil), c.config.Scopes...) }

func reference(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, ch := range s {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
			return false
		}
	}
	return true
}

func requestPath(q Request) (string, error) {
	path := ""
	list := false
	switch q.Operation {
	case "context":
		path = q.Operation
	case "configuration", "users", "agents", "installations", "grants", "audit":
		path = q.Operation
		list = true
	case "user", "agent", "installation", "grant":
		if !reference(q.ID) {
			return "", validationError("a valid object id is required")
		}
		plural := map[string]string{"user": "users", "agent": "agents", "installation": "installations", "grant": "grants"}
		path = plural[q.Operation] + "/" + q.ID
	case "sessions":
		if !reference(q.UserID) {
			return "", validationError("a valid user_id is required")
		}
		path = "users/" + q.UserID + "/sessions"
		list = true
	default:
		return "", validationError("unknown read-only query operation")
	}
	if q.ID != "" && slices.Contains([]string{"context", "configuration", "users", "agents", "installations", "grants", "audit", "sessions"}, q.Operation) {
		return "", validationError("id is not accepted by this query")
	}
	values := url.Values{}
	if list {
		if q.Limit == 0 {
			q.Limit = 20
		}
		if q.Limit < 1 || q.Limit > 100 {
			return "", validationError("limit must be between 1 and 100")
		}
		values.Set("limit", fmt.Sprint(q.Limit))
		if q.Cursor != "" {
			if !reference(q.Cursor) {
				return "", validationError("invalid cursor")
			}
			values.Set("cursor", q.Cursor)
		}
	} else if q.Limit != 0 || q.Cursor != "" {
		return "", validationError("pagination is not accepted by this query")
	}
	filters := q.AgentID != "" || q.InstallationID != "" || q.TenantID != "" || q.UserID != "" && q.Operation != "sessions"
	if q.Operation == "installations" && q.InstallationID != "" {
		return "", validationError("use the installation query to read one installation")
	}
	if filters && q.Operation != "installations" && q.Operation != "grants" {
		return "", validationError("filters are not accepted by this query")
	}
	for k, v := range map[string]string{"agent_id": q.AgentID, "installation_id": q.InstallationID, "tenant_id": q.TenantID, "user_id": q.UserID} {
		if v == "" || k == "user_id" && q.Operation == "sessions" {
			continue
		}
		if len(v) > 128 || strings.ContainsAny(v, "\r\n\x00") {
			return "", validationError("invalid query filter")
		}
		values.Set(k, v)
	}
	if len(values) > 0 {
		path += "?" + values.Encode()
	}
	return path, nil
}

func (c *Client) Query(ctx context.Context, q Request) (Response, error) {
	var out Response
	path, err := requestPath(q)
	if err != nil {
		return out, err
	}
	required := ""
	for _, op := range Operations() {
		if op.Name == q.Operation {
			required = op.Scope
		}
	}
	if !slices.Contains(c.config.Scopes, required) {
		return out, validationError("query capability is not configured")
	}
	token, err := c.accessToken(ctx)
	if err != nil {
		return out, err
	}
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.config.BaseURL+"/api/v1/apps/"+c.config.AppID+"/query/"+path, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Accept", "application/json")
	response, err := c.http.Do(r)
	if err != nil {
		return out, dependency("authova query dependency unavailable", err)
	}
	defer response.Body.Close()
	out.RequestID = safeCode(response.Header.Get("X-Request-ID"))
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1048577))
	if err != nil || len(raw) > 1048576 {
		return out, &dependencyError{message: "invalid or oversized query response", cause: err, id: out.RequestID, line: contactLine()}
	}
	id := safeCode(response.Header.Get("X-Request-ID"))
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusUnauthorized {
			c.mu.Lock()
			if c.token == token {
				c.token = ""
			}
			c.mu.Unlock()
		}
		var problem struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(raw, &problem)
		code := "query_rejected"
		if slices.Contains([]string{"invalid_request", "authentication_required", "forbidden", "not_found", "rate_limited", "dependency_unavailable", "outcome_unknown"}, problem.Code) {
			code = problem.Code
		}
		return out, &Error{Status: response.StatusCode, Code: code, RequestID: id}
	}
	media, _, parseErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if parseErr != nil || media != "application/json" {
		return out, validationError("query response must be JSON")
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return out, validationError("invalid query JSON response")
	}
	out.Data, out.RequestID = raw, id
	return out, nil
}

func safeCode(value string) string {
	if len(value) > 128 {
		return "unavailable"
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return "unavailable"
		}
	}
	return value
}

func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.token != "" && time.Now().Add(10*time.Second).Before(c.expires) {
		token := c.token
		c.mu.Unlock()
		return token, nil
	}
	if load := c.loading; load != nil {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", dependency("query token request cancelled", ctx.Err())
		case <-load.done:
			return load.token, load.err
		}
	}
	load := &tokenLoad{done: make(chan struct{})}
	c.loading = load
	c.mu.Unlock()
	token, expires, err := c.fetchToken(ctx)
	c.mu.Lock()
	if err == nil {
		c.token, c.expires = token, expires
	}
	load.token, load.err = token, err
	c.loading = nil
	close(load.done)
	c.mu.Unlock()
	return token, err
}

func (c *Client) fetchToken(ctx context.Context) (string, time.Time, error) {
	form := url.Values{"grant_type": {"client_credentials"}, "installation_id": {c.config.InstallationID}, "resource": {"urn:authova:query:" + c.config.AppID}, "scope": {strings.Join(c.config.Scopes, " ")}}
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.config.Issuer+"/oauth/token", strings.NewReader(form.Encode()))
	r.SetBasicAuth(url.QueryEscape(c.config.ClientID), url.QueryEscape(c.config.ClientSecret))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "application/json")
	started := time.Now()
	response, err := c.http.Do(r)
	if err != nil {
		return "", time.Time{}, dependency("query token dependency unavailable", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 32769))
	if err != nil || len(raw) > 32768 {
		return "", time.Time{}, &dependencyError{message: "invalid token response", cause: err, id: safeCode(response.Header.Get("X-Request-ID")), line: contactLine()}
	}
	if response.StatusCode != http.StatusOK {
		return "", time.Time{}, &Error{Status: response.StatusCode, Code: "query_token_rejected", RequestID: safeCode(response.Header.Get("X-Request-ID"))}
	}
	media, _, parseErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if parseErr != nil || media != "application/json" {
		return "", time.Time{}, validationError("token response must be JSON")
	}
	var token struct {
		Access  string `json:"access_token"`
		Type    string `json:"token_type"`
		Expires int64  `json:"expires_in"`
		Scope   string `json:"scope"`
	}
	if json.Unmarshal(raw, &token) != nil || token.Access == "" || len(token.Access) > 16384 || strings.ContainsAny(token.Access, " \r\n\t") || !strings.EqualFold(token.Type, "Bearer") || token.Expires < 1 || token.Expires > 300 {
		return "", time.Time{}, validationError("invalid token response")
	}
	for _, scope := range strings.Fields(token.Scope) {
		if !slices.Contains(c.config.Scopes, scope) {
			return "", time.Time{}, validationError("unexpected token scope")
		}
	}
	return token.Access, started.Add(time.Duration(token.Expires) * time.Second), nil
}

func dependency(message string, cause error) error {
	_, _, line, _ := runtime.Caller(1)
	return &dependencyError{message: message, cause: cause, line: line}
}
func (e *dependencyError) DiagnosticContact() (string, int) { return "client.go", e.line }

func contactLine() int { _, _, line, _ := runtime.Caller(1); return line }

// PublicError only renders SDK-owned constructors, never a foreign Error method.
// Callers may inspect causes; raw causes are not a public display contract.
type validationError string

func (e validationError) Error() string { return string(e) }
func PublicError(err error) string {
	switch e := err.(type) {
	case validationError:
		return string(e)
	case *dependencyError:
		if e != nil {
			return e.Error()
		}
	case *Error:
		if e == nil {
			break
		}
		code := "query_rejected"
		switch e.Code {
		case "query_token_rejected", "invalid_request", "authentication_required", "forbidden", "not_found", "rate_limited", "dependency_unavailable", "outcome_unknown":
			code = e.Code
		}
		s := *e
		s.Code = code
		s.RequestID = safeCode(e.RequestID)
		return s.Error()
	}
	return "authova query failed"
}

// FailureKind classifies only SDK-owned errors. It never trusts a foreign message.
func FailureKind(err error) string {
	switch err.(type) {
	case validationError:
		return "invalid"
	case *dependencyError:
		return "dependency"
	case *Error:
		return "rejected"
	}
	return "internal"
}
