package sqlstore

import (
	"context"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/resource"
)

func (s *Store) OutboxStats(ctx context.Context) (facts domain.OutboxStats, err error) {
	if s.Admission != nil {
		release, e := s.Admission.Acquire(ctx, "", resource.Database)
		if e != nil {
			return facts, e
		}
		defer release()
	}
	var oldest int64
	now := time.Now().Unix()
	err = s.db.QueryRowContext(ctx, s.bind("SELECT (SELECT COUNT(*) FROM (SELECT id FROM outbox WHERE completed=0 LIMIT 10001) pending_rows),(SELECT COUNT(*) FROM (SELECT id FROM outbox WHERE completed=2 LIMIT 10001) failed_rows),COALESCE((SELECT available_at FROM outbox WHERE completed=0 AND available_at<=? ORDER BY available_at LIMIT 1),?)"), now, now).Scan(&facts.Pending, &facts.Failed, &oldest)
	facts.PendingCapped = facts.Pending > 10000
	facts.FailedCapped = facts.Failed > 10000
	facts.Pending = min(facts.Pending, 10000)
	facts.Failed = min(facts.Failed, 10000)
	facts.OldestDueSeconds = float64(max(int64(0), now-oldest))
	return facts, classify(err)
}
