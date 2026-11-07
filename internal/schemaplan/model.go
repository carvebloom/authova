// Package schemaplan builds review-only Atlas OSS plans from Ent fields and an
// explicit physical extension. It never applies DDL or mutates a source database.
package schemaplan

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"ariga.io/atlas/sql/migrate"
	"ariga.io/atlas/sql/mysql"
	"ariga.io/atlas/sql/postgres"
	"ariga.io/atlas/sql/schema"
	entsql "entgo.io/ent/dialect/sql"
	entschema "entgo.io/ent/dialect/sql/schema"
	entmodel "github.com/carvebloom/authova/internal/ent/migrate"
)

//go:embed model/*.json
var models embed.FS

type columnSpec struct {
	Type               string
	Null               bool
	Default, Generated string
}
type indexSpec struct {
	EntIndex   string `json:"ent_index"`
	Name       string
	Unique     bool
	Columns    []string
	Descending []bool
	Predicate  string
}
type foreignKeySpec struct {
	Name       string
	Columns    []string
	Table      string
	References []string
	OnDelete   string `json:"on_delete"`
	OnUpdate   string `json:"on_update"`
}
type tableSpec struct {
	Columns     map[string]columnSpec
	PrimaryKey  []string `json:"primary_key"`
	Indexes     []indexSpec
	ForeignKeys []foreignKeySpec `json:"foreign_keys"`
	Checks      map[string]string
}

func Open(tx *sql.Tx, dialect string) (migrate.Driver, error) {
	switch dialect {
	case "postgres":
		return postgres.Open(tx)
	case "mysql":
		return mysql.Open(tx)
	default:
		return nil, fmt.Errorf("unsupported dialect %q", dialect)
	}
}

// Desired uses fresh Ent table copies; runtime generated metadata is never changed.
// Column types/nullability/defaults come only from Ent. The extension supplies
// compound identities, named constraints/indexes and SQL-only/generated columns.
func Desired(ctx context.Context, tx *sql.Tx, dialect, name string) (*schema.Schema, error) {
	return DesiredFromTables(ctx, tx, dialect, name, entmodel.Tables)
}

