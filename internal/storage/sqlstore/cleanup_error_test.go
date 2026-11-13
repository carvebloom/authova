package sqlstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"github.com/carvebloom/authova/internal/diagnostic"
	"sync/atomic"
	"testing"
)

var unlockSequence atomic.Uint64
var errLockCleanup = errors.New("CANARY driver unlock failure")

type unlockDriver struct{ closed *atomic.Bool }

func (d unlockDriver) Open(string) (driver.Conn, error) { return &unlockConn{closed: d.closed}, nil }

type unlockConn struct {
	driver.Conn
	closed *atomic.Bool
}

func (c *unlockConn) Close() error { c.closed.Store(true); return nil }
func (c *unlockConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return nil, errLockCleanup
}
func TestFailedUnlockDiscardsPhysicalConnectionAndKeepsCommittedFact(t *testing.T) {
	closed := &atomic.Bool{}
	name := fmt.Sprintf("authova_unlock_fixture_%d", unlockSequence.Add(1))
	sql.Register(name, unlockDriver{closed})
	db, e := sql.Open(name, "")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	conn, e := db.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	cleanup := releaseMigrationLock(conn, "fixed SQL unlock")
	if !errors.Is(cleanup, errLockCleanup) || !closed.Load() {
		t.Fatal("unlock failure lost or physical connection reused")
	}
	e = diagnostic.Combine(nil, cleanup, true)
	if diagnostic.Inspect(e).Kind != "committed_cleanup" || !errors.Is(e, errLockCleanup) {
		t.Fatal("committed outcome lost")
	}
}
