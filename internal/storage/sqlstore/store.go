// Package sqlstore implements the domain repository contract for PostgreSQL and MySQL.
// The SQL helpers are private; application code never supplies a table or predicate.
package sqlstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/carvebloom/authova/internal/diagnostic"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ent/app"
	migrationcatalog "github.com/carvebloom/authova/internal/migrations"
	"github.com/carvebloom/authova/internal/observability"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/resource"
	"github.com/carvebloom/authova/internal/secure"
	mysql "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"go.opentelemetry.io/otel/attribute"
)

var migrations = migrationcatalog.Files

type Store struct {
	ObserveTransaction func(bool, time.Duration, error)
	Admission          *resource.Manager
	db                 *sql.DB
	dialect, baseURL   string
	vault              *secure.Vault
	clientCacheMu      sync.Mutex
	clientCache        map[string]parsedClient
	clientCacheBytes   int
}

func Open(ctx context.Context, dialect, dsn, baseURL string, vault *secure.Vault, maxConnections int) (*Store, error) {
	if dialect != "postgres" && dialect != "mysql" {
		return nil, fmt.Errorf("unsupported database: %s", dialect)
	}
	driver := "pgx"
	if dialect == "mysql" {
		driver = "mysql"
		cfg, err := mysql.ParseDSN(dsn)
		if err != nil {
			return nil, errors.New("invalid MySQL connection configuration")
		}
		if cfg.Params == nil {
			cfg.Params = map[string]string{}
		}
		if v, ok := cfg.Params["autocommit"]; ok && v != "1" && v != "true" {
			return nil, errors.New("MySQL autocommit must be enabled")
		}
		if v, ok := cfg.Params["transaction_isolation"]; ok && v != "'REPEATABLE-READ'" {
			return nil, errors.New("MySQL session isolation must be REPEATABLE-READ")
		}
		if _, ok := cfg.Params["tx_isolation"]; ok {
			return nil, errors.New("legacy MySQL tx_isolation overrides are not supported")
		}
		// Single-statement authority snapshots must never inherit a retained
		// transaction from a server configured with autocommit disabled.
		cfg.Params["autocommit"] = "1"
		cfg.Params["transaction_isolation"] = "'REPEATABLE-READ'"
		dsn = cfg.FormatDSN()
	}
	db, e := sql.Open(driver, dsn)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(maxConnections)
	db.SetMaxIdleConns(maxConnections)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
	s := &Store{db: db, dialect: dialect, baseURL: strings.TrimRight(baseURL, "/"), vault: vault}
	connect, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if e = db.PingContext(connect); e != nil {
		db.Close()
		return nil, e
	}
	return s, nil
}
func (s *Store) Close() error                     { return s.db.Close() }
func (s *Store) Health(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *Store) Stats() ports.PoolStats {
	stats := s.db.Stats()
	return ports.PoolStats{MaxOpen: stats.MaxOpenConnections, Open: stats.OpenConnections, InUse: stats.InUse, Idle: stats.Idle, WaitCount: stats.WaitCount, WaitSeconds: stats.WaitDuration.Seconds()}
}
func (s *Store) Dialect() string { return s.dialect }
func (s *Store) CheckRuntime(ctx context.Context) error {
	if s.dialect == "postgres" {
		var namespaceOK bool
		if e := s.db.QueryRowContext(ctx, "SELECT current_schemas(false)::text='{public}'").Scan(&namespaceOK); e != nil {
			return e
		}
		if !namespaceOK {
			return errors.New("PostgreSQL runtime search_path must resolve only the public application schema")
		}
		var privileged bool
		e := s.db.QueryRowContext(ctx, "SELECT rolsuper OR rolbypassrls OR rolcreaterole OR rolcreatedb OR rolreplication OR has_schema_privilege(current_user,'public','CREATE') OR EXISTS(SELECT 1 FROM pg_tables WHERE schemaname='public' AND tableowner=current_user) OR EXISTS(SELECT 1 FROM pg_auth_members WHERE member=pg_roles.oid) FROM pg_roles WHERE rolname=current_user").Scan(&privileged)
		if e != nil {
			return e
		}
		if privileged {
			return errors.New("PostgreSQL runtime must not be superuser, BYPASSRLS or table owner")
		}
	}
	if s.dialect == "postgres" {
		var protected int
		e := s.db.QueryRowContext(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname='public' AND c.relname IN ('clients','users','identifiers','admin_grants','owner_locks','sessions','flows','codes','refresh_families','refresh_tokens','access_tokens','challenges','connections','external_identities','signing_keys','audit_events','idempotency','oauth_consents','passkeys','passkey_ceremonies','agents','agent_installations','agent_grants','agent_tokens')
		AND c.relrowsecurity AND c.relforcerowsecurity AND
		(SELECT count(*) FROM pg_policy p WHERE p.polrelid=c.oid)=1 AND
		EXISTS(SELECT 1 FROM pg_policy p WHERE p.polrelid=c.oid AND p.polname='app_scope'
		AND p.polcmd='*' AND p.polpermissive AND p.polroles=ARRAY[0]::oid[]
		AND pg_get_expr(p.polqual,p.polrelid)='((app_id)::text = current_setting(''authova.app_id''::text, true))'
		AND pg_get_expr(p.polwithcheck,p.polrelid)='((app_id)::text = current_setting(''authova.app_id''::text, true))')`).Scan(&protected)
		if e != nil {
			return e
		}
		if protected != 24 {
			return errors.New("PostgreSQL runtime requires forced RLS on all App tables")
		}
	}
	if s.dialect == "mysql" {
		var role string
		if e := s.db.QueryRowContext(ctx, "SELECT CURRENT_ROLE()").Scan(&role); e != nil {
			return e
		}
		if role != "NONE" {
			return errors.New("MySQL runtime requires explicit DML grants without active roles")
		}
		rows, e := s.db.QueryContext(ctx, "SHOW GRANTS FOR CURRENT_USER")
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var grant string
			if e = rows.Scan(&grant); e != nil {
				return e
			}
			if !safeMySQLGrant(grant) {
				return errors.New("MySQL runtime requires only explicit scoped SELECT, INSERT, UPDATE, DELETE grants")
			}
		}
		if e = rows.Err(); e != nil {
			return e
		}
	}

	var version int
	var dirty int
	if e := s.db.QueryRowContext(ctx, "SELECT version,dirty FROM authova_schema WHERE singleton=1").Scan(&version, &dirty); e != nil {
		return e
	}
	if version != schemaVersion || dirty != 0 {
		return fmt.Errorf("database schema requires a clean version %d migration", schemaVersion)
	}
	names, e := migrationcatalog.Names(s.dialect)
	if e != nil {
		return e
	}
	for index, name := range names {
		data, e := migrations.ReadFile("sql/" + s.dialect + "/" + name)
		if e != nil {
			return e
		}
		hash := sha256.Sum256(data)
		var recorded string
		if e = s.db.QueryRowContext(ctx, s.bind("SELECT checksum FROM authova_migration_history WHERE version=?"), index+1).Scan(&recorded); e != nil {
			return e
		}
		if recorded != hex.EncodeToString(hash[:]) {
			return errors.New("runtime migration checksum mismatch")
		}
	}
	return nil
}

func safeMySQLGrant(grant string) bool {
	upper := strings.ToUpper(grant)
	end := strings.Index(upper, " ON ")
	if end < 0 || !strings.HasPrefix(upper, "GRANT ") || strings.Contains(upper, "WITH GRANT OPTION") {
		return false
	}
	for _, privilege := range strings.Split(upper[len("GRANT "):end], ",") {
		privilege = strings.TrimSpace(privilege)
		if privilege != "USAGE" && privilege != "SELECT" && privilege != "INSERT" && privilege != "UPDATE" && privilege != "DELETE" {
			return false
		}
		if privilege != "USAGE" && strings.HasPrefix(upper[end:], " ON *.* ") {
			return false
		}
	}
	return true
}
func (s *Store) bind(q string) string {
	if s.dialect != "postgres" {
		return q
	}
	var b strings.Builder
	n := 0
	for _, c := range q {
		if c == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}
func (s *Store) transaction(ctx context.Context, app string, write bool, fn func(*tx) error) (err error) {
	started := time.Now()
	ctx, span := observability.Start(ctx, "db.transaction", attribute.String("db.system.name", s.dialect), attribute.Bool("db.transaction.write", write))
	defer func() {
		if err != nil && ctx.Err() != nil && !diagnostic.Has(err, ctx.Err()) {
			err = errors.Join(err, ctx.Err())
		}
		observability.End(span, err)
		if s.ObserveTransaction != nil {
			s.ObserveTransaction(write, time.Since(started), err)
		}
	}()
	level := sql.LevelRepeatableRead
	if write {
		level = sql.LevelReadCommitted
	}
	t, e := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: level, ReadOnly: !write})
	if e != nil {
		return classify(e, "begin")
	}
	defer func() {
		if e := t.Rollback(); e != nil && !errors.Is(e, sql.ErrTxDone) {
			err = diagnostic.Combine(err, e, false)
			diagnostic.Log(ctx, "storage", "rollback", err)
		}
	}()
	if s.dialect == "postgres" {
		if _, e = t.ExecContext(ctx, "SELECT set_config('authova.app_id',$1,true)", app); e != nil {
			return classify(e)
		}
	}
	u := &tx{store: s, sql: t, ctx: ctx, app: app, write: write}
	if e = fn(u); e != nil {
		return e
	}
	if e = t.Commit(); e != nil {
		if !write {
			return classify(e)
		}
		return storageError(e, "commit", domain.ErrCommitUnknown)
	}
	return nil
}
func (s *Store) App(ctx context.Context, scope domain.Scope, write bool, fn func(ports.AppTx) error) error {
	if scope.ID() == "" {
		return domain.ErrForbidden
	}
	if s.Admission != nil {
		release, err := s.Admission.Acquire(ctx, scope.ID(), resource.Database)
		if err != nil {
			return err
		}
		defer release()
	}
	return s.transaction(ctx, scope.ID(), write, func(t *tx) error { return fn(t) })
}
func (s *Store) Platform(ctx context.Context, write bool, fn func(ports.PlatformTx) error) error {
	if s.Admission != nil {
		release, err := s.Admission.Acquire(ctx, "", resource.Database)
		if err != nil {
			return err
		}
		defer release()
	}
	return s.transaction(ctx, "", write, func(t *tx) error { return fn(&platformTx{t}) })
}
func (s *Store) RouteApp(ctx context.Context, key string) (domain.App, error) {
	if s.Admission != nil {
		release, err := s.Admission.AcquireMetadata(ctx)
		if err != nil {
			return domain.App{}, err
		}
		defer release()
	}
	var out domain.App
	err := s.transaction(ctx, "", false, func(t *tx) error {
		row, e := t.entities().App.Query().Where(app.AppKeyEQ(key)).Only(ctx)
		if e != nil {
			return classify(e)
		}
		out, e = appDomain(row)
		return e
	})
	return out, err
}

type tx struct {
	store *Store
	sql   *sql.Tx
	ctx   context.Context
	app   string
	write bool
}

func (t *tx) exec(q string, args ...any) error {
	if !t.write {
		return domain.ErrForbidden
	}
	_, e := t.sql.ExecContext(t.ctx, t.store.bind(q), args...)
	return classify(e, "execute")
}
func (t *tx) one(q string, args ...any) *sql.Row {
	return t.sql.QueryRowContext(t.ctx, t.store.bind(q), args...)
}
func (t *tx) rows(q string, args ...any) (*sql.Rows, error) {
	r, e := t.sql.QueryContext(t.ctx, t.store.bind(q), args...)
	return r, classify(e, "query")
}
func (t *tx) lock(enabled bool) string {
	if enabled && t.write {
		return " FOR UPDATE"
	}
	return ""
}
func (t *tx) seal(kind string, value any) ([]byte, error) {
	data, err := encodePayload(kind, value)
	if err != nil {
		return nil, diagnostic.Wrap(errors.Join(domain.ErrUnavailable, err), "storage", "encode")
	}
	return t.store.vault.Seal(t.app+":"+kind, data)
}
func (t *tx) open(kind string, b []byte, v any) error {
	data, err := t.store.vault.Open(t.app+":"+kind, b)
	if err != nil {
		return diagnostic.Wrap(errors.Join(domain.ErrUnavailable, err), "storage", "decode")
	}
	if err = decodePayload(kind, data, v); err != nil {
		return diagnostic.Wrap(errors.Join(domain.ErrUnavailable, err), "storage", "decode")
	}
	return nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
