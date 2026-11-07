package integration

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
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/federation"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

func TestAlipayConnectionLifecycle(t *testing.T) {
	configured := false
	for _, driver := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(driver) + "_DSN")
		if dsn == "" {
			continue
		}
		configured = true
		t.Run(driver, func(t *testing.T) { setup(t, driver, dsn).alipayChecks(t) })
	}
	if !configured {
		t.Skip("real PostgreSQL/MySQL required")
	}
}

func (f *fixture) alipayChecks(t *testing.T) {
	ctx := context.Background()
	applicationKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	platformKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&platformKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	credentials := federation.AlipayCredentials{
		PrivateKey:      string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(applicationKey)})),
		AlipayPublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})),
	}
	input := map[string]any{"name": "Alipay integration", "provider_type": "alipay", "client_id": "2021000000000001", "alipay_credentials": credentials}
	status, body := f.managementJSON("POST", "/connections", input)
	if status != http.StatusCreated {
		t.Fatal("Alipay configuration failed", status)
	}
	var connection domain.Connection
	if json.Unmarshal(body, &connection) != nil || connection.ProviderType != "alipay" || connection.Issuer != "https://openauth.alipay.com" || strings.Contains(string(body), "PRIVATE KEY") || strings.Contains(string(body), "alipay_credentials") {
		t.Fatal("Alipay creation leaked credentials or returned invalid metadata")
	}
	for name, patch := range map[string]map[string]any{
		"shared-secret": {"client_secret": "not-an-alipay-key"},
		"partial-keys":  {"alipay_credentials": map[string]string{"private_key": credentials.PrivateKey}},
		"identity":      {"client_id": "2021000000000002"},
	} {
		patch["config_version"], patch["reason"] = 1, "reject invalid "+name
		if status, _ = f.managementJSON("PATCH", "/connections/"+connection.ID, patch); status != http.StatusBadRequest {
			t.Fatalf("invalid %s update accepted: %d", name, status)
		}
	}
	rotatedKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	platformKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rotatedPublic, err := x509.MarshalPKIXPublicKey(&platformKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	rotatedCredentials := credentials
	rotatedCredentials.PrivateKey = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rotatedKey)}))
	rotatedCredentials.AlipayPublicKey = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: rotatedPublic}))
	status, body = f.managementJSON("PATCH", "/connections/"+connection.ID, map[string]any{"config_version": 1, "reason": "rotate complete Alipay pair", "alipay_credentials": rotatedCredentials})
	if status != http.StatusOK || strings.Contains(string(body), "PRIVATE KEY") || strings.Contains(string(body), "alipay_credentials") {
		t.Fatal("Alipay atomic key update failed or leaked credentials", status)
	}
	if status, _ = f.managementJSON("PATCH", "/connections/"+connection.ID, map[string]any{"config_version": 1, "reason": "reject stale rotation", "alipay_credentials": credentials}); status != http.StatusConflict {
		t.Fatal("stale Alipay rotation accepted", status)
	}
	if err = f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
		stored, err := tx.Connection(connection.ID)
		if err == nil {
			var pair federation.AlipayCredentials
			if json.Unmarshal([]byte(stored.Secret), &pair) != nil || stored.Version != 2 {
				t.Fatal("rotated Alipay credential pair was not persisted atomically")
			}
			// Compare actual RSA key identities, independent of PEM whitespace.
			privateBlock, _ := pem.Decode([]byte(pair.PrivateKey))
			publicBlock, _ := pem.Decode([]byte(pair.AlipayPublicKey))
			if privateBlock == nil || publicBlock == nil {
				t.Fatal("persisted key pair cannot be decoded")
			}
			private, privateErr := x509.ParsePKCS1PrivateKey(privateBlock.Bytes)
			publicValue, publicErr := x509.ParsePKIXPublicKey(publicBlock.Bytes)
			public, ok := publicValue.(*rsa.PublicKey)
			if privateErr != nil || publicErr != nil || !ok || private.N.Cmp(rotatedKey.N) != 0 || public.N.Cmp(platformKey.N) != 0 {
				t.Fatal("persisted Alipay keys do not match the complete rotated pair")
			}
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	f.service.UpstreamClient = &http.Client{Transport: identityTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.URL.String() != "https://openapi.alipay.com/gateway.do" || r.Method != "POST" || r.ParseForm() != nil || r.PostForm.Get("app_id") != "2021000000000001" {
			t.Fatal("noncanonical Alipay request")
		}
		method := r.PostForm.Get("method")
		var response string
		switch method {
		case "alipay.system.oauth.token":
			if r.PostForm.Get("code") != "valid-auth-code" {
				t.Fatal("Alipay auth_code was not passed to the token endpoint")
			}
			response = `{"access_token":"transient-token","open_id":"same-alipay-openid"}`
		case "alipay.user.info.share":
			response = `{"code":"10000","open_id":"same-alipay-openid","nick_name":"Alipay integration user"}`
		default:
			t.Fatal("unexpected Alipay method")
		}
		hash := sha256.Sum256([]byte(response))
		sig, err := rsa.SignPKCS1v15(rand.Reader, platformKey, crypto.SHA256, hash[:])
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(map[string]any{strings.ReplaceAll(method, ".", "_") + "_response": json.RawMessage(response), "sign": base64.StdEncoding.EncodeToString(sig)})
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(data))), Request: r}, nil
	})}
	policy := domain.AuthenticationPolicy{RegistrationEnabled: true, FederationEnabled: true, ExternalRegistrationEnabled: true}
	status, _ = f.managementJSON("PATCH", "/authentication-config", map[string]any{"config_version": 1, "reason": "allow third-party methods only", "policy": policy})
	if status != http.StatusOK {
		t.Fatal("third-party-only policy rejected", status)
	}
	for _, method := range []string{"password", "email", "phone"} {
		flow := f.securityFlow(t, "authorize")
		if method == "password" {
			err = f.service.PasswordLogin(ctx, f.scope1, flow.ID, "email", f.user1.Email, password)
		} else {
			value := f.user1.Email
			if method == "phone" {
				value = "+8613800138000"
			}
			_, err = f.service.RequestChallenge(ctx, f.scope1, flow.ID, "login", method, value, true)
		}
		if !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("disabled %s method bypassed policy: %v", method, err)
		}
	}
	// An App 2 flow still follows its own unchanged policy.
	if err = f.store.App(ctx, f.scope2, false, func(tx ports.AppTx) error {
		app, err := tx.App()
		if err == nil && app.AuthenticationPolicy() != domain.DefaultAuthenticationPolicy() {
			t.Fatal("App 2 policy changed")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	start := func() string {
		flow := f.securityFlow(t, "authorize")
		handle := secure.Random(32)
		flow.CookieDigest = f.service.Digest(f.app1.ID, "flow-cookie", handle)
		if err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error { return tx.PutFlow(flow) }); err != nil {
			t.Fatal(err)
		}
		base, _ := url.Parse(f.server.URL)
		f.http.Jar.SetCookies(base, []*http.Cookie{{Name: "av_flow_" + flow.ID, Value: handle, Path: "/a/" + f.app1.Key}})
		status, data := f.post(f.app1.Issuer+"/ui/flows/"+flow.ID+"/federation", url.Values{"_csrf": {flow.CSRF}, "connection_id": {connection.ID}}, false)
		link, found := identityProviderURL(data)
		if status != http.StatusOK || !found {
			t.Fatal("Alipay authorization did not start", status)
		}
		u, _ := url.Parse(link)
		return f.app1.Issuer + "/ui/federation/callback?state=" + url.QueryEscape(u.Query().Get("state"))
	}
	callback := start() + "&auth_code=valid-auth-code&app_id=2021000000000001"
	response, err := f.http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || calls.Load() != 2 {
		t.Fatal("signed Alipay login did not reach consent", response.StatusCode)
	}
	flowUser := func(callback string) string {
		u, _ := url.Parse(callback)
		flowID := strings.SplitN(u.Query().Get("state"), ".", 2)[0]
		var userID string
		if err := f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
			flow, err := tx.Flow(flowID, false)
			if err == nil {
				if flow.Step != domain.StepConsent || flow.PrimaryMethod != "federated" || flow.UserID == "" || flow.UserID == f.user1.ID {
					t.Fatal("Alipay proof did not produce an independent authenticated identity")
				}
				userID = flow.UserID
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return userID
	}
	firstUser := flowUser(callback)
	secondCallback := start() + "&auth_code=valid-auth-code&app_id=2021000000000001"
	response, err = f.http.Get(secondCallback)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || flowUser(secondCallback) != firstUser {
		t.Fatal("Alipay open_id did not resolve to its existing local identity")
	}
	for _, suffix := range []string{"&auth_code=valid-auth-code&app_id=2021000000000002", "&code=valid-auth-code&app_id=2021000000000001", "&auth_code=valid-auth-code&auth_code=ambiguous&app_id=2021000000000001"} {
		before := calls.Load()
		response, err = f.http.Get(start() + suffix)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized || calls.Load() != before {
			t.Fatal("invalid Alipay callback reached upstream")
		}
	}
	before := calls.Load()
	response, err = f.http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode == http.StatusOK || calls.Load() != before {
		t.Fatal("Alipay state replay accepted")
	}
	callback = start() + "&auth_code=valid-auth-code&app_id=2021000000000001"
	policy.FederationEnabled = false
	status, _ = f.managementJSON("PATCH", "/authentication-config", map[string]any{"config_version": 2, "reason": "disable before callback", "policy": policy})
	if status != http.StatusOK {
		t.Fatal("policy disable failed", status)
	}
	before = calls.Load()
	response, err = f.http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden || calls.Load() != before {
		t.Fatal("disabled federation performed provider I/O")
	}
}
