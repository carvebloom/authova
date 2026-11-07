package schemaplan

import (
	"context"
	"database/sql"
	"fmt"

	"ariga.io/atlas/sql/migrate"
	"ariga.io/atlas/sql/postgres"
	"ariga.io/atlas/sql/schema"
)

// Plan is read-only even when called with a migration-owner credential. Unknown
// tables and metadata are outside this planner's ownership and are preserved.
// Destructive changes require explicit review; no plan is automatically applied.
func Plan(ctx context.Context, tx *sql.Tx, dialect, name string, allowDestructive bool) (*migrate.Plan, error) {
	driver, err := Open(tx, dialect)
	if err != nil {
		return nil, err
	}
	current, err := driver.InspectSchema(ctx, name, &schema.InspectOptions{Mode: schema.InspectTables})
	if err != nil {
		return nil, err
	}
	desired, err := Desired(ctx, tx, dialect, name)
	if err != nil {
		return nil, err
	}
	return Diff(ctx, driver, current, desired, allowDestructive)
}

func Diff(ctx context.Context, driver migrate.Driver, current, desired *schema.Schema, allowDestructive bool) (*migrate.Plan, error) {
	// Database-level charset/collation are owned by deployment, not Ent.
	desired.Attrs = current.Attrs
	// Preserve externally owned objects explicitly by identity, rather than
	// filtering SQL strings after planning. Their columns/indexes/FKs stay intact.
	for _, t := range current.Tables {
		if _, ok := desired.Table(t.Name); !ok {
			desired.Tables = append(desired.Tables, t)
		}
	}
	changes, err := driver.SchemaDiff(current, desired)
	if err != nil {
		return nil, err
	}
	if !allowDestructive {
		if err = reviewChanges(changes); err != nil {
			return nil, err
		}
	}
	qualifier := ""
	if _, pg := driver.(*postgres.Driver); pg {
		qualifier = current.Name
	}
	return driver.PlanChanges(ctx, "ent_schema", changes, func(o *migrate.PlanOptions) { o.SchemaQualifier = &qualifier; o.Mode = migrate.PlanModeDeferred })
}
func reviewChanges(changes []schema.Change) error {
	for _, c := range changes {
		switch v := c.(type) {
		case *schema.AddTable:
			return fmt.Errorf("new table %s requires explicit security/ownership review", v.T.Name)
		case *schema.ModifyTable:
			if err := reviewChanges(v.Changes); err != nil {
				return err
			}
		case *schema.AddColumn:
			if !v.C.Type.Null && v.C.Default == nil {
				return fmt.Errorf("non-null column %s requires backfill review", v.C.Name)
			}
		case *schema.AddIndex, *schema.AddForeignKey, *schema.AddCheck: // Reviewed non-destructive additions.
		default:
			return fmt.Errorf("change %T requires explicit destructive/backfill review", c)
		}
	}
	return nil
}
