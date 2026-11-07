package sqlstore

import (
	"entgo.io/ent/dialect/sql"
	"github.com/carvebloom/authova/internal/domain"
)

// conflictColumns keeps the original guarded-upsert semantics. MySQL can resolve
// a conflict using ANY unique index, unlike PostgreSQL's explicit target. Guard
// every assignment against the intended compound identity, then verify that
// identity with a typed query on the SAME transaction. These expressions contain
// schema constants only; caller values go through Ent's bound parameters.
func (t *tx) conflictColumns(keys, updates []string) []sql.ConflictOption {
	return []sql.ConflictOption{sql.ConflictColumns(keys...), sql.ResolveWith(func(u *sql.UpdateSet) {
		for _, column := range updates {
			if t.store.dialect != "mysql" {
				u.SetExcluded(column)
				continue
			}
			u.Set(column, sql.ExprFunc(func(b *sql.Builder) {
				b.WriteString("IF(")
				for i, key := range keys {
					if i > 0 {
						b.WriteString(" AND ")
					}
					b.Ident(key).WriteString("=VALUES(").Ident(key).WriteByte(')')
				}
				b.WriteString(",VALUES(").Ident(column).WriteString("),").Ident(column).WriteByte(')')
			}))
		}
	})}
}
func (t *tx) typedUpsert(exec func() error, exists func() (bool, error)) error {
	if !t.write {
		return domain.ErrForbidden
	}
	if err := exec(); err != nil {
		return classify(err, "upsert")
	}
	if t.store.dialect == "mysql" {
		ok, err := exists()
		if err != nil {
			return classify(err)
		}
		if !ok {
			return domain.ErrConflict
		}
	}
	return nil
}
func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func expectOne(n int, err error) error {
	if err != nil {
		return classify(err)
	}
	if n != 1 {
		return domain.ErrConflict
	}
	return nil
}
