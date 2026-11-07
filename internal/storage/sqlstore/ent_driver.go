package sqlstore

import (
	"context"
	"errors"
	"github.com/carvebloom/authova/internal/domain"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/carvebloom/authova/internal/ent"
)

// boundDriver has no pool: all generated Ent queries and native SQL share the
// caller's exact transaction, connection, isolation level and PG app setting.
// Nested transactions, close, commit and rollback remain owned by transaction().
type boundDriver struct {
	dialect string
	conn    entsql.Conn
}

func (d *boundDriver) Dialect() string { return d.dialect }
func (d *boundDriver) Exec(ctx context.Context, q string, args, v any) error {
	return d.conn.Exec(ctx, q, args, v)
}
func (d *boundDriver) Query(ctx context.Context, q string, args, v any) error {
	return d.conn.Query(ctx, q, args, v)
}
func (*boundDriver) Tx(context.Context) (dialect.Tx, error) {
	return nil, errors.New("nested repository transactions forbidden")
}
func (*boundDriver) Close() error { return errors.New("repository transaction is caller-owned") }
func (t *tx) entities() *ent.Client {
	client := ent.NewClient(ent.Driver(&boundDriver{dialect: t.store.dialect, conn: entsql.Conn{ExecQuerier: t.sql}}))
	client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if !t.write {
				return nil, domain.ErrForbidden
			}
			return next.Mutate(ctx, m)
		})
	})
	return client
}

func appDomain(row *ent.App) (domain.App, error) {
	a := domain.App{ID: row.ID, Key: row.AppKey, Slug: row.Slug, Name: row.Name, Status: row.Status, Version: row.ConfigVersion, CreatedAt: time.Unix(row.CreatedAt, 0).UTC(), RequireMFA: row.RequireMfa == 1, Issuer: row.Issuer, KeyEpoch: row.KeyEpoch}
	err := decodeAppConfiguration(&a, row.AuthenticationPolicy, row.ResourceLimits)
	return a, err
}
