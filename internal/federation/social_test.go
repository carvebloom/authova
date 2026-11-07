package federation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/carvebloom/authova/internal/domain"
)

func TestSocialAuthorization(t *testing.T) {
	for _, tc := range []struct{ kind, host, path, scope string }{{"wechat", "open.weixin.qq.com", "/connect/qrconnect", "snsapi_login"}, {"qq", "graph.qq.com", "/oauth2.0/authorize", "get_user_info"}} {
		t.Run(tc.kind, func(t *testing.T) {
			p, e := New(tc.kind, &http.Client{})
			if e != nil {
				t.Fatal(e)
			}
			raw, e := p.Authorization(context.Background(), domain.Connection{ClientID: "test-app", Secret: "private-fixture-secret"}, "https://auth.example/callback?value=x&next=y", "one-time-state", "nonce", "verifier")
			if e != nil {
				t.Fatal(e)
			}
			u, e := url.Parse(raw)
			if e != nil {
				t.Fatal(e)
			}
			q := u.Query()
			if u.Scheme != "https" || u.Host != tc.host || u.Path != tc.path || q.Get("state") != "one-time-state" || q.Get("response_type") != "code" || q.Get("scope") != tc.scope || q.Get("redirect_uri") != "https://auth.example/callback?value=x&next=y" {
				t.Fatal("authorization incorrectly encoded")
			}
			if q.Has("code_challenge") || q.Has("client_secret") || strings.Contains(raw, "private-fixture-secret") {
				t.Fatal("unsupported PKCE or credential in browser navigation")
			}
			if tc.kind == "wechat" && (q.Get("appid") != "test-app" || u.Fragment != "wechat_redirect") {
				t.Fatal("wechat authorization contract")
			}
			if tc.kind == "qq" && q.Get("client_id") != "test-app" {
				t.Fatal("qq authorization contract")
			}
		})
	}
}
func socialFixture(t *testing.T, kind string, override map[string]string, status int) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		body := ""
		if r.Method != "GET" || r.URL.Scheme != "https" {
			t.Fatal("noncanonical social request")
		}
		if kind == "wechat" {
			if r.URL.Host != "api.weixin.qq.com" {
				t.Fatal("wechat credentials outside fixed host")
			}
			switch r.URL.Path {
			case "/sns/oauth2/access_token":
				if q.Get("appid") != "test-app" || q.Get("secret") != "secret&?=" || q.Get("grant_type") != "authorization_code" || q.Get("code") != "code" {
					t.Fatal("wechat token parameters")
				}
				body = `{"access_token":"transient&token","openid":"wx_openid_1","scope":"snsapi_login","unionid":"same_union"}`
			case "/sns/userinfo":
				if q.Get("openid") != "wx_openid_1" || q.Get("access_token") != "transient&token" {
					t.Fatal("wechat profile proof")
				}
				body = `{"openid":"wx_openid_1","nickname":"微信用户","unionid":"same_union"}`
			default:
				t.Fatal("unexpected wechat endpoint")
			}
		} else {
			if r.URL.Host != "graph.qq.com" {
				t.Fatal("qq credentials outside fixed host")
			}
			switch r.URL.Path {
			case "/oauth2.0/token":
				if q.Get("client_id") != "test-app" || q.Get("client_secret") != "secret&?=" || q.Get("redirect_uri") != "https://auth.example/callback" || q.Get("fmt") != "json" || q.Get("code") != "code" {
					t.Fatal("qq token proof")
				}
				body = `{"access_token":"transient&token","expires_in":7776000}`
			case "/oauth2.0/me":
				if q.Get("access_token") != "transient&token" || q.Get("fmt") != "json" {
					t.Fatal("qq identity proof")
				}
				body = `{"client_id":"test-app","openid":"qq_openid_1","unionid":"same_union"}`
			case "/user/get_user_info":
				if q.Get("oauth_consumer_key") != "test-app" || q.Get("openid") != "qq_openid_1" || q.Get("access_token") != "transient&token" {
					t.Fatal("qq profile proof")
				}
				body = `{"ret":0,"nickname":"QQ用户"}`
			default:
				t.Fatal("unexpected qq endpoint")
			}
		}
		if replacement, ok := override[r.URL.Path]; ok {
			body = replacement
		}
		return response(r, status, body), nil
	})}
}
func TestSocialStableOpenID(t *testing.T) {
	for _, kind := range []string{"wechat", "qq"} {
		t.Run(kind, func(t *testing.T) {
			client := socialFixture(t, kind, nil, 200)
			p, _ := New(kind, client)
			c := domain.Connection{ClientID: "test-app", Secret: "secret&?="}
			id, e := p.Authenticate(context.Background(), c, "https://auth.example/callback", "code", "nonce", "verifier")
			if e != nil {
				t.Fatal(e)
			}
			prefix := "wx"
			if kind == "qq" {
				prefix = "qq"
			}
			if id.Subject != prefix+"_openid_1" || id.Email != "" || id.EmailVerified {
				t.Fatal("unstable subject or fabricated email")
			}
			if kind == "wechat" {
				client = socialFixture(t, kind, map[string]string{"/sns/oauth2/access_token": `{"access_token":"transient&token","openid":"wx_openid_1","scope":"snsapi_login"}`}, 200)
				p, _ = New(kind, client)
				withoutUnion, e := p.Authenticate(context.Background(), c, "https://auth.example/callback", "code", "nonce", "verifier")
				if e != nil || withoutUnion.Subject != id.Subject {
					t.Fatal("optional unionid changed identity", e)
				}
			}
		})
	}
}
func TestSocialRejectsUnverifiedResponses(t *testing.T) {
	cases := []struct{ kind, path, body string }{
		{"wechat", "/sns/oauth2/access_token", `{"errcode":40163,"errmsg":"used code"}`},
		{"wechat", "/sns/oauth2/access_token", `{"access_token":"transient&token","openid":"wx_openid_1","scope":"snsapi_base"}`},
		{"wechat", "/sns/oauth2/access_token", `{"access_token":"transient&token","scope":"snsapi_login"}`},
		{"wechat", "/sns/userinfo", `{"openid":"another_user"}`},
		{"wechat", "/sns/userinfo", `{"openid":"wx_openid_1","unionid":"different_union"}`},
		{"wechat", "/sns/userinfo", `{"errcode":40003,"openid":"wx_openid_1"}`},
		{"qq", "/oauth2.0/token", `{"error":100001,"access_token":"transient&token"}`},
		{"qq", "/oauth2.0/me", `{"client_id":"other-app","openid":"qq_openid_1"}`},
		{"qq", "/oauth2.0/me", `{"client_id":"test-app","openid":"bad\u0000subject"}`},
		{"qq", "/oauth2.0/me", `callback({"client_id":"test-app","openid":"qq_openid_1"});`},
		{"qq", "/user/get_user_info", `{"nickname":"without-positive-ret"}`},
		{"qq", "/user/get_user_info", `{"ret":1002,"nickname":"denied"}`},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprintf("%s-%d", tc.kind, i), func(t *testing.T) {
			p, _ := New(tc.kind, socialFixture(t, tc.kind, map[string]string{tc.path: tc.body}, 200))
			if _, e := p.Authenticate(context.Background(), domain.Connection{ClientID: "test-app", Secret: "secret&?="}, "https://auth.example/callback", "code", "nonce", "verifier"); e == nil {
				t.Fatal("unverified identity accepted")
			}
		})
	}
}
func TestSocialErrorsDoNotLeakSecrets(t *testing.T) {
	for _, kind := range []string{"wechat", "qq"} {
		t.Run(kind, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return nil, errors.New(r.URL.String() + "&upstream-token=private")
			})}
			p, _ := New(kind, client)
			_, e := p.Authenticate(context.Background(), domain.Connection{ClientID: "test-app", Secret: "private-secret"}, "https://auth.example/callback", "private-code", "nonce", "verifier")
			if e != domain.ErrUnavailable || strings.Contains(e.Error(), "private") {
				t.Fatal("HTTP error leaked query credentials")
			}
			p, _ = New(kind, socialFixture(t, kind, nil, 503))
			if _, e = p.Authenticate(context.Background(), domain.Connection{ClientID: "test-app", Secret: "secret&?="}, "https://auth.example/callback", "code", "nonce", "verifier"); e != domain.ErrUnavailable {
				t.Fatal("dependency failure not explicit", e)
			}
		})
	}
}
func TestSocialBoundedJSONAndNames(t *testing.T) {
	p, _ := New("wechat", socialFixture(t, "wechat", map[string]string{"/sns/userinfo": "{\"openid\":\"wx_openid_1\",\"nickname\":\"" + strings.Repeat("x", 1<<20) + "\"}"}, 200))
	if _, e := p.Authenticate(context.Background(), domain.Connection{ClientID: "test-app", Secret: "secret&?="}, "https://auth.example/callback", "code", "nonce", "verifier"); e != domain.ErrUnavailable {
		t.Fatal("unbounded response accepted", e)
	}
	name := profileName(strings.Repeat("微信用户", 100))
	if !utf8.ValidString(name) || len(name) > 200 {
		t.Fatal("nickname truncation damaged UTF-8")
	}
}
