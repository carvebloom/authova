package sqlstore

import (
	"context"
	"database/sql"
	"github.com/carvebloom/authova/internal/schemaplan"
	"os"
	"strings"
	"testing"
)

func TestAtlasPlanning(t *testing.T) {
	for _, dialect := range []string{"postgres", "mysql"} {
		t.Run(dialect, func(t *testing.T) {
			dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(dialect) + "_DSN")
			if dsn == "" {
				t.Skip("explicit restricted fixture required")
			}
			driver := dialect
			if driver == "postgres" {
				driver = "pgx"
			}
			db, e := sql.Open(driver, dsn)
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			tx, e := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			name := "public"
			if dialect == "mysql" {
				if e = tx.QueryRow("SELECT DATABASE()").Scan(&name); e != nil {
					t.Fatal(e)
				}
			}
			plan, e := schemaplan.Plan(context.Background(), tx, dialect, name, true)
			if e != nil {
				t.Fatal(e)
			}
			for _, c := range plan.Changes {
				t.Log(c.Cmd)
			}
			if dialect == "mysql" {
				if _, e := schemaplan.InspectProtection(context.Background(), tx, dialect, name); e == nil {
					t.Fatal("DML-only account was reported as complete metadata visibility")
				}
			}
			if len(plan.Changes) != 0 {
				t.Fatalf("v1 physical mapping drift: %d statements", len(plan.Changes))
			}
		})
	}
}
