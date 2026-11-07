package application

import (
	"context"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
)

func (s *Service) NewRecoveryCodes(t ports.AppTx, app, userID string) ([]string, error) {
	user, e := t.User(userID, true)
	if e != nil {
		return nil, e
	}
	codes := make([]string, 10)
	user.RecoveryDigests = make([]string, 10)
	for i := range codes {
		codes[i] = secure.Random(16)
		user.RecoveryDigests[i] = s.Digest(app, "mfa-recovery", codes[i])
	}
	if e = t.PutUser(user); e != nil {
		return nil, e
	}
	return codes, t.Audit(audit(app, userID, "user.recovery_codes_generate", userID))
}
func (s *Service) RecoveryMFA(ctx context.Context, scope domain.Scope, flow, code string) error {
	return s.Store.App(ctx, scope, true, func(t ports.AppTx) error {
		f, e := t.Flow(flow, true)
		if e != nil {
			return e
		}
		if e = s.LiveFlow(f); e != nil {
			return e
		}
		if f.Step != "mfa" || f.UserID == "" {
			return domain.ErrForbidden
		}
		user, e := authenticatedFlowUser(t, f, true)
		if e != nil {
			return e
		}
		digest := s.Digest(scope.ID(), "mfa-recovery", code)
		found := -1
		for i, value := range user.RecoveryDigests {
			if secure.Equal(value, digest) {
				found = i
			}
		}
		if found < 0 {
			return domain.ErrUnauthenticated
		}
		user.RecoveryDigests = append(user.RecoveryDigests[:found], user.RecoveryDigests[found+1:]...)
		if e = t.PutUser(user); e != nil {
			return e
		}
		f.Step = domain.StepMFAReplaceConfirm
		f.MFAOperation = "recovery"
		f.PendingTOTP = secure.NewTOTP()
		f.MFATime = s.Now().Unix()
		f.AMR = append(f.AMR, "recovery")
		if e = t.PutFlow(f); e != nil {
			return e
		}
		return t.Audit(audit(scope.ID(), user.ID, "user.mfa_recovery", user.ID))
	})
}
