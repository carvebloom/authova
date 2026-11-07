package secure

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	jose "github.com/go-jose/go-jose/v4"
)

type AccessClaims struct {
	Issuer    string   `json:"iss"`
	Subject   string   `json:"sub"`
	Audience  []string `json:"aud"`
	Expires   int64    `json:"exp"`
	Issued    int64    `json:"iat"`
	ID        string   `json:"jti"`
	ClientID  string   `json:"client_id"`
	Scope     string   `json:"scope"`
	Principal string   `json:"principal_type"`
	SessionID string   `json:"sid"`
}

func VerifyAccess(token, issuer string, keys []domain.SigningKey) (AccessClaims, error) {
	var c AccessClaims
	if len(token) > 16384 {
		return c, domain.ErrUnauthenticated
	}
	signed, e := jose.ParseSigned(token, []jose.SignatureAlgorithm{jose.RS256})
	if e != nil || len(signed.Signatures) != 1 {
		return c, domain.ErrUnauthenticated
	}
	header := signed.Signatures[0].Header
	if header.ExtraHeaders[jose.HeaderType] != "at+jwt" || header.KeyID == "" {
		return c, domain.ErrUnauthenticated
	}
	for _, k := range keys {
		if k.RevokedAt != 0 || k.ID != header.KeyID || (!k.Current && k.Retire < time.Now().Unix()) {
			continue
		}
		private, e := x509.ParsePKCS1PrivateKey(k.Private)
		if e != nil {
			return c, domain.ErrUnavailable
		}
		raw, e := signed.Verify(&private.PublicKey)
		if e != nil {
			return c, domain.ErrUnauthenticated
		}
		if json.Unmarshal(raw, &c) != nil || c.Issuer != issuer || c.Expires <= time.Now().Unix() || c.Issued > time.Now().Add(30*time.Second).Unix() || c.Subject == "" || c.ID == "" || len(c.Audience) == 0 || c.ClientID == "" || !slices.Contains([]string{"user", "client"}, c.Principal) {
			return c, domain.ErrUnauthenticated
		}
		return c, nil
	}
	return c, domain.ErrUnauthenticated
}

// IssuerHint is only a routing hint. VerifyAccess must authorize the selected App.
func IssuerHint(token string) (string, error) {
	j, e := jose.ParseSigned(token, []jose.SignatureAlgorithm{jose.RS256})
	if e != nil {
		return "", domain.ErrUnauthenticated
	}
	var c AccessClaims
	if json.Unmarshal(j.UnsafePayloadWithoutVerification(), &c) != nil {
		return "", errors.New("invalid claims")
	}
	return c.Issuer, nil
}
