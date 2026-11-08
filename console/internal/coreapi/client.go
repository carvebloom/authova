// Code generated from authova-contracts/sdk/client.go; DO NOT EDIT.
// Package coreapi provides a small transport client for Authova's versioned management API.
// OAuth/OIDC applications should use a standard OIDC relying party library.
package coreapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func New(base string) (*Client, error) {
	u, e := url.Parse(base)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("invalid Authova origin")
	}
	return &Client{BaseURL: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	Code      string `json:"code"`
	RequestID string `json:"request_id"`
}

func (p *Problem) Error() string { return p.Code + ": " + p.Title }

// DependencyError preserves transport causes without exposing credential-bearing URLs.
type DependencyError struct {
	cause error
	line  int
	id    string
}

func (e *DependencyError) Error() string { return "Authova dependency unavailable" }
func (e *DependencyError) Unwrap() error { return e.cause }

// ProjectProblem treats upstream message/metadata as untrusted, even for familiar codes.
func ProjectProblem(status int, body []byte, requestID string) *Problem {
	code := "dependency_unavailable"
	switch status {
	case 400:
		code = "invalid_request"
	case 401:
		code = "authentication_required"
	case 403:
		code = "forbidden"
	case 404:
		code = "not_found"
	case 405:
		code = "method_not_allowed"
	case 409:
		code = "conflict"
	case 410:
		code = "flow_expired"
	case 429:
		code = "rate_limited"
	}
	var raw struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &raw)
	if status == 503 && raw.Code == "outcome_unknown" {
		code = raw.Code
	}
	requestID = correlationID(requestID)
	return &Problem{Type: "urn:authova:problem:" + code, Detail: "The request could not be completed.", Title: http.StatusText(status), Status: status, Code: code, RequestID: requestID}
}

type Response struct {
	Status      int
	Body        []byte
	ContentType string
	RequestID   string
}

// Request does not retry writes: their outcome may be unknown. Reconcile using Idempotency-Key.
func (c *Client) Request(ctx context.Context, method, path, token, idempotency string, body []byte) (Response, error) {
	var out Response
	if !strings.HasPrefix(path, "/api/v1/") || strings.ContainsAny(path, "\r\n") || strings.Contains(path, "..") || strings.Contains(path, "\\") {
		return out, errors.New("invalid API path")
	}
	var input io.Reader = bytes.NewReader(body)
	if method != http.MethodGet && method != http.MethodHead {
		// NewRequest recognizes bytes.Reader and enables body rewinding. Combined
		// with Idempotency-Key, Transport can then replay a committed mutation
		// whose response was lost. Keep writes non-rewindable, even with a key:
		// only the caller may reconcile and explicitly repeat the operation.
		input = io.NopCloser(bytes.NewReader(body))
	}
	r, e := http.NewRequestWithContext(ctx, method, c.BaseURL+path, input)
	if e != nil {
		return out, dependency(e)
	}
	if len(body) > 0 {
		r.ContentLength = int64(len(body))
	}
	r.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if idempotency != "" {
		r.Header.Set("Idempotency-Key", idempotency)
	}
	resp, e := c.HTTP.Do(r)
	if e != nil {
		return out, dependency(e)
	}
	defer resp.Body.Close()
	out.Status, out.ContentType, out.RequestID = resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("X-Request-ID")
	out.Body, e = io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if e != nil {
		return out, dependency(e, out.RequestID)
	}
	if len(out.Body) > 4*1024*1024 {
		return out, dependency(errors.New("API response too large"), out.RequestID)
	}
	out.Status, out.ContentType, out.RequestID = resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("X-Request-ID")
	return out, nil
}

type Operator struct {
	AppID  string `json:"app_id,omitempty"`
	ID     string `json:"id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	Status string `json:"status"`
}
type Login struct {
	Token     string    `json:"token"`
	Operator  Operator  `json:"operator"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (c *Client) Login(ctx context.Context, email, password, totp string) (Login, error) {
	var out Login
	b, _ := json.Marshal(map[string]string{"email": email, "password": password, "totp": totp})
	resp, e := c.Request(ctx, "POST", "/api/v1/platform/sessions", "", "", b)
	if e != nil {
		return out, e
	}
	if resp.Status != 201 {
		return out, ProjectProblem(resp.Status, resp.Body, resp.RequestID)
	}
	e = json.Unmarshal(resp.Body, &out)
	if e == nil && (out.Token == "" || out.Operator.ID == "" || !out.ExpiresAt.After(time.Now())) {
		return Login{}, errors.New("invalid login response")
	}
	if e != nil {
		return out, dependency(e, resp.RequestID)
	}
	return out, nil
}

func dependency(cause error, ids ...string) error {
	_, _, line, _ := runtime.Caller(1)
	id := ""
	if len(ids) > 0 {
		id = correlationID(ids[0])
	}
	return &DependencyError{cause: cause, line: line, id: id}
}
func (e *DependencyError) DiagnosticContact() (string, int) { return "client.go", e.line }
func (e *DependencyError) DiagnosticRequestID() string      { return e.id }

func correlationID(id string) string {
	if len(id) > 128 {
		return ""
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return ""
		}
	}
	return id
}
