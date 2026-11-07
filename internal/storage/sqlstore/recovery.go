package sqlstore

import (
	"github.com/carvebloom/authova/internal/domain"
)

// RecoveryBatch is deliberately restricted to a closed set of scoped facts.
// The ingress and all other writers must be stopped before maintenance. Each
// page commits independently; a failed page leaves the App fence in place.
func (t *tx) RecoveryBatch(kind domain.RecoveryFact, after string, limit int) (domain.RecoveryBatch, error) {
	var out domain.RecoveryBatch
	if limit < 1 || limit > 1000 {
		return out, domain.ErrInvalid
	}
	var table string
	switch kind {
	case domain.RecoverySessions:
		table = "sessions"
	case domain.RecoveryFamilies:
		table = "refresh_families"
	case domain.RecoveryFlows:
		table = "flows"
	default:
		return out, domain.ErrInvalid
	}
	rows, err := t.rows("SELECT id FROM "+table+" WHERE app_id=? AND id>? ORDER BY id LIMIT ? FOR UPDATE", t.app, after, limit)
	if err != nil {
		return out, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, classify(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, classify(err)
	}
	for _, id := range ids {
		changed := false
		switch kind {
		case domain.RecoverySessions:
			session, e := t.Session(id, true)
			if e != nil {
				return out, e
			}
			if session.Status != "revoked" {
				session.Status = "revoked"
				if e = t.PutSession(session); e != nil {
					return out, e
				}
				changed = true
			}
		case domain.RecoveryFamilies:
			var revoked int
			if e := t.one("SELECT revoked FROM refresh_families WHERE app_id=? AND id=?", t.app, id).Scan(&revoked); e != nil {
				return out, classify(e)
			}
			if revoked == 0 {
				if e := t.RevokeFamily(id); e != nil {
					return out, e
				}
				changed = true
			}
		case domain.RecoveryFlows:
			flow, e := t.Flow(id, true)
			if e != nil {
				return out, e
			}
			if flow.Expires != 0 {
				flow.Expires = 0
				flow.Completed = true
				flow.Step = domain.StepCancelled
				if e = t.PutFlow(flow); e != nil {
					return out, e
				}
				changed = true
			}
		}
		out.Scanned++
		out.After = id
		if changed {
			out.Changed++
		}
	}
	return out, nil
}

func (p *platformTx) RecoveryManagementSessions(after string, limit int) (domain.RecoveryBatch, error) {
	var out domain.RecoveryBatch
	if limit < 1 || limit > 1000 {
		return out, domain.ErrInvalid
	}
	rows, err := p.rows("SELECT digest FROM management_sessions WHERE digest>? ORDER BY digest LIMIT ? FOR UPDATE", after, limit)
	if err != nil {
		return out, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, classify(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, classify(err)
	}
	for _, id := range ids {
		session, e := p.ManagementSession(id)
		if e != nil {
			return out, e
		}
		if !session.Revoked {
			session.Revoked = true
			if e = p.PutManagementSession(session); e != nil {
				return out, e
			}
			out.Changed++
		}
		out.Scanned++
		out.After = id
	}
	return out, nil
}
