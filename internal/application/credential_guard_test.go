package application

import (
	"errors"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"testing"
	"time"
)

type guardFacts struct {
	ports.AppTx
	session domain.Session
}

func (f guardFacts) Session(string, bool) (domain.Session, error) { return f.session, nil }
func TestLockedSessionTimeBoundaries(t *testing.T) {
	now := time.Unix(1800000000, 0)
	s := &Service{now: func() time.Time { return now }}
	user := domain.User{ID: "user", Status: "active", Epoch: 7}
	session := domain.Session{ID: "session", UserID: user.ID, Epoch: 7, Status: "active", ExpiresAt: now.Add(time.Second), AuthTime: now.Add(-10 * time.Minute).Unix()}
	if _, e := s.lockedSessionForUser(guardFacts{session: session}, session.ID, user, true); e != nil {
		t.Fatal("recent lower boundary changed", e)
	}
	now = now.Add(time.Second)
	if _, e := s.lockedSessionForUser(guardFacts{session: session}, session.ID, user, true); !errors.Is(e, domain.ErrUnauthenticated) {
		t.Fatal("expired/old session accepted")
	}
	session.ExpiresAt = now.Add(time.Hour)
	if _, e := s.lockedSessionForUser(guardFacts{session: session}, session.ID, user, true); !errors.Is(e, domain.ErrUnauthenticated) {
		t.Fatal("old authentication accepted")
	}
	if _, e := s.lockedSessionForUser(guardFacts{session: session}, session.ID, user, false); e != nil {
		t.Fatal("ordinary session rejected", e)
	}
}
