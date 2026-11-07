package protocol

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	jose "github.com/go-jose/go-jose/v4"
)

type profileFacts struct {
	ports.AppTx
	signing domain.SigningKey
}

func (t profileFacts) Keys() ([]domain.SigningKey, error) { return t.VerificationKeys() }
func (t profileFacts) VerificationKeys() ([]domain.SigningKey, error) {
	return []domain.SigningKey{t.signing}, nil
}
func (t profileFacts) Client(string) (domain.Client, error) {
	return domain.Client{ClientID: "client"}, nil
}
func (t profileFacts) Token(string) (domain.Token, error) {
	return domain.Token{ID: "token", Audiences: []string{"urn:api"}}, nil
}
func signedProfileFixture(tb testing.TB, jti any) (context.Context, *Provider, []byte) {
	tb.Helper()
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		tb.Fatal(e)
	}
	tx := profileFacts{signing: domain.SigningKey{ID: "key", AppID: "app", Private: x509.MarshalPKCS1PrivateKey(key), Current: true}}
	ctx := WithTransaction(context.Background(), tx, domain.App{ID: "app"}, "client", "")
	signer, e := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT"))
	if e != nil {
		tb.Fatal(e)
	}
	raw, _ := json.Marshal(map[string]any{"jti": jti, "aud": []string{"client"}})
	j, e := signer.Sign(raw)
	if e != nil {
		tb.Fatal(e)
	}
	signed, e := j.CompactSerialize()
	if e != nil {
		tb.Fatal(e)
	}
	response, _ := json.Marshal(map[string]any{"access_token": signed, "id_token": signed, "token_type": "Bearer"})
	return ctx, &Provider{storage: &Storage{}}, response
}
func TestSignedTokenWithoutStringIDFailsClosed(t *testing.T) {
	ctx, p, response := signedProfileFixture(t, 123)
	rec := httptest.NewRecorder()
	rec.Body.Write(response)
	if e := p.profile(ctx, rec); e == nil {
		t.Fatal("non-string token ID accepted")
	}
}

// CPU/allocation microbenchmark of the real profile path. Store facts are fixed;
// this excludes SQL, engine issuance, HTTP and concurrency: never a capacity gate.
func BenchmarkTokenProfileCPU(b *testing.B) {
	ctx, p, response := signedProfileFixture(b, "token")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		rec.Body.Write(response)
		if e := p.profile(ctx, rec); e != nil {
			b.Fatal(e)
		}
	}
}
