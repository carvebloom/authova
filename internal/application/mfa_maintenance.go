package application

import (
	"context"
	"slices"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

// BeginMFAReplacement verifies an existing factor but consumes its proof only
// on confirmation. A lost setup response cannot burn the user's last recovery
// code. The flow contains a digest/counter, never the submitted proof.
func (s *Service) BeginMFAReplacement(ctx context.Context, scope domain.Scope, handle, proof, flowCookie string) (f domain.Flow, err error) {
	err = s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		session, u, e := s.LockedSession(t, scope, handle, true)
		if e != nil {
			return e
		}
		if u.Epoch != session.Epoch || u.Status != "active" || u.TOTPSecret == "" {
			return domain.ErrUnauthenticated
		}
		step, ok := secure.VerifyTOTP(u.TOTPSecret, proof, u.TOTPLast, s.Now())
		digest := ""
		if !ok {
			digest = s.Digest(scope.ID(), "mfa-recovery", proof)
			if !slices.Contains(u.RecoveryDigests, digest) {
				return domain.ErrUnauthenticated
			}
		}
		f = domain.Flow{ID: secure.Random(32), ClientID: session.ClientID, SessionID: session.ID, UserID: u.ID, AuthEpoch: u.Epoch, AuthTime: session.AuthTime, MFATime: session.MFATime, AMR: session.AMR, CookieDigest: s.Digest(scope.ID(), "flow-cookie", flowCookie), Purpose: "mfa_maintenance", Step: domain.StepMFAReplaceConfirm, MFAOperation: "replace", MFAProofDigest: digest, MFAProofStep: step, PendingTOTP: secure.NewTOTP(), Expires: s.Now().Add(10 * time.Minute).Unix(), CSRF: secure.Random(32)}
		return t.PutFlow(f)
	})
	return
}

func (s *Service) ConfirmMFAReplacement(ctx context.Context, scope domain.Scope, flow, code string) (codes []string, err error) {
	err = s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		f, e := t.Flow(flow, true)
		if e != nil {
			return e
		}
		if e = s.LiveFlow(f); e != nil {
			return e
		}
		if f.Step != "mfa_replace_confirm" || (f.MFAOperation != "replace" && f.MFAOperation != "recovery") || f.PendingTOTP == "" || f.AuthTime < s.Now().Add(-10*time.Minute).Unix() {
			return domain.ErrForbidden
		}
		u, e := authenticatedFlowUser(t, f, true)
		if e != nil {
			return e
		}
		if f.MFAOperation == "replace" {
			if _, e = s.lockedSessionForUser(t, f.SessionID, u, true); e != nil {
				return e
			}
			if f.MFAProofDigest != "" {
				if !slices.Contains(u.RecoveryDigests, f.MFAProofDigest) {
					return domain.ErrConflict
				}
			} else if f.MFAProofStep <= u.TOTPLast {
				return domain.ErrConflict
			}
		}
		step, ok := secure.VerifyTOTP(f.PendingTOTP, code, 0, s.Now())
		if !ok {
			return domain.ErrUnauthenticated
		}
		u.TOTPSecret = f.PendingTOTP
		u.TOTPLast = step
		u.Epoch++
		if e = t.PutUser(u); e != nil {
			return e
		}
		if e = t.RevokeSessions(u.ID); e != nil {
			return e
		}
		f.PendingTOTP = ""
		f.MFAProofDigest = ""
		f.MFAProofStep = 0
		f.AuthEpoch = u.Epoch
		f.MFATime = s.Now().Unix()
		f.AMR = append(f.AMR, "otp")
		if f.MFAOperation == "recovery" {
			f.Step = domain.StepConsent
		} else {
			f.Completed = true
			f.Step = domain.StepMFAMaintenanceComplete
		}
		if e = t.PutFlow(f); e != nil {
			return e
		}
		codes, e = s.NewRecoveryCodes(t, scope.ID(), u.ID)
		if e != nil {
			return e
		}
		if e = t.Audit(audit(scope.ID(), u.ID, "user.mfa_replace", u.ID)); e != nil {
			return e
		}
		return notifyMFAChange(t, scope.ID(), u)
	})
	return
}

// Recovery-code replacement requires the current TOTP factor. Recovery-only
// users first replace the lost authenticator, so a lost one-time-code response
// cannot remove their only remaining way to prove ownership.
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, scope domain.Scope, handle, code string) (codes []string, err error) {
	err = s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		session, u, e := s.LockedSession(t, scope, handle, true)
		if e != nil {
			return e
		}
		if u.Status != "active" || u.Epoch != session.Epoch || u.TOTPSecret == "" {
			return domain.ErrUnauthenticated
		}
		step, ok := secure.VerifyTOTP(u.TOTPSecret, code, u.TOTPLast, s.Now())
		if !ok {
			return domain.ErrUnauthenticated
		}
		u.TOTPLast = step
		u.Epoch++
		if e = t.PutUser(u); e != nil {
			return e
		}
		if e = t.RevokeSessions(u.ID); e != nil {
			return e
		}
		codes, e = s.NewRecoveryCodes(t, scope.ID(), u.ID)
		if e != nil {
			return e
		}
		return notifyMFAChange(t, scope.ID(), u)
	})
	return
}
func notifyMFAChange(t ports.AppTx, app string, u domain.User) error {
	kind, destination := "email", u.Email
	if destination == "" {
		kind, destination = "phone", u.Phone
	}
	if destination == "" {
		return nil
	}
	now := time.Now().Unix()
	return t.Enqueue(domain.Message{ID: ID(), AppID: app, Kind: kind, Destination: destination, Body: "Your Authova MFA credentials changed. Existing sessions and previous recovery codes were revoked. If this was not you, contact support.", Created: now, Available: now, Expires: now + 900})
}
