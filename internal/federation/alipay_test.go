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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/carvebloom/authova/internal/domain"
)

func alipayTestCredentials(t *testing.T) (domain.Connection, *rsa.PrivateKey, *rsa.PrivateKey) {
	t.Helper()
	application, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	platform, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	public, err := x509.MarshalPKIXPublicKey(&platform.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	credentials := AlipayCredentials{
		PrivateKey:      string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(application)})),
		AlipayPublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public})),
	}
	secret, err := EncodeAlipayCredentials(credentials)
	if err != nil {
		t.Fatal(err)
	}
	return domain.Connection{ClientID: "2021000000000001", ProviderType: "alipay", Secret: secret}, application, platform
}

func TestAlipayAuthorization(t *testing.T) {
	p, err := New("alipay", &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := p.Authorization(context.Background(), domain.Connection{ClientID: "2021000000000001", Secret: "never-in-browser"}, "https://auth.example/callback?x=1&y=2", "bound-state", "", "")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "openauth.alipay.com" || u.Path != "/oauth2/publicAppAuthorize.htm" {
		t.Fatal("unexpected Alipay authorization origin")
	}
	q := u.Query()
	if q.Get("app_id") != "2021000000000001" || q.Get("scope") != "auth_user" || q.Get("state") != "bound-state" || q.Get("redirect_uri") != "https://auth.example/callback?x=1&y=2" || strings.Contains(raw, "never-in-browser") || q.Has("code_challenge") {
		t.Fatal("invalid Alipay browser authorization parameters")
	}
	if _, err = p.Authorization(context.Background(), domain.Connection{ClientID: "invalid"}, "https://auth.example/callback", "state", "", ""); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("malformed AppID accepted")
	}
}

