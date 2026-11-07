package schemaplan

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"ariga.io/atlas/sql/schema"
	entmodel "github.com/carvebloom/authova/internal/ent/migrate"
)

// Protection snapshots objects the OSS table differ must not own. Only hashes
// leave the DB, not function bodies or potential credentials in definitions.
type Protection struct {
	Kind   string `json:"kind"`
	Count  int    `json:"count"`
	SHA256 string `json:"sha256"`
}

func protectedTables() map[string]bool {
	out := map[string]bool{"owner_locks": true}
	for _, t := range entmodel.Tables {
		if t.Name == "outbox" {
			continue
		}
		for _, c := range t.Columns {
			if c.Name == "app_id" {
				out[t.Name] = true
			}
		}
	}
	return out
}
func InspectProtection(ctx context.Context, tx *sql.Tx, dialect, name string) ([]Protection, error) {
	return inspectProtection(ctx, tx, dialect, name, false)
}

func inspectProtection(ctx context.Context, tx *sql.Tx, dialect, name string, reviewNewTables bool) ([]Protection, error) {
	type query struct{ kind, sql string }
	var queries []query
	var newTables []string
	if dialect == "postgres" {
		rows, err := tx.QueryContext(ctx, `SELECT c.relname,c.relrowsecurity,c.relforcerowsecurity FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relkind IN ('r','p')`, name)
		if err != nil {
			return nil, err
		}
		required := protectedTables()
		for rows.Next() {
			var table string
			var enabled, forced bool
			if err = rows.Scan(&table, &enabled, &forced); err != nil {
				rows.Close()
				return nil, err
			}
			if required[table] {
				if !enabled || !forced {
					rows.Close()
					return nil, fmt.Errorf("FORCE RLS missing on %s", table)
				}
				delete(required, table)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if len(required) > 0 {
			if !reviewNewTables {
				return nil, fmt.Errorf("required RLS tables missing")
			}
			for n := range required {
				newTables = append(newTables, n)
			}
			sort.Strings(newTables)
		}
		// Each App table must retain the exact restrictive app_scope policy shape.
		rows, err = tx.QueryContext(ctx, `SELECT c.relname,p.polname,p.polpermissive,p.polcmd,p.polroles::text,pg_get_expr(p.polqual,p.polrelid),pg_get_expr(p.polwithcheck,p.polrelid) FROM pg_policy p JOIN pg_class c ON c.oid=p.polrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1`, name)
		if err != nil {
			return nil, err
		}
		required = protectedTables()
		expected := protectedTables()
		for _, n := range newTables {
			delete(required, n)
			delete(expected, n)
		}
		for rows.Next() {
			var table, policy, cmd, roles string
			var permissive bool
			var using, check sql.NullString
			if err = rows.Scan(&table, &policy, &permissive, &cmd, &roles, &using, &check); err != nil {
				rows.Close()
				return nil, err
			}
			if expected[table] {
				if !required[table] || policy != "app_scope" || !permissive || cmd != "*" || roles != "{0}" || using.String != "((app_id)::text = current_setting('authova.app_id'::text, true))" || check.String != "((app_id)::text = current_setting('authova.app_id'::text, true))" {
					rows.Close()
					return nil, fmt.Errorf("unexpected RLS policy on %s; explicit security review required", table)
				}
				delete(required, table)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if len(required) > 0 {
			return nil, fmt.Errorf("required app_scope policies missing")
		}
		queries = []query{
			{"row-security", `SELECT json_build_array(c.relname,c.relrowsecurity,c.relforcerowsecurity,c.relacl::text)::text FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relkind IN ('r','p')`},
			{"policies", `SELECT json_build_array(c.relname,p.polname,p.polpermissive,p.polcmd,p.polroles::text,pg_get_expr(p.polqual,p.polrelid),pg_get_expr(p.polwithcheck,p.polrelid))::text FROM pg_policy p JOIN pg_class c ON c.oid=p.polrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1`},
			{"routines", `SELECT json_build_array(p.proname,pg_get_function_identity_arguments(p.oid),pg_get_functiondef(p.oid),p.proacl::text)::text FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND p.prokind IN ('f','p')`},
			{"triggers", `SELECT json_build_array(c.relname,t.tgname,pg_get_triggerdef(t.oid))::text FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND NOT t.tgisinternal`},
			{"views", `SELECT json_build_array(c.relname,pg_get_viewdef(c.oid),c.relacl::text)::text FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relkind IN ('v','m')`},
		}
	} else if dialect == "mysql" {
		if err := mysqlVisibility(ctx, tx, name); err != nil {
			return nil, err
		}
		queries = []query{
			{"triggers", `SELECT JSON_ARRAY(TRIGGER_NAME,EVENT_MANIPULATION,EVENT_OBJECT_TABLE,ACTION_STATEMENT,ACTION_TIMING,DEFINER) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA=?`},
			{"routines", `SELECT IF(ROUTINE_DEFINITION IS NULL,NULL,JSON_ARRAY(ROUTINE_NAME,ROUTINE_TYPE,ROUTINE_DEFINITION,SECURITY_TYPE,DEFINER)) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA=?`},
			{"views", `SELECT IF(VIEW_DEFINITION IS NULL,NULL,JSON_ARRAY(TABLE_NAME,VIEW_DEFINITION,CHECK_OPTION,SECURITY_TYPE,DEFINER)) FROM information_schema.VIEWS WHERE TABLE_SCHEMA=?`},
		}
	} else {
		return nil, fmt.Errorf("unsupported dialect")
	}
	out := []Protection{}
	for _, q := range queries {
		rows, err := tx.QueryContext(ctx, q.sql, name)
		if err != nil {
			return nil, err
		}
		values := []string{}
		for rows.Next() {
			var v sql.NullString
			if err = rows.Scan(&v); err != nil {
				rows.Close()
				return nil, err
			}
			if !v.Valid {
				rows.Close()
				return nil, fmt.Errorf("cannot inspect protected %s definitions", q.kind)
			}
			values = append(values, v.String)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		sort.Strings(values)
		hash := sha256.Sum256([]byte(strings.Join(values, "\n")))
		out = append(out, Protection{q.kind, len(values), hex.EncodeToString(hash[:])})
	}
	if len(newTables) > 0 {
		h := sha256.Sum256([]byte(strings.Join(newTables, "\n")))
		out = append(out, Protection{"new-app-tables-require-policy-review", len(newTables), hex.EncodeToString(h[:])})
	}
	return out, nil
}

func ValidateBoundForeignKeys(s *schema.Schema) error {
	for _, t := range s.Tables {
		for _, fk := range t.ForeignKeys {
			_, local := t.Column("app_id")
			_, remote := fk.RefTable.Column("app_id")
			if local && remote {
				bound := false
				for i, c := range fk.Columns {
					if c.Name == "app_id" && fk.RefColumns[i].Name == "app_id" {
						bound = true
					}
				}
				if !bound {
					return fmt.Errorf("foreign key %s on %s is not App-bound", fk.Symbol, t.Name)
				}
			}
		}
	}
	return nil
}

// MySQL filters information_schema by privilege. Absence is only meaningful
// when the planning account can see all routines, views and triggers in scope.
func mysqlVisibility(ctx context.Context, tx *sql.Tx, name string) error {
	rows, err := tx.QueryContext(ctx, "SHOW GRANTS FOR CURRENT_USER")
	if err != nil {
		return err
	}
	defer rows.Close()
	routine, view, trigger := false, false, false
	scope := " ON `" + strings.ReplaceAll(strings.ToUpper(name), "`", "``") + "`.* "
	for rows.Next() {
		var grant string
		if err = rows.Scan(&grant); err != nil {
			return err
		}
		g := strings.ToUpper(grant)
		global := strings.Contains(g, " ON *.* ")
		local := strings.Contains(g, scope)
		all := strings.Contains(g, "ALL PRIVILEGES")
		if global && (all || strings.Contains(g, "SHOW_ROUTINE")) {
			routine = true
		}
		if (global || local) && (all || strings.Contains(g, "SHOW VIEW")) {
			view = true
		}
		if (global || local) && (all || strings.Contains(g, "TRIGGER")) {
			trigger = true
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if !routine || !view || !trigger {
		return fmt.Errorf("complete MySQL protection inspection requires SHOW_ROUTINE globally and SHOW VIEW/TRIGGER in this schema (direct grants or owner); table-only planning remains available to restricted runtime users")
	}
	return nil
}
