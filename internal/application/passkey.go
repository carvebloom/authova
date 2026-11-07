package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	wp "github.com/go-webauthn/webauthn/protocol"
	wa "github.com/go-webauthn/webauthn/webauthn"
)

const maxPasskeys = 20

type passkeyUser struct {
	handle        []byte
	name, display string
	keys          []wa.Credential
}

func (u passkeyUser) WebAuthnID() []byte                   { return u.handle }
func (u passkeyUser) WebAuthnName() string                 { return u.name }
func (u passkeyUser) WebAuthnDisplayName() string          { return u.display }
func (u passkeyUser) WebAuthnCredentials() []wa.Credential { return u.keys }

type credentialRecordV1 struct {
	Version    int           `json:"version"`
	Credential wa.Credential `json:"credential"`
}
type pendingPasskeyUserV1 struct{ ID, Name, Email, Phone string }
type ceremonyRecordV1 struct {
	Version         int                   `json:"version"`
	Session         wa.SessionData        `json:"session"`
	Pending         *pendingPasskeyUserV1 `json:"pending,omitempty"`
	FlowProof, Name string
}

func (s *Service) passkeyEngine(a domain.App) (*wa.WebAuthn, error) {
	u, err := url.Parse(s.BaseURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && u.Hostname() == "localhost")) || net.ParseIP(u.Hostname()) != nil || a.Issuer != s.BaseURL+"/a/"+a.Key {
		return nil, domain.ErrUnavailable
	}
	timeout := wa.TimeoutConfig{Enforce: true, Timeout: 2 * time.Minute, TimeoutUVD: 2 * time.Minute}
	return wa.New(&wa.Config{RPID: u.Hostname(), RPOrigins: []string{s.BaseURL}, RPDisplayName: a.Name, AttestationPreference: wp.PreferNoAttestation, AuthenticatorSelection: wp.AuthenticatorSelection{ResidentKey: wp.ResidentKeyRequirementRequired, UserVerification: wp.VerificationRequired}, Timeouts: wa.TimeoutsConfig{Login: timeout, Registration: timeout}})
}
func (s *Service) webUser(a domain.App, u domain.User, keys []domain.Passkey) (passkeyUser, error) {
	handle, err := hex.DecodeString(s.Digest(a.ID, "passkey-user", u.ID))
	if err != nil {
		return passkeyUser{}, domain.ErrUnavailable
	}
	v := passkeyUser{handle: handle, name: a.Name + " / " + u.DisplayName, display: u.DisplayName, keys: []wa.Credential{}}
	for _, key := range keys {
		var record credentialRecordV1
		if json.Unmarshal(key.Record, &record) != nil || record.Version != 1 {
			return v, domain.ErrUnavailable
		}
		v.keys = append(v.keys, record.Credential)
	}
	return v, nil
}
func (s *Service) credentialHash(app string, id []byte) string {
	return s.Digest(app, "passkey-credential", base64.RawURLEncoding.EncodeToString(id))
}
func (s *Service) flowPasskeyProof(app string, f domain.Flow) string {
	return s.Digest(app, "passkey-flow", f.ID+":"+f.VerifiedKind+":"+f.VerifiedValue)
}
func ceremonyRecord(c domain.PasskeyCeremony) (ceremonyRecordV1, error) {
	var record ceremonyRecordV1
	if json.Unmarshal(c.Record, &record) != nil || record.Version != 1 {
		return record, domain.ErrUnavailable
	}
	return record, nil
}
func (s *Service) liveCeremony(c domain.PasskeyCeremony, kind string) error {
	if c.Used || c.Kind != kind {
		return domain.ErrConflict
	}
	if c.Expires <= s.Now().Unix() {
		return domain.ErrExpired
	}
	return nil
}
func (s *Service) saveCeremony(t ports.AppTx, c domain.PasskeyCeremony, record ceremonyRecordV1) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	c.Record = data
	return t.PutPasskeyCeremony(c)
}
func (s *Service) registrationAllowed(a domain.App) error {
	if a.Status != "active" || !a.AuthenticationPolicy().PasskeyEnabled {
		return domain.ErrForbidden
	}
	return nil
}

func credentialRequest(ctx context.Context, data []byte) (*http.Request, error) {
	if len(data) == 0 || len(data) > 60*1024 {
		return nil, domain.ErrInvalid
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://authova.invalid/passkey", bytes.NewReader(data))
	if err == nil {
		r.Header.Set("Content-Type", "application/json")
	}
	return r, err
}

// Cryptographic verification runs outside the write transaction. The commit
// phase rechecks flow, user epoch, current session, and the one-use ceremony.
// Avoid exposing storage or cryptographic parser details through HTTP errors.
func PasskeyError(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return domain.ErrUnauthenticated
	}
	return err
}

// Notifications are committed with the credential change, then delivered by the
// existing outbox. No authenticator details or user-controlled names are sent.
func (s *Service) notifyPasskeyChange(t ports.AppTx, app string, u domain.User, action string) error {
	kind, destination := "email", u.Email
	if destination == "" {
		kind, destination = "phone", u.Phone
	}
	if destination == "" {
		return nil
	}
	now := s.Now().Unix()
	return t.Enqueue(domain.Message{ID: ID(), AppID: app, Kind: kind, Destination: destination, Body: "A Passkey was " + action + " on your Authova account. If this was not you, contact support.", Created: now, Available: now, Expires: now + 900})
}

// PasskeyAvailable describes canonical deployment suitability, independent of
// the App policy. Browsers reject IP literals as RP IDs, including loopback.
func (s *Service) PasskeyAvailable(a domain.App) bool {
	_, err := s.passkeyEngine(a)
	return err == nil
}
