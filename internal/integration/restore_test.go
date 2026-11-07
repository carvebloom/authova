package integration

import (
	"context"
	"crypto/x509"
	"os"
	"strings"
	"testing"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/secure"
	"github.com/carvebloom/authova/internal/storage/sqlstore"
)

func TestRestoredEncryptedFacts(t *testing.T) {
	tested := false
	for _, driver := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_RESTORE_" + strings.ToUpper(driver) + "_DSN")
		if dsn == "" {
			continue
		}
		tested = true
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			master, e := os.ReadFile(os.Getenv("AUTHOVA_MASTER_KEY_FILE"))
			if e != nil {
				t.Fatal(e)
			}
			vault, e := secure.NewVault(master)
			if e != nil {
				t.Fatal(e)
			}
			store, e := sqlstore.Open(ctx, driver, dsn, "http://127.0.0.1:18080", vault, 4)
			if e != nil {
				t.Fatal(e)
			}
			defer store.Close()
			if e = store.CheckRuntime(ctx); e != nil {
				t.Fatal(e)
			}
			var apps []domain.App
			e = store.Platform(ctx, false, func(tx ports.PlatformTx) error { var err error; apps, err = tx.Apps("", 100); return err })
			if e != nil || len(apps) == 0 {
				t.Fatal("restored Apps unavailable", e)
			}
			users, keys := 0, 0
			for _, app := range apps {
				scope, _ := domain.AppScope(app.ID)
				e = store.App(ctx, scope, false, func(tx ports.AppTx) error {
					records, e := tx.Users("", 100)
					if e != nil {
						return e
					}
					for _, user := range records {
						if user.ID == "" || user.AppID != app.ID {
							t.Fatal("restored user scope corrupt")
						}
						users++
					}
					signers, e := tx.Keys()
					if e != nil {
						return e
					}
					for _, key := range signers {
						if _, e = x509.ParsePKCS1PrivateKey(key.Private); e != nil {
							t.Fatal("restored encrypted signing key corrupt")
						}
						keys++
					}
					return nil
				})
				if e != nil {
					t.Fatal(e)
				}
			}
			if users == 0 || keys == 0 {
				t.Fatal("restore did not preserve user/key facts")
			}
		})
	}
	if !tested {
		t.Skip("set AUTHOVA_RESTORE_POSTGRES_DSN and AUTHOVA_RESTORE_MYSQL_DSN to verify a real restored backup")
	}
}
