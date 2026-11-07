package integration

import (
	"os"
	"regexp"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
)

var ownedRecoveryName = regexp.MustCompile(`^authova_(quality|recovery)_[a-f0-9]{12}(_restored)?$`)

func isOwnedRecoveryDatabase(driver, dsn, owned string) bool {
	if !ownedRecoveryName.MatchString(owned) {
		return false
	}
	switch driver {
	case "postgres":
		config, e := pgx.ParseConfig(dsn)
		return e == nil && config.Database == owned
	case "mysql":
		config, e := mysql.ParseDSN(dsn)
		return e == nil && config.DBName == owned
	}
	return false
}

// Global recovery fixtures may invalidate all identities, so merely providing
// a general test DSN is insufficient. The owning runner must attest the exact
// newly created random database name, and it must match the parsed DSN.
func requireOwnedRecoveryDatabase(t *testing.T, driver, dsn, variable string) {
	t.Helper()
	owned := os.Getenv(variable)
	if owned == "" {
		t.Skip("offline recovery requires an owning disposable-database runner")
	}
	if !isOwnedRecoveryDatabase(driver, dsn, owned) {
		t.Fatal("refusing global recovery outside the runner's disposable database")
	}
}

func TestRecoveryDatabaseOwnershipBoundary(t *testing.T) {
	name := "authova_quality_123456abcdef"
	for _, tc := range []struct {
		driver, dsn, owner string
		ok                 bool
	}{
		{"postgres", "postgres://fixture:fixture@localhost/" + name, name, true},
		{"mysql", "fixture:fixture@tcp(localhost:3306)/" + name, name, true},
		{"postgres", "postgres://fixture:fixture@localhost/authova_demo", name, false},
		{"mysql", "fixture:fixture@tcp(localhost:3306)/authova_demo", name, false},
		{"postgres", "postgres://fixture:fixture@localhost/authova_demo", "authova_demo", false},
		{"postgres", "postgres://fixture:fixture@localhost/" + name, "", false},
	} {
		if got := isOwnedRecoveryDatabase(tc.driver, tc.dsn, tc.owner); got != tc.ok {
			t.Fatalf("ownership guard %s got=%v want=%v", tc.driver, got, tc.ok)
		}
	}
}
