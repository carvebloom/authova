// Package domain defines Authova's persisted facts, without transport or database types.
package domain

import (
	"errors"
	"time"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrConflict        = errors.New("conflict")
	ErrForbidden       = errors.New("forbidden")
	ErrUnauthenticated = errors.New("unauthenticated")
	ErrInvalid         = errors.New("invalid input")
	ErrExpired         = errors.New("expired")
	ErrRateLimit       = errors.New("rate limit")
	ErrUnavailable     = errors.New("dependency unavailable")
	ErrLeaseLost       = errors.New("delivery lease lost")
	ErrCommitUnknown   = errors.New("commit outcome unknown")
)

// Scope binds repositories to one App. A zero scope is always invalid.
type Scope struct{ id string }

func AppScope(id string) (Scope, error) {
	if id == "" {
		return Scope{}, ErrInvalid
	}
	return Scope{id: id}, nil
}
func (s Scope) ID() string { return s.id }

// AuthenticationPolicy controls end-user methods, not provider deployment credentials.
type AuthenticationPolicy struct {
	PasskeyEnabled              bool `json:"passkey_enabled"`
	RegistrationEnabled         bool `json:"registration_enabled"`
	PasswordLoginEnabled        bool `json:"password_login_enabled"`
	EmailOTPEnabled             bool `json:"email_otp_enabled"`
	PhoneOTPEnabled             bool `json:"phone_otp_enabled"`
	FederationEnabled           bool `json:"federation_enabled"`
	ExternalRegistrationEnabled bool `json:"external_registration_enabled"`
}

func DefaultAuthenticationPolicy() AuthenticationPolicy {
	return AuthenticationPolicy{RegistrationEnabled: true, PasswordLoginEnabled: true, EmailOTPEnabled: true, PhoneOTPEnabled: true, FederationEnabled: true, ExternalRegistrationEnabled: true}
}
func (a App) AuthenticationPolicy() AuthenticationPolicy {
	if a.Policy == nil {
		return DefaultAuthenticationPolicy()
	}
	return *a.Policy
}

type App struct {
	Resources  *ResourceLimits       `json:"-"`
	Policy     *AuthenticationPolicy `json:"-"`
	KeyEpoch   int64                 `json:"-"`
	ID         string                `json:"id"`
	Key        string                `json:"app_key"`
	Name       string                `json:"name"`
	Slug       string                `json:"slug"`
	Status     string                `json:"status"`
	Issuer     string                `json:"issuer"`
	Version    int64                 `json:"config_version"`
	CreatedAt  time.Time             `json:"created_at"`
	RequireMFA bool                  `json:"require_mfa"`
}
type SigningKey struct {
	ID, AppID       string
	Private         []byte
	Created, Retire int64
	Current         bool
	RevokedAt       int64
}
type Client struct {
	System                 bool      `json:"system"`
	ID                     string    `json:"id"`
	AppID                  string    `json:"app_id"`
	ClientID               string    `json:"client_id"`
	Name                   string    `json:"name"`
	Kind                   string    `json:"kind"`
	Status                 string    `json:"status"`
	RedirectURIs           []string  `json:"redirect_uris"`
	Scopes                 []string  `json:"allowed_scopes"`
	Audiences              []string  `json:"allowed_audiences"`
	IntrospectionAudiences []string  `json:"allowed_introspection_audiences"`
	Grants                 []string  `json:"grant_types"`
	SecretDigest           string    `json:"-"`
	PreviousSecretDigest   string    `json:"-"`
	PreviousSecretExpires  int64     `json:"previous_secret_expires_at"`
	Version                int64     `json:"config_version"`
	CreatedAt              time.Time `json:"created_at"`
}
type User struct {
	RecoveryDigests []string  `json:"-"`
	AdminRole       string    `json:"admin_role,omitempty"`
	ID              string    `json:"id"`
	AppID           string    `json:"app_id"`
	DisplayName     string    `json:"display_name"`
	Status          string    `json:"status"`
	Email           string    `json:"email,omitempty"`
	Phone           string    `json:"phone,omitempty"`
	Epoch           int64     `json:"session_epoch"`
	CreatedAt       time.Time `json:"created_at"`
	PasswordHash    string    `json:"-"`
	TOTPSecret      string    `json:"-"`
	TOTPLast        int64     `json:"-"`
}
type Session struct {
	ClientID     string    `json:"-"`
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	AMR          []string  `json:"amr"`
	Epoch        int64     `json:"-"`
	HandleDigest string    `json:"-"`
	AuthTime     int64     `json:"-"`
	MFATime      int64     `json:"-"`
}

