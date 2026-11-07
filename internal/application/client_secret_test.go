package application

import (
	"bytes"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/secure"
)

func TestClientSecretCurrentPreviousExpiryAndScope(t *testing.T) {
	vault, e := secure.NewVault(bytes.Repeat([]byte{1}, 32))
	if e != nil {
		t.Fatal(e)
	}
	s := &Service{Vault: vault}
	c := domain.Client{Kind: "confidential", SecretDigest: s.Digest("app1", "client-secret", "current"), PreviousSecretDigest: s.Digest("app1", "client-secret", "previous"), PreviousSecretExpires: time.Now().Add(time.Minute).Unix()}
	if !s.ClientSecretMatches("app1", c, "current") || !s.ClientSecretMatches("app1", c, "previous") {
		t.Fatal("overlap rejected")
	}
	if s.ClientSecretMatches("app2", c, "current") || s.ClientSecretMatches("app1", c, "") || s.ClientSecretMatches("app1", c, "incorrect") {
		t.Fatal("cross-app/empty/incorrect accepted")
	}
	c.PreviousSecretExpires = time.Now().Unix()
	if s.ClientSecretMatches("app1", c, "previous") || !s.ClientSecretMatches("app1", c, "current") {
		t.Fatal("expiry boundary incorrect")
	}
	c.Kind = "public"
	if s.ClientSecretMatches("app1", c, "current") {
		t.Fatal("public client authenticated with secret")
	}
}
