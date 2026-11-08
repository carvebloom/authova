package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"math"
	"sort"
	"strings"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
)

const outboxLeaseSeconds = 120

// outbox_dispatch is trusted worker metadata, like the existing shared outbox.
// It contains no identity or payload and is not exposed through an App API.
// Every operation locks the dispatch row before touching message rows.
func (t *tx) Enqueue(m domain.Message) error {
	if m.AppID != t.app || t.app == "" {
		return domain.ErrForbidden
	}
	if m.ID == "" || m.Available < 0 || m.Available > math.MaxInt64/1000000 || m.Attempts < 0 || m.Attempts >= math.MaxInt32 {
		return domain.ErrInvalid
	}
	b, err := t.seal("message", m)
	if err != nil {
		return err
	}
	query := `INSERT INTO outbox_dispatch(app_id,last_dispatch_at,next_dispatch_at) VALUES (?,0,?) `
	if t.store.dialect == "postgres" {
		query += `ON CONFLICT(app_id) DO UPDATE SET next_dispatch_at=CASE WHEN outbox_dispatch.next_dispatch_at IS NULL THEN GREATEST(outbox_dispatch.last_dispatch_at,EXCLUDED.next_dispatch_at) ELSE LEAST(outbox_dispatch.next_dispatch_at,GREATEST(outbox_dispatch.last_dispatch_at,EXCLUDED.next_dispatch_at)) END`
	} else {
		query += `ON DUPLICATE KEY UPDATE next_dispatch_at=CASE WHEN next_dispatch_at IS NULL THEN GREATEST(last_dispatch_at,VALUES(next_dispatch_at)) ELSE LEAST(next_dispatch_at,GREATEST(last_dispatch_at,VALUES(next_dispatch_at))) END`
	}
	if err = t.exec(query, t.app, m.Available*1000000); err != nil {
		return err
	}
	_, err = t.entities().OutboxMessage.Create().SetID(m.ID).SetAppID(t.app).SetAvailableAt(m.Available).SetAttempts(m.Attempts).SetCompleted(0).SetPayload(b).Save(t.ctx)
	return classify(err)
}

func validateDeliveryLease(request ports.DeliveryLeaseRequest) error {
	if request.Limit < 1 || request.Limit > 1000 || request.AppLimit < 1 || request.AppLimit > 1000 || len(request.InFlight) > 1000 {
		return domain.ErrInvalid
	}
	for app, count := range request.InFlight {
		if app == "" || len(app) > 128 || count < 0 || count > request.AppLimit {
			return domain.ErrInvalid
		}
	}
	return nil
}

func (t *tx) outboxTime() (int64, error) {
	query := "SELECT CAST(EXTRACT(EPOCH FROM clock_timestamp())*1000000 AS BIGINT)"
	if t.store.dialect == "mysql" {
		query = "SELECT CAST(UNIX_TIMESTAMP(CURRENT_TIMESTAMP(6))*1000000 AS SIGNED)"
	}
	var now int64
	err := t.one(query).Scan(&now)
	return now, classify(err)
}

// LeaseMessages rotates persisted App opportunities, then leases within the
// caller's remaining per-worker budget. Waiting Apps do not depend on message
// backlog size or another worker's in-memory cursor. SKIP LOCKED lets a peer
// progress while an App's producer/worker holds its scheduler row.
func (s *Store) LeaseMessages(ctx context.Context, request ports.DeliveryLeaseRequest) ([]domain.Message, error) {
	if err := validateDeliveryLease(request); err != nil {
		return nil, err
	}
	out := []domain.Message{}
	err := s.transaction(ctx, "", true, func(t *tx) error {
		now, err := t.outboxTime()
		if err != nil {
			return err
		}
		excluded := []string{}
		for app, count := range request.InFlight {
			if count >= request.AppLimit {
				excluded = append(excluded, app)
			}
		}
		sort.Strings(excluded)
		query := "SELECT app_id,last_dispatch_at FROM outbox_dispatch WHERE next_dispatch_at<=?"
		args := []any{now}
		if len(excluded) != 0 {
			query += " AND app_id NOT IN (?" + strings.Repeat(",?", len(excluded)-1) + ")"
			for _, app := range excluded {
				args = append(args, app)
			}
		}
		query += " ORDER BY next_dispatch_at,app_id LIMIT ? FOR UPDATE SKIP LOCKED"
		args = append(args, min(64, request.Limit*4))
		rows, err := t.rows(query, args...)
		if err != nil {
			return err
		}
		type candidate struct {
			app  string
			last int64
		}
		apps := []candidate{}
		for rows.Next() {
			var app candidate
			if err = rows.Scan(&app.app, &app.last); err != nil {
				rows.Close()
				return classify(err)
			}
			apps = append(apps, app)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return classify(err)
		}
		if err = rows.Close(); err != nil {
			return classify(err)
		}
		for _, app := range apps {
			if len(out) >= request.Limit {
				break
			}
			if app.last == math.MaxInt64 {
				return domain.ErrInvalid
			}
			stamp := max(now, app.last+1)
			limit := min(request.Limit-len(out), request.AppLimit-request.InFlight[app.app])
			messages, unavailable, err := t.outboxAppMessages(app.app, now/1000000, limit)
			if err != nil {
				return err
			}
			if unavailable != "" {
				// Keep ciphertext and attempts intact: a temporarily missing old
				// key must not silently destroy valid mail. Isolate this App so
				// another App can still make progress until the fault is repaired.
				if err = t.exec("UPDATE outbox_dispatch SET last_dispatch_at=?,next_dispatch_at=? WHERE app_id=?", stamp, max(stamp, now+5000000), app.app); err != nil {
					return err
				}
				slog.Error("outbox payload unavailable", "app_id", app.app, "message_id", unavailable)
				continue
			}
			for _, message := range messages {
				if err = t.exec("UPDATE outbox SET available_at=?,attempts=?,failure_phase='send',failure_code='unknown',delivery_state='unknown',outcome_unknown=1 WHERE app_id=? AND id=? AND completed=0 AND attempts=?", now/1000000+outboxLeaseSeconds, message.Attempts, app.app, message.ID, message.Attempts-1); err != nil {
					return err
				}
			}
			if err = t.scheduleOutbox(app.app, stamp); err != nil {
				return err
			}
			out = append(out, messages...)
		}
		return nil
	})
	if err != nil {
		return nil, err // Includes unknown commit outcomes; never hand out uncommitted leases.
	}
	return out, nil
}

