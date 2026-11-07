package sqlstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/carvebloom/authova/internal/domain"

	"github.com/google/uuid"
)

// LoadSyntheticUsers is used only by the development capacity tool, never an HTTP API.
// Each row has its own identifier and authenticated ciphertext; no repeated fake payload.
func (s *Store) LoadSyntheticUsers(ctx context.Context, scope domain.Scope, count int, passwordHash string, progress func(int)) ([]string, error) {
	samples := []string{}
	already, e := s.UserCount(ctx, scope)
	if e != nil {
		return nil, e
	}
	if already > 0 {
		e = s.transaction(ctx, scope.ID(), false, func(t *tx) error {
			users, err := t.Users("", 1000)
			if err != nil {
				return err
			}
			for _, user := range users {
				samples = append(samples, user.ID)
			}
			return nil
		})
		if e != nil {
			return nil, e
		}
	}
	for offset := int(already); offset < count; offset += 500 {
		n := min(500, count-offset)
		e := s.transaction(ctx, scope.ID(), true, func(t *tx) error {
			args := make([]any, 0, n*6)
			identifiers := make([]any, 0, n*4)
			for i := 0; i < n; i++ {
				id := uuid.Must(uuid.NewV7()).String()
				email := fmt.Sprintf("load-%d@example.test", offset+i)
				user := domain.User{ID: id, AppID: scope.ID(), DisplayName: "Synthetic load user", Status: "active", Epoch: 1, Email: email, PasswordHash: passwordHash, CreatedAt: time.Now().UTC()}
				payload, e := t.seal("user", user)
				if e != nil {
					return e
				}
				args = append(args, scope.ID(), id, "active", int64(1), user.CreatedAt.Unix(), payload)
				identifiers = append(identifiers, scope.ID(), "email", s.vault.Digest(scope.ID()+":identifier:email", email), id)
				if len(samples) < 1000 {
					samples = append(samples, id)
				}
			}
			values := strings.TrimSuffix(strings.Repeat("(?,?,?,?,?,?),", n), ",")
			if e := t.exec("INSERT INTO users(app_id,id,status,session_epoch,created_at,payload) VALUES "+values, args...); e != nil {
				return e
			}
			values = strings.TrimSuffix(strings.Repeat("(?,?,?,?),", n), ",")
			return t.exec("INSERT INTO identifiers(app_id,kind,digest,user_id) VALUES "+values, identifiers...)
		})
		if e != nil {
			return nil, e
		}
		if progress != nil && ((offset+n)/100000 > offset/100000 || offset+n == count) {
			progress(offset + n)
		}
	}
	return samples, nil
}
func (s *Store) UserCount(ctx context.Context, scope domain.Scope) (n int64, err error) {
	err = s.transaction(ctx, scope.ID(), false, func(t *tx) error {
		return classify(t.one("SELECT COUNT(*) FROM users WHERE app_id=?", scope.ID()).Scan(&n))
	})
	return
}

// SyntheticSampleIDs selects spread-out synthetic users by their indexed email
// digests. It never scans OFFSET positions or decrypts the full dataset.
func (s *Store) SyntheticSampleIDs(ctx context.Context, scope domain.Scope, population int64, count int) (ids []string, err error) {
	if population < 1 || count < 1 || int64(count) > population {
		return nil, domain.ErrInvalid
	}
	err = s.transaction(ctx, scope.ID(), false, func(t *tx) error {
		for index := 0; index < count; index++ {
			ordinal := int64(index) * population / int64(count)
			email := fmt.Sprintf("load-%d@example.test", ordinal)
			var id string
			if e := t.one("SELECT user_id FROM identifiers WHERE app_id=? AND kind='email' AND digest=?", scope.ID(), s.vault.Digest(scope.ID()+":identifier:email", email)).Scan(&id); e != nil {
				return classify(e)
			}
			ids = append(ids, id)
		}
		return nil
	})
	return
}
