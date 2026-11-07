package sqlstore

import (
	"bytes"
	"github.com/carvebloom/authova/internal/domain"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestFrozenLegacyPayloadUpgrade(t *testing.T) {
	// Fixed files were encoded with the pre-codec domain layout. Never regenerate
	// them from evolving structs. Values are synthetic; there are no real secrets.
	cases := []struct {
		kind     string
		expected any
		target   any
	}{
		{"user", domain.User{ID: "user", AppID: "app", DisplayName: "Fixture", Status: "active", Email: "fixture@example.test", Phone: "+8613800138000", Epoch: 7, CreatedAt: time.Unix(1700000000, 0).UTC(), PasswordHash: "synthetic-password-hash", TOTPSecret: "SYNTHETIC", TOTPLast: 123, RecoveryDigests: []string{"digest1", "digest2"}}, &domain.User{}},
		{"flow", domain.Flow{ID: "flow", ClientID: "client", UserID: "user", SessionID: "session", Step: "reset_password", Purpose: "reset_password", AuthEpoch: 7, AuthTime: 1700000000, MFATime: 1700000001, VerifiedKind: "email", VerifiedValue: "fixture@example.test", Expires: 1700000600, AMR: []string{"pwd", "otp"}, MFAOperation: "replace", MFAProofDigest: "proof", MFAProofStep: 123, PendingTOTP: "PENDING", Data: []byte("opaque-protocol"), Federation: []byte("opaque-federation")}, &domain.Flow{}},
		{"client", domain.Client{ID: "client", AppID: "app", ClientID: "avc_fixture", Name: "Fixture", Kind: "confidential", Status: "active", Version: 9, SecretDigest: "current", PreviousSecretDigest: "previous", PreviousSecretExpires: 1700000100, RedirectURIs: []string{"https://fixture.example/callback"}, Scopes: []string{"openid"}, Audiences: []string{"urn:fixture"}, IntrospectionAudiences: []string{"urn:fixture"}, Grants: []string{"authorization_code"}}, &domain.Client{}},
		{"key", domain.SigningKey{ID: "key", AppID: "app", Private: []byte("SYNTHETIC-PRIVATE-KEY"), Created: 1700000000, Current: false, Retire: 1700000600, RevokedAt: 1700000010}, &domain.SigningKey{}},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			legacy, e := os.ReadFile("testdata/legacy/" + tc.kind + ".gob")
			if e != nil {
				t.Fatal(e)
			}
			if e = decodePayload(tc.kind, legacy, tc.target); e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(reflect.ValueOf(tc.target).Elem().Interface(), tc.expected) {
				t.Fatal("legacy security facts changed")
			}
			upgraded, e := upgradePayload(tc.kind, legacy)
			if e != nil || !bytes.HasPrefix(upgraded, payloadMagic) {
				t.Fatal("upgrade failed", e)
			}
			if e = decodePayload(tc.kind, upgraded, tc.target); e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(reflect.ValueOf(tc.target).Elem().Interface(), tc.expected) {
				t.Fatal("upgraded security facts changed")
			}
			if e = decodePayload("wrong", upgraded, tc.target); e == nil {
				t.Fatal("kind mismatch accepted")
			}
			upgraded[len(payloadMagic)-1] = 99
			if e = decodePayload(tc.kind, upgraded, tc.target); e == nil {
				t.Fatal("future version accepted")
			}
		})
	}
}

func TestLoginProofVersionSurvivesPayloadRoundtrip(t *testing.T) {
	before := domain.Flow{ID: "bound-login", UserID: "user", AuthEpoch: 7, PrimaryMethod: "email_otp", LoginProofVersion: 1, Step: domain.StepConsent}
	data, err := encodePayload("flow", before)
	if err != nil {
		t.Fatal(err)
	}
	var after domain.Flow
	if err = decodePayload("flow", data, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("bound login proof lost its generation")
	}
}
