package sqlstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

func TestMySQLRuntimeGrantAllowlist(t *testing.T) {
	for grant, want := range map[string]bool{
		"GRANT USAGE ON *.* TO 'runtime'@'%'":                                         true,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON `create_authova`.* TO 'runtime'@'%'": true,
		"GRANT SELECT ON *.* TO 'runtime'@'%'":                                        false,
		"GRANT SELECT ON `authova`.* TO 'runtime'@'%' WITH GRANT OPTION":              false,
		"GRANT FILE ON *.* TO 'runtime'@'%'":                                          false,
		"GRANT EXECUTE ON `authova`.* TO 'runtime'@'%'":                               false,
		"GRANT CONNECTION_ADMIN ON *.* TO 'runtime'@'%'":                              false,
		"GRANT 'admin'@'%' TO 'runtime'@'%'":                                          false,
	} {
		if got := safeMySQLGrant(grant); got != want {
			t.Errorf("grant acceptance=%v want=%v: %s", got, want, grant)
		}
	}
}

// These destructive negative gates only touch a newly created, test-owned database.
// They require an explicit migrator DSN; ordinary test runs cannot mutate a live schema.
func TestMaintenanceGates(t *testing.T) {
	for _, dialect := range []string{"postgres", "mysql"} {
		t.Run(dialect, func(t *testing.T) {
			dsn := os.Getenv("AUTHOVA_TEST_MIGRATOR_" + strings.ToUpper(dialect) + "_DSN")
			if dsn == "" {
				t.Skip("explicit test migrator DSN required")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			vault, _ := secure.NewVault(bytes.Repeat([]byte{7}, 32))
			admin, e := Open(ctx, dialect, dsn, "https://identity.example", vault, 8)
			if e != nil {
				t.Fatal(e)
			}
			defer admin.Close()
			suffix := make([]byte, 6)
			if _, e = rand.Read(suffix); e != nil {
				t.Fatal(e)
			}
			name := "authova_gate_" + hex.EncodeToString(suffix)
			exec := func(s *Store, q string, args ...any) {
				t.Helper()
				if _, e := s.db.ExecContext(ctx, s.bind(q), args...); e != nil {
					t.Fatal(e)
				}
			}
			exec(admin, "CREATE DATABASE "+name)
			defer func() {
				if dialect == "postgres" {
					_, e = admin.db.ExecContext(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
				} else {
					_, e = admin.db.ExecContext(context.Background(), "DROP DATABASE "+name)
				}
				if e != nil {
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
			migrator, e := Open(ctx, dialect, target, "https://identity.example", vault, 8)
			if e != nil {
				t.Fatal(e)
			}
			defer migrator.Close()

			exec(migrator, "CREATE TABLE unversioned_sentinel (id INTEGER PRIMARY KEY)")
			if e = migrator.Migrate(ctx); e == nil {
				t.Fatal("unversioned nonempty database adopted")
			}
			var metadata int
			query := "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='authova_schema'"
			if dialect == "postgres" {
				query = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='public' AND table_name='authova_schema'"
			}
			if e = migrator.db.QueryRowContext(ctx, query).Scan(&metadata); e != nil || metadata != 0 {
				t.Fatal("refused baseline changed migration metadata", metadata, e)
			}
			exec(migrator, "DROP TABLE unversioned_sentinel")
			// Fresh v1 contains every invariant; reruns preserve existing facts.
			data, e := migrations.ReadFile("sql/" + dialect + "/001_initial.sql")
			if e != nil {
				t.Fatal(e)
			}
			hash := sha256.Sum256(data)
			checksum := hex.EncodeToString(hash[:])
			if e = migrator.Migrate(ctx); e != nil {
				t.Fatal(e)
			}
			exec(migrator, "INSERT INTO apps(id,app_key,slug,name,status,config_version,created_at,issuer) VALUES('app','key','gate','Gate','active',1,0,'https://identity.example/a/key')")
			e = migrator.transaction(ctx, "app", true, func(tx *tx) error {
				return tx.PutClient(domain.Client{ID: "client", AppID: "app", ClientID: "client", Kind: "confidential", Status: "active", IntrospectionAudiences: []string{"api"}})
			})
			if e != nil {
				t.Fatal(e)
			}
			if e = migrator.Migrate(ctx); e != nil {
				t.Fatal(e)
			}

			var revisions int
			if e = migrator.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM authova_atlas_revisions").Scan(&revisions); e != nil || revisions != schemaVersion {
				t.Fatal("Atlas did not record the verified baseline and applied versions", revisions, e)
			}
			if e = migrator.Migrate(ctx); e != nil {
				t.Fatal("Atlas rerun is not idempotent", e)
			}
			var revision string
			if e = migrator.db.QueryRowContext(ctx, "SELECT record FROM authova_atlas_revisions WHERE version='001'").Scan(&revision); e != nil {
				t.Fatal(e)
			}
			exec(migrator, "UPDATE authova_atlas_revisions SET record='{}' WHERE version='001'")
			if e = migrator.Migrate(ctx); e == nil {
				t.Fatal("tampered Atlas baseline accepted")
			}
			exec(migrator, "UPDATE authova_atlas_revisions SET record=? WHERE version='001'", revision)
			migrated, e := migrator.RouteApp(ctx, "key")
			if e != nil || migrated.AuthenticationPolicy() != domain.DefaultAuthenticationPolicy() || migrated.KeyEpoch != 1 {
				t.Fatal("v1 App lost its policy or verification generation", e)
			}
			var issuer, kind string
			var count int
			if e = migrator.db.QueryRowContext(ctx, "SELECT issuer FROM apps WHERE id='app'").Scan(&issuer); e != nil || issuer != "https://identity.example/a/key" {
				t.Fatalf("issuer preservation: %q %v", issuer, e)
			}
			if e = migrator.db.QueryRowContext(ctx, "SELECT kind,introspection_count FROM clients WHERE app_id='app'").Scan(&kind, &count); e != nil || kind != "confidential" || count != 1 {
				t.Fatalf("client preservation: %s %d %v", kind, count, e)
			}
			runtimeDSN := target
			runtimeInput := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(dialect) + "_DSN")
			if dialect == "postgres" {
				exec(migrator, "GRANT USAGE ON SCHEMA public TO authova_runtime")
				exec(migrator, "GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO authova_runtime")
				u, _ := url.Parse(target)
				u.User = url.UserPassword("authova_runtime", "authova-local-runtime-only")
				if runtimeInput != "" {
					input, _ := url.Parse(runtimeInput)
					u.User = input.User
				}
				runtimeDSN = u.String()
			} else {
				exec(admin, "GRANT SELECT,INSERT,UPDATE,DELETE ON "+name+".* TO 'authova_runtime'@'%'")
				defer admin.db.ExecContext(context.Background(), "REVOKE SELECT,INSERT,UPDATE,DELETE ON "+name+".* FROM 'authova_runtime'@'%'")
				c, _ := mysql.ParseDSN(target)
				c.User = "authova_runtime"
				c.Passwd = "authova-local-runtime-only"
				if runtimeInput != "" {
					input, e := mysql.ParseDSN(runtimeInput)
					if e != nil {
						t.Fatal(e)
					}
					c.User, c.Passwd = input.User, input.Passwd
				}
				runtimeDSN = c.FormatDSN()
			}
			runtime, e := Open(ctx, dialect, runtimeDSN, "https://identity.example", vault, 8)
			if e != nil {
				t.Fatal(e)
			}
			defer runtime.Close()
			healthy := func() {
				t.Helper()
				if e := runtime.CheckRuntime(ctx); e != nil {
					t.Fatal(e)
				}
			}
			reject := func() {
				t.Helper()
				if e := runtime.CheckRuntime(ctx); e == nil {
					t.Fatal("unsafe runtime accepted")
				}
			}
			healthy()
			exec(migrator, "UPDATE authova_schema SET version=8")
			reject()
			if e = migrator.Migrate(ctx); e == nil {
				t.Fatal("pre-consolidation schema was renumbered")
			}
			var preservedVersion int
			if e = migrator.db.QueryRowContext(ctx, "SELECT version FROM authova_schema WHERE singleton=1").Scan(&preservedVersion); e != nil || preservedVersion != 8 {
				t.Fatal("refused old history was modified", preservedVersion, e)
			}
			exec(migrator, "UPDATE authova_schema SET version=1")
			if e = migrator.CheckRuntime(ctx); e == nil {
				t.Fatal("privileged migrator accepted as runtime")
			}
			exec(migrator, "UPDATE authova_schema SET dirty=1")
			reject()
			if e = migrator.Migrate(ctx); e == nil {
				t.Fatal("dirty migration retried")
			}
			exec(migrator, "UPDATE authova_schema SET dirty=0")
			exec(migrator, "UPDATE authova_migration_history SET checksum='bad' WHERE version=1")
			reject()
			if e = migrator.Migrate(ctx); e == nil {
				t.Fatal("tampered migration accepted")
			}
			exec(migrator, "UPDATE authova_migration_history SET checksum=? WHERE version=1", checksum)
			if dialect == "postgres" {
				exec(migrator, "CREATE SCHEMA authova_gate_shadow")
				exec(migrator, "GRANT USAGE ON SCHEMA authova_gate_shadow TO authova_runtime")
				u, _ := url.Parse(runtimeDSN)
				q := u.Query()
				q.Set("search_path", "authova_gate_shadow,public")
				u.RawQuery = q.Encode()
				shadow, e := Open(ctx, dialect, u.String(), "https://identity.example", vault, 1)
				if e != nil {
					t.Fatal(e)
				}
				if e = shadow.CheckRuntime(ctx); e == nil {
					t.Fatal("shadow application namespace accepted")
				}
				shadow.Close()
				exec(migrator, "ALTER TABLE clients NO FORCE ROW LEVEL SECURITY")
				reject()
				exec(migrator, "ALTER TABLE clients FORCE ROW LEVEL SECURITY")
				exec(migrator, "ALTER POLICY app_scope ON clients USING(true) WITH CHECK(true)")
				reject()
				exec(migrator, "ALTER POLICY app_scope ON clients USING(app_id=current_setting('authova.app_id',true)) WITH CHECK(app_id=current_setting('authova.app_id',true))")
				exec(migrator, "CREATE POLICY unsafe ON clients USING(true)")
				reject()
				exec(migrator, "DROP POLICY unsafe ON clients")
				exec(migrator, "GRANT CREATE ON SCHEMA public TO authova_runtime")
				reject()
				exec(migrator, "REVOKE CREATE ON SCHEMA public FROM authova_runtime")
			} else {
				exec(admin, "GRANT CREATE ON "+name+".* TO 'authova_runtime'@'%'")
				reject()
				exec(admin, "REVOKE CREATE ON "+name+".* FROM 'authova_runtime'@'%'")
			}
			healthy()
			// Three expired chains, batch=2: exercise FK order and bounded committed counts.
			old := time.Now().Add(-48 * time.Hour).Unix()
			young := time.Now().Unix()
			exec(migrator, "INSERT INTO operators(id,email_digest,status,role,payload) VALUES('operator','digest','active','owner',?)", []byte{1})
			exec(migrator, "INSERT INTO users(app_id,id,status,session_epoch,created_at,payload) VALUES('app','user','active',1,0,?)", []byte{1})
			for i := 0; i < 3; i++ {
				id := fmt.Sprint(i)
				exec(migrator, "INSERT INTO sessions(app_id,id,user_id,handle_digest,status,session_epoch,expires_at,payload) VALUES('app',?,'user',?,'active',1,?,?)", id, id, old, []byte{1})
				exec(migrator, "INSERT INTO flows(app_id,id,client_id,expires_at,payload) VALUES('app',?,'client',?,?)", id, old, []byte{1})
				exec(migrator, "INSERT INTO codes(app_id,digest,flow_id,expires_at,used) VALUES('app',?,?,?,0)", id, id, old)
				exec(migrator, "INSERT INTO challenges(app_id,id,flow_id,expires_at,payload) VALUES('app',?,?,?,?)", id, id, old, []byte{1})
				exec(migrator, "INSERT INTO refresh_families(app_id,id,client_id,session_id,expires_at,revoked) VALUES('app',?,'client',?,?,0)", id, id, old)
				exec(migrator, "INSERT INTO refresh_tokens(app_id,digest,family_id,generation,used,expires_at,payload) VALUES('app',?,?,0,0,?,?)", id, id, old, []byte{1})
				exec(migrator, "INSERT INTO access_tokens(app_id,id,client_id,user_id,session_id,family_id,expires_at,revoked,payload) VALUES('app',?,'client','user',?,?,?,0,?)", id, id, id, old, []byte{1})
				exec(migrator, "INSERT INTO idempotency(app_id,idem_key,fingerprint,expires_at,payload) VALUES('app',?,'fp',?,?)", id, old, []byte{1})
				for _, app := range []string{"app", ""} {
					exec(migrator, "INSERT INTO audit_events(app_id,id,created_at,payload) VALUES(?,?,?,?)", app, id, old, []byte{1})
				}
				exec(migrator, "INSERT INTO outbox(id,app_id,available_at,attempts,completed,payload) VALUES(?,'app',?,1,1,?)", id, old, []byte{1})
				exec(migrator, "INSERT INTO management_sessions(digest,operator_id,expires_at,revoked,payload) VALUES(?,'operator',?,0,?)", id, old, []byte{1})
				exec(migrator, "INSERT INTO platform_idempotency(operator_id,idem_key,fingerprint,expires_at,payload) VALUES('operator',?,'fp',?,?)", id, old, []byte{1})
			}
			exec(migrator, "INSERT INTO audit_events(app_id,id,created_at,payload) VALUES('app','retain',?,?)", young, []byte{1})
			exec(migrator, "INSERT INTO outbox(id,app_id,available_at,attempts,completed,payload) VALUES('pending','app',?,0,0,?)", old, []byte{1})
			// Retain expired parents while non-expired children still reference them.
			exec(migrator, "INSERT INTO sessions(app_id,id,user_id,handle_digest,status,session_epoch,expires_at,payload) VALUES('app','protected','user','protected','active',1,?,?)", old, []byte{1})
			exec(migrator, "INSERT INTO refresh_families(app_id,id,client_id,session_id,expires_at,revoked) VALUES('app','protected','client','protected',?,0)", old)
			exec(migrator, "INSERT INTO access_tokens(app_id,id,client_id,user_id,session_id,family_id,expires_at,revoked,payload) VALUES('app','protected','client','user','protected','protected',?,0,?)", young+3600, []byte{1})
			exec(migrator, "INSERT INTO flows(app_id,id,client_id,expires_at,payload) VALUES('app','protected','client',?,?)", old, []byte{1})
			exec(migrator, "INSERT INTO codes(app_id,digest,flow_id,expires_at,used) VALUES('app','protected','protected',?,0)", young+3600)
			// Fail after child deletion to prove rollback does not inflate removed counts.
			if dialect == "postgres" {
				exec(migrator, "CREATE FUNCTION gate_failure() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RAISE EXCEPTION ''test failure''; END'")
				exec(migrator, "CREATE TRIGGER gate_failure BEFORE DELETE ON audit_events FOR EACH ROW EXECUTE FUNCTION gate_failure()")
			} else {
				exec(migrator, "CREATE TRIGGER gate_failure BEFORE DELETE ON audit_events FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='test failure'")
			}
			n, cleanupErr := runtime.Cleanup(ctx, 2, 1)
			if cleanupErr == nil || n != 0 {
				t.Fatalf("failed cleanup counted uncommitted rows: %d %v", n, cleanupErr)
			}
			var remaining int
			if e = migrator.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM access_tokens").Scan(&remaining); e != nil || remaining != 4 {
				t.Fatalf("cleanup rollback lost tokens: %d %v", remaining, e)
			}
			if dialect == "postgres" {
				exec(migrator, "DROP TRIGGER gate_failure ON audit_events")
				exec(migrator, "DROP FUNCTION gate_failure()")
			} else {
				exec(migrator, "DROP TRIGGER gate_failure")
			}
			for _, want := range []int64{26, 13, 0} {
				n, e := runtime.Cleanup(ctx, 2, 1)
				if e != nil || n != want {
					t.Fatalf("cleanup removed=%d want=%d err=%v", n, want, e)
				}
			}
			for table, want := range map[string]int{"users": 1, "clients": 1, "outbox": 1, "audit_events": 1, "sessions": 1, "flows": 1, "access_tokens": 1, "refresh_families": 1, "codes": 1, "platform_idempotency": 0} {
				var got int
				if e = migrator.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&got); e != nil || got != want {
					t.Fatalf("%s got=%d want=%d %v", table, got, want, e)
				}
			}
			// Re-lease fencing: a stale worker cannot mark a newer attempt complete.
			exec(migrator, "DELETE FROM outbox")
			e = migrator.transaction(ctx, "app", true, func(tx *tx) error {
				m := domain.Message{ID: "message", AppID: "app", Kind: "email", Body: "test", Created: young, Expires: young + 300}
				m.Available = old
				return tx.Enqueue(m)
			})
			if e != nil {
				t.Fatal(e)
			}
			first, e := runtime.LeaseMessages(ctx, ports.DeliveryLeaseRequest{Limit: 20, AppLimit: 2})
			if e != nil || len(first) != 1 {
				t.Fatalf("first lease %d %v", len(first), e)
			}
			again, e := runtime.LeaseMessages(ctx, ports.DeliveryLeaseRequest{Limit: 20, AppLimit: 2})
			if e != nil || len(again) != 0 {
				t.Fatalf("lease exposed twice %d %v", len(again), e)
			}
			exec(migrator, "UPDATE outbox SET available_at=?", old)
			exec(migrator, "UPDATE outbox_dispatch SET next_dispatch_at=?", old*1000000)
			second, e := runtime.LeaseMessages(ctx, ports.DeliveryLeaseRequest{Limit: 20, AppLimit: 2})
			if e != nil || len(second) != 1 {
				t.Fatalf("second lease %d %v", len(second), e)
			}
			if e = runtime.FinishMessage(ctx, "message", first[0].Attempts, 1, young); e != nil {
				t.Fatal(e)
			}
			var completed int
			if e = migrator.db.QueryRowContext(ctx, "SELECT completed FROM outbox").Scan(&completed); e != nil || completed != 0 {
				t.Fatal("stale completion changed newer lease", e)
			}
			if e = runtime.FinishMessage(ctx, "message", second[0].Attempts, 1, young); e != nil {
				t.Fatal(e)
			}
			if e = migrator.db.QueryRowContext(ctx, "SELECT completed FROM outbox").Scan(&completed); e != nil || completed != 1 {
				t.Fatal("current completion failed", e)
			}
			t.Log("v1 initialization/rerun, dirty/checksum/privilege/RLS rejection, bounded cleanup and lease fencing passed")
		})
	}
}
