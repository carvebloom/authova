package sqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/secure"
)

type rewrapTable struct {
	name, kind    string
	keys          []string
	platform, raw bool
}

var rewrapTables = []rewrapTable{
	{"agents", "agent", []string{"id"}, false, true},
	{"agent_installations", "agent-installation", []string{"id"}, false, true},
	{"agent_grants", "agent-grant", []string{"id"}, false, true},
	{"passkeys", "passkey", []string{"id"}, false, true},
	{"passkey_ceremonies", "passkey-ceremony", []string{"id"}, false, true},
	{"operators", "operator", []string{"id"}, true, false},
	{"management_sessions", "management-session", []string{"digest"}, true, false},
	{"platform_idempotency", "", []string{"operator_id", "idem_key"}, true, true},
	{"clients", "client", []string{"id"}, false, false},
	{"users", "user", []string{"id"}, false, false},
	{"sessions", "session", []string{"id"}, false, false},
	{"flows", "flow", []string{"id"}, false, false},
	{"refresh_tokens", "refresh", []string{"digest"}, false, false},
	{"access_tokens", "token", []string{"id"}, false, false},
	{"challenges", "challenge", []string{"id"}, false, false},
	{"connections", "connection", []string{"id"}, false, false},
	{"signing_keys", "key", []string{"id"}, false, false},
	{"audit_events", "audit", []string{"id"}, false, false},
	{"outbox", "message", []string{"id"}, false, false},
	{"idempotency", "idempotency", []string{"idem_key"}, false, true},
	{"oauth_consents", "consent", []string{"user_id", "client_id"}, false, false},
}

// RewrapTables is an allowlist, never caller-supplied SQL. Empty App selects
// only platform payloads (including platform audits). Each App is explicit so
// normal runtime permissions and PostgreSQL RLS continue to apply.
func RewrapTables(app string) []string {
	out := []string{}
	for _, table := range rewrapTables {
		if table.platform == (app == "") || table.name == "audit_events" {
			out = append(out, table.name)
		}
	}
	return out
}

type RewrapResult struct {
	Scanned      int            `json:"scanned"`
	Updated      int            `json:"updated"`
	NeedsRewrite int            `json:"needs_rewrite"`
	KeyCounts    map[string]int `json:"key_counts"`
	Next         []string       `json:"next"`
}

