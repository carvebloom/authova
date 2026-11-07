package migrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"ariga.io/atlas/sql/migrate"
	"ariga.io/atlas/sql/mysql"
	"ariga.io/atlas/sql/postgres"
)

// Executor uses Atlas's versioned execution engine over the exact connection
// holding Authova's deployment lock. The caller retains the versioned dirty and
// checksum history as a compatibility fence, separate from generated planning artifacts.
// A dirty database is never automatically resumed, even though Atlas records
// per-statement progress for operator reconciliation.
type Executor struct {
	engine    *migrate.Executor
	revisions revisionStore
	dir       migrate.Dir
}

func NewExecutor(ctx context.Context, conn *sql.Conn, dialect string) (*Executor, error) {
	dir, err := fs.Sub(Files, "sql/"+dialect)
	if err != nil {
		return nil, err
	}
	catalog := embeddedDir{dir}
	if err = migrate.Validate(catalog); err != nil {
		return nil, err
	}
	if _, err = conn.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS authova_atlas_revisions (version VARCHAR(64) PRIMARY KEY, record TEXT NOT NULL)"); err != nil {
		return nil, err
	}
	var driver migrate.Driver
	switch dialect {
	case "postgres":
		driver, err = postgres.Open(conn)
	case "mysql":
		driver, err = mysql.Open(conn)
	default:
		return nil, fmt.Errorf("unsupported migration dialect")
	}
	if err != nil {
		return nil, err
	}
	rrw := revisionStore{conn: conn, dialect: dialect}
	engine, err := migrate.NewExecutor(driver, catalog, rrw)
	if err != nil {
		return nil, err
	}
	return &Executor{engine: engine, revisions: rrw, dir: catalog}, nil
}
func (e *Executor) Apply(ctx context.Context, name string) error {
	data, err := fs.ReadFile(e.dir, name)
	if err != nil {
		return err
	}
	file := migrate.NewLocalFile(name, data)
	if _, err := e.revisions.ReadRevision(ctx, file.Version()); !errors.Is(err, migrate.ErrRevisionNotExist) {
		if err != nil {
			return err
		}
		return fmt.Errorf("atlas revision already exists outside verified Authova history; reconcile before applying")
	}
	return e.engine.Execute(ctx, file)
}

// Baseline records only a file whose original Authova SHA-256 history has already
// been verified by the caller. It does not execute SQL or replace that history.
func (e *Executor) Baseline(ctx context.Context, name string) error {
	data, err := fs.ReadFile(e.dir, name)
	if err != nil {
		return err
	}
	file := migrate.NewLocalFile(name, data)
	statements, err := file.Stmts()
	if err != nil {
		return err
	}
	sums, err := e.dir.Checksum()
	if err != nil {
		return err
	}
	hash, err := sums.SumByName(name)
	if err != nil {
		return err
	}
	r, err := e.revisions.ReadRevision(ctx, file.Version())
	if err == nil {
		if r.Hash != hash || r.Total != len(statements) || r.Applied != r.Total || r.Error != "" {
			return fmt.Errorf("atlas revision differs from verified Authova history")
		}
		return nil
	}
	if !errors.Is(err, migrate.ErrRevisionNotExist) {
		return err
	}
	return e.revisions.WriteRevision(ctx, &migrate.Revision{Version: file.Version(), Description: file.Desc(), Type: migrate.RevisionTypeBaseline, Applied: len(statements), Total: len(statements), Hash: hash})
}

type revisionRecord struct {
	Revision      *revisionJSON
	Hash          string
	PartialHashes []string
}

// Atlas RevisionType marshals as text but has no inverse UnmarshalText. Store
// its stable numeric flags explicitly rather than losing the revision on rerun.
type revisionJSON struct {
	*migrate.Revision
	Type uint
}
type revisionStore struct {
	conn    *sql.Conn
	dialect string
}

func (r revisionStore) Ident() *migrate.TableIdent {
	return &migrate.TableIdent{Name: "authova_atlas_revisions"}
}
func (r revisionStore) bind(q string) string {
	if r.dialect == "postgres" {
		return q + "$1"
	}
	return q + "?"
}
func decodeRevision(data string) (*migrate.Revision, error) {
	var record revisionRecord
	if err := json.Unmarshal([]byte(data), &record); err != nil {
		return nil, err
	}
	if record.Revision == nil || record.Revision.Revision == nil {
		return nil, fmt.Errorf("invalid Atlas revision record")
	}
	record.Revision.Hash = record.Hash
	record.Revision.PartialHashes = record.PartialHashes
	record.Revision.Revision.Type = migrate.RevisionType(record.Revision.Type)
	return record.Revision.Revision, nil
}
func (r revisionStore) ReadRevisions(ctx context.Context) ([]*migrate.Revision, error) {
	rows, err := r.conn.QueryContext(ctx, "SELECT record FROM authova_atlas_revisions ORDER BY version")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*migrate.Revision{}
	for rows.Next() {
		var data string
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		v, err := decodeRevision(data)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (r revisionStore) ReadRevision(ctx context.Context, version string) (*migrate.Revision, error) {
	var data string
	err := r.conn.QueryRowContext(ctx, r.bind("SELECT record FROM authova_atlas_revisions WHERE version="), version).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, migrate.ErrRevisionNotExist
	}
	if err != nil {
		return nil, err
	}
	return decodeRevision(data)
}
func (r revisionStore) WriteRevision(ctx context.Context, v *migrate.Revision) error {
	data, err := json.Marshal(revisionRecord{&revisionJSON{v, uint(v.Type)}, v.Hash, v.PartialHashes})
	if err != nil {
		return err
	}
	q := "INSERT INTO authova_atlas_revisions(version,record) VALUES (?,?) ON DUPLICATE KEY UPDATE record=VALUES(record)"
	if r.dialect == "postgres" {
		q = "INSERT INTO authova_atlas_revisions(version,record) VALUES ($1,$2) ON CONFLICT(version) DO UPDATE SET record=EXCLUDED.record"
	}
	_, err = r.conn.ExecContext(ctx, q, v.Version, string(data))
	return err
}
func (r revisionStore) DeleteRevision(ctx context.Context, version string) error {
	_, err := r.conn.ExecContext(ctx, r.bind("DELETE FROM authova_atlas_revisions WHERE version="), version)
	return err
}
