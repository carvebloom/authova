package integration

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/storage/sqlstore"
	mysql "github.com/go-sql-driver/mysql"
)

func TestPrimaryIntrospectionSnapshot(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		code, verifier := f.code()
		status, issued := f.exchange(code, verifier)
		if status != 200 {
			t.Fatal("initial issuance failed", status)
		}
		id := requireActive(t, f, issued.Access, true)["jti"].(string)
		ctx := context.Background()
		check := func(expected bool) {
			t.Helper()
			client, token, active, err := f.store.ReadIntrospection(ctx, f.scope1, f.client.ClientID, id)
			if err != nil || active != expected || client.AppID != f.app1.ID || token.ID != id || token.UserID != f.user1.ID {
				t.Fatal("primary authority snapshot mismatch", active, err)
			}
			if err := f.store.App(ctx, f.scope1, false, func(tx ports.AppTx) error {
				otherClient, otherToken, otherActive, err := tx.IntrospectionState(f.client.ClientID, id)
				if err != nil {
					return err
				}
				if !reflect.DeepEqual(client, otherClient) || !reflect.DeepEqual(token, otherToken) || active != otherActive {
					return errors.New("native snapshot and scoped transaction disagree")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			requireActive(t, f, issued.Access, expected)
		}
		check(true)
		if err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
			app, err := tx.App()
			if err != nil {
				return err
			}
			app.RequireMFA, app.Version = true, app.Version+1
			return tx.UpdateAppConfig(app)
		}); err != nil {
			t.Fatal(err)
		}
		check(false)
		_, token, _, _ := f.store.ReadIntrospection(ctx, f.scope1, f.client.ClientID, id)
		if err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
			s, err := tx.Session(token.SessionID, true)
			if err != nil {
				return err
			}
			// Synthetic persisted proof, without extending any credential TTL.
			s.MFATime = time.Now().Unix()
			return tx.PutSession(s)
		}); err != nil {
			t.Fatal(err)
		}
		check(true)
		if err := f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
			s, err := tx.Session(token.SessionID, true)
			if err != nil {
				return err
			}
			s.Status = "revoked"
			return tx.PutSession(s)
		}); err != nil {
			t.Fatal(err)
		}
		check(false)
	})
}

type legacyIntrospectionStore struct{ ports.Store }
type legacyIntrospectionTx struct{ ports.AppTx }

func (s legacyIntrospectionStore) App(ctx context.Context, scope domain.Scope, write bool, fn func(ports.AppTx) error) error {
	return s.Store.App(ctx, scope, write, func(tx ports.AppTx) error { return fn(legacyIntrospectionTx{tx}) })
}

func (t legacyIntrospectionTx) IntrospectionState(client, id string) (domain.Client, domain.TokenState, bool, error) {
	c, state, active, err := t.AppTx.IntrospectionState(client, id)
	// Models an alternative adapter that returns the required-MFA flag but
	// relies on the protocol to load its real session proof, as before.
	state.RequiresMFA = true
	return c, state, active, err
}

func TestTransactionIntrospectionFallbackRetainsMFA(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		code, verifier := f.code()
		status, issued := f.exchange(code, verifier)
		if status != 200 {
			t.Fatal(status)
		}
		requireActive(t, f, issued.Access, true)
		f.service.Store = legacyIntrospectionStore{f.store}
		requireActive(t, f, issued.Access, false)
	})
}