func TestAlipaySignedIdentity(t *testing.T) {
	c, application, platform := alipayTestCredentials(t)
	for _, tc := range []struct {
		name, token, profile, mode string
		accepted                   bool
	}{
		{"signed-openid", `{"access_token":"transient&?token","open_id":"ali_subject_1","user_id":"2088_ignored"}`, `{"code":"10000", "open_id":"ali_subject_1","nick_name":"支付宝用户"}`, "", true},
		{"token-business-error", `{"code":"40004","access_token":"t","open_id":"ali_subject_1"}`, `{}`, "", false},
		{"legacy-userid-only", `{"access_token":"t","user_id":"2088100000000000"}`, `{}`, "", false},
		{"missing-token", `{"open_id":"ali_subject_1"}`, `{}`, "", false},
		{"profile-substitution", `{"access_token":"t","open_id":"ali_subject_1"}`, `{"code":"10000","open_id":"another"}`, "", false},
		{"profile-error", `{"access_token":"t","open_id":"ali_subject_1"}`, `{"code":"40004","open_id":"ali_subject_1"}`, "", false},
		{"tampered-claims", `{"access_token":"t","open_id":"ali_subject_1"}`, `{}`, "tamper", false},
		{"unsigned", `{"access_token":"t","open_id":"ali_subject_1"}`, `{}`, "unsigned", false},
		{"wrong-key", `{"access_token":"t","open_id":"ali_subject_1"}`, `{}`, "wrong-key", false},
		{"ambiguous-error", `{"access_token":"t","open_id":"ali_subject_1"}`, `{}`, "error", false},
		{"oversized", `{}`, `{}`, "oversized", false},
		{"redirect", `{}`, `{}`, "redirect", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "POST" || r.URL.String() != "https://openapi.alipay.com/gateway.do" || r.ParseForm() != nil {
					t.Fatal("Alipay credentials left the fixed POST gateway")
				}
				q := r.PostForm
				var content, response string
				switch q.Get("method") {
				case "alipay.system.oauth.token":
					content = fmt.Sprintf("app_id=2021000000000001&charset=utf-8&code=code&?=bound&format=JSON&grant_type=authorization_code&method=alipay.system.oauth.token&sign_type=RSA2&timestamp=%s&version=1.0", q.Get("timestamp"))
					response = tc.token
				case "alipay.user.info.share":
					var token struct {
						AccessToken string `json:"access_token"`
					}
					if json.Unmarshal([]byte(tc.token), &token) != nil || q.Get("auth_token") != token.AccessToken {
						t.Fatal("token was not bound to profile lookup")
					}
					content = fmt.Sprintf("app_id=2021000000000001&auth_token=%s&charset=utf-8&format=JSON&method=alipay.user.info.share&sign_type=RSA2&timestamp=%s&version=1.0", token.AccessToken, q.Get("timestamp"))
					response = tc.profile
				default:
					t.Fatal("unexpected Alipay method")
				}
				sign, err := base64.StdEncoding.DecodeString(q.Get("sign"))
				digest := sha256.Sum256([]byte(content))
				if err != nil || rsa.VerifyPKCS1v15(&application.PublicKey, crypto.SHA256, digest[:], sign) != nil {
					t.Fatal("RSA2 request signature does not match the protocol canonical string")
				}
				signer := platform
				if tc.mode == "wrong-key" {
					signer = application
				}
				hash := sha256.Sum256([]byte(response))
				sign, err = rsa.SignPKCS1v15(rand.Reader, signer, crypto.SHA256, hash[:])
				if err != nil {
					t.Fatal(err)
				}
				if tc.mode == "tamper" {
					response = strings.ReplaceAll(response, "ali_subject_1", "attacker_subject")
				}
				value := base64.StdEncoding.EncodeToString(sign)
				if tc.mode == "unsigned" {
					value = ""
				}
				body := fmt.Sprintf(`{%q:%s,"sign":%q}`, strings.ReplaceAll(q.Get("method"), ".", "_")+"_response", response, value)
				if tc.mode == "error" {
					body = strings.TrimSuffix(body, "}") + `,"error_response":{"code":"40004"}}`
				}
				if tc.mode == "oversized" {
					body = strings.Repeat("x", (1<<20)+1)
				}
				status, headers := http.StatusOK, http.Header{"Content-Type": {"application/json"}}
				if tc.mode == "redirect" {
					status = http.StatusTemporaryRedirect
					headers.Set("Location", "https://attacker.example/steal")
				}
				return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			p, _ := New("alipay", client)
			identity, err := p.Authenticate(context.Background(), c, "https://auth.example/callback", "code&?=bound", "", "")
			if (err == nil) != tc.accepted {
				t.Fatalf("identity accepted=%v, expected=%v: %v", err == nil, tc.accepted, err)
			}
			if tc.accepted && (identity.Subject != "ali_subject_1" || identity.Name != "支付宝用户" || identity.Email != "" || identity.EmailVerified || calls != 2) {
				t.Fatal("incorrect or fabricated identity claims")
			}
			if !tc.accepted && calls > 2 {
				t.Fatal("unexpected provider I/O or redirect follow")
			}
		})
	}
}

func TestAlipayCredentialValidation(t *testing.T) {
	c, application, _ := alipayTestCredentials(t)
	var valid AlipayCredentials
	if err := json.Unmarshal([]byte(c.Secret), &valid); err != nil {
		t.Fatal(err)
	}
	publicDER, _ := x509.MarshalPKIXPublicKey(&application.PublicKey)
	for name, in := range map[string]AlipayCredentials{
		"missing-private": {AlipayPublicKey: valid.AlipayPublicKey},
		"missing-public":  {PrivateKey: valid.PrivateKey},
		"same-key":        {PrivateKey: valid.PrivateKey, AlipayPublicKey: base64.StdEncoding.EncodeToString(publicDER)},
		"oversized":       {PrivateKey: strings.Repeat("x", 8193), AlipayPublicKey: valid.AlipayPublicKey},
		"invalid-private": {PrivateKey: "not-a-private-key", AlipayPublicKey: valid.AlipayPublicKey},
		"extra-PEM":       {PrivateKey: valid.PrivateKey + valid.PrivateKey, AlipayPublicKey: valid.AlipayPublicKey},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := EncodeAlipayCredentials(in); !errors.Is(err, domain.ErrInvalid) {
				t.Fatal("invalid Alipay key pair accepted")
			}
		})
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(application)
	if err != nil {
		t.Fatal(err)
	}
	valid.PrivateKey = base64.StdEncoding.EncodeToString(pkcs8)
	if _, err = EncodeAlipayCredentials(valid); err != nil {
		t.Fatal("valid PKCS8 base64 key rejected")
	}
}
