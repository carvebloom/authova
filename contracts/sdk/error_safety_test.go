package sdk

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("SECRET BODY") }
func (failingBody) Close() error             { return nil }

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestTruncatedBodyRetainsHeaderAndSafeCause(t *testing.T) {
	c, _ := New("https://core.example")
	c.HTTP.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Request-Id": []string{"req-123"}}, Body: failingBody{}}, nil
	})
	r, e := c.Request(context.Background(), "POST", "/api/v1/apps", "", "id", nil)
	if e == nil || r.RequestID != "req-123" || strings.Contains(e.Error(), "SECRET") {
		t.Fatal("lost header or unsafe error")
	}
	var d *DependencyError
	if !errors.As(e, &d) || errors.Unwrap(d) == nil || d.DiagnosticRequestID() != "req-123" {
		t.Fatal("lost cause")
	}
	_, e = c.Login(context.Background(), "user@example.test", "SECRET PASSWORD", "")
	if !errors.As(e, &d) || d.DiagnosticRequestID() != "req-123" {
		t.Fatal("Login lost the truncated response association")
	}
	for _, code := range []int{400, 401, 409, 503} {
		p := ProjectProblem(code, []byte(`{"title":"SECRET","detail":"SECRET","metadata":{"token":"SECRET"}}`), "req-123")
		if strings.Contains(p.Error(), "SECRET") || p.RequestID != "req-123" {
			t.Fatal(p)
		}
	}
	if ProjectProblem(503, nil, "unsafe ID\tCANARY").RequestID != "" {
		t.Fatal("unbounded correlation field")
	}
}
