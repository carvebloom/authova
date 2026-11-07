package application

import (
	"errors"
	"time"

	"github.com/carvebloom/authova/internal/domain"
)

const identifierProofVersion = 1

// credentialReader is deliberately smaller than AppTx. Guards run inside the
// caller's write transaction; they never begin or commit a second transaction.
type credentialReader interface {
	User(string, bool) (domain.User, error)
	FindUser(string, string) (domain.User, error)
	Session(string, bool) (domain.Session, error)
	SessionByHandle(string) (domain.Session, error)
}

// LockedSession serializes sensitive writes with credential changes. Lock order:
// optional flow, optional App owner lock, user, session. Revocation can lock a
// session alone, but must never acquire a user lock afterwards.
// KDF and network I/O must be completed before calling this guard.
func (s *Service) LockedSession(t credentialReader, scope domain.Scope, handle string, recent bool) (session domain.Session, user domain.User, err error) {
	session, err = t.SessionByHandle(s.Digest(scope.ID(), "session-handle", handle))
	if err != nil {
		return
	}
	user, err = t.User(session.UserID, true)
	if err != nil {
		return
	}
	session, err = s.lockedSessionForUser(t, session.ID, user, recent)
	return
}
func (s *Service) lockedSessionForUser(t credentialReader, id string, u domain.User, recent bool) (domain.Session, error) {
	session, err := t.Session(id, true)
	if err != nil {
		return session, err
	}
	now := s.Now()
	if u.Status != "active" || session.UserID != u.ID || session.Epoch != u.Epoch || session.Status != "active" || !session.ExpiresAt.After(now) || (recent && session.AuthTime < now.Add(-10*time.Minute).Unix()) {
		return session, domain.ErrUnauthenticated
	}
	return session, nil
}

// identifierProofUser validates the identity and credential generation captured
// before a proof was issued or an expensive password check began. Caller holds
// the flow lock; this guard locks the user before checking current alias ownership.
func (s *Service) identifierProofUser(t credentialReader, scope domain.Scope, f domain.Flow) (domain.User, error) {
	// Old flows without a bound target/version fail closed after upgrade.
	if f.UserID == "" || f.AuthEpoch < 1 || (f.VerifiedKind != "email" && f.VerifiedKind != "phone") {
		return domain.User{}, domain.ErrUnauthenticated
	}
	u, err := t.User(f.UserID, true)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			err = domain.ErrUnauthenticated
		}
		return u, err
	}
	if u.Status != "active" || u.Epoch != f.AuthEpoch {
		return u, domain.ErrUnauthenticated
	}
	owner, err := t.FindUser(f.VerifiedKind, s.Digest(scope.ID(), "identifier:"+f.VerifiedKind, f.VerifiedValue))
	if errors.Is(err, domain.ErrNotFound) {
		return u, domain.ErrUnauthenticated
	}
	if err != nil {
		return u, err
	}
	if owner.ID != u.ID {
		return u, domain.ErrUnauthenticated
	}
	return u, nil
}