func (t *tx) outboxAppMessages(app string, now int64, limit int) ([]domain.Message, string, error) {
	rows, err := t.rows("SELECT id,payload,attempts FROM outbox WHERE app_id=? AND completed=0 AND available_at<=? ORDER BY available_at,id LIMIT ? FOR UPDATE SKIP LOCKED", app, now, limit)
	if err != nil {
		return nil, "", err
	}
	type pending struct {
		id       string
		data     []byte
		attempts int
	}
	items := []pending{}
	for rows.Next() {
		var item pending
		if err = rows.Scan(&item.id, &item.data, &item.attempts); err != nil {
			rows.Close()
			return nil, "", classify(err)
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, "", classify(err)
	}
	if err = rows.Close(); err != nil {
		return nil, "", classify(err)
	}
	// Decode the entire App group before changing any attempts. One corrupt
	// envelope must not leave a half-published App lease or cross-App payload.
	scoped := *t
	scoped.app = app
	out := []domain.Message{}
	for _, item := range items {
		var message domain.Message
		if err = scoped.open("message", item.data, &message); err != nil || message.ID != item.id || message.AppID != app || item.attempts < 0 || item.attempts >= math.MaxInt32 {
			return nil, item.id, nil
		}
		message.Attempts = item.attempts + 1
		out = append(out, message)
	}
	return out, "", nil
}

func (t *tx) scheduleOutbox(app string, last int64) error {
	var next sql.NullInt64
	if err := t.one("SELECT MIN(available_at) FROM outbox WHERE app_id=? AND completed=0", app).Scan(&next); err != nil {
		return classify(err)
	}
	if !next.Valid {
		return t.exec("UPDATE outbox_dispatch SET last_dispatch_at=?,next_dispatch_at=NULL WHERE app_id=?", last, app)
	}
	if next.Int64 < 0 || next.Int64 > math.MaxInt64/1000000 {
		return domain.ErrInvalid
	}
	return t.exec("UPDATE outbox_dispatch SET last_dispatch_at=?,next_dispatch_at=? WHERE app_id=?", last, max(last, next.Int64*1000000), app)
}

func (s *Store) FinishMessage(ctx context.Context, id string, attempt, outcome int, next int64) error {
	if id == "" || attempt < 1 || outcome < 0 || outcome > 2 || next < 0 || next > math.MaxInt64/1000000 {
		return domain.ErrInvalid
	}
	return s.finishMessage(ctx, id, attempt, outcome, next, ports.DeliveryFailure{Phase: "unknown", Code: "unknown", DeliveryState: "unknown"}, false)
}

func (s *Store) FinishMessageWithFailure(ctx context.Context, id string, attempt, outcome int, next int64, f ports.DeliveryFailure) error {
	if id == "" || attempt < 1 || outcome < 0 || outcome > 2 || next < 0 || next > math.MaxInt64/1000000 {
		return domain.ErrInvalid
	}
	switch f.DeliveryState {
	case "unknown", "not_sent", "accepted":
	default:
		return domain.ErrInvalid
	}
	switch f.Phase {
	case "send", "ack", "unknown":
	default:
		return domain.ErrInvalid
	}
	switch f.Code {
	case "unknown", "dependency", "internal", "cancelled", "deadline", "outcome_unknown", "expired", "accepted", "lease_lost", "committed_cleanup", "invalid", "not_found", "conflict", "forbidden", "authentication", "rate_limited", "provider_missing", "provider_rejected", "transport":
	default:
		return domain.ErrInvalid
	}
	return s.finishMessage(ctx, id, attempt, outcome, next, f, true)
}
func (s *Store) finishMessage(ctx context.Context, id string, attempt, outcome int, next int64, f ports.DeliveryFailure, strict bool) error {
	return s.transaction(ctx, "", true, func(t *tx) error {
		var app string
		err := t.one("SELECT app_id FROM outbox WHERE id=? AND attempts=? AND completed=0", id, attempt).Scan(&app)
		if errors.Is(err, sql.ErrNoRows) {
			if strict {
				return domain.ErrLeaseLost
			}
			return nil
		}
		if err != nil {
			return classify(err)
		}
		var last int64
		if err = t.one("SELECT last_dispatch_at FROM outbox_dispatch WHERE app_id=? FOR UPDATE", app).Scan(&last); err != nil {
			return classify(err)
		}
		result, err := t.sql.ExecContext(t.ctx, t.store.bind("UPDATE outbox SET completed=?,available_at=?,failure_phase=?,failure_code=?,outcome_unknown=?,delivery_state=? WHERE app_id=? AND id=? AND attempts=? AND completed=0"), outcome, next, f.Phase, f.Code, boolInt(f.OutcomeUnknown), f.DeliveryState, app, id, attempt)
		if err != nil {
			return classify(err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return classify(err)
		}
		if changed == 0 {
			if strict {
				return domain.ErrLeaseLost
			}
			return nil
		}
		return t.scheduleOutbox(app, last)
	})
}
