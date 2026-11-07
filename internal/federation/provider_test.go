package federation

import (
	"context"
	"github.com/carvebloom/authova/internal/domain"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}
}
func TestGitHubVerifiedIdentity(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host == "github.com" {
			if r.Method != "POST" || r.URL.Path != "/login/oauth/access_token" {
				t.Fatal("incorrect token endpoint")
			}
			r.ParseForm()
			if r.Form.Get("code_verifier") != "verifier" || r.Form.Get("client_secret") != "secret" || r.Form.Get("redirect_uri") != "https://local.example/callback" {
				t.Fatal("missing PKCE or exact redirect")
			}
			return response(r, 200, `{"access_token":"transient","token_type":"bearer"}`), nil
		}
		if r.URL.Host != "api.github.com" || r.Header.Get("Authorization") != "Bearer transient" {
			t.Fatal("credential left trusted endpoint")
		}
		if r.URL.Path == "/user" {
			return response(r, 200, `{"id":123,"login":"renameable","name":"User","email":"unverified@example.test"}`), nil
		}
		return response(r, 200, `[{"email":"unverified@example.test","primary":true,"verified":false},{"email":"verified@example.test","primary":true,"verified":true}]`), nil
	})}
	p, _ := New("github", client)
	c := domain.Connection{ClientID: "client", Secret: "secret"}
	auth, err := p.Authorization(context.Background(), c, "https://local.example/callback", "state", "nonce", "verifier")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(auth)
	if u.Host != "github.com" || u.Query().Get("code_challenge_method") != "S256" || u.Query().Get("state") != "state" {
		t.Fatal("unsafe authorization request")
	}
	identity, err := p.Authenticate(context.Background(), c, "https://local.example/callback", "code", "nonce", "verifier")
	if err != nil || identity.Subject != "123" || !identity.EmailVerified || identity.Email != "verified@example.test" || calls != 3 {
		t.Fatalf("bad identity result: %v", err)
	}
}
func TestGitHubRejectsInvalidIdentity(t *testing.T) {
	for _, body := range []string{`{"id":0,"login":"x"}`, `{"id":123}`, `{"id":"123","login":"x"}`, `{broken`} {
		t.Run(body, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "github.com" {
					return response(r, 200, `{"access_token":"token","token_type":"bearer"}`), nil
				}
				return response(r, 200, body), nil
			})}
			p, _ := New("github", client)
			if _, err := p.Authenticate(context.Background(), domain.Connection{}, "https://local.example/callback", "code", "nonce", "verifier"); err == nil {
				t.Fatal("invalid subject accepted")
			}
		})
	}
}
func TestGitHubEmailFailureIsExplicit(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host + r.URL.Path {
		case "github.com/login/oauth/access_token":
			return response(r, 200, `{"access_token":"token","token_type":"bearer"}`), nil
		case "api.github.com/user":
			return response(r, 200, `{"id":123,"login":"x"}`), nil
		default:
			return response(r, 503, `{}`), nil
		}
	})}
	p, _ := New("github", client)
	if _, err := p.Authenticate(context.Background(), domain.Connection{}, "https://local.example/callback", "code", "nonce", "verifier"); err != domain.ErrUnavailable {
		t.Fatal("email dependency failure hidden", err)
	}
}
