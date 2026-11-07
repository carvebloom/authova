package application

import (
	"context"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
)

// challengeForIssuance is used before quota admission and again under the write
// lock. No external limiter/network call runs while a database lock is held.
func (s *Service) challengeForIssuance(t ports.AppTx, id, kind, purpose string, lock bool) (domain.Flow, domain.App, error) {
	f, err := t.Flow(id, lock)
	if err != nil {
		return f, domain.App{}, err
	}
	if err = s.LiveFlow(f); err != nil {
		return f, domain.App{}, err
	}
	if f.Step != domain.StepChoose && f.Step != "password" && f.Step != domain.StepVerifyIdentifier {
		return f, domain.App{}, domain.ErrConflict
	}
	a, err := t.App()
	if err != nil {
		return f, a, err
	}
	if a.Status != "active" {
		return f, a, domain.ErrForbidden
	}
	if err = challengePolicy(t, f, kind, purpose); err != nil {
		return f, a, err
	}
	if purpose == "change_identifier" && (f.Purpose != "change_identifier" || f.UserID == "" || f.AuthTime < s.Now().Add(-10*time.Minute).Unix()) {
		return f, a, domain.ErrUnauthenticated
	}
	return f, a, nil
}

func (s *Service) admitChallenge(ctx context.Context, app, kind, value string, limits domain.ResourceLimits) error {
	if s.ChallengeLimiter == nil || !limits.Valid() {
		return domain.ErrUnavailable
	}
	maximum := limits.EmailChallengesPerHour
	if kind == "phone" {
		maximum = limits.SMSChallengesPerHour
	}
	for _, budget := range []struct {
		key     string
		maximum int
		reason  string
	}{
		{"app-challenges:" + app + ":" + kind, maximum, "app"},
		{"otp-destination:" + s.Digest(app, "otp-budget:"+kind, value), 5, "destination"},
	} {
		allowed, err := s.ChallengeLimiter.Allow(ctx, budget.key, budget.maximum, time.Hour)
		if err != nil {
			return domain.ErrUnavailable
		}
		if !allowed {
			if s.ObserveChallengeLimit != nil {
				s.ObserveChallengeLimit(budget.reason)
			}
			return &domain.RateLimitError{RetryAfterSeconds: 3600}
		}
	}
	return nil
}
