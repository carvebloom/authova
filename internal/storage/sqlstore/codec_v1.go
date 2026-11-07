package sqlstore

import (
	"bytes"
	"encoding/gob"
	"errors"
	"time"

	"github.com/carvebloom/authova/internal/domain"
)

// Immutable version 1 persistence schemas, deliberately independent of domain
// declarations and HTTP JSON tags. Add a new codec version for incompatible
// changes. Conversion compilation forces explicit review when domain fields move.
type signingKeyV1 struct {
	ID, AppID       string
	Private         []byte
	Created, Retire int64
	Current         bool
	RevokedAt       int64
}
type clientV1 struct {
	System                 bool
	ID                     string
	AppID                  string
	ClientID               string
	Name                   string
	Kind                   string
	Status                 string
	RedirectURIs           []string
	Scopes                 []string
	Audiences              []string
	IntrospectionAudiences []string
	Grants                 []string
	SecretDigest           string
	PreviousSecretDigest   string
	PreviousSecretExpires  int64
	Version                int64
	CreatedAt              time.Time
}
type userV1 struct {
	RecoveryDigests []string
	AdminRole       string
	ID              string
	AppID           string
	DisplayName     string
	Status          string
	Email           string
	Phone           string
	Epoch           int64
	CreatedAt       time.Time
	PasswordHash    string
	TOTPSecret      string
	TOTPLast        int64
}
type sessionV1 struct {
	ClientID     string
	ID           string
	UserID       string
	Status       string
	CreatedAt    time.Time
	ExpiresAt    time.Time
	AMR          []string
	Epoch        int64
	HandleDigest string
	AuthTime     int64
	MFATime      int64
}
type flowV1 struct {
	LoginProofVersion                                                                               int `json:"login_proof_version,omitempty"`
	PrimaryMethod                                                                                   string
	Registered                                                                                      bool
	MFAProofDigest                                                                                  string
	MFAProofStep                                                                                    int64
	MFAOperation                                                                                    string
	ChallengeID                                                                                     string
	Federation                                                                                      []byte
	PendingTOTP                                                                                     string
	ID, ClientID, UserID, SessionID, CookieDigest, CSRF, Step, Purpose, VerifiedKind, VerifiedValue string
	Data                                                                                            []byte
	Expires                                                                                         int64
	Completed                                                                                       bool
	AuthTime, MFATime, AuthEpoch                                                                    int64
	AMR                                                                                             []string
}
type tokenV1 struct {
	RequiresMFA                                        bool
	ID, ClientID, UserID, SessionID, FamilyID, Subject string
	Scopes, Audiences, AMR                             []string
	Created, Expires, AuthTime, Epoch                  int64
	Revoked                                            bool
}
type refreshV1 struct {
	Digest, FamilyID, ClientID string
	Data                       []byte
	Generation, Expires        int64
	Used                       bool
}
type challengeV1 struct {
	ID, FlowID, Kind, Destination, Purpose, Digest string
	Expires                                        int64
	Attempts                                       int
	Used                                           bool
}
type connectionV1 struct {
	ID           string
	Name         string
	Issuer       string
	ClientID     string
	Secret       string
	Status       string
	ProviderType string
	Version      int64
	CreatedAt    time.Time
}
type operatorV1 struct {
	AppID          string
	ID             string
	Email          string
	Role           string
	Status         string
	Password, TOTP string
	TOTPLast       int64
}
type managementSessionV1 struct {
	Digest, OperatorID        string
	Created, Expires, MFATime int64
	Revoked                   bool
}
type auditV1 struct {
	Reason    string
	ID        string
	AppID     string
	ActorID   string
	Action    string
	SubjectID string
	CreatedAt time.Time
}
type messageV1 struct {
	ID, AppID, Kind, Destination, Body string
	Attempts                           int
	Created, Available, Expires        int64
}
type consentV1 struct {
	User, Client string
	Scopes       []string
	At           int64
}

var payloadMagic = []byte("AUTHOVAP\x00\x01")

