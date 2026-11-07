package sqlstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/carvebloom/authova/internal/domain"
)

// Cleanup reclaims expired transient facts in bounded App transactions.
// User/identifier identities and retained issuer/key identities are never silently deleted.
func (s *Store) Cleanup(ctx context.Context, batch int, auditDays int) (removed int64, err error) {
	if batch < 1 || batch > 1000 {
		return 0, domain.ErrInvalid
	}
	apps := []domain.App{}
	err = s.transaction(ctx, "", false, func(t *tx) error {
		cursor := ""
		for {
			items, e := (&platformTx{t}).Apps(cursor, 100)
			if e != nil {
				return e
			}
			apps = append(apps, items...)
			if len(items) < 100 {
				return nil
			}
			cursor = items[len(items)-1].ID
		}
	})
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-24 * time.Hour).Unix()
	for _, app := range apps {
		var committed int64
		err = s.transaction(ctx, app.ID, true, func(t *tx) error {
			tables := []struct{ table, column, predicate string }{
				{"access_tokens", "id", "expires_at<?"},
				{"refresh_tokens", "digest", "expires_at<?"},
				{"codes", "digest", "expires_at<?"},
				{"challenges", "id", "expires_at<?"},
				{"passkey_ceremonies", "id", "expires_at<?"},
				{"flows", "id", "expires_at<? AND NOT EXISTS(SELECT 1 FROM codes c WHERE c.app_id=flows.app_id AND c.flow_id=flows.id) AND NOT EXISTS(SELECT 1 FROM challenges c WHERE c.app_id=flows.app_id AND c.flow_id=flows.id)"},
				{"refresh_families", "id", "expires_at<? AND NOT EXISTS(SELECT 1 FROM access_tokens x WHERE x.app_id=refresh_families.app_id AND x.family_id=refresh_families.id) AND NOT EXISTS(SELECT 1 FROM refresh_tokens x WHERE x.app_id=refresh_families.app_id AND x.family_id=refresh_families.id)"},
				{"sessions", "id", "expires_at<? AND NOT EXISTS(SELECT 1 FROM access_tokens x WHERE x.app_id=sessions.app_id AND x.session_id=sessions.id) AND NOT EXISTS(SELECT 1 FROM refresh_families x WHERE x.app_id=sessions.app_id AND x.session_id=sessions.id)"},
				{"idempotency", "idem_key", "expires_at<?"},
			}
			for _, item := range tables {
				rows, e := t.rows("SELECT "+item.column+" FROM "+item.table+" WHERE app_id=? AND "+item.predicate+" LIMIT ? FOR UPDATE SKIP LOCKED", app.ID, cutoff, batch)
				if e != nil {
					return e
				}
				ids := []string{}
				for rows.Next() {
					var id string
					if e = rows.Scan(&id); e != nil {
						rows.Close()
						return classify(e)
					}
					ids = append(ids, id)
				}
				if e = rows.Err(); e != nil {
					rows.Close()
					return classify(e)
				}
				rows.Close()
				if len(ids) > 0 {
					args := []any{app.ID}
					for _, id := range ids {
						args = append(args, id)
					}
					placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
					if e = t.exec("DELETE FROM "+item.table+" WHERE app_id=? AND "+item.column+" IN ("+placeholders+")", args...); e != nil {
						return e
					}
					committed += int64(len(ids))
				}
			}
			if auditDays > 0 {
				// Consent retention follows audit retention. Token bindings are removed by
				// access-token FK cascade. Keep autonomous grants while their installation
				// can still be renewed; delegated refresh state cannot outlive this cutoff.
				agentCutoff := time.Now().Add(-time.Duration(auditDays) * 24 * time.Hour).Unix()
				n, e := t.purge("agent_grants", "id", `app_id=? AND expires_at<? AND NOT EXISTS(SELECT 1 FROM agent_tokens b WHERE b.app_id=agent_grants.app_id AND b.grant_id=agent_grants.id) AND (user_id IS NOT NULL OR NOT EXISTS(SELECT 1 FROM agent_installations i WHERE i.app_id=agent_grants.app_id AND i.id=agent_grants.installation_id AND i.expires_at>?))`, batch, app.ID, agentCutoff, time.Now().Unix())
				if e != nil {
					return e
				}
				committed += n
				n, e = t.purge("agent_installations", "id", `app_id=? AND expires_at<? AND NOT EXISTS(SELECT 1 FROM agent_grants g WHERE g.app_id=agent_installations.app_id AND g.installation_id=agent_installations.id) AND NOT EXISTS(SELECT 1 FROM agent_tokens b WHERE b.app_id=agent_installations.app_id AND b.installation_id=agent_installations.id)`, batch, app.ID, agentCutoff)
				if e != nil {
					return e
				}
				committed += n
				n, e = t.purge("audit_events", "id", "app_id=? AND created_at<?", batch, app.ID, time.Now().Add(-time.Duration(auditDays)*24*time.Hour).Unix())
				if e != nil {
					return e
				}
				committed += n
			}
			return nil
		})
		if err != nil {
			return
		}
		removed += committed
	}
	var committed int64
	err = s.transaction(ctx, "", true, func(t *tx) error {
		for _, item := range []struct {
			table, column, predicate string
			args                     []any
		}{
			{"outbox", "id", "completed<>0 AND available_at<?", []any{cutoff}},
			{"management_sessions", "digest", "expires_at<?", []any{cutoff}},
		} {
			n, e := t.purge(item.table, item.column, item.predicate, batch, item.args...)
			if e != nil {
				return e
			}
			committed += n
		}
		rows, e := t.rows("SELECT operator_id,idem_key FROM platform_idempotency WHERE expires_at<? LIMIT ? FOR UPDATE SKIP LOCKED", cutoff, batch)
		if e != nil {
			return e
		}
		keys := [][2]string{}
		for rows.Next() {
			var key [2]string
			if e = rows.Scan(&key[0], &key[1]); e != nil {
				rows.Close()
				return classify(e)
			}
			keys = append(keys, key)
		}
		if e = rows.Err(); e != nil {
			rows.Close()
			return classify(e)
		}
		rows.Close()
		for _, key := range keys {
			if e = t.exec("DELETE FROM platform_idempotency WHERE operator_id=? AND idem_key=?", key[0], key[1]); e != nil {
				return e
			}
			committed++
		}
		if auditDays > 0 {
			n, e := t.purge("audit_events", "id", "app_id='' AND created_at<?", batch, time.Now().Add(-time.Duration(auditDays)*24*time.Hour).Unix())
			if e != nil {
				return e
			}
			committed += n
		}
		return nil
	})
	if err == nil {
		removed += committed
	}
	return
}

// All identifiers are private constants above; callers cannot inject SQL shape.
func (t *tx) purge(table, column, predicate string, batch int, args ...any) (int64, error) {
	arguments := append(append([]any{}, args...), batch)
	rows, err := t.rows(fmt.Sprintf("SELECT %s FROM %s WHERE %s LIMIT ? FOR UPDATE SKIP LOCKED", column, table, predicate), arguments...)
	if err != nil {
		return 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, classify(err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, classify(err)
	}
	rows.Close()
	removed := int64(0)
	for _, id := range ids {
		// Recheck the expiry predicate when deleting a previously selected record.
		values := append(append([]any{}, args...), id)
		result, e := t.sql.ExecContext(t.ctx, t.store.bind(fmt.Sprintf("DELETE FROM %s WHERE %s AND %s=?", table, predicate, column)), values...)
		if e != nil {
			return 0, classify(e)
		}
		n, e := result.RowsAffected()
		if e != nil {
			return 0, classify(e)
		}
		removed += n
	}
	return removed, nil
}
