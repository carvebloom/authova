package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

func TestSocialProviders(t *testing.T) {
	configured := false
	for _, driver := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(driver) + "_DSN")
		if dsn == "" {
			continue
		}
		configured = true
		t.Run(driver, func(t *testing.T) { f := setup(t, driver, dsn); f.socialProviderChecks(t) })
	}
	if !configured {
		t.Skip("real PostgreSQL/MySQL DSNs required")
	}
}
func (f *fixture) socialConnection(t *testing.T, app domain.App, kind string) domain.Connection {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"name": kind + " fixture", "provider_type": kind, "client_id": application.ID(), "client_secret": "social-test-only-secret"})
	r, _ := http.NewRequest("POST", f.server.URL+"/api/v1/apps/"+app.ID+"/connections", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+f.operatorToken)
	r.Header.Set("Content-Type", "application/json")
	resp, e := f.http.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 201 {
		t.Fatal("social configuration failed", resp.StatusCode)
	}
	var c domain.Connection
	if json.Unmarshal(data, &c) != nil || c.ProviderType != kind || c.Version != 1 || strings.Contains(string(data), "social-test-only-secret") {
		t.Fatal("social config response invalid")
	}
	expected := "https://graph.qq.com"
	if kind == "wechat" {
		expected = "https://open.weixin.qq.com"
	}
	if c.Issuer != expected {
		t.Fatal("issuer not fixed")
	}
	return c
}
func (f *fixture) socialLogin(t *testing.T, app domain.App, scope domain.Scope, c domain.Connection) string {
	t.Helper()
	flow := domain.Flow{ID: secure.Random(32), ClientID: f.client.ClientID, Step: "choose_method", Purpose: "authorize", CSRF: secure.Random(32), Expires: time.Now().Add(time.Minute).Unix()}
	if app.ID == f.app2.ID {
		client, _, e := f.service.CreateClient(context.Background(), f.operatorToken, scope, domain.Client{Name: "Social App2 RP", Kind: "public", RedirectURIs: []string{f.server.URL + "/test-callback"}, Scopes: []string{"openid"}, Audiences: []string{"urn:social:app2"}, Grants: []string{"authorization_code"}}, "social-app2-"+application.ID())
		if e != nil {
			t.Fatal(e)
		}
		flow.ClientID = client.ClientID
	}
	handle := secure.Random(32)
	flow.CookieDigest = f.service.Digest(app.ID, "flow-cookie", handle)
	if e := f.store.App(context.Background(), scope, true, func(tx ports.AppTx) error { return tx.PutFlow(flow) }); e != nil {
		t.Fatal(e)
	}
	base, _ := url.Parse(f.server.URL)
	f.http.Jar.SetCookies(base, []*http.Cookie{{Name: "av_flow_" + flow.ID, Value: handle, Path: "/a/" + app.Key}})
	status, data := f.post(app.Issuer+"/ui/flows/"+flow.ID+"/federation", url.Values{"_csrf": {flow.CSRF}, "connection_id": {c.ID}}, false)
	if status != 200 {
		t.Fatal("social login start", status)
	}
	link, found := identityProviderURL(data)
	if !found {
		t.Fatal("social navigation absent")
	}
	u, _ := url.Parse(link)
	resp, e := f.http.Get(app.Issuer + "/ui/federation/callback?state=" + url.QueryEscape(u.Query().Get("state")) + "&code=valid")
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("social login callback", resp.StatusCode)
	}
	var user string
	if e = f.store.App(context.Background(), scope, false, func(tx ports.AppTx) error {
		current, e := tx.Flow(flow.ID, false)
		if e == nil {
			if current.Step != "consent" || current.UserID == "" {
				t.Fatal("social login did not reach authenticated consent")
			}
			user = current.UserID
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	return user
}
func (f *fixture) socialProviderChecks(t *testing.T) {
	var calls atomic.Int64
	f.service.UpstreamClient = &http.Client{Transport: identityTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		q := r.URL.Query()
		var body any
		switch r.URL.Host + r.URL.Path {
		case "api.weixin.qq.com/sns/oauth2/access_token":
			body = map[string]any{"access_token": "wx-transient", "openid": "wx_stable_openid", "scope": "snsapi_login", "unionid": "shared-union"}
		case "api.weixin.qq.com/sns/userinfo":
			body = map[string]any{"openid": "wx_stable_openid", "nickname": "微信用户", "unionid": "shared-union"}
		case "graph.qq.com/oauth2.0/token":
			body = map[string]any{"access_token": "qq-token:" + q.Get("client_id")}
		case "graph.qq.com/oauth2.0/me":
			body = map[string]any{"client_id": strings.TrimPrefix(q.Get("access_token"), "qq-token:"), "openid": "qq_stable_openid", "unionid": "shared-union"}
		case "graph.qq.com/user/get_user_info":
			body = map[string]any{"ret": 0, "nickname": "QQ用户"}
		default:
			t.Fatal("unexpected social fixture endpoint")
		}
		data, _ := json.Marshal(body)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(data)), Request: r}, nil
	})}
	f.sessionFor(t, f.user1)
	connections := []domain.Connection{f.socialConnection(t, f.app1, "wechat"), f.socialConnection(t, f.app1, "qq")}
	for _, c := range connections {
		state, pkce := f.linkConnection(t, c)
		if pkce != "" {
			t.Fatal("unsupported social upstream PKCE advertised")
		}
		before := calls.Load()
		resp, e := f.http.Get(f.app1.Issuer + "/ui/federation/callback?state=" + url.QueryEscape(state+"bad") + "&code=valid")
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
		if resp.StatusCode != 403 || calls.Load() != before {
			t.Fatal("invalid state caused upstream I/O")
		}
		callback := f.app1.Issuer + "/ui/federation/callback?state=" + url.QueryEscape(state) + "&code=valid"
		resp, e = f.http.Get(callback)
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatal("social binding failed", resp.StatusCode)
		}
		before = calls.Load()
		resp, e = f.http.Get(callback)
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
		if (resp.StatusCode != 403 && resp.StatusCode != 409) || calls.Load() != before {
			t.Fatal("social state replay accepted", resp.StatusCode)
		}
		if user := f.socialLogin(t, f.app1, f.scope1, c); user != f.user1.ID {
			t.Fatal("provider login did not reuse bound local account")
		}
	}
	if e := f.store.App(context.Background(), f.scope1, false, func(tx ports.AppTx) error {
		bindings, e := tx.ExternalBindings(f.user1.ID)
		if e == nil && len(bindings) != 2 {
			t.Fatal("single user not linked to both providers")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	other := domain.User{ID: application.ID(), AppID: f.app1.ID, Status: "active", Epoch: 1, CreatedAt: time.Now()}
	if e := f.store.App(context.Background(), f.scope1, true, func(tx ports.AppTx) error { return tx.CreateUser(other, "", "") }); e != nil {
		t.Fatal(e)
	}
	f.sessionFor(t, other)
	state, _ := f.linkConnection(t, connections[0])
	resp, e := f.http.Get(f.app1.Issuer + "/ui/federation/callback?state=" + url.QueryEscape(state) + "&code=valid")
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 409 {
		t.Fatal("wechat identity stolen from existing account")
	}
	// Identical upstream OpenIDs/UnionIDs in another local App never merge.
	for _, kind := range []string{"wechat", "qq"} {
		c := f.socialConnection(t, f.app2, kind)
		user := f.socialLogin(t, f.app2, f.scope2, c)
		if user == f.user1.ID {
			t.Fatal("social identity crossed App boundary")
		}
		if e = f.store.App(context.Background(), f.scope2, false, func(tx ports.AppTx) error {
			u, e := tx.User(user, false)
			if e == nil && (u.AppID != f.app2.ID || u.Email != "") {
				t.Fatal("wrong namespace or fabricated email")
			}
			return e
		}); e != nil {
			t.Fatal(e)
		}
	}
	// An arbitrary issuer must not redirect the social client secret elsewhere.
	status, _ := f.managementJSON("POST", "/connections", map[string]string{"name": "invalid issuer", "provider_type": "wechat", "issuer": "https://evil.example", "client_id": "appid", "client_secret": "test-secret"})
	if status != 400 {
		t.Fatal("social issuer override accepted")
	}
}