func TestIntrospectionReadsOnlyCommittedAuthority(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		code, verifier := f.code()
		status, issued := f.exchange(code, verifier)
		if status != 200 {
			t.Fatal(status)
		}
		id := requireActive(t, f, issued.Access, true)["jti"].(string)
		check := func(expected bool) {
			t.Helper()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, _, active, err := f.store.ReadIntrospection(ctx, f.scope1, f.client.ClientID, id); err != nil || active != expected {
				t.Fatal("snapshot used dirty or retained authority", active, err)
			}
		}
		writeWhileReading := func(status string, expected, commit bool) {
			t.Helper()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			entered, finish := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(finish) }) }
			defer unblock()
			result := make(chan error, 1)
			go func() {
				result <- f.store.App(ctx, f.scope1, true, func(tx ports.AppTx) error {
					u, err := tx.User(f.user1.ID, true)
					if err != nil {
						return err
					}
					u.Status = status
					if err := tx.PutUser(u); err != nil {
						return err
					}
					close(entered)
					select {
					case <-finish:
						if !commit {
							return domain.ErrConflict
						}
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
			}()
			select {
			case <-entered:
			case err := <-result:
				t.Fatal("authority write fixture failed", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			check(expected)
			unblock()
			err := <-result
			if (commit && err != nil) || (!commit && !errors.Is(err, domain.ErrConflict)) {
				t.Fatal("authority commit/rollback failed", err)
			}
		}
		writeWhileReading("disabled", true, true)
		check(false) // A new read must see the committed disable.
		writeWhileReading("active", false, false)
		check(false) // An uncommitted activation must never restore access.
	})
}

func TestIntrospectionCancellationDuringDatabaseRead(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		dialect := f.store.Dialect()
		dsn := os.Getenv("AUTHOVA_TEST_MIGRATOR_" + strings.ToUpper(dialect) + "_DSN")
		if dsn == "" {
			t.Skip("requires the owned acceptance database's migration credential")
		}
		code, verifier := f.code()
		status, issued := f.exchange(code, verifier)
		if status != 200 {
			t.Fatal(status)
		}
		id := requireActive(t, f, issued.Access, true)["jti"].(string)
		driver := dialect
		if driver == "postgres" {
			driver = "pgx"
		}
		admin, err := sql.Open(driver, dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		conn, err := admin.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		var unlock func()
		if dialect == "postgres" {
			tx, err := conn.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			unlock = func() { _ = tx.Rollback() }
			defer unlock()
			if _, err = tx.ExecContext(context.Background(), "LOCK TABLE access_tokens IN ACCESS EXCLUSIVE MODE"); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err = conn.ExecContext(context.Background(), "LOCK TABLES access_tokens WRITE"); err != nil {
				t.Fatal(err)
			}
			unlock = func() { _, _ = conn.ExecContext(context.Background(), "UNLOCK TABLES") }
			defer unlock()
		}
		reader, err := sqlstore.Open(context.Background(), dialect, os.Getenv("AUTHOVA_TEST_"+strings.ToUpper(dialect)+"_DSN"), f.server.URL, f.service.Vault, 1)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		type outcome struct {
			active bool
			err    error
		}
		result := make(chan outcome, 1)
		go func() {
			_, _, active, err := reader.ReadIntrospection(ctx, f.scope1, f.client.ClientID, id)
			result <- outcome{active, err}
		}()
		query := "SELECT COUNT(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT c.payload,c.status,x.id,%'"
		if dialect == "mysql" {
			query = "SELECT COUNT(*) FROM performance_schema.processlist WHERE DB=DATABASE() AND USER='authova_runtime' AND STATE LIKE 'Waiting for table%'"
		}
		blocked := false
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			var count int
			if err := admin.QueryRowContext(context.Background(), query).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count > 0 {
				blocked = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !blocked {
			t.Fatal("did not observe the real authority read waiting on the owned table lock")
		}
		cancel()
		select {
		case out := <-result:
			if out.err == nil || out.active {
				t.Fatal("cancelled read succeeded")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("cancelled authority read did not drain")
		}
		unlock()
		if _, _, active, err := reader.ReadIntrospection(context.Background(), f.scope1, f.client.ClientID, id); err != nil || !active || reader.Stats().InUse != 0 {
			t.Fatal("in-flight cancellation leaked or poisoned a DB connection", active, err, reader.Stats())
		}
	})
}

func TestIntrospectionPooledAppScopeAndCancellation(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		code, verifier := f.code()
		status, issued := f.exchange(code, verifier)
		if status != 200 {
			t.Fatal(status)
		}
		id := requireActive(t, f, issued.Access, true)["jti"].(string)
		other := f.client
		other.ID, other.AppID, other.Name = application.ID(), f.app2.ID, "same external client identifier in another App"
		if err := f.store.App(context.Background(), f.scope2, true, func(tx ports.AppTx) error { return tx.PutClient(other) }); err != nil {
			t.Fatal(err)
		}
		driver := f.store.Dialect()
		if driver == "mysql" {
			cfg, err := mysql.ParseDSN(os.Getenv("AUTHOVA_TEST_MYSQL_DSN"))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Params == nil {
				cfg.Params = map[string]string{}
			}
			cfg.Params["autocommit"] = "0"
			unsafe, err := sqlstore.Open(context.Background(), driver, cfg.FormatDSN(), f.server.URL, f.service.Vault, 1)
			if unsafe != nil {
				unsafe.Close()
			}
			if err == nil {
				t.Fatal("retained MySQL transactions were permitted for authority snapshots")
			}
			delete(cfg.Params, "autocommit")
			cfg.Params["transaction_isolation"] = "'READ-UNCOMMITTED'"
			unsafe, err = sqlstore.Open(context.Background(), driver, cfg.FormatDSN(), f.server.URL, f.service.Vault, 1)
			if unsafe != nil {
				unsafe.Close()
			}
			if err == nil {
				t.Fatal("dirty MySQL reads were permitted for authority snapshots")
			}
		}
		reader, err := sqlstore.Open(context.Background(), driver, os.Getenv("AUTHOVA_TEST_"+strings.ToUpper(driver)+"_DSN"), f.server.URL, f.service.Vault, 1)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		var group sync.WaitGroup
		for worker := 0; worker < 8; worker++ {
			group.Add(1)
			go func() {
				defer group.Done()
				for n := 0; n < 10; n++ {
					for _, scope := range []domain.Scope{f.scope1, f.scope2} {
						client, token, active, err := reader.ReadIntrospection(context.Background(), scope, f.client.ClientID, id)
						if err != nil || client.AppID != scope.ID() || active != (scope.ID() == f.app1.ID) || (scope.ID() == f.app2.ID && token.ID != "") {
							t.Error("pooled connection crossed App scope", active, err)
							return
						}
					}
				}
			}()
		}
		group.Wait()
		if _, _, _, err := reader.ReadIntrospection(context.Background(), f.scope1, "missing-client", id); !errors.Is(err, domain.ErrNotFound) {
			t.Fatal("missing client lost its error classification", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, active, err := reader.ReadIntrospection(ctx, f.scope1, f.client.ClientID, id); err == nil || active {
			t.Fatal("cancelled authority read accepted")
		}
		if _, _, active, err := reader.ReadIntrospection(context.Background(), f.scope1, f.client.ClientID, id); err != nil || !active || reader.Stats().InUse != 0 {
			t.Fatal("failed read poisoned the pooled snapshot", active, err, reader.Stats())
		}
	})
}