// RewrapPage authenticates every scanned payload, upgrades its codec and
// re-encrypts under the active key. Run with writers stopped, retain backups and
// old keys. Each bounded page is atomic. After commit-unknown do not advance a
// checkpoint; inspect/rerun in verify mode. There are no automatic write retries.
// verify=true authenticates without changing data and permits read-only DB use.
func (s *Store) RewrapPage(ctx context.Context, app, table string, after []string, limit int, verify bool) (result RewrapResult, err error) {
	if limit < 1 || limit > 500 {
		return result, domain.ErrInvalid
	}
	var spec *rewrapTable
	for i := range rewrapTables {
		if rewrapTables[i].name == table {
			spec = &rewrapTables[i]
			break
		}
	}
	if spec == nil || (spec.platform != (app == "") && spec.name != "audit_events") || (len(after) != 0 && len(after) != len(spec.keys)) {
		return result, domain.ErrInvalid
	}
	result.KeyCounts = map[string]int{}
	err = s.transaction(ctx, app, !verify, func(t *tx) error {
		args := []any{}
		where := "1=1"
		if !spec.platform {
			where = "app_id=?"
			args = append(args, app)
		}
		if len(after) > 0 {
			clauses := []string{}
			for i, key := range spec.keys {
				terms := []string{}
				for j := 0; j < i; j++ {
					terms = append(terms, spec.keys[j]+"=?")
					args = append(args, after[j])
				}
				terms = append(terms, key+">?")
				args = append(args, after[i])
				clauses = append(clauses, "("+strings.Join(terms, " AND ")+")")
			}
			where += " AND (" + strings.Join(clauses, " OR ") + ")"
		}
		args = append(args, limit)
		rows, e := t.rows("SELECT "+strings.Join(spec.keys, ",")+",payload FROM "+spec.name+" WHERE "+where+" ORDER BY "+strings.Join(spec.keys, ",")+" LIMIT ?"+t.lock(!verify), args...)
		if e != nil {
			return e
		}
		type row struct {
			keys []string
			data []byte
		}
		batch := []row{}
		for rows.Next() {
			item := row{keys: make([]string, len(spec.keys))}
			dest := []any{}
			for i := range item.keys {
				dest = append(dest, &item.keys[i])
			}
			dest = append(dest, &item.data)
			if e = rows.Scan(dest...); e != nil {
				rows.Close()
				return classify(e, "rewrap_scan")
			}
			batch = append(batch, item)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return classify(e, "rewrap_scan")
		}
		for _, item := range batch {
			purpose := app + ":" + spec.kind
			if spec.name == "passkeys" || spec.name == "passkey_ceremonies" || strings.HasPrefix(spec.kind, "agent") {
				purpose += ":" + item.keys[0]
			}
			if spec.name == "platform_idempotency" {
				purpose = "platform-idempotency:" + item.keys[0]
			}
			id, e := secure.CiphertextKeyID(item.data)
			if e != nil {
				return errors.New("invalid rewrap ciphertext")
			}
			label := id
			if label == "" {
				label = "legacy"
			}
			result.KeyCounts[label]++
			plain, e := s.vault.Open(purpose, item.data)
			if e != nil {
				return errors.New("rewrap authentication failed")
			}
			if spec.name == "passkeys" || spec.name == "passkey_ceremonies" {
				if validatePasskeyEnvelope(spec.kind, item.keys[0], plain) != nil {
					return errors.New("rewrap codec failed")
				}
			}
			if strings.HasPrefix(spec.kind, "agent") && validateAgentEnvelope(spec.kind, item.keys[0], plain) != nil {
				return errors.New("rewrap agent codec failed")
			}
			upgraded := plain
			if !spec.raw {
				upgraded, e = upgradePayload(spec.kind, plain)
				if e != nil {
					return errors.New("rewrap codec failed")
				}
			}
			needs := id != s.vault.ActiveKeyID() || !reflect.DeepEqual(plain, upgraded)
			result.Scanned++
			result.Next = append([]string{}, item.keys...)
			if !needs {
				continue
			}
			result.NeedsRewrite++
			if verify {
				continue
			}
			encrypted, e := s.vault.Seal(purpose, upgraded)
			if e != nil {
				return e
			}
			values := []any{encrypted}
			conditions := []string{}
			if !spec.platform {
				conditions = append(conditions, "app_id=?")
				values = append(values, app)
			}
			for i, key := range spec.keys {
				conditions = append(conditions, key+"=?")
				values = append(values, item.keys[i])
			}
			// CAS also defends against accidental concurrent writers on a broken maintenance procedure.
			conditions = append(conditions, "payload=?")
			values = append(values, item.data)
			changed, e := t.sql.ExecContext(ctx, s.bind("UPDATE "+spec.name+" SET payload=? WHERE "+strings.Join(conditions, " AND ")), values...)
			if e != nil {
				return classify(e, "rewrap_update")
			}
			n, e := changed.RowsAffected()
			if e != nil {
				return classify(e, "rewrap_count")
			}
			if n != 1 {
				return domain.ErrConflict
			}
			result.Updated++
		}
		return nil
	})
	return
}
func upgradePayload(kind string, plain []byte) ([]byte, error) {
	var target any
	switch kind {
	case "operator":
		target = &domain.Operator{}
	case "management-session":
		target = &domain.ManagementSession{}
	case "client":
		target = &domain.Client{}
	case "user":
		target = &domain.User{}
	case "session":
		target = &domain.Session{}
	case "flow":
		target = &domain.Flow{}
	case "refresh":
		target = &domain.Refresh{}
	case "token":
		target = &domain.Token{}
	case "challenge":
		target = &domain.Challenge{}
	case "connection":
		target = &domain.Connection{}
	case "key":
		target = &domain.SigningKey{}
	case "audit":
		target = &domain.Audit{}
	case "message":
		target = &domain.Message{}
	case "consent":
		target = &consentV1{}
	default:
		return nil, errors.New("unknown payload kind")
	}
	if err := decodePayload(kind, plain, target); err != nil {
		return nil, err
	}
	// A v1 payload has already been validated. Preserve its exact bytes rather than
	// depending on gob encoding identity across Go compiler versions.
	if strings.HasPrefix(string(plain), string(payloadMagic)) {
		return plain, nil
	}
	return encodePayload(kind, reflect.ValueOf(target).Elem().Interface())
}

// RewrapCheckpoint can be persisted by deployment automation after a known commit.
// It contains row IDs, never plaintext payloads or keys.
type RewrapCheckpoint struct {
	App   string   `json:"app_id"`
	Table string   `json:"table"`
	After []string `json:"after"`
}

func (c RewrapCheckpoint) JSON() ([]byte, error) { return json.Marshal(c) }
