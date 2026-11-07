package sqlstore

import (
	"context"
	"database/sql"
	"os"
	"reflect"
	"strings"
	"testing"

	"ariga.io/atlas/sql/migrate"
	"ariga.io/atlas/sql/mysql"
	"ariga.io/atlas/sql/postgres"
	"ariga.io/atlas/sql/schema"
	entmodel "github.com/carvebloom/authova/internal/ent/migrate"
)

// Ent's entity ID is an adapter-local convenience. Physical compound identity
// remains defined by reviewed versioned DDL. This gate compares all mapped
// columns and the actual Atlas-inspected PK/FK/index graph, using runtime users.
// It never calls Schema.Create or produces an executable Ent-only diff.
func TestEntPhysicalMapping(t *testing.T) {
	keys := map[string][]string{
		"apps": {"id"}, "operators": {"id"}, "management_sessions": {"digest"}, "outbox": {"id"},
		"clients": {"app_id", "id"}, "users": {"app_id", "id"}, "sessions": {"app_id", "id"}, "flows": {"app_id", "id"},
		"codes": {"app_id", "digest"}, "refresh_tokens": {"app_id", "digest"}, "refresh_families": {"app_id", "id"}, "access_tokens": {"app_id", "id"},
		"challenges": {"app_id", "id"}, "connections": {"app_id", "id"}, "signing_keys": {"app_id", "id"}, "audit_events": {"app_id", "id"},
		"identifiers": {"app_id", "kind", "digest"}, "admin_grants": {"app_id", "user_id"}, "oauth_consents": {"app_id", "user_id", "client_id"},
		"external_identities": {"app_id", "connection_id", "subject_digest"}, "idempotency": {"app_id", "idem_key"}, "platform_idempotency": {"operator_id", "idem_key"},
		"agents": {"app_id", "id"}, "agent_installations": {"app_id", "id"}, "agent_grants": {"app_id", "id"}, "agent_tokens": {"app_id", "token_id"},
		"passkeys": {"app_id", "id"}, "passkey_ceremonies": {"app_id", "id"},
	}
	fkCounts := map[string]int{"clients": 1, "users": 1, "sessions": 1, "flows": 1, "codes": 1, "refresh_tokens": 1, "refresh_families": 2, "access_tokens": 4, "challenges": 1, "connections": 1, "signing_keys": 1, "identifiers": 1, "admin_grants": 1, "oauth_consents": 2, "external_identities": 2, "idempotency": 1, "platform_idempotency": 1, "management_sessions": 1, "outbox": 1, "agents": 1, "agent_installations": 2, "agent_grants": 3, "agent_tokens": 4, "passkeys": 1, "passkey_ceremonies": 1}
	uniques := map[string][]string{"clients": {"app_id", "client_id"}, "sessions": {"app_id", "handle_digest"}, "refresh_tokens": {"app_id", "family_id", "generation"}, "agents": {"app_id", "client_id"}, "passkeys": {"app_id", "credential_hash"}}
	for _, dialect := range []string{"postgres", "mysql"} {
		t.Run(dialect, func(t *testing.T) {
			dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(dialect) + "_DSN")
			if dsn == "" {
				t.Skip("explicit runtime database fixture required")
			}
			ctx := context.Background()
			driver := "pgx"
			if dialect == "mysql" {
				driver = "mysql"
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			var inspector migrate.Driver
			name := "public"
			if dialect == "postgres" {
				inspector, err = postgres.Open(tx)
			} else {
				inspector, err = mysql.Open(tx)
				if err == nil {
					err = tx.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&name)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			physical, err := inspector.InspectSchema(ctx, name, &schema.InspectOptions{Mode: schema.InspectTables})
			if err != nil {
				t.Fatal(err)
			}
			if len(keys) != len(entmodel.Tables) {
				t.Fatal("mapped table identity inventory is incomplete")
			}
			for _, entity := range entmodel.Tables {
				table, ok := physical.Table(entity.Name)
				if !ok {
					t.Fatal("mapped table missing", entity.Name)
				}
				if table.PrimaryKey == nil || !reflect.DeepEqual(indexColumns(table.PrimaryKey), keys[entity.Name]) {
					t.Fatal("physical primary key changed", entity.Name)
				}
				if len(table.ForeignKeys) != fkCounts[entity.Name] {
					t.Fatal("physical foreign-key graph changed", entity.Name, len(table.ForeignKeys))
				}
				allowedExtra := 0
				if dialect == "mysql" && entity.Name == "signing_keys" {
					allowedExtra = 1
				}
				if len(table.Columns) != len(entity.Columns)+allowedExtra {
					t.Fatal("mapped column inventory changed", entity.Name)
				}
				for _, field := range entity.Columns {
					column, ok := table.Column(field.Name)
					if !ok || field.Nullable != column.Type.Null {
						t.Fatal("mapped column/nullability drift", entity.Name, field.Name)
					}
					if text, ok := column.Type.Type.(*schema.StringType); ok && field.Size > 0 && int64(text.Size) != field.Size {
						t.Fatal("mapped string length drift", entity.Name, field.Name, text.Size, field.Size)
					}
				}
				for _, fk := range table.ForeignKeys {
					_, localApp := table.Column("app_id")
					_, remoteApp := fk.RefTable.Column("app_id")
					if localApp && remoteApp {
						bound := false
						for i, c := range fk.Columns {
							if c.Name == "app_id" && fk.RefColumns[i].Name == "app_id" {
								bound = true
							}
						}
						if !bound {
							t.Fatal("foreign key permits cross-App reference", table.Name, fk.Symbol)
						}
					}
				}
				if cols, ok := uniques[entity.Name]; ok {
					found := false
					for _, index := range table.Indexes {
						if index.Unique && reflect.DeepEqual(indexColumns(index), cols) {
							found = true
						}
					}
					if !found {
						t.Fatal("physical compound uniqueness missing", table.Name, cols)
					}
				}
			}
			keysTable, _ := physical.Table("signing_keys")
			current, ok := keysTable.Index("signing_keys_current_unique")
			if !ok || !current.Unique {
				t.Fatal("current signing-key uniqueness missing")
			}
			if dialect == "postgres" {
				if !reflect.DeepEqual(indexColumns(current), []string{"app_id"}) {
					t.Fatal("current signing-key index columns")
				}
				predicate := false
				for _, attr := range current.Attrs {
					if p, ok := attr.(*postgres.IndexPredicate); ok && strings.Contains(p.P, "current_key") {
						predicate = true
					}
				}
				if !predicate {
					t.Fatal("current signing-key partial predicate missing")
				}
			} else {
				column, ok := keysTable.Column("current_app_id")
				if !ok || !reflect.DeepEqual(indexColumns(current), []string{"current_app_id"}) {
					t.Fatal("current signing-key generated index missing")
				}
				generated := false
				for _, attr := range column.Attrs {
					if expr, ok := attr.(*schema.GeneratedExpr); ok && strings.Contains(expr.Expr, "current_key") && strings.Contains(expr.Expr, "app_id") {
						generated = true
					}
				}
				if !generated {
					t.Fatal("current signing-key generated expression missing")
				}
			}
			t.Log("Atlas OSS inspected all 28 Ent mappings, compound identities, App-bound foreign keys and special uniqueness under a read-only restricted-runtime transaction")
		})
	}
}
func indexColumns(index *schema.Index) []string {
	cols := make([]string, len(index.Parts))
	for i, part := range index.Parts {
		if part.C != nil {
			cols[i] = part.C.Name
		}
	}
	return cols
}
