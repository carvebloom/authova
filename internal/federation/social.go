package federation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/carvebloom/authova/internal/domain"
)

// These website protocols do not document upstream PKCE. Authova still binds
// each callback to a one-time state, browser cookie and configuration version.
// Query credentials are mandated by the providers: never return/log raw HTTP
// errors, response bodies or request URLs from these adapters.
type wechatProvider struct{ client *http.Client }

func (p wechatProvider) Authorization(_ context.Context, c domain.Connection, redirect, state, _, _ string) (string, error) {
	q := url.Values{"appid": {c.ClientID}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {"snsapi_login"}, "state": {state}}
	return "https://open.weixin.qq.com/connect/qrconnect?" + q.Encode() + "#wechat_redirect", nil
}
func (p wechatProvider) Authenticate(ctx context.Context, c domain.Connection, _, code, _, _ string) (Identity, error) {
	if code == "" {
		return Identity{}, domain.ErrUnauthenticated
	}
	var token struct {
		AccessToken string `json:"access_token"`
		OpenID      string `json:"openid"`
		Scope       string `json:"scope"`
		UnionID     string `json:"unionid"`
		ErrCode     int64  `json:"errcode"`
	}
	if err := socialJSON(ctx, p.client, "https://api.weixin.qq.com/sns/oauth2/access_token", url.Values{"appid": {c.ClientID}, "secret": {c.Secret}, "code": {code}, "grant_type": {"authorization_code"}}, &token); err != nil {
		return Identity{}, err
	}
	scopes := strings.Split(token.Scope, ",")
	for i := range scopes {
		scopes[i] = strings.TrimSpace(scopes[i])
	}
	if token.ErrCode != 0 || token.AccessToken == "" || len(token.AccessToken) > 4096 || !validOpenID(token.OpenID) || !slices.Contains(scopes, "snsapi_login") {
		return Identity{}, domain.ErrUnauthenticated
	}
	var profile struct {
		OpenID   string `json:"openid"`
		Nickname string `json:"nickname"`
		UnionID  string `json:"unionid"`
		ErrCode  int64  `json:"errcode"`
	}
	if err := socialJSON(ctx, p.client, "https://api.weixin.qq.com/sns/userinfo", url.Values{"access_token": {token.AccessToken}, "openid": {token.OpenID}, "lang": {"zh_CN"}}, &profile); err != nil {
		return Identity{}, err
	}
	if profile.ErrCode != 0 || profile.OpenID != token.OpenID || (token.UnionID != "" && profile.UnionID != "" && token.UnionID != profile.UnionID) {
		return Identity{}, domain.ErrUnauthenticated
	}
	// OpenID is scoped to the immutable AppID/connection. UnionID is optional;
	// choosing it conditionally would change the identity when it later appears.
	return Identity{Subject: token.OpenID, Name: profileName(profile.Nickname)}, nil
}

type qqProvider struct{ client *http.Client }

func (p qqProvider) Authorization(_ context.Context, c domain.Connection, redirect, state, _, _ string) (string, error) {
	q := url.Values{"client_id": {c.ClientID}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {"get_user_info"}, "state": {state}}
	return "https://graph.qq.com/oauth2.0/authorize?" + q.Encode(), nil
}
func (p qqProvider) Authenticate(ctx context.Context, c domain.Connection, redirect, code, _, _ string) (Identity, error) {
	if code == "" {
		return Identity{}, domain.ErrUnauthenticated
	}
	var token struct {
		AccessToken string          `json:"access_token"`
		Error       json.RawMessage `json:"error"`
	}
	// fmt=json is officially supported; JSONP/JavaScript is never evaluated.
	if err := socialJSON(ctx, p.client, "https://graph.qq.com/oauth2.0/token", url.Values{"grant_type": {"authorization_code"}, "client_id": {c.ClientID}, "client_secret": {c.Secret}, "code": {code}, "redirect_uri": {redirect}, "fmt": {"json"}}, &token); err != nil {
		return Identity{}, err
	}
	if len(token.Error) != 0 || token.AccessToken == "" || len(token.AccessToken) > 4096 {
		return Identity{}, domain.ErrUnauthenticated
	}
	var identity struct {
		ClientID string          `json:"client_id"`
		OpenID   string          `json:"openid"`
		Error    json.RawMessage `json:"error"`
	}
	if err := socialJSON(ctx, p.client, "https://graph.qq.com/oauth2.0/me", url.Values{"access_token": {token.AccessToken}, "fmt": {"json"}}, &identity); err != nil {
		return Identity{}, err
	}
	if len(identity.Error) != 0 || identity.ClientID != c.ClientID || !validOpenID(identity.OpenID) {
		return Identity{}, domain.ErrUnauthenticated
	}
	var profile struct {
		Ret      *int64 `json:"ret"`
		Nickname string `json:"nickname"`
	}
	if err := socialJSON(ctx, p.client, "https://graph.qq.com/user/get_user_info", url.Values{"access_token": {token.AccessToken}, "oauth_consumer_key": {c.ClientID}, "openid": {identity.OpenID}}, &profile); err != nil {
		return Identity{}, err
	}
	if profile.Ret == nil || *profile.Ret != 0 {
		return Identity{}, domain.ErrUnauthenticated
	}
	return Identity{Subject: identity.OpenID, Name: profileName(profile.Nickname)}, nil
}

func socialJSON(ctx context.Context, client *http.Client, endpoint string, query url.Values, out any) error {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return domain.ErrUnavailable
	}
	r.Header.Set("Accept", "application/json")
	r.Header.Set("User-Agent", "Authova")
	r.Header.Set("Cache-Control", "no-store")
	resp, err := client.Do(r)
	if err != nil {
		return domain.ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return domain.ErrUnauthenticated
	}
	if resp.StatusCode != http.StatusOK {
		return domain.ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || json.Unmarshal(data, out) != nil {
		return domain.ErrUnavailable
	}
	return nil
}
func validOpenID(id string) bool {
	if id == "" || len(id) > 256 {
		return false
	}
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func profileName(name string) string {
	name = strings.TrimSpace(name)
	if len(name) > 200 {
		name = name[:200]
		for !utf8.ValidString(name) {
			name = name[:len(name)-1]
		}
	}
	return name
}
