package application

import (
	"errors"
	"github.com/carvebloom/authova/internal/domain"
	"testing"
	"time"
)

func TestFlowExpiryExactBoundary(t *testing.T) {
	now := time.Unix(1800000000, 0)
	s := &Service{now: func() time.Time { return now }}
	f := domain.Flow{Expires: now.Unix() + 1}
	if err := s.LiveFlow(f); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if !errors.Is(s.LiveFlow(f), domain.ErrExpired) {
		t.Fatal("expiry boundary accepted")
	}
	f.Expires++
	f.Completed = true
	if !errors.Is(s.LiveFlow(f), domain.ErrConflict) {
		t.Fatal("completed flow accepted")
	}
}
