package federation

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/carvebloom/authova/internal/domain"
)

// AlipayCredentials is a write-only RSA2 public-key-mode credential pair. Its
// encoded representation lives in the existing encrypted connection payload.
// Certificate mode and Alipay+ are distinct protocols and are not accepted.
type AlipayCredentials struct {
	PrivateKey      string `json:"private_key"`
	AlipayPublicKey string `json:"alipay_public_key"`
}

func EncodeAlipayCredentials(in AlipayCredentials) (string, error) {
	in.PrivateKey, in.AlipayPublicKey = strings.TrimSpace(in.PrivateKey), strings.TrimSpace(in.AlipayPublicKey)
	if _, _, err := in.keys(); err != nil {
		return "", err
	}
	data, err := json.Marshal(in)
	return string(data), err
}

func (c AlipayCredentials) keys() (*rsa.PrivateKey, *rsa.PublicKey, error) {
	privateDER, err := alipayKeyDER(c.PrivateKey, 8192)
	if err != nil {
		return nil, nil, domain.ErrInvalid
	}
	private, err := x509.ParsePKCS1PrivateKey(privateDER)
	if err != nil {
		value, parseErr := x509.ParsePKCS8PrivateKey(privateDER)
		if parseErr != nil {
			return nil, nil, domain.ErrInvalid
		}
		private, _ = value.(*rsa.PrivateKey)
	}
	if private == nil || !alipayRSA(&private.PublicKey) || private.Validate() != nil {
		return nil, nil, domain.ErrInvalid
	}
	publicDER, err := alipayKeyDER(c.AlipayPublicKey, 2048)
	if err != nil {
		return nil, nil, domain.ErrInvalid
	}
	value, err := x509.ParsePKIXPublicKey(publicDER)
	if err != nil {
		return nil, nil, domain.ErrInvalid
	}
	public, ok := value.(*rsa.PublicKey)
	if !ok || !alipayRSA(public) || public.N.Cmp(private.N) == 0 {
		return nil, nil, domain.ErrInvalid
	}
	return private, public, nil
}

func alipayRSA(key *rsa.PublicKey) bool {
	return key.N != nil && key.N.BitLen() >= 2048 && key.N.BitLen() <= 4096 && key.E >= 3 && key.E%2 == 1
}

func alipayKeyDER(value string, limit int) ([]byte, error) {
	if value == "" || len(value) > limit {
		return nil, domain.ErrInvalid
	}
	if strings.HasPrefix(value, "-----BEGIN ") {
		block, rest := pem.Decode([]byte(value))
		if block == nil || len(block.Headers) != 0 || strings.TrimSpace(string(rest)) != "" {
			return nil, domain.ErrInvalid
		}
		return block.Bytes, nil
	}
	return base64.StdEncoding.DecodeString(strings.Join(strings.Fields(value), ""))
}

func ValidAlipayAppID(id string) bool {
	if len(id) < 16 || len(id) > 32 {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

type alipayProvider struct{ client *http.Client }

func (p alipayProvider) Authorization(_ context.Context, c domain.Connection, redirect, state, _, _ string) (string, error) {
	if !ValidAlipayAppID(c.ClientID) {
		return "", domain.ErrInvalid
	}
	q := url.Values{"app_id": {c.ClientID}, "scope": {"auth_user"}, "redirect_uri": {redirect}, "state": {state}}
	return "https://openauth.alipay.com/oauth2/publicAppAuthorize.htm?" + q.Encode(), nil
}

func (p alipayProvider) Authenticate(ctx context.Context, c domain.Connection, _, code, _, _ string) (Identity, error) {
	if code == "" || len(code) > 4096 || !ValidAlipayAppID(c.ClientID) {
		return Identity{}, domain.ErrUnauthenticated
	}
	var credentials AlipayCredentials
	if json.Unmarshal([]byte(c.Secret), &credentials) != nil {
		return Identity{}, domain.ErrUnavailable
	}
	private, public, err := credentials.keys()
	if err != nil {
		return Identity{}, domain.ErrUnavailable
	}
	var token struct {
		Code        string `json:"code"`
		AccessToken string `json:"access_token"`
		OpenID      string `json:"open_id"`
	}
	if err = p.call(ctx, c.ClientID, private, public, "alipay.system.oauth.token", url.Values{"grant_type": {"authorization_code"}, "code": {code}}, &token); err != nil {
		return Identity{}, err
	}
	// Older token responses omit the success code. Modern integrations must
	// return open_id; do not switch between user_id/open_id or use union_id.
	if (token.Code != "" && token.Code != "10000") || token.AccessToken == "" || len(token.AccessToken) > 4096 || !validOpenID(token.OpenID) {
		return Identity{}, domain.ErrUnauthenticated
	}
	var profile struct {
		Code     string `json:"code"`
		OpenID   string `json:"open_id"`
		Nickname string `json:"nick_name"`
	}
	if err = p.call(ctx, c.ClientID, private, public, "alipay.user.info.share", url.Values{"auth_token": {token.AccessToken}}, &profile); err != nil {
		return Identity{}, err
	}
	if profile.Code != "10000" || profile.OpenID != token.OpenID {
		return Identity{}, domain.ErrUnauthenticated
	}
	return Identity{Subject: token.OpenID, Name: profileName(profile.Nickname)}, nil
}

func (p alipayProvider) call(ctx context.Context, appID string, private *rsa.PrivateKey, public *rsa.PublicKey, method string, params url.Values, out any) error {
	params.Set("app_id", appID)
	params.Set("method", method)
	params.Set("format", "JSON")
	params.Set("charset", "utf-8")
	params.Set("sign_type", "RSA2")
	params.Set("timestamp", time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02 15:04:05"))
	params.Set("version", "1.0")
	digest := sha256.Sum256([]byte(alipaySignContent(params)))
	signature, err := rsa.SignPKCS1v15(rand.Reader, private, crypto.SHA256, digest[:])
	if err != nil {
		return domain.ErrUnavailable
	}
	params.Set("sign", base64.StdEncoding.EncodeToString(signature))
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://openapi.alipay.com/gateway.do", strings.NewReader(params.Encode()))
	if err != nil {
		return domain.ErrUnavailable
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	r.Header.Set("Accept", "application/json")
	r.Header.Set("User-Agent", "Authova")
	r.Header.Set("Cache-Control", "no-store")
	// Signed credentials and transient tokens must never follow a redirect.
	client := *p.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(r)
	if err != nil {
		return domain.ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return domain.ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || !utf8.Valid(data) {
		return domain.ErrUnavailable
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(data, &envelope) != nil {
		return domain.ErrUnauthenticated
	}
	body := envelope[strings.ReplaceAll(method, ".", "_")+"_response"]
	var sign string
	if len(body) == 0 || body[0] != '{' || len(envelope["error_response"]) != 0 || json.Unmarshal(envelope["sign"], &sign) != nil || len(sign) > 1024 {
		return domain.ErrUnauthenticated
	}
	sig, err := base64.StdEncoding.DecodeString(sign)
	if err != nil {
		return domain.ErrUnauthenticated
	}
	// Verify the exact JSON object bytes before decoding any identity claims.
	hash := sha256.Sum256(body)
	if rsa.VerifyPKCS1v15(public, crypto.SHA256, hash[:], sig) != nil || json.Unmarshal(body, out) != nil {
		return domain.ErrUnauthenticated
	}
	return nil
}

func alipaySignContent(params url.Values) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		if key != "sign" && params.Get(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+params.Get(key))
	}
	return strings.Join(parts, "&")
}