// Flow.Data is owned by the protocol adapter, never an external API DTO.
type Flow struct {
	// LoginProofVersion fences in-flight OTP flows accepted by older binaries.
	LoginProofVersion                                                                int
	PrimaryMethod                                                                    string
	Registered                                                                       bool
	MFAProofDigest                                                                   string
	MFAProofStep                                                                     int64
	MFAOperation                                                                     string
	ChallengeID                                                                      string
	Federation                                                                       []byte
	PendingTOTP                                                                      string
	ID, ClientID, UserID, SessionID, CookieDigest, CSRF, VerifiedKind, VerifiedValue string
	Step                                                                             FlowStep
	Purpose                                                                          FlowPurpose
	Data                                                                             []byte
	Expires                                                                          int64
	Completed                                                                        bool
	AuthTime, MFATime, AuthEpoch                                                     int64
	AMR                                                                              []string
}
type Code struct {
	Digest, FlowID string
	Expires        int64
	Used           bool
}
type Token struct {
	RequiresMFA                                        bool
	ID, ClientID, UserID, SessionID, FamilyID, Subject string
	Scopes, Audiences, AMR                             []string
	Created, Expires, AuthTime, Epoch                  int64
	Revoked                                            bool
}

// TokenState is the primary liveness projection. Public claims come from the
// verified issuer signature and must match these immutable database identities.
type TokenState struct {
	Agent                                    *AgentTokenBinding
	KeyEpoch                                 int64
	ID, ClientID, UserID, Subject, SessionID string
	Expires                                  int64
	RequiresMFA                              bool
}
type Refresh struct {
	Digest, FamilyID, ClientID string
	Data                       []byte
	Generation, Expires        int64
	Used                       bool
}
type Challenge struct {
	ID, FlowID, Kind, Destination, Purpose, Digest string
	Expires                                        int64
	Attempts                                       int
	Used                                           bool
}
type Connection struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Issuer       string    `json:"issuer"`
	ClientID     string    `json:"client_id"`
	Secret       string    `json:"-"`
	Status       string    `json:"status"`
	ProviderType string    `json:"provider_type"`
	Version      int64     `json:"config_version"`
	CreatedAt    time.Time `json:"created_at,omitzero"`
}

// ExternalBinding exposes provider membership, never the upstream subject or token.
type ExternalBinding struct {
	ConnectionID string `json:"connection_id"`
	Count        int    `json:"identity_count"`
}
type Operator struct {
	AppID          string `json:"app_id,omitempty"`
	ID             string `json:"id"`
	Email          string `json:"email"`
	Role           string `json:"role"`
	Status         string `json:"status"`
	Password, TOTP string `json:"-"`
	TOTPLast       int64  `json:"-"`
}
type ManagementSession struct {
	Digest, OperatorID        string
	Created, Expires, MFATime int64
	Revoked                   bool
}
type Audit struct {
	Reason    string    `json:"reason,omitempty"`
	ID        string    `json:"id"`
	AppID     string    `json:"app_id,omitempty"`
	ActorID   string    `json:"actor_id"`
	Action    string    `json:"action"`
	SubjectID string    `json:"subject_id"`
	CreatedAt time.Time `json:"created_at"`
}
type Message struct {
	ID, AppID, Kind, Destination, Body string
	Attempts                           int
	Created, Available, Expires        int64
}
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}
