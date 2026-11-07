package integration

import (
	"bytes"
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/gob"
	"encoding/json"
	"os"
	"testing"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
	"github.com/carvebloom/authova/internal/storage/sqlstore"
)

func TestCapacityRestore(t *testing.T) {
	planPath := os.Getenv("AUTHOVA_CAPACITY_RESTORE_PLAN")
	if planPath == "" {
		t.Skip("requires actual 12M restored databases and private plan")
	}
	data, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan []struct{ Driver, Source, Target, Fixture string }
	if json.Unmarshal(data, &plan) != nil || len(plan) != 2 {
		t.Fatal("two completed backend restores required")
	}
	master, err := os.ReadFile(os.Getenv("AUTHOVA_MASTER_KEY_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	vault, _ := secure.NewVault(master)
	ctx := context.Background()
	for _, item := range plan {
		t.Run(item.Driver, func(t *testing.T) {
			var fixture struct {
				App    domain.App `json:"app"`
				Users  []string   `json:"sample_user_ids"`
				Actual int64      `json:"actual_users"`
			}
			data, err := os.ReadFile(item.Fixture)
			if err != nil || json.Unmarshal(data, &fixture) != nil {
				t.Fatal("private capacity fixture unavailable")
			}
			scope, err := domain.AppScope(fixture.App.ID)
			if err != nil {
				t.Fatal(err)
			}
			source, err := sqlstore.Open(ctx, item.Driver, item.Source, fixture.App.Issuer, vault, 4)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			restored, err := sqlstore.Open(ctx, item.Driver, item.Target, fixture.App.Issuer, vault, 4)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			if err = restored.CheckRuntime(ctx); err != nil {
				t.Fatal(err)
			}
			count, err := restored.UserCount(ctx, scope)
			if err != nil || count != fixture.Actual || count < 12000000 {
				t.Fatal("full user cardinality not restored", count, err)
			}
			for _, dsn := range []string{item.Source, item.Target} {
				if identifiers := restoredIdentifierCount(t, item.Driver, dsn, scope); identifiers != count {
					t.Fatal("source/restored identifier cardinality mismatch", identifiers, count)
				}
			}
			for _, id := range fixture.Users {
				var original, copy domain.User
				if err = source.App(ctx, scope, false, func(tx ports.AppTx) error {
					var e error
					original, e = tx.User(id, false)
					if e != nil {
						return e
					}
					found, e := tx.FindUser("email", vault.Digest(scope.ID()+":identifier:email", original.Email))
					if e == nil && found.ID != id {
						return domain.ErrUnavailable
					}
					return e
				}); err != nil {
					t.Fatal(err)
				}
				if err = restored.App(ctx, scope, false, func(tx ports.AppTx) error {
					var e error
					copy, e = tx.User(id, false)
					if e != nil {
						return e
					}
					found, e := tx.FindUser("email", vault.Digest(scope.ID()+":identifier:email", copy.Email))
					if e == nil && found.ID != id {
						return domain.ErrUnavailable
					}
					return e
				}); err != nil {
					t.Fatal(err)
				}
				var a, b bytes.Buffer
				if gob.NewEncoder(&a).Encode(original) != nil || gob.NewEncoder(&b).Encode(copy) != nil || !bytes.Equal(a.Bytes(), b.Bytes()) {
					t.Fatal("decrypted user facts changed during restore")
				}
			}
			var keys []domain.SigningKey
			if err = restored.App(ctx, scope, false, func(tx ports.AppTx) error { var e error; keys, e = tx.Keys(); return e }); err != nil || len(keys) == 0 {
				t.Fatal("signing keys not restored", err)
			}
			for _, key := range keys {
				if _, err = x509.ParsePKCS1PrivateKey(key.Private); err != nil {
					t.Fatal("restored private key cannot sign")
				}
			}
			t.Logf("restored users=%d identifiers=%d; exact decrypted samples and identifier bindings=%d; parsed private keys=%d", count, count, len(fixture.Users), len(keys))
		})
	}
}

// Exercise the runtime role and the same explicit App scope, without adding a
// production repository method solely for an offline cardinality assertion.
func restoredIdentifierCount(t *testing.T, driver, dsn string, scope domain.Scope) int64 {
	t.Helper()
	name := driver
	if driver == "postgres" {
		name = "pgx"
	}
	db, err := sql.Open(name, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	query := "SELECT COUNT(*) FROM identifiers WHERE app_id=?"
	if driver == "postgres" {
		if _, err := tx.ExecContext(context.Background(), "SELECT set_config('authova.app_id',$1,true)", scope.ID()); err != nil {
			t.Fatal(err)
		}
		query = "SELECT COUNT(*) FROM identifiers WHERE app_id=$1"
	}
	var count int64
	if err := tx.QueryRowContext(context.Background(), query, scope.ID()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return count
}
