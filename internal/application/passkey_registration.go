package application

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
	wp "github.com/go-webauthn/webauthn/protocol"
	wa "github.com/go-webauthn/webauthn/webauthn"
)

// BeginPasskeyRegistration supports a recently authenticated account, or a
// verified-address registration flow. All ceremony facts remain server-side.
func (s *Service) BeginPasskeyRegistration(ctx context.Context, scope domain.Scope, handle, flow, name, display string) (id string, options any, err error) {
	name, display = strings.TrimSpace(name), strings.TrimSpace(display)
	if len(name) > 100 || len(display) > 200 {
		return "", nil, domain.ErrInvalid
	}
	if name == "" {
		name = "Passkey"
	}
	err = s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		a, e := t.App()
		if e != nil {
			return e
		}
		if e = s.registrationAllowed(a); e != nil {
			return e
		}
		engine, e := s.passkeyEngine(a)
		if e != nil {
			return e
		}
		var user domain.User
		var session domain.Session
		var pending *pendingPasskeyUserV1
		proof := ""
		if flow != "" {
			f, e := t.Flow(flow, true)
			if e != nil {
				return e
			}
			if e = s.LiveFlow(f); e != nil {
				return e
			}
			if f.Purpose != domain.PurposeRegister || f.Step != domain.StepSetPassword || f.VerifiedValue == "" || (f.VerifiedKind != "email" && f.VerifiedKind != "phone") || display == "" {
				return domain.ErrForbidden
			}
			if e = CheckAuthenticationPolicy(t, f, "passkey", "register"); e != nil {
				return e
			}
			user = domain.User{ID: ID(), AppID: scope.ID(), DisplayName: display, Status: "active", Epoch: 1}
			if f.VerifiedKind == "email" {
				user.Email = f.VerifiedValue
			} else {
				user.Phone = f.VerifiedValue
			}
			pending = &pendingPasskeyUserV1{user.ID, display, user.Email, user.Phone}
			proof = s.flowPasskeyProof(scope.ID(), f)
		} else {
			session, user, e = s.LockedSession(t, scope, handle, true)
			if e != nil {
				return e
			}
		}
		keys, e := t.Passkeys(user.ID)
		if e != nil {
			return e
		}
		if len(keys) >= maxPasskeys {
			return domain.ErrConflict
		}
		wu, e := s.webUser(a, user, keys)
		if e != nil {
			return e
		}
		exclude := []wp.CredentialDescriptor{}
		for _, key := range wu.keys {
			exclude = append(exclude, key.Descriptor())
		}
		creation, data, e := engine.BeginRegistration(wu, wa.WithExclusions(exclude), wa.WithExtensions(wa.WithExtensionCredProps()))
		if e != nil {
			return domain.ErrUnavailable
		}
		id = secure.Random(32)
		c := domain.PasskeyCeremony{ID: id, Kind: "register", FlowID: flow, SessionID: session.ID, UserID: user.ID, Epoch: user.Epoch, Expires: s.Now().Add(2 * time.Minute).Unix()}
		if e = s.saveCeremony(t, c, ceremonyRecordV1{Version: 1, Session: *data, Pending: pending, FlowProof: proof, Name: name}); e != nil {
			return e
		}
		options = creation
		return nil
	})
	return
}

