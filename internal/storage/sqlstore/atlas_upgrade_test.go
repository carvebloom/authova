package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"ariga.io/atlas/sql/migrate"
	"ariga.io/atlas/sql/mysql"
	"ariga.io/atlas/sql/postgres"
	"ariga.io/atlas/sql/schema"
	entschema "entgo.io/ent/dialect/sql/schema"
	"entgo.io/ent/schema/field"
	entmodel "github.com/carvebloom/authova/internal/ent/migrate"
	"github.com/carvebloom/authova/internal/schemaplan"
)

func TestAtlasProposedUpgrade(t *testing.T) {
	if !strings.HasPrefix(os.Getenv("AUTHOVA_TEST_DISPOSABLE_DATABASE"), "authova_quality_") {
		t.Skip("owned disposable fixture required for DDL rehearsal")
	}
	for _, dialect := range []string{"postgres", "mysql"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			dsn := os.Getenv("AUTHOVA_TEST_MIGRATOR_" + strings.ToUpper(dialect) + "_DSN")
			if dsn == "" {
				t.Skip("explicit migrator fixture required")
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
			db.SetMaxOpenConns(2)
			exec := func(q string) {
				t.Helper()
				if _, e := db.ExecContext(ctx, q); e != nil {
					t.Fatal(e)
				}
			}
			exec("CREATE TABLE engineering_unowned (id INTEGER PRIMARY KEY)")
			defer db.ExecContext(ctx, "DROP TABLE engineering_unowned")
			exec("CREATE VIEW engineering_view AS SELECT id FROM engineering_unowned")
			defer db.ExecContext(ctx, "DROP VIEW engineering_view")
			if dialect == "postgres" {
				exec("CREATE FUNCTION engineering_function() RETURNS integer LANGUAGE SQL IMMUTABLE AS 'SELECT 7'")
				defer db.ExecContext(ctx, "DROP FUNCTION engineering_function()")
			} else {
				exec("CREATE TRIGGER engineering_trigger BEFORE INSERT ON engineering_unowned FOR EACH ROW SET NEW.id = NEW.id")
				defer db.ExecContext(ctx, "DROP TRIGGER engineering_trigger")
			}
			name := "public"
			if dialect == "mysql" {
				if e = db.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&name); e != nil {
					t.Fatal(e)
				}
			}
			read := func() *sql.Tx {
				t.Helper()
				tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
				if e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { tx.Rollback() })
				return tx
			}
			tx := read()
			report, e := schemaplan.BuildReport(ctx, tx, dialect, name, false)
			if e != nil {
				t.Fatal(e)
			}
			if len(report.Statements) != 0 {
				t.Fatal("unowned objects caused DDL", report.Statements)
			}
			encoded, _ := json.Marshal(report)
			if e = schemaplan.Verify(ctx, tx, dialect, name, encoded); e != nil {
				t.Fatal(e)
			}
			before, e := schemaplan.InspectProtection(ctx, tx, dialect, name)
			if e != nil {
				t.Fatal(e)
			}
			tx.Rollback()
			if dialect == "postgres" {
				exec("CREATE OR REPLACE FUNCTION engineering_function() RETURNS integer LANGUAGE SQL IMMUTABLE AS 'SELECT 8'")
			} else {
				exec("ALTER VIEW engineering_view AS SELECT id+1 AS id FROM engineering_unowned")
			}
			tx = read()
			e = schemaplan.Verify(ctx, tx, dialect, name, encoded)
			tx.Rollback()
			if e == nil {
				t.Fatal("changed protected definition accepted")
			}
			if dialect == "postgres" {
				exec("CREATE OR REPLACE FUNCTION engineering_function() RETURNS integer LANGUAGE SQL IMMUTABLE AS 'SELECT 7'")
			} else {
				exec("ALTER VIEW engineering_view AS SELECT id FROM engineering_unowned")
			}
			proposed, e := entschema.CopyTables(entmodel.Tables)
			if e != nil {
				t.Fatal(e)
			}
			for _, table := range proposed {
				if table.Name == "apps" {
					column := &entschema.Column{Name: "engineering_probe", Type: field.TypeString, Size: 64, Nullable: true, SchemaType: map[string]string{"postgres": "character varying(64)", "mysql": "varchar(64)"}}
					table.Columns = append(table.Columns, column)
					table.Indexes = append(table.Indexes, &entschema.Index{Name: "engineering_probe_lookup", Columns: []*entschema.Column{column}})
				}
			}
			planFor := func() *migrate.Plan {
				t.Helper()
				tx := read()
				defer tx.Rollback()
				atlas, e := schemaplan.Open(tx, dialect)
				if e != nil {
					t.Fatal(e)
				}
				current, e := atlas.InspectSchema(ctx, name, &schema.InspectOptions{Mode: schema.InspectTables})
				if e != nil {
					t.Fatal(e)
				}
				target, e := schemaplan.DesiredFromTables(ctx, tx, dialect, name, proposed)
				if e != nil {
					t.Fatal(e)
				}
				plan, e := schemaplan.Diff(ctx, atlas, current, target, false)
				if e != nil {
					t.Fatal(e)
				}
				return plan
			}
			plan := planFor()
			if len(plan.Changes) < 1 || !strings.Contains(plan.Changes[0].Cmd, "engineering_probe") {
				t.Fatalf("unexpected additive plan: %#v", plan)
			}
			// Execute the generated versioned file with the same pinned Atlas OSS engine;
			// a no-op revision writer is used only in this disposable DDL rehearsal.
			directory, e := migrate.NewLocalDir(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			statements := []string{}
			for _, change := range plan.Changes {
				statements = append(statements, change.Cmd)
			}
			file := migrate.NewLocalFile("002_engineering_probe.sql", []byte(strings.Join(statements, ";\n")+";\n"))
			if e = directory.WriteFile(file.Name(), file.Bytes()); e != nil {
				t.Fatal(e)
			}
			sum, e := directory.Checksum()
			if e != nil {
				t.Fatal(e)
			}
			if e = migrate.WriteSumFile(directory, sum); e != nil {
				t.Fatal(e)
			}
			conn, e := db.Conn(ctx)
			if e != nil {
				t.Fatal(e)
			}
			var atlas migrate.Driver
			// Atlas's driver accepts the deployment connection directly, outside read-only planning.
			atlas, e = engineeringAtlasDriver(conn, dialect)
			if e != nil {
				t.Fatal(e)
			}
			engine, e := migrate.NewExecutor(atlas, directory, migrate.NopRevisionReadWriter{})
			if e != nil {
				t.Fatal(e)
			}
			if e = engine.Execute(ctx, file); e != nil {
				t.Fatal(e)
			}
			conn.Close()
			defer db.ExecContext(ctx, "ALTER TABLE apps DROP COLUMN engineering_probe")
			if next := planFor(); len(next.Changes) != 0 {
				t.Fatal("generated upgrade did not converge")
			}
			tx = read()
			after, e := schemaplan.InspectProtection(ctx, tx, dialect, name)
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("upgrade changed protected objects")
			}
			if e = schemaplan.Verify(ctx, tx, dialect, name, encoded); e == nil {
				t.Fatal("stale plan accepted after schema change")
			}
			tx.Rollback()
			if dialect == "postgres" {
				// A missing/new App table is refused normally. Explicit review
				// can emit table DDL, but records the required policy companion.
				exec("DROP TABLE owner_locks")
				tx = read()
				_, e = schemaplan.BuildReport(ctx, tx, dialect, name, false)
				tx.Rollback()
				if e == nil {
					t.Fatal("missing RLS table accepted without explicit review")
				}
				tx = read()
				repair, err := schemaplan.BuildReport(ctx, tx, dialect, name, true)
				tx.Rollback()
				if err != nil {
					t.Fatal(err)
				}
				flagged := false
				for _, p := range repair.Protected {
					if p.Kind == "new-app-tables-require-policy-review" && p.Count == 1 {
						flagged = true
					}
				}
				if !flagged {
					t.Fatal("new App table did not require policy review")
				}
				// repair also proposes removing our not-yet-promoted test field;
				// exercise only the explicitly reviewed missing table candidate.
				created := false
				for _, stmt := range repair.Statements {
					if strings.HasPrefix(stmt, "CREATE TABLE \"public\".\"owner_locks\"") {
						exec(stmt)
						created = true
					}
				}
				if !created {
					t.Fatal("missing table candidate not generated")
				}
				exec("ALTER TABLE owner_locks ENABLE ROW LEVEL SECURITY")
				exec("ALTER TABLE owner_locks FORCE ROW LEVEL SECURITY")
				exec("CREATE POLICY app_scope ON owner_locks USING(app_id=current_setting('authova.app_id',true)) WITH CHECK(app_id=current_setting('authova.app_id',true))")
				tx = read()
				_, e = schemaplan.InspectProtection(ctx, tx, dialect, name)
				tx.Rollback()
				if e != nil {
					t.Fatal(e)
				}
			}
			if dialect == "postgres" {
				exec("ALTER TABLE users DISABLE ROW LEVEL SECURITY")
				tx = read()
				_, e = schemaplan.InspectProtection(ctx, tx, dialect, name)
				tx.Rollback()
				exec("ALTER TABLE users ENABLE ROW LEVEL SECURITY")
				if e == nil {
					t.Fatal("disabled RLS accepted")
				}
				exec("CREATE POLICY engineering_bypass ON users USING (true)")
				tx = read()
				_, e = schemaplan.InspectProtection(ctx, tx, dialect, name)
				tx.Rollback()
				exec("DROP POLICY engineering_bypass ON users")
				if e == nil {
					t.Fatal("additional permissive policy accepted")
				}
			}
		})
	}
}

func engineeringAtlasDriver(conn *sql.Conn, dialect string) (migrate.Driver, error) {
	if dialect == "postgres" {
		return postgres.Open(conn)
	}
	return mysql.Open(conn)
}
