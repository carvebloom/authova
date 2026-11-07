package application

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
	wp "github.com/go-webauthn/webauthn/protocol"
	wa "github.com/go-webauthn/webauthn/webauthn"
)

func (s *Service) BeginPasskeyLogin(ctx context.Context, scope domain.Scope, flow string) (id string, options any, err error) {
	err = s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		f, e := t.Flow(flow, true)
		if e != nil {
			return e
		}
		if e = s.LiveFlow(f); e != nil {
			return e
		}
		if f.Purpose != domain.PurposeAuthorize || (f.Step != domain.StepChoose && f.Step != domain.StepPassword) {
			return domain.ErrConflict
		}
		if e = CheckAuthenticationPolicy(t, f, "passkey", "login"); e != nil {
			return e
		}
		a, e := t.App()
		if e != nil {
			return e
		}
		engine, e := s.passkeyEngine(a)
		if e != nil {
			return e
		}
		assertion, data, e := engine.BeginDiscoverableLogin(wa.WithUserVerification(wp.VerificationRequired))
		if e != nil {
			return domain.ErrUnavailable
		}
		id = secure.Random(32)
		c := domain.PasskeyCeremony{ID: id, Kind: "login", FlowID: flow, Expires: s.Now().Add(2 * time.Minute).Unix()}
		if e = s.saveCeremony(t, c, ceremonyRecordV1{Version: 1, Session: *data}); e != nil {
			return e
		}
		options = assertion
		return nil
	})
	return
}

func (s *Service) FinishPasskeyLogin(ctx context.Context, scope domain.Scope, flow, id string, response []byte) error {
	var c domain.PasskeyCeremony
	var a domain.App
	var record ceremonyRecordV1
	err := s.Store.App(ctx, scope, false, func(t ports.AppTx) error {
		var e error
		c, e = t.PasskeyCeremony(id, false)
		if e != nil {
			return e
		}
		if e = s.liveCeremony(c, "login"); e != nil {
			return e
		}
		if c.FlowID != flow {
			return domain.ErrForbidden
		}
		a, e = t.App()
		if e != nil {
			return e
		}
		record, e = ceremonyRecord(c)
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
	var snapshot domain.User
	var keySnapshot domain.Passkey
	_, credential, err := engine.FinishPasskeyLogin(func(rawID, handle []byte) (wa.User, error) {
		var wu passkeyUser
		e := s.Store.App(ctx, scope, false, func(t ports.AppTx) error {
			var e error
			keySnapshot, e = t.PasskeyByCredential(s.credentialHash(scope.ID(), rawID), false)
			if e != nil {
				return e
			}
			snapshot, e = t.User(keySnapshot.UserID, false)
			if e != nil {
				return e
			}
			if snapshot.Status != "active" {
				return domain.ErrUnauthenticated
			}
			keys, e := t.Passkeys(snapshot.ID)
			if e != nil {
				return e
			}
			wu, e = s.webUser(a, snapshot, keys)
			if e != nil {
				return e
			}
			if !bytes.Equal(handle, wu.handle) {
				return domain.ErrUnauthenticated
			}
			return nil
		})
		return wu, e
	}, record.Session, request)
	if err != nil || credential == nil || credential.Authenticator.CloneWarning || !credential.Flags.UserVerified || !credential.Flags.UserPresent {
		return domain.ErrUnauthenticated
	}
	return s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		f, e := t.Flow(flow, true)
		if e != nil {
			return e
		}
		if e = s.LiveFlow(f); e != nil {
			return e
		}
		if f.Purpose != domain.PurposeAuthorize || (f.Step != domain.StepChoose && f.Step != domain.StepPassword) {
			return domain.ErrConflict
		}
		if e = CheckAuthenticationPolicy(t, f, "passkey", "login"); e != nil {
			return e
		}
		user, e := t.User(snapshot.ID, true)
		if e != nil {
			return e
		}
		if user.Status != "active" || user.Epoch != snapshot.Epoch {
			return domain.ErrUnauthenticated
		}
		key, e := t.Passkey(keySnapshot.ID, true)
		if e != nil {
			return e
		}
		if key.Version != keySnapshot.Version || key.UserID != user.ID {
			return domain.ErrConflict
		}
		latest, e := t.PasskeyCeremony(id, true)
		if e != nil {
			return e
		}
		if e = s.liveCeremony(latest, "login"); e != nil {
			return e
		}
		if !bytes.Equal(c.Record, latest.Record) {
			return domain.ErrConflict
		}
		key.Record, e = json.Marshal(credentialRecordV1{1, *credential})
		if e != nil {
			return e
		}
		key.Version++
		key.UpdatedAt = s.Now().UTC()
		if e = t.PutPasskey(key); e != nil {
			return e
		}
		latest.Used = true
		if e = t.PutPasskeyCeremony(latest); e != nil {
			return e
		}
		f.UserID = user.ID
		f.AuthEpoch = user.Epoch
		f.AuthTime = s.Now().Unix()
		f.PrimaryMethod = "passkey"
		f.AMR = []string{"passkey"}
		f.Step = domain.StepConsent
		a, e = t.App()
		if e != nil {
			return e
		}
		client, e := t.Client(f.ClientID)
		if e != nil {
			return e
		}
		if user.TOTPSecret != "" || a.RequireMFA || client.System {
			f.Step = domain.StepMFA
		}
		if e = t.PutFlow(f); e != nil {
			return e
		}
		return t.Audit(audit(scope.ID(), user.ID, "user.authenticate_passkey", user.ID))
	})
}