func (s *Service) FinishPasskeyRegistration(ctx context.Context, scope domain.Scope, handle, flow, id string, response []byte) error {
	var c domain.PasskeyCeremony
	var record ceremonyRecordV1
	var a domain.App
	var user domain.User
	var wu passkeyUser
	err := s.Store.App(ctx, scope, false, func(t ports.AppTx) error {
		var e error
		c, e = t.PasskeyCeremony(id, false)
		if e != nil {
			return e
		}
		if e = s.liveCeremony(c, "register"); e != nil {
			return e
		}
		if c.FlowID != flow {
			return domain.ErrForbidden
		}
		record, e = ceremonyRecord(c)
		if e != nil {
			return e
		}
		a, e = t.App()
		if e != nil {
			return e
		}
		if flow != "" {
			if record.Pending == nil {
				return domain.ErrForbidden
			}
			p := record.Pending
			user = domain.User{ID: p.ID, AppID: scope.ID(), DisplayName: p.Name, Email: p.Email, Phone: p.Phone, Epoch: 1}
		}
		if flow == "" {
			session, u, e := s.AuthenticatedSession(t, scope, handle, true)
			if e != nil {
				return e
			}
			if session.ID != c.SessionID || u.ID != c.UserID || u.Epoch != c.Epoch {
				return domain.ErrUnauthenticated
			}
			user = u
		}
		keys, e := t.Passkeys(user.ID)
		if e != nil {
			return e
		}
		wu, e = s.webUser(a, user, keys)
		return e
	})
	if err != nil {
		return err
	}
	engine, err := s.passkeyEngine(a)
	if err != nil {
		return err
	}
	request, err := credentialRequest(ctx, response)
	if err != nil {
		return err
	}
	credential, err := engine.FinishRegistration(wu, record.Session, request)
	if err != nil {
		return domain.ErrUnauthenticated
	}
	if !credential.Flags.UserVerified || !credential.Flags.UserPresent {
		return domain.ErrUnauthenticated
	}
	return s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		var f domain.Flow
		var e error
		if flow != "" {
			f, e = t.Flow(flow, true)
			if e != nil {
				return e
			}
			if e = s.LiveFlow(f); e != nil {
				return e
			}
			if f.Purpose != domain.PurposeRegister || f.Step != domain.StepSetPassword || record.FlowProof != s.flowPasskeyProof(scope.ID(), f) {
				return domain.ErrConflict
			}
			if e = CheckAuthenticationPolicy(t, f, "passkey", "register"); e != nil {
				return e
			}
		}
		if flow == "" {
			session, current, e := s.LockedSession(t, scope, handle, true)
			if e != nil {
				return e
			}
			if current.ID != c.UserID || current.Epoch != c.Epoch || session.ID != c.SessionID {
				return domain.ErrUnauthenticated
			}
			user = current
		}
		a, e = t.App()
		if e != nil {
			return e
		}
		if e = s.registrationAllowed(a); e != nil {
			return e
		}
		latest, e := t.PasskeyCeremony(id, true)
		if e != nil {
			return e
		}
		if e = s.liveCeremony(latest, "register"); e != nil {
			return e
		}
		if !bytes.Equal(latest.Record, c.Record) {
			return domain.ErrConflict
		}
		keys, e := t.Passkeys(user.ID)
		if e != nil {
			return e
		}
		if len(keys) >= maxPasskeys {
			return domain.ErrConflict
		}
		if flow != "" {
			user.Status = "active"
			user.CreatedAt = s.Now().UTC()
			if e = t.CreateUser(user, f.VerifiedKind, s.Digest(scope.ID(), "identifier:"+f.VerifiedKind, f.VerifiedValue)); e != nil {
				return e
			}
		}
		data, e := json.Marshal(credentialRecordV1{1, *credential})
		if e != nil {
			return e
		}
		now := s.Now().UTC()
		key := domain.Passkey{ID: ID(), UserID: user.ID, CredentialHash: s.credentialHash(scope.ID(), credential.ID), Name: record.Name, Record: data, Version: 1, CreatedAt: now, UpdatedAt: now}
		if e = t.PutPasskey(key); e != nil {
			return e
		}
		latest.Used = true
		if e = t.PutPasskeyCeremony(latest); e != nil {
			return e
		}
		if flow != "" {
			f.Registered = true
			f.PrimaryMethod = "passkey"
			f.UserID = user.ID
			f.AuthEpoch = user.Epoch
			f.AuthTime = s.Now().Unix()
			f.AMR = []string{"passkey"}
			f.Purpose = domain.PurposeAuthorize
			f.Step = domain.StepConsent
			f.VerifiedValue = ""
			client, e := t.Client(f.ClientID)
			if e != nil {
				return e
			}
			if a.RequireMFA || client.System {
				f.Step = domain.StepMFA
			}
			if e = t.PutFlow(f); e != nil {
				return e
			}
		}
		if e = t.Audit(audit(scope.ID(), user.ID, "passkey.register", key.ID)); e != nil {
			return e
		}
		return s.notifyPasskeyChange(t, scope.ID(), user, "added")
	})
}
