package sqlstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/carvebloom/authova/internal/diagnostic"
	"github.com/carvebloom/authova/internal/domain"
)

// Exercise Store.transaction and database/sql's actual commit boundary rather
// than placing the outcome sentinel directly in a synthetic diagnostic tree.
type failingCommitConnector struct {
	cause   error
	commits atomic.Int32
}

func (c *failingCommitConnector) Connect(context.Context) (driver.Conn, error) {
	return &failingCommitConn{connector: c}, nil
}
func (c *failingCommitConnector) Driver() driver.Driver { return failingCommitDriver{c} }

type failingCommitDriver struct{ connector *failingCommitConnector }

func (d failingCommitDriver) Open(string) (driver.Conn, error) {
	return d.connector.Connect(context.Background())
}

type failingCommitConn struct{ connector *failingCommitConnector }

func (*failingCommitConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("fixture does not execute statements")
}
func (*failingCommitConn) Close() error { return nil }
func (c *failingCommitConn) Begin() (driver.Tx, error) {
	return &failingCommitTx{c.connector}, nil
}
func (c *failingCommitConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}

type failingCommitTx struct{ connector *failingCommitConnector }

func (t *failingCommitTx) Commit() error {
	t.connector.commits.Add(1)
	return t.connector.cause
}
func (*failingCommitTx) Rollback() error { return nil }

func TestCommitUnknownDiagnosticThroughTransaction(t *testing.T) {
	for _, reason := range []error{context.Canceled, context.DeadlineExceeded, errors.Join(context.Canceled, context.DeadlineExceeded)} {
		connector := &failingCommitConnector{cause: fmt.Errorf("CANARY driver commit: %w", reason)}
		db := sql.OpenDB(connector)
		s := &Store{db: db, dialect: "mysql"}
		calls := 0
		err := s.transaction(context.Background(), "fixture-app", true, func(*tx) error {
			calls++
			return nil
		})
		if e := db.Close(); e != nil {
			t.Fatal("fixture close failed")
		}
		var storage *Error
		if !errors.As(err, &storage) || !errors.Is(err, reason) || !errors.Is(err, domain.ErrCommitUnknown) {
			t.Fatal("transaction lost the storage error, context cause or outcome fact")
		}
		info := diagnostic.Inspect(err)
		if info.Kind != "outcome_unknown" || info.Operation != "storage" || info.Phase != "commit" || len(info.Frames) == 0 || !diagnostic.Has(err, domain.ErrCommitUnknown) {
			t.Fatal("safe diagnostic lost commit precedence or first contact")
		}
		if calls != 1 || connector.commits.Load() != 1 {
			t.Fatal("uncertain write was replayed")
		}
	}
}
