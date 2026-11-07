package sqlstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/carvebloom/authova/internal/domain"
)

// Fault injection tests transaction ownership when a driver cannot confirm commit.
// Real server isolation and rollback behavior are tested separately on both databases.
type commitFaultDriver struct{}
type commitFaultConn struct{}
type commitFaultTx struct{}

var commitFaultCalls atomic.Int32
var errCommitTransport = errors.New("injected connection loss during commit")

func (commitFaultDriver) Open(string) (driver.Conn, error) { return commitFaultConn{}, nil }
func (commitFaultConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (commitFaultConn) Close() error              { return nil }
func (commitFaultConn) Begin() (driver.Tx, error) { return commitFaultTx{}, nil }
func (commitFaultConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return commitFaultTx{}, nil
}
func (commitFaultTx) Commit() error   { commitFaultCalls.Add(1); return errCommitTransport }
func (commitFaultTx) Rollback() error { return nil }
func init()                           { sql.Register("authova-commit-fault", commitFaultDriver{}) }

func TestUnknownWriteCommitNeverReplays(t *testing.T) {
	db, err := sql.Open("authova-commit-fault", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Store{db: db, dialect: "mysql"}
	for _, write := range []bool{true, false} {
		commitFaultCalls.Store(0)
		callbacks := 0
		err = store.transaction(context.Background(), "app", write, func(*tx) error { callbacks++; return nil })
		if !errors.Is(err, errCommitTransport) {
			t.Fatal("lost original commit cause", err)
		}
		if callbacks != 1 || commitFaultCalls.Load() != 1 {
			t.Fatal("transaction automatically replayed")
		}
		if write && !errors.Is(err, domain.ErrCommitUnknown) {
			t.Fatal("unknown write commit reported as retryable/success", err)
		}
		if !write && errors.Is(err, domain.ErrCommitUnknown) {
			t.Fatal("read commit incorrectly classified as unknown write")
		}
	}
}

func TestCallbackFailureNeverCommits(t *testing.T) {
	db, err := sql.Open("authova-commit-fault", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Store{db: db, dialect: "mysql"}
	commitFaultCalls.Store(0)
	rejected := errors.New("abort without commit")
	err = store.transaction(context.Background(), "app", true, func(*tx) error { return rejected })
	if !errors.Is(err, rejected) || commitFaultCalls.Load() != 0 {
		t.Fatal("failed callback committed", err)
	}
}
