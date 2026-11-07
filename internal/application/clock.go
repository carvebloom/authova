package application

import (
	"github.com/carvebloom/authova/internal/domain"
	"time"
)

// Option establishes immutable service dependencies at construction time.
type Option func(*Service)

func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }
func (s *Service) Now() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}
func (s *Service) LiveFlow(f domain.Flow) error { return liveFlowAt(f, s.Now()) }
func liveFlowAt(f domain.Flow, now time.Time) error {
	if f.Expires <= now.Unix() {
		return domain.ErrExpired
	}
	if f.Completed {
		return domain.ErrConflict
	}
	return nil
}
