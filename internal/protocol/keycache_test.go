package protocol

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	jose "github.com/go-jose/go-jose/v4"
)

func TestMalformedSuccessfulTokenResponseIsRejected(t *testing.T) {
	for _, body := range []string{`{"access_token":123}`, `{"access_token":"test","id_token":123}`, `{"access_token":"test","id_token":null}`} {
		record := httptest.NewRecorder()
		record.Body.WriteString(body)
		if err := (&Provider{}).profile(context.Background(), record); err == nil {
			t.Fatal("malformed token response accepted", body)
		}
	}
}

func TestParsedIntrospectionJWTMustStillVerify(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	app := domain.App{ID: "test-app", Issuer: "https://identity.example/a/test"}
	provider := &Provider{cache: verificationCache{sets: map[string]verificationSet{app.ID + ":1": {expires: time.Now().Add(time.Minute), keys: map[string]verificationKey{"key": {public: &key.PublicKey, current: true, expires: time.Now().Add(time.Minute)}}}}}}
	ctx := WithTransaction(context.Background(), nil, app, "", "")
	payload := map[string]any{"iss": app.Issuer, "sub": "user", "client_id": "client", "jti": "token", "sid": "session", "aud": []string{"api"}, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(), "principal_type": "user", "scope": "openid api.read"}
	sign := func(typ string) string {
		t.Helper()
		signer, e := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType(jose.ContentType(typ)).WithHeader("kid", "key"))
		if e != nil {
			t.Fatal(e)
		}
		body, _ := json.Marshal(payload)
		signed, e := signer.Sign(body)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := signed.CompactSerialize()
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	verify := func(raw string) error {
		parsed, e := jose.ParseSigned(raw, []jose.SignatureAlgorithm{jose.RS256})
		if e != nil {
			return e
		}
		_, e = provider.cachedVerify(ctx, parsed, 1)
		return e
	}
	valid := sign("at+jwt")
	if err = verify(valid); err != nil {
		t.Fatal("valid signature rejected", err)
	}
	// Keep the genuine jti/sub/client/sid/expiry, changing only privileges.
	parts := strings.Split(valid, ".")
	payload["scope"] = "openid admin"
	changed, _ := json.Marshal(payload)
	parts[1] = base64.RawURLEncoding.EncodeToString(changed)
	if err = verify(strings.Join(parts, ".")); err == nil {
		t.Fatal("unverified lookup payload trusted")
	}
	payload["scope"] = "openid api.read"
	if err = verify(sign("JWT")); err == nil {
		t.Fatal("ID-token-like typ accepted as access token")
	}
	for _, field := range []string{"sub", "client_id", "jti", "principal_type"} {
		old := payload[field]
		payload[field] = ""
		if err = verify(sign("at+jwt")); err == nil {
			t.Error("missing claim accepted", field)
		}
		payload[field] = old
	}
	old := payload["iss"]
	payload["iss"] = "https://other.example/a/test"
	if err = verify(sign("at+jwt")); err == nil {
		t.Fatal("cross issuer accepted")
	}
	payload["iss"] = old
	payload["exp"] = time.Now().Add(-time.Minute).Unix()
	if err = verify(sign("at+jwt")); err == nil {
		t.Fatal("expired JWT accepted")
	}
	if _, err = provider.cachedVerify(ctx, nil, 1); err == nil {
		t.Fatal("missing parse accepted")
	}
}
