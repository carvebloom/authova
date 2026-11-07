package sqlstore

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/gob"
	"encoding/hex"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
	mysql "github.com/go-sql-driver/mysql"
)

func TestKeyRotationRehearsal(t *testing.T) {
	for _, dialect := range []string{"postgres", "mysql"} {
		t.Run(dialect, func(t *testing.T) {
			dsn := os.Getenv("AUTHOVA_TEST_MIGRATOR_" + strings.ToUpper(dialect) + "_DSN")
			if dsn == "" {
				t.Skip("explicit migrator DSN for isolated test DB required")
			}
			ctx := context.Background()
			old := bytes.Repeat([]byte{4}, 32)
			next := bytes.Repeat([]byte{5}, 32)
			oldVault, _ := secure.NewVault(old)
			admin, e := Open(ctx, dialect, dsn, "https://identity.example", oldVault, 4)
			if e != nil {
				t.Fatal(e)
			}
			defer admin.Close()
			suffix := make([]byte, 6)
			rand.Read(suffix)
			name := "authova_rewrap_" + hex.EncodeToString(suffix)
			if _, e = admin.db.ExecContext(ctx, "CREATE DATABASE "+name); e != nil {
				t.Fatal(e)
			}
			defer func() {
				q := "DROP DATABASE " + name
				if dialect == "postgres" {
					q += " WITH (FORCE)"
				}
				if _, e := admin.db.ExecContext(ctx, q); e != nil {
					t.Error(e)
				}
			}()
			target := dsn
			if dialect == "postgres" {
				u, _ := url.Parse(dsn)
				u.Path = "/" + name
				target = u.String()
			} else {
				c, _ := mysql.ParseDSN(dsn)
				c.DBName = name
				target = c.FormatDSN()
			}
			store, e := Open(ctx, dialect, target, "https://identity.example", oldVault, 4)
			if e != nil {
				t.Fatal(e)
			}
			defer store.Close()
			must := func(e error) {
				t.Helper()
				if e != nil {
					t.Fatal(e)
				}
			}
			must(store.Migrate(ctx))
			scope, _ := domain.AppScope("app")
			must(store.Platform(ctx, true, func(tx ports.PlatformTx) error {
				if e := tx.PutOperator(domain.Operator{ID: "operator", Email: "fixture@example.test", Status: "active", Role: "owner", Password: "synthetic-hash", TOTP: "SYNTHETIC"}); e != nil {
					return e
				}
				if e := tx.PutManagementSession(domain.ManagementSession{Digest: "management", OperatorID: "operator", Expires: 1800000000}); e != nil {
					return e
				}
				if e := tx.PutIdempotency("operator", "idem", "fp", []byte(`{"fixture":true}`), 1800000000); e != nil {
					return e
				}
				return tx.ProvisionApp(domain.App{ID: "app", Key: "key", Slug: "fixture", Name: "Fixture", Status: "active", Version: 1, CreatedAt: time.Now(), Issuer: "https://identity.example/a/key"}, domain.SigningKey{ID: "key", AppID: "app", Private: []byte("synthetic-key"), Current: true}, domain.Client{ID: "client", AppID: "app", ClientID: "client", Status: "active", Kind: "confidential", Version: 1, SecretDigest: oldVault.Digest("app:client-secret", "secret")}, domain.Audit{ID: "platform-audit", ActorID: "operator", Action: "app.create", CreatedAt: time.Now()})
			}))
			must(store.App(ctx, scope, true, func(tx ports.AppTx) error {
				for _, id := range []string{"user1", "user2", "user3"} {
					if e := tx.CreateUser(domain.User{ID: id, AppID: "app", Status: "active", Epoch: 7, Email: id + "@example.test", PasswordHash: "synthetic-hash", TOTPSecret: "SYNTHETIC", TOTPLast: 123, RecoveryDigests: []string{"recovery"}, CreatedAt: time.Now()}, "email", oldVault.Digest("app:identifier:email", id+"@example.test")); e != nil {
						return e
					}
				}

				if e := tx.PutAgent(domain.Agent{ID: "agent", AppID: "app", ClientID: "client", Name: "Synthetic Agent", Kind: "confidential", Status: "active", Modes: []string{"delegated"}, Scopes: []string{"api.read"}, Resources: []string{"urn:test:resource"}, TokenTTL: 120, GrantTTL: 3600, Version: 1, CreatedAt: time.Now()}); e != nil {
					return e
				}
				if e := tx.PutAgentInstallation(domain.AgentInstallation{ID: "installation", AppID: "app", AgentID: "agent", UserID: "user1", TenantID: "tenant", Resource: "urn:test:resource", Scopes: []string{"api.read"}, Modes: []string{"delegated"}, Status: "active", Version: 1, CreatedAt: time.Now(), Expires: 1800000000}); e != nil {
					return e
				}
				if e := tx.PutAgentGrant(domain.AgentGrant{ID: "grant", AppID: "app", AgentID: "agent", InstallationID: "installation", ClientID: "client", UserID: "user1", TenantID: "tenant", Mode: "delegated", Resource: "urn:test:resource", Scopes: []string{"api.read"}, Status: "active", Version: 1, CreatedAt: time.Now(), Expires: 1800000000}); e != nil {
					return e
				}
				if e := tx.PutPasskey(domain.Passkey{ID: "passkey", UserID: "user1", CredentialHash: strings.Repeat("a", 64), Name: "Synthetic device", Record: []byte(`{"version":1,"credential":{}}`), Version: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}); e != nil {
					return e
				}
				if e := tx.PutPasskeyCeremony(domain.PasskeyCeremony{ID: "ceremony", UserID: "user1", Kind: "register", Expires: 1800000000, Record: []byte(`{"version":1,"session":{}}`)}); e != nil {
					return e
				}
				if e := tx.PutSession(domain.Session{ID: "session", ClientID: "client", UserID: "user1", Epoch: 7, Status: "active", HandleDigest: "handle", ExpiresAt: time.Unix(1800000000, 0)}); e != nil {
					return e
				}
				if e := tx.PutFlow(domain.Flow{ID: "flow", ClientID: "client", UserID: "user1", AuthEpoch: 7, Step: "reset_password", Purpose: "reset_password", VerifiedKind: "email", VerifiedValue: "user1@example.test", Expires: 1800000000}); e != nil {
					return e
				}
				if e := tx.CreateFamily("family", "client", "session", 1800000000); e != nil {
					return e
				}
				if e := tx.PutRefresh(domain.Refresh{Digest: "refresh", FamilyID: "family", ClientID: "client", Generation: 1, Expires: 1800000000, Data: []byte("opaque")}); e != nil {
					return e
				}
				if e := tx.PutToken(domain.Token{ID: "token", ClientID: "client", UserID: "user1", Subject: "user1", SessionID: "session", FamilyID: "family", Expires: 1800000000, Epoch: 7}); e != nil {
					return e
				}
				if e := tx.PutChallenge(domain.Challenge{ID: "challenge", FlowID: "flow", Kind: "email", Destination: "user1@example.test", Digest: "digest", Expires: 1800000000}); e != nil {
					return e
				}
				if e := tx.PutConnection(domain.Connection{ID: "connection", ProviderType: "github", Issuer: "https://github.com", ClientID: "provider", Secret: "synthetic-secret", Status: "active", Version: 1}); e != nil {
					return e
				}
				if e := tx.BindExternal("connection", oldVault.Digest("app:subject", "external"), "user1"); e != nil {
					return e
				}
				if e := tx.Consent("user1", "client", []string{"openid"}); e != nil {
					return e
				}
				if e := tx.Audit(domain.Audit{ID: "audit", AppID: "app", ActorID: "user1", Action: "fixture", CreatedAt: time.Now()}); e != nil {
					return e
				}
				if e := tx.Enqueue(domain.Message{ID: "message", AppID: "app", Kind: "email", Destination: "user1@example.test", Body: "synthetic message"}); e != nil {
					return e
				}
				return tx.PutIdempotency("idem", "fp", []byte(`{"fixture":true}`), 1800000000)
			}))
			// Manufacture actual historical nonce+ciphertext + direct-domain gob, not a
			// new codec round trip mislabeled as a migration. Raw idempotency stays raw.
			for _, app := range []string{"", "app"} {
				for _, table := range RewrapTables(app) {
					var spec rewrapTable
					for _, v := range rewrapTables {
						if v.name == table {
							spec = v
						}
					}
					must(store.transaction(ctx, app, true, func(tx *tx) error {
						where := "1=1"
						args := []any{}
						if !spec.platform {
							where = "app_id=?"
							args = append(args, app)
						}
						rows, e := tx.rows("SELECT "+strings.Join(spec.keys, ",")+",payload FROM "+table+" WHERE "+where, args...)
						if e != nil {
							return e
						}
						type item struct {
							keys    []string
							payload []byte
						}
						items := []item{}
						for rows.Next() {
							v := item{keys: make([]string, len(spec.keys))}
							dest := []any{}
							for i := range v.keys {
								dest = append(dest, &v.keys[i])
							}
							dest = append(dest, &v.payload)
							if e = rows.Scan(dest...); e != nil {
								rows.Close()
								return e
							}
							items = append(items, v)
						}
						e = rows.Err()
						rows.Close()
						if e != nil {
							return e
						}
						for _, v := range items {
							purpose := app + ":" + spec.kind
							if table == "passkeys" || table == "passkey_ceremonies" || strings.HasPrefix(spec.kind, "agent") {
								purpose += ":" + v.keys[0]
							}
							if table == "platform_idempotency" {
								purpose = "platform-idempotency:" + v.keys[0]
							}
							plain, e := oldVault.Open(purpose, v.payload)
							if e != nil {
								return e
							}
							if !spec.raw {
								plain, e = legacyGob(spec.kind, plain)
								if e != nil {
									return e
								}
							}
							key := oldVault.Derive(purpose)
							block, _ := aes.NewCipher(key[:])
							g, _ := cipher.NewGCM(block)
							nonce := make([]byte, g.NonceSize())
							rand.Read(nonce)
							encrypted := g.Seal(nonce, nonce, plain, []byte(purpose))
							conditions := []string{}
							values := []any{encrypted}
							if !spec.platform {
								conditions = append(conditions, "app_id=?")
								values = append(values, app)
							}
							for i, k := range spec.keys {
								conditions = append(conditions, k+"=?")
								values = append(values, v.keys[i])
							}
							if e = tx.exec("UPDATE "+table+" SET payload=? WHERE "+strings.Join(conditions, " AND "), values...); e != nil {
								return e
							}
						}
						return nil
					}))
				}
			}
			rotated, e := secure.NewVaultKeyring(old, "second", map[string][]byte{"initial": old, "second": next}, old)
			must(e)
			store.vault = rotated
			// A corrupt later record must roll back earlier writes in the same page.
			var saved []byte
			must(store.transaction(ctx, "app", true, func(tx *tx) error {
				if e := tx.one("SELECT payload FROM users WHERE app_id=? AND id=?", "app", "user2").Scan(&saved); e != nil {
					return e
				}
				bad := bytes.Clone(saved)
				bad[len(bad)-1] ^= 1
				return tx.exec("UPDATE users SET payload=? WHERE app_id=? AND id=?", bad, "app", "user2")
			}))
			if _, e = store.RewrapPage(ctx, "app", "users", nil, 3, false); e == nil {
				t.Fatal("corrupt page accepted")
			}
			must(store.transaction(ctx, "app", true, func(tx *tx) error {
				var data []byte
				if e := tx.one("SELECT payload FROM users WHERE app_id=? AND id=?", "app", "user1").Scan(&data); e != nil {
					return e
				}
				if id, _ := secure.CiphertextKeyID(data); id != "" {
					t.Fatal("partial page committed")
				}
				return tx.exec("UPDATE users SET payload=? WHERE app_id=? AND id=?", saved, "app", "user2")
			}))
			total := 0
			for _, app := range []string{"", "app"} {
				for _, table := range RewrapTables(app) {
					cursor := []string(nil)
					for {
						before, e := store.RewrapPage(ctx, app, table, cursor, 1, true)
						must(e)
						page, e := store.RewrapPage(ctx, app, table, cursor, 1, false)
						must(e)
						if before.Scanned != page.Scanned || page.Updated != page.Scanned || page.NeedsRewrite != page.Scanned {
							t.Fatal("inventory/write mismatch", table, page)
						}
						total += page.Updated
						if page.Scanned == 0 {
							break
						}
						cursor = page.Next
					}
					check, e := store.RewrapPage(ctx, app, table, nil, 500, true)
					must(e)
					if check.Scanned == 0 || check.NeedsRewrite != 0 || check.KeyCounts["second"] != check.Scanned {
						t.Fatal("retirement inventory failed", table, check)
					}
				}
			}
			retired, e := secure.NewVaultKeyring(old, "second", map[string][]byte{"second": next}, nil)
			must(e)
			store.vault = retired
			// Authenticate every table with old encryption keys physically absent.
			for _, app := range []string{"", "app"} {
				for _, table := range RewrapTables(app) {
					_, e := store.RewrapPage(ctx, app, table, nil, 500, true)
					must(e)
				}
			}
			must(store.App(ctx, scope, false, func(tx ports.AppTx) error {
				u, e := tx.FindUser("email", retired.Digest("app:identifier:email", "user1@example.test"))
				if e != nil {
					return e
				}
				if u.ID != "user1" || u.Epoch != 7 || u.PasswordHash != "synthetic-hash" || u.TOTPLast != 123 || len(u.RecoveryDigests) != 1 {
					t.Fatal("credential facts changed")
				}
				external, e := tx.ExternalUser("connection", retired.Digest("app:subject", "external"))
				if e != nil {
					return e
				}
				if external != "user1" {
					t.Fatal("irreversible external lookup changed")
				}
				client, e := tx.Client("client")
				if e != nil {
					return e
				}
				if client.SecretDigest != retired.Digest("app:client-secret", "secret") {
					t.Fatal("client secret lookup changed")
				}
				_, e = tx.Idempotency("idem", "fp")
				return e
			}))
			t.Logf("legacy codec/ciphertext upgraded across all scoped/platform table selections; %d payloads, one-row resume, corruption rollback, inventory and retirement verified", total)
		})
	}
}
func legacyGob(kind string, plain []byte) ([]byte, error) {
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
	}
	if e := decodePayload(kind, plain, target); e != nil {
		return nil, e
	}
	var b bytes.Buffer
	e := gob.NewEncoder(&b).Encode(target)
	return b.Bytes(), e
}
