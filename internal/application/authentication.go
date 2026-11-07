package application

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/resource"
	"github.com/carvebloom/authova/internal/secure"
)

func LiveFlow(f domain.Flow) error { return liveFlowAt(f, time.Now()) }
func (s *Service) PasswordLogin(ctx context.Context, scope domain.Scope, flow, kind, value, password string) error {
	ctx = resource.WithApp(ctx, scope.ID())
	value, e := secure.Normalize(kind, value)
	if e != nil {
		return domain.ErrUnauthenticated
	}
	var u domain.User
	e = s.Store.App(ctx, scope, false, func(t ports.AppTx) error {
		f, err := t.Flow(flow, false)
		if err != nil {
			return err
		}
		if err = s.LiveFlow(f); err != nil {
			return err
		}
		if err = CheckAuthenticationPolicy(t, f, "password", "login"); err != nil {
			return err
		}
		u, e = t.FindUser(kind, s.Digest(scope.ID(), "identifier:"+kind, value))
		return e
	})
	if e != nil && !errors.Is(e, domain.ErrNotFound) {
		return e
	}
	if e2 := s.CheckPassword(ctx, password, u.PasswordHash); e2 != nil {
		return e2
	}
	if e != nil {
		return domain.ErrUnauthenticated
	}
	hash := u.PasswordHash
	return s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		f, e := t.Flow(flow, true)
		if e != nil {
			return e
		}
		if e = s.LiveFlow(f); e != nil {
			return e
		}
		if f.Step != "choose_method" && f.Step != "password" {
			return domain.ErrConflict
		}
		if e = CheckAuthenticationPolicy(t, f, "password", "login"); e != nil {
			return e
		}
		f.PrimaryMethod = "password"
		current, e := s.identifierProofUser(t, scope, domain.Flow{UserID: u.ID, AuthEpoch: u.Epoch, VerifiedKind: kind, VerifiedValue: value})
		if e != nil {
			return e
		}
		if current.Status != "active" || current.PasswordHash != hash {
			return domain.ErrUnauthenticated
		}
		a, e := t.App()
		if e != nil {
			return e
		}
		if a.Status != "active" {
			return domain.ErrForbidden
		}
		f.UserID = u.ID
		f.AuthEpoch = current.Epoch
		f.AuthTime = s.Now().Unix()
		f.AMR = []string{"pwd"}
		f.Step = domain.StepConsent
		client, e := t.Client(f.ClientID)
		if e != nil {
			return e
		}
		if current.TOTPSecret != "" || a.RequireMFA || client.System {
			f.Step = domain.StepMFA
		}
		if e = t.PutFlow(f); e != nil {
			return e
		}
		return t.Audit(audit(scope.ID(), u.ID, "user.authenticate", u.ID))
	})
}
func (s *Service) RequestChallenge(ctx context.Context, scope domain.Scope, flow, purpose, kind, value string, deliveryConfigured bool) (id string, err error) {
	if !deliveryConfigured {
		return "", domain.ErrUnavailable
	}
	if purpose != "register" && purpose != "login" && purpose != "reset_password" && purpose != "change_identifier" {
		return "", domain.ErrInvalid
	}
	value, e := secure.Normalize(kind, value)
	if e != nil {
		return "", domain.ErrInvalid
	}
	var admitted domain.ResourceLimits
	err = s.Store.App(ctx, scope, false, func(t ports.AppTx) error {
		_, a, e := s.challengeForIssuance(t, flow, kind, purpose, false)
		admitted = a.ResourceLimits()
		return e
	})
	if err != nil {
		return "", err
	}
	if err = s.admitChallenge(ctx, scope.ID(), kind, value, admitted); err != nil {
		return "", err
	}
	id = ID()
	code := secure.OTP()
	now := s.Now()
	err = s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		f, a, e := s.challengeForIssuance(t, flow, kind, purpose, true)
		if e != nil {
			return e
		}
		// A concurrent budget change requires fresh admission; do not send under
		// a stale, more permissive policy or repeat a counter operation in a lock.
		if a.ResourceLimits() != admitted {
			return domain.ErrConflict
		}
		// Bind login and reset proofs before a contact can change owner.
		// Unknown users remain unbound even if the identifier is registered later.
		if purpose == "reset_password" || purpose == "login" {
			f.UserID, f.AuthEpoch = "", 0
			u, err := t.FindUser(kind, s.Digest(scope.ID(), "identifier:"+kind, value))
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				return err
			}
			if err == nil && u.Status == "active" {
				f.UserID, f.AuthEpoch = u.ID, u.Epoch
			}
		}
		f.LoginProofVersion = 0
		if purpose == "login" {
			f.LoginProofVersion = identifierProofVersion
		}
		f.ChallengeID = id
		if purpose == "change_identifier" && (f.UserID == "" || f.AuthTime < s.Now().Add(-10*time.Minute).Unix()) {
			return domain.ErrForbidden
		}
		f.Step = domain.StepVerifyIdentifier
		f.Purpose = domain.FlowPurpose(purpose)
		f.VerifiedKind = ""
		f.VerifiedValue = ""
		c := domain.Challenge{ID: id, FlowID: flow, Kind: kind, Destination: value, Purpose: purpose, Digest: s.Digest(scope.ID(), "otp:"+id, code), Expires: now.Add(5 * time.Minute).Unix()}
		if e = t.PutChallenge(c); e != nil {
			return e
		}
		if e = t.PutFlow(f); e != nil {
			return e
		}
		return t.Enqueue(domain.Message{ID: ID(), AppID: scope.ID(), Kind: kind, Destination: value, Body: "Authova verification code: " + code + ". Expires in 5 minutes. Never share this code.", Created: now.Unix(), Available: now.Unix(), Expires: c.Expires})
	})
	return
}
func (s *Service) VerifyChallenge(ctx context.Context, scope domain.Scope, flow, id, code string) error {
	var outcome error
	err := s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		f, e := t.Flow(flow, true)
		if e != nil {
			return e
		}
		if e = s.LiveFlow(f); e != nil {
			return e
		}
		c, e := t.Challenge(id, true)
		if e != nil {
			return e
		}
		if f.ChallengeID != id || c.FlowID != flow || c.Purpose != string(f.Purpose) || f.Step != "verify_identifier" || c.Used || c.Expires <= s.Now().Unix() || c.Attempts >= 5 {
			return domain.ErrUnauthenticated
		}
		if e = challengePolicy(t, f, c.Kind, c.Purpose); e != nil {
			return e
		}
		c.Attempts++
		if !secure.Equal(c.Digest, s.Digest(scope.ID(), "otp:"+id, code)) {
			outcome = domain.ErrUnauthenticated
			return t.PutChallenge(c)
		}
		c.Used = true
		if e = t.PutChallenge(c); e != nil {
			return e
		}
		f.VerifiedKind, f.VerifiedValue = c.Kind, c.Destination
		if c.Purpose == "login" {
			f.PrimaryMethod = c.Kind + "_otp"
		}
		if f.Purpose != "change_identifier" {
			f.AMR = []string{"otp"}
			f.AuthTime = s.Now().Unix()
		}
		switch f.Purpose {
		case "change_identifier":
			f.Step = domain.StepBindIdentifier
		case "register":
			f.Step = domain.StepSetPassword
		case "reset_password":
			if _, err := s.identifierProofUser(t, scope, f); err != nil {
				return err
			}
			f.Step = domain.StepResetPassword
		case "login":
			if f.LoginProofVersion != identifierProofVersion {
				return domain.ErrUnauthenticated
			}
			u, e := s.identifierProofUser(t, scope, f)
			if errors.Is(e, domain.ErrUnauthenticated) {
				// Commit one-use consumption even when the original identity vanished.
				outcome = domain.ErrUnauthenticated
				return nil
			}
			if e != nil {
				return e
			}
			f.UserID = u.ID
			f.AuthEpoch = u.Epoch
			f.Step = domain.StepConsent
			a, e := t.App()
			if e != nil {
				return e
			}
			client, e := t.Client(f.ClientID)
			if e != nil {
				return e
			}
			if u.TOTPSecret != "" || a.RequireMFA || client.System {
				f.Step = domain.StepMFA
			}
		}
		return t.PutFlow(f)
	})
	if err != nil {
		return err
	}
	return outcome
}
func (s *Service) Register(ctx context.Context, scope domain.Scope, flow, password, name string) error {
	ctx = resource.WithApp(ctx, scope.ID())
	if len(name) > 200 {
		return domain.ErrInvalid
	}
	var method string
	e := s.Store.App(ctx, scope, false, func(t ports.AppTx) error {
		f, err := t.Flow(flow, false)
		if err != nil {
			return err
		}
		method, err = s.registrationMethod(t, f, password)
		return err
	})
	if e != nil {
		return e
	}
	h := ""
	if method == "password" {
		h, e = s.HashPassword(ctx, password)
		if e != nil {
			return e
		}
	}
	return s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		f, e := t.Flow(flow, true)
		if e != nil {
			return e
		}
		if e = s.LiveFlow(f); e != nil {
			return e
		}
		if f.Purpose != "register" || f.Step != "set_password" || f.VerifiedValue == "" {
			return domain.ErrForbidden
		}
		a, e := t.App()
		if e != nil {
			return e
		}
		if a.Status != "active" {
			return domain.ErrForbidden
		}
		currentMethod, e := s.registrationMethod(t, f, password)
		if e != nil {
			return e
		}
		if currentMethod != method {
			return domain.ErrConflict
		}
		f.Registered = true
		f.PrimaryMethod = method
		if method == "email_otp" || method == "phone_otp" {
			f.LoginProofVersion = identifierProofVersion
		}
		u := domain.User{ID: ID(), AppID: scope.ID(), DisplayName: name, Status: "active", Epoch: 1, CreatedAt: s.Now().UTC(), PasswordHash: h}
		if f.VerifiedKind == "email" {
			u.Email = f.VerifiedValue
		} else {
			u.Phone = f.VerifiedValue
		}
		if e = t.CreateUser(u, f.VerifiedKind, s.Digest(scope.ID(), "identifier:"+f.VerifiedKind, f.VerifiedValue)); e != nil {
			return e
		}
		f.UserID = u.ID
		f.AuthEpoch = u.Epoch
		f.Purpose = "authorize"
		f.Step = domain.StepConsent
		client, e := t.Client(f.ClientID)
		if e != nil {
			return e
		}
		if a.RequireMFA || client.System {
			f.Step = domain.StepMFA
		}
		f.VerifiedValue = ""
		if method == "password" {
			f.AMR = append(f.AMR, "pwd")
		}
		if e = t.PutFlow(f); e != nil {
			return e
		}
		return t.Audit(audit(scope.ID(), u.ID, "user.register", u.ID))
	})
}
func (s *Service) ResetPassword(ctx context.Context, scope domain.Scope, flow, password string) error {
	ctx = resource.WithApp(ctx, scope.ID())
	h, e := s.HashPassword(ctx, password)
	if e != nil {
		return e
	}
	return s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		f, e := t.Flow(flow, true)
		if e != nil {
			return e
		}
		if e = s.LiveFlow(f); e != nil {
			return e
		}
		if f.Purpose != "reset_password" || f.Step != "reset_password" || f.VerifiedValue == "" {
			return domain.ErrForbidden
		}
		if e = CheckAuthenticationPolicy(t, f, "password", "reset_password"); e != nil {
			return e
		}
		if e = t.LockOwners(); e != nil {
			return e
		}
		u, e := s.identifierProofUser(t, scope, f)
		if e != nil {
			return e
		}
		u.PasswordHash = h
		u.Epoch++
		if e = t.PutUser(u); e != nil {
			return e
		}
		if e = t.RevokeSessions(u.ID); e != nil {
			return e
		}
		if e = f.FinishCredential(domain.PurposeResetPassword, domain.StepResetPassword); e != nil {
			return e
		}
		if e = t.PutFlow(f); e != nil {
			return e
		}
		if e = t.Audit(audit(scope.ID(), u.ID, "user.password_reset", u.ID)); e != nil {
			return e
		}
		return t.Enqueue(domain.Message{ID: ID(), AppID: scope.ID(), Kind: f.VerifiedKind, Destination: func() string {
			if f.VerifiedKind == "phone" {
				return u.Phone
			}
			return u.Email
		}(), Body: "Your Authova password was reset and existing sessions were revoked.", Created: s.Now().Unix(), Available: s.Now().Unix()})
	})
}
func (s *Service) VerifyMFA(ctx context.Context, scope domain.Scope, flow, code string) error {
	return s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		f, e := t.Flow(flow, true)
		if e != nil {
			return e
		}
		if e = s.LiveFlow(f); e != nil {
			return e
		}
		if f.Step != "mfa" || f.UserID == "" {
			return domain.ErrConflict
		}
		u, e := authenticatedFlowUser(t, f, true)
		if e != nil {
			return e
		}
		if u.Status != "active" || u.TOTPSecret == "" {
			return domain.ErrForbidden
		}
		step, ok := secure.VerifyTOTP(u.TOTPSecret, code, u.TOTPLast, s.Now())
		if !ok {
			return domain.ErrUnauthenticated
		}
		u.TOTPLast = step
		if e = t.PutUser(u); e != nil {
			return e
		}
		f.MFATime = s.Now().Unix()
		f.AMR = append(f.AMR, "otp")
		f.Step = domain.StepConsent
		if e = t.PutFlow(f); e != nil {
			return e
		}
		return t.Audit(audit(scope.ID(), u.ID, "user.mfa", u.ID))
	})
}
func (s *Service) Complete(ctx context.Context, scope domain.Scope, flow string) (handle string, err error) {
	handle = secure.Random(32)
	err = s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		f, e := t.Flow(flow, true)
		if e != nil {
			return e
		}
		if e = s.LiveFlow(f); e != nil {
			return e
		}
		if f.Step != "consent" || f.UserID == "" {
			return domain.ErrForbidden
		}
		if (f.PrimaryMethod == "email_otp" || f.PrimaryMethod == "phone_otp") && f.LoginProofVersion != identifierProofVersion {
			return domain.ErrUnauthenticated
		}
		a, e := t.App()
		if e != nil {
			return e
		}
		if a.Status != "active" {
			return domain.ErrForbidden
		}
		client, e := t.Client(f.ClientID)
		if e != nil {
			return e
		}
		if e = CheckAuthenticationPolicy(t, f, f.PrimaryMethod, "complete"); e != nil {
			return e
		}
		if client.Status != "active" {
			return domain.ErrForbidden
		}
		var approved struct {
			RedirectURI       string
			Scopes, Audiences []string
		}
		if json.Unmarshal(f.Data, &approved) != nil || !slices.Contains(client.RedirectURIs, approved.RedirectURI) {
			return domain.ErrForbidden
		}
		for _, value := range approved.Scopes {
			if !slices.Contains(client.Scopes, value) {
				return domain.ErrForbidden
			}
		}
		for _, value := range approved.Audiences {
			if !slices.Contains(client.Audiences, value) {
				return domain.ErrForbidden
			}
		}

		u, e := authenticatedFlowUser(t, f, true)
		if e != nil {
			return e
		}
		if (a.RequireMFA || client.System || u.TOTPSecret != "") && f.MFATime == 0 {
			return domain.ErrUnauthenticated
		}
		if e = t.Consent(u.ID, f.ClientID, approved.Scopes); e != nil {
			return e
		}

		now := s.Now().UTC()
		session := domain.Session{ClientID: f.ClientID, ID: ID(), UserID: u.ID, Status: "active", CreatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour), Epoch: u.Epoch, HandleDigest: s.Digest(scope.ID(), "session-handle", handle), AuthTime: f.AuthTime, MFATime: f.MFATime, AMR: f.AMR}
		if e = t.PutSession(session); e != nil {
			return e
		}
		if e = s.CompleteAgentConsent(t, scope, &f, session); e != nil {
			return e
		}
		f.SessionID = session.ID
		f.Completed = true
		f.Step = domain.StepComplete
		f.Purpose = "authorize"
		if e = t.PutFlow(f); e != nil {
			return e
		}
		return t.Audit(audit(scope.ID(), u.ID, "session.create", session.ID))
	})
	return
}
func (s *Service) AuthenticatedSession(t ports.AppTx, scope domain.Scope, handle string, recent bool) (session domain.Session, u domain.User, err error) {
	session, err = t.SessionByHandle(s.Digest(scope.ID(), "session-handle", handle))
	if err != nil {
		return
	}
	if session.Status != "active" || session.ExpiresAt.Unix() <= s.Now().Unix() || (recent && session.AuthTime < s.Now().Add(-10*time.Minute).Unix()) {
		err = domain.ErrUnauthenticated
		return
	}
	u, err = t.User(session.UserID, false)
	if err != nil {
		return
	}
	if u.Status != "active" || u.Epoch != session.Epoch {
		err = domain.ErrUnauthenticated
	}
	return
}
func (s *Service) EnrollMFA(ctx context.Context, scope domain.Scope, flow string) (secret string, err error) {
	secret = secure.NewTOTP()
	err = s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		f, e := t.Flow(flow, true)
		if e != nil {
			return e
		}
		if e = s.LiveFlow(f); e != nil {
			return e
		}
		if f.UserID == "" || f.AuthTime < s.Now().Add(-10*time.Minute).Unix() || f.Step != "mfa" {
			return domain.ErrForbidden
		}
		u, e := authenticatedFlowUser(t, f, true)
		if e != nil {
			return e
		}
		if u.TOTPSecret != "" {
			return domain.ErrConflict
		}
		f.PendingTOTP = secret
		return t.PutFlow(f)
	})
	return
}
func (s *Service) ConfirmMFA(ctx context.Context, scope domain.Scope, flow, code string) (codes []string, err error) {
	err = s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		f, e := t.Flow(flow, true)
		if e != nil {
			return e
		}
		if e = s.LiveFlow(f); e != nil {
			return e
		}
		if f.PendingTOTP == "" || f.Step != "mfa" || f.AuthTime < s.Now().Add(-10*time.Minute).Unix() {
			return domain.ErrForbidden
		}
		u, e := authenticatedFlowUser(t, f, true)
		if e != nil {
			return e
		}
		if u.TOTPSecret != "" {
			return domain.ErrConflict
		}
		step, ok := secure.VerifyTOTP(f.PendingTOTP, code, 0, s.Now())
		if !ok {
			return domain.ErrUnauthenticated
		}
		u.Epoch++
		if e = t.RevokeSessions(u.ID); e != nil {
			return e
		}

		u.TOTPSecret = f.PendingTOTP
		u.TOTPLast = step
		f.PendingTOTP = ""
		f.AuthEpoch = u.Epoch
		f.MFATime = s.Now().Unix()
		f.Step = domain.StepConsent
		f.AMR = append(f.AMR, "otp")
		if e = t.PutUser(u); e != nil {
			return e
		}
		if e = t.PutFlow(f); e != nil {
			return e
		}
		if e = t.Audit(audit(scope.ID(), u.ID, "user.mfa_enroll", u.ID)); e != nil {
			return e
		}
		codes, e = s.NewRecoveryCodes(t, scope.ID(), u.ID)
		return e
	})

	return codes, err
}

// authenticatedFlowUser binds a pending proof to the current credential epoch.
// Legacy pending proofs without an epoch fail closed and must restart login.
func authenticatedFlowUser(t ports.AppTx, f domain.Flow, lock bool) (domain.User, error) {
	u, err := t.User(f.UserID, lock)
	if err != nil {
		return u, err
	}
	if u.Status != "active" || f.AuthEpoch == 0 || f.AuthEpoch != u.Epoch || f.AuthTime == 0 {
		return u, domain.ErrUnauthenticated
	}
	return u, nil
}