func encodePayload(kind string, value any) ([]byte, error) {
	var snapshot any
	switch v := value.(type) {
	case domain.SigningKey:
		if kind != "key" {
			return nil, errors.New("payload kind mismatch")
		}
		snapshot = signingKeyV1(v)
	case domain.Client:
		if kind != "client" {
			return nil, errors.New("payload kind mismatch")
		}
		snapshot = clientV1(v)
	case domain.User:
		if kind != "user" {
			return nil, errors.New("payload kind mismatch")
		}
		snapshot = userV1(v)
	case domain.Session:
		if kind != "session" {
			return nil, errors.New("payload kind mismatch")
		}
		snapshot = sessionV1(v)
	case domain.Flow:
		if kind != "flow" {
			return nil, errors.New("payload kind mismatch")
		}
		snapshot = flowV1{LoginProofVersion: v.LoginProofVersion, PrimaryMethod: v.PrimaryMethod, Registered: v.Registered, MFAProofDigest: v.MFAProofDigest, MFAProofStep: v.MFAProofStep, MFAOperation: v.MFAOperation, ChallengeID: v.ChallengeID, Federation: v.Federation, PendingTOTP: v.PendingTOTP, ID: v.ID, ClientID: v.ClientID, UserID: v.UserID, SessionID: v.SessionID, CookieDigest: v.CookieDigest, CSRF: v.CSRF, Step: string(v.Step), Purpose: string(v.Purpose), VerifiedKind: v.VerifiedKind, VerifiedValue: v.VerifiedValue, Data: v.Data, Expires: v.Expires, Completed: v.Completed, AuthTime: v.AuthTime, MFATime: v.MFATime, AuthEpoch: v.AuthEpoch, AMR: v.AMR}
	case domain.Token:
		if kind != "token" {
			return nil, errors.New("payload kind mismatch")
		}
		snapshot = tokenV1(v)
	case domain.Refresh:
		if kind != "refresh" {
			return nil, errors.New("payload kind mismatch")
		}
		snapshot = refreshV1(v)
	case domain.Challenge:
		if kind != "challenge" {
			return nil, errors.New("payload kind mismatch")
		}
		snapshot = challengeV1(v)
	case domain.Connection:
		if kind != "connection" {
			return nil, errors.New("payload kind mismatch")
		}
		snapshot = connectionV1(v)
	case domain.Operator:
		if kind != "operator" {
			return nil, errors.New("payload kind mismatch")
		}
		snapshot = operatorV1(v)
	case domain.ManagementSession:
		if kind != "management-session" {
			return nil, errors.New("payload kind mismatch")
		}
		snapshot = managementSessionV1(v)
	case domain.Audit:
		if kind != "audit" {
			return nil, errors.New("payload kind mismatch")
		}
		snapshot = auditV1(v)
	case domain.Message:
		if kind != "message" {
			return nil, errors.New("payload kind mismatch")
		}
		snapshot = messageV1(v)
	case consentV1:
		if kind != "consent" {
			return nil, errors.New("payload kind mismatch")
		}
		snapshot = v
	default:
		return nil, errors.New("unsupported persistence value")
	}
	var buf bytes.Buffer
	buf.Write(payloadMagic)
	buf.WriteByte(byte(len(kind)))
	buf.WriteString(kind)
	if err := gob.NewEncoder(&buf).Encode(snapshot); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Both legacy gob and v1 decode through frozen snapshots. No legacy payload is
// decoded straight into a domain struct. Unknown envelopes fail closed.
func decodePayload(kind string, data []byte, target any) error {
	if bytes.HasPrefix(data, []byte("AUTHOVAP")) {
		if !bytes.HasPrefix(data, payloadMagic) || len(data) <= len(payloadMagic) {
			return errors.New("unsupported payload version")
		}
		n := int(data[len(payloadMagic)])
		offset := len(payloadMagic) + 1
		if len(data) < offset+n || string(data[offset:offset+n]) != kind {
			return errors.New("payload kind mismatch")
		}
		data = data[offset+n:]
	}
	decode := func(v any) error {
		reader := bytes.NewReader(data)
		d := gob.NewDecoder(reader)
		if err := d.Decode(v); err != nil {
			return err
		}
		return nil
	}
	switch v := target.(type) {
	case *domain.SigningKey:
		if kind != "key" {
			return errors.New("payload kind mismatch")
		}
		var snapshot signingKeyV1
		if err := decode(&snapshot); err != nil {
			return err
		}
		*v = domain.SigningKey(snapshot)
	case *domain.Client:
		if kind != "client" {
			return errors.New("payload kind mismatch")
		}
		var snapshot clientV1
		if err := decode(&snapshot); err != nil {
			return err
		}
		*v = domain.Client(snapshot)
	case *domain.User:
		if kind != "user" {
			return errors.New("payload kind mismatch")
		}
		var snapshot userV1
		if err := decode(&snapshot); err != nil {
			return err
		}
		*v = domain.User(snapshot)
	case *domain.Session:
		if kind != "session" {
			return errors.New("payload kind mismatch")
		}
		var snapshot sessionV1
		if err := decode(&snapshot); err != nil {
			return err
		}
		*v = domain.Session(snapshot)
	case *domain.Flow:
		if kind != "flow" {
			return errors.New("payload kind mismatch")
		}
		var snapshot flowV1
		if err := decode(&snapshot); err != nil {
			return err
		}
		*v = domain.Flow{LoginProofVersion: snapshot.LoginProofVersion, PrimaryMethod: snapshot.PrimaryMethod, Registered: snapshot.Registered, MFAProofDigest: snapshot.MFAProofDigest, MFAProofStep: snapshot.MFAProofStep, MFAOperation: snapshot.MFAOperation, ChallengeID: snapshot.ChallengeID, Federation: snapshot.Federation, PendingTOTP: snapshot.PendingTOTP, ID: snapshot.ID, ClientID: snapshot.ClientID, UserID: snapshot.UserID, SessionID: snapshot.SessionID, CookieDigest: snapshot.CookieDigest, CSRF: snapshot.CSRF, Step: domain.FlowStep(snapshot.Step), Purpose: domain.FlowPurpose(snapshot.Purpose), VerifiedKind: snapshot.VerifiedKind, VerifiedValue: snapshot.VerifiedValue, Data: snapshot.Data, Expires: snapshot.Expires, Completed: snapshot.Completed, AuthTime: snapshot.AuthTime, MFATime: snapshot.MFATime, AuthEpoch: snapshot.AuthEpoch, AMR: snapshot.AMR}
	case *domain.Token:
		if kind != "token" {
			return errors.New("payload kind mismatch")
		}
		var snapshot tokenV1
		if err := decode(&snapshot); err != nil {
			return err
		}
		*v = domain.Token(snapshot)
	case *domain.Refresh:
		if kind != "refresh" {
			return errors.New("payload kind mismatch")
		}
		var snapshot refreshV1
		if err := decode(&snapshot); err != nil {
			return err
		}
		*v = domain.Refresh(snapshot)
	case *domain.Challenge:
		if kind != "challenge" {
			return errors.New("payload kind mismatch")
		}
		var snapshot challengeV1
		if err := decode(&snapshot); err != nil {
			return err
		}
		*v = domain.Challenge(snapshot)
	case *domain.Connection:
		if kind != "connection" {
			return errors.New("payload kind mismatch")
		}
		var snapshot connectionV1
		if err := decode(&snapshot); err != nil {
			return err
		}
		*v = domain.Connection(snapshot)
	case *domain.Operator:
		if kind != "operator" {
			return errors.New("payload kind mismatch")
		}
		var snapshot operatorV1
		if err := decode(&snapshot); err != nil {
			return err
		}
		*v = domain.Operator(snapshot)
	case *domain.ManagementSession:
		if kind != "management-session" {
			return errors.New("payload kind mismatch")
		}
		var snapshot managementSessionV1
		if err := decode(&snapshot); err != nil {
			return err
		}
		*v = domain.ManagementSession(snapshot)
	case *domain.Audit:
		if kind != "audit" {
			return errors.New("payload kind mismatch")
		}
		var snapshot auditV1
		if err := decode(&snapshot); err != nil {
			return err
		}
		*v = domain.Audit(snapshot)
	case *domain.Message:
		if kind != "message" {
			return errors.New("payload kind mismatch")
		}
		var snapshot messageV1
		if err := decode(&snapshot); err != nil {
			return err
		}
		*v = domain.Message(snapshot)
	case *consentV1:
		if kind != "consent" {
			return errors.New("payload kind mismatch")
		}
		return decode(v)
	default:
		return errors.New("unsupported persistence target")
	}
	return nil
}
