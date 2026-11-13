package sqlstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	dbdriver "database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/carvebloom/authova/internal/diagnostic"
	migrationcatalog "github.com/carvebloom/authova/internal/migrations"
	"time"
)

var schemaVersion = migrationcatalog.LatestVersion()

// Migrate uses a dedicated deployment credential. Runtime startup never migrates.
func (s *Store) Migrate(ctx context.Context) (result error) {
	if err := migrationcatalog.Validate(s.dialect); err != nil {
		return err
	}
	c, e := s.db.Conn(ctx)
	if e != nil {
		return e
	}
	defer c.Close()
	if s.dialect == "postgres" {
		if _, e = c.ExecContext(ctx, "SELECT pg_advisory_lock(193813751)"); e != nil {
			return e
		}
		defer func() {
			if e := releaseMigrationLock(c, "SELECT pg_advisory_unlock(193813751)"); e != nil {
				result = diagnostic.Combine(result, e, result == nil)
				diagnostic.Log(ctx, "storage", "cleanup", result)
			}
		}()
	} else {
		var ok int
		if e = c.QueryRowContext(ctx, "SELECT GET_LOCK('authova_migrate',10)").Scan(&ok); e != nil || ok != 1 {
			return errors.New("cannot acquire migration lock")
		}
		defer func() {
			if e := releaseMigrationLock(c, "SELECT RELEASE_LOCK('authova_migrate')"); e != nil {
				result = diagnostic.Combine(result, e, result == nil)
				diagnostic.Log(ctx, "storage", "cleanup", result)
			}
		}()
	}
	if e = s.checkMigrationNamespace(ctx, c); e != nil {
		return e
	}
	if _, e = c.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS authova_schema (singleton INTEGER PRIMARY KEY, version INTEGER NOT NULL, dirty INTEGER NOT NULL, checksum VARCHAR(64) NOT NULL)"); e != nil {
		return e
	}
	if _, e = c.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS authova_migration_history (version INTEGER PRIMARY KEY, checksum VARCHAR(64) NOT NULL)"); e != nil {
		return e
	}
	var version, dirty int
	var checksum string
	e = c.QueryRowContext(ctx, "SELECT version,dirty,checksum FROM authova_schema WHERE singleton=1").Scan(&version, &dirty, &checksum)
	if e != nil {
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if _, e = c.ExecContext(ctx, "INSERT INTO authova_schema VALUES (1,0,0,'')"); e != nil {
			return e
		}
		version = 0
	}
	if dirty != 0 {
		return errors.New("dirty migration: reconcile or restore before retry")
	}
	if version > schemaVersion {
		return fmt.Errorf("database schema version %d is newer than this binary (%d); use a matching binary or a new empty database", version, schemaVersion)
	}
	executor, e := migrationcatalog.NewExecutor(ctx, c, s.dialect)
	if e != nil {
		return e
	}
	names, e := migrationcatalog.Names(s.dialect)
	if e != nil {
		return e
	}
	for index, name := range names {
		next := index + 1
		data, e := migrations.ReadFile("sql/" + s.dialect + "/" + name)
		if e != nil {
			return e
		}
		hash := sha256.Sum256(data)
		want := hex.EncodeToString(hash[:])
		if next <= version {
			var stored string
			err := c.QueryRowContext(ctx, s.bind("SELECT checksum FROM authova_migration_history WHERE version=?"), next).Scan(&stored)
			if errors.Is(err, sql.ErrNoRows) && next == version {
				stored = checksum
				if _, e = c.ExecContext(ctx, s.bind("INSERT INTO authova_migration_history(version,checksum) VALUES (?,?)"), next, stored); e != nil {
					return e
				}
			} else if err != nil {
				return err
			}
			if stored != want {
				return errors.New("migration checksum mismatch")
			}
			if e = executor.Baseline(ctx, name); e != nil {
				return e
			}
			continue
		}
		if _, e = c.ExecContext(ctx, "UPDATE authova_schema SET dirty=1 WHERE singleton=1"); e != nil {
			return e
		}
		if e = executor.Apply(ctx, name); e != nil {
			return fmt.Errorf("migration %d failed (dirty marker retained): %w", next, e)
		}
		if _, e = c.ExecContext(ctx, s.bind("INSERT INTO authova_migration_history(version,checksum) VALUES (?,?)"), next, want); e != nil {
			return e
		}
		if _, e = c.ExecContext(ctx, s.bind("UPDATE authova_schema SET version=?,dirty=0,checksum=? WHERE singleton=1"), next, want); e != nil {
			return e
		}
		version = next
	}
	return nil
}

func releaseMigrationLock(conn *sql.Conn, statement string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, e := conn.ExecContext(ctx, statement); e != nil {
		// sql.Conn.Close returns a healthy connection to the pool; force physical
		// disposal on unlock failure so an advisory lock cannot survive reuse.
		_ = conn.Raw(func(any) error { return dbdriver.ErrBadConn })
		return diagnostic.Wrap(e, "storage", "cleanup")
	}
	return nil
}

// The unpublished baseline is only initialized in an empty application schema.
// Existing migration histories must match their compiled checksums; no renumbering.
func (s *Store) checkMigrationNamespace(ctx context.Context, c *sql.Conn) error {
	condition := "table_schema=DATABASE()"
	if s.dialect == "postgres" {
		var ok bool
		if e := c.QueryRowContext(ctx, "SELECT current_schemas(false)::text='{public}'").Scan(&ok); e != nil {
			return e
		}
		if !ok {
			return errors.New("PostgreSQL migration search_path must resolve only public")
		}
		condition = "table_schema='public'"
	}
	var existing, total int
	if e := c.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE "+condition+" AND table_name='authova_schema'").Scan(&existing); e != nil {
		return e
	}
	if existing > 0 {
		return nil
	}
	if e := c.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE "+condition).Scan(&total); e != nil {
		return e
	}
	if total > 0 {
		return errors.New("initial migration requires an empty database schema; refusing to adopt unversioned tables")
	}
	return nil
}
