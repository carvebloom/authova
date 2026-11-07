package sqlstore

import (
	"context"
	"database/sql/driver"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/observability"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/resource"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"go.opentelemetry.io/otel/attribute"
)

var _ ports.IntrospectionReader = (*Store)(nil)
var postgresIntrospectionQuery = (&Store{dialect: "postgres"}).bind(introspectionQuery)

// ReadIntrospection keeps authority reads on the primary and releases the DB
// connection before JWT verification and HTTP encoding. MySQL uses one MVCC
// statement in autocommit mode. PostgreSQL pipelines a read-only transaction,
// its transaction-local forced-RLS scope, the same statement and COMMIT. No
// result is usable until the entire batch has drained successfully.
func (s *Store) ReadIntrospection(ctx context.Context, scope domain.Scope, client, id string) (requester domain.Client, token domain.TokenState, live bool, err error) {
	if scope.ID() == "" {
		return requester, token, false, domain.ErrForbidden
	}
	if s.Admission != nil {
		release, e := s.Admission.Acquire(ctx, scope.ID(), resource.Database)
		if e != nil {
			return requester, token, false, e
		}
		defer release()
	}
	started := time.Now()
	ctx, span := observability.Start(ctx, "db.introspection", attribute.String("db.system.name", s.dialect))
	defer func() {
		observability.End(span, err)
		if s.ObserveTransaction != nil {
			s.ObserveTransaction(false, time.Since(started), err)
		}
	}()
	now := time.Now().Unix()
	args := []any{now, now, now, id, scope.ID(), client}
	decoder := &tx{store: s, ctx: ctx, app: scope.ID()}
	if s.dialect == "mysql" {
		// Open pins autocommit=1 on every physical connection. All joins repeat
		// the App key; there is no multi-statement read or retained snapshot.
		return decoder.scanIntrospection(s.db.QueryRowContext(ctx, introspectionQuery, args...))
	}
	conn, e := s.db.Conn(ctx)
	if e != nil {
		return requester, token, false, classify(e, "introspection")
	}
	defer conn.Close()
	var readErr error
	err = conn.Raw(func(raw any) error {
		c, ok := raw.(*stdlib.Conn)
		if !ok {
			return domain.ErrUnavailable
		}
		pg := c.Conn()
		if pg.PgConn().TxStatus() != 'I' {
			return driver.ErrBadConn
		}
		batch := &pgx.Batch{}
		batch.Queue("BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY")
		batch.Queue("SELECT set_config('authova.app_id',$1,true)", scope.ID())
		batch.Queue(postgresIntrospectionQuery, args...)
		batch.Queue("COMMIT")
		results := pg.SendBatch(ctx, batch)
		_, beginErr := results.Exec()
		_, scopeErr := results.Exec()
		requester, token, live, readErr = decoder.scanIntrospection(results.QueryRow())
		_, commitErr := results.Exec()
		closeErr := results.Close()
		// SQL/cancellation errors can skip the queued COMMIT. Discard that
		// connection rather than return any transaction or App scope to the
		// pool. Raw's ErrBadConn path handles the database/sql pool lifetime.
		if pg.IsClosed() || pg.PgConn().TxStatus() != 'I' || closeErr != nil {
			return driver.ErrBadConn
		}
		for _, failure := range []error{beginErr, scopeErr, commitErr} {
			if failure != nil {
				return classify(failure, "introspection")
			}
		}
		return readErr
	})
	if err != nil {
		return domain.Client{}, domain.TokenState{}, false, classify(err, "introspection")
	}
	return requester, token, live, nil
}