// DesiredFromTables supports validating a proposed generated Ent graph before
// promoting its reviewed SQL. The supplied graph is cloned before conversion.
func DesiredFromTables(ctx context.Context, tx *sql.Tx, dialect, name string, entityTables []*entschema.Table) (*schema.Schema, error) {
	data, err := models.ReadFile("model/" + dialect + ".json")
	if err != nil {
		return nil, err
	}
	specs := map[string]tableSpec{}
	if err = json.Unmarshal(data, &specs); err != nil {
		return nil, err
	}
	tables, err := entschema.CopyTables(entityTables)
	if err != nil {
		return nil, err
	}
	a, err := entschema.NewMigrate(entsql.NewDriver(dialect, entsql.Conn{ExecQuerier: tx}))
	if err != nil {
		return nil, err
	}
	realm, err := a.StateReader(tables...)(ctx)
	if err != nil {
		return nil, err
	}
	if len(realm.Schemas) != 1 {
		return nil, fmt.Errorf("expected one Ent schema")
	}
	s := realm.Schemas[0]
	s.Name = name
	for _, t := range s.Tables {
		// Ent wraps expression defaults in parentheses. Literal string defaults
		// remain literals for MySQL's differ (not computed default expressions).
		if dialect == "mysql" {
			for _, c := range t.Columns {
				if x, ok := c.Default.(*schema.RawExpr); ok {
					v := strings.TrimSpace(x.X)
					if strings.HasPrefix(v, "(") && strings.HasSuffix(v, ")") {
						v = v[1 : len(v)-1]
					}
					if _, err := strconv.Unquote(v); err == nil {
						c.Default = &schema.Literal{V: v}
					}
				}
			}
		}
	}
	for _, t := range s.Tables {
		if _, ok := specs[t.Name]; !ok {
			return nil, fmt.Errorf("missing physical extension for Ent table %s", t.Name)
		}
	}
	names := make([]string, 0, len(specs))
	for n := range specs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		spec := specs[n]
		t, exists := s.Table(n)
		if !exists {
			if n != "owner_locks" && n != "outbox_dispatch" {
				return nil, fmt.Errorf("orphan extension table %s", n)
			}
			t = schema.NewTable(n)
			s.AddTables(t)
		}
		columnNames := make([]string, 0, len(spec.Columns))
		for cn := range spec.Columns {
			columnNames = append(columnNames, cn)
		}
		sort.Strings(columnNames)
		for _, cn := range columnNames {
			c := spec.Columns[cn]
			if exists && c.Generated == "" {
				return nil, fmt.Errorf("ordinary column must be defined by Ent: %s.%s", n, cn)
			}
			var typ schema.Type
			if dialect == "postgres" {
				typ, err = postgres.ParseType(c.Type)
			} else {
				typ, err = mysql.ParseType(c.Type)
			}
			if err != nil {
				return nil, err
			}
			col := schema.NewColumn(cn).SetType(typ).SetNull(c.Null)
			if c.Default != "" {
				col.Default = &schema.RawExpr{X: c.Default}
			}
			if c.Generated != "" {
				col.Attrs = append(col.Attrs, &schema.GeneratedExpr{Expr: c.Generated, Type: "STORED"})
			}
			t.AddColumns(col)
		}
		// The entity ID is an ORM convenience; replace only the planning graph.
		t.PrimaryKey = nil
		entIndexes := t.Indexes
		t.Indexes = nil
		t.ForeignKeys = nil
		t.Attrs = nil
		if dialect == "mysql" {
			t.Attrs = []schema.Attr{&schema.Charset{V: "utf8mb4"}, &schema.Collation{V: "utf8mb4_bin"}, &mysql.Engine{V: "InnoDB"}}
		}
		pk := schema.NewPrimaryKey()
		for _, cn := range spec.PrimaryKey {
			c, ok := t.Column(cn)
			if !ok {
				return nil, fmt.Errorf("missing primary column %s.%s", n, cn)
			}
			pk.AddColumns(c)
		}
		t.SetPrimaryKey(pk)
		aliases := map[string]string{}
		for _, is := range spec.Indexes {
			if is.EntIndex != "" {
				aliases[is.EntIndex] = is.Name
			}
		}
		for _, idx := range entIndexes {
			if alias, ok := aliases[idx.Name]; ok {
				delete(aliases, idx.Name)
				idx.Name = alias
			}
			t.AddIndexes(idx)
		}
		if len(aliases) > 0 {
			return nil, fmt.Errorf("stale Ent index aliases on %s: %v", n, aliases)
		}
		for _, is := range spec.Indexes {
			if is.EntIndex != "" {
				continue
			}
			idx := schema.NewIndex(is.Name).SetUnique(is.Unique)
			for i, cn := range is.Columns {
				c, ok := t.Column(cn)
				if !ok {
					return nil, fmt.Errorf("missing index column %s.%s", n, cn)
				}
				part := schema.NewIndexPart().SetColumn(c)
				if i < len(is.Descending) {
					part.SetDesc(is.Descending[i])
				}
				idx.AddParts(part)
			}
			if is.Predicate != "" {
				idx.Attrs = append(idx.Attrs, &postgres.IndexPredicate{P: is.Predicate})
			}
			t.AddIndexes(idx)
		}
		checkNames := make([]string, 0, len(spec.Checks))
		for k := range spec.Checks {
			checkNames = append(checkNames, k)
		}
		sort.Strings(checkNames)
		for _, k := range checkNames {
			t.Attrs = append(t.Attrs, &schema.Check{Name: k, Expr: spec.Checks[k]})
		}
	}
	for _, n := range names {
		t, _ := s.Table(n)
		for _, fs := range specs[n].ForeignKeys {
			ref, ok := s.Table(fs.Table)
			if !ok {
				return nil, fmt.Errorf("missing reference table %s", fs.Table)
			}
			fk := schema.NewForeignKey(fs.Name).SetRefTable(ref).SetOnDelete(schema.ReferenceOption(fs.OnDelete)).SetOnUpdate(schema.ReferenceOption(fs.OnUpdate))
			for i, cn := range fs.Columns {
				c, ok := t.Column(cn)
				if !ok {
					return nil, fmt.Errorf("missing FK column %s.%s", n, cn)
				}
				rc, ok := ref.Column(fs.References[i])
				if !ok {
					return nil, fmt.Errorf("missing FK reference %s", fs.References[i])
				}
				fk.AddColumns(c)
				fk.AddRefColumns(rc)
			}
			t.AddForeignKeys(fk)
		}
	}
	if err := ValidateBoundForeignKeys(s); err != nil {
		return nil, err
	}
	return s, nil
}
