package sqlstore

import (
	"database/sql"
	"errors"
	"github.com/carvebloom/authova/internal/ent"
	"log/slog"
	"strconv"

	"github.com/carvebloom/authova/internal/domain"
	mysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

// Error preserves driver/context causes for errors.Is/As while its printable
// representation is safe for operational logs. Never log Unwrap's driver error:
// it may contain SQL, values or connection details. Transient is diagnostic only;
// callers must not automatically retry writes (especially an unknown commit).
type Error struct {
	Operation, Driver, Code string
	Transient               bool
	kind, cause             error
}

func (e *Error) Error() string {
	return "storage " + e.Operation + " (" + e.Driver + "/" + e.Code + "): " + e.kind.Error()
}
func (e *Error) Unwrap() error        { return e.cause }
func (e *Error) Is(target error) bool { return errors.Is(e.kind, target) }
func (e *Error) LogValue() slog.Value {
	return slog.GroupValue(slog.String("operation", e.Operation), slog.String("driver", e.Driver), slog.String("code", e.Code), slog.Bool("transient", e.Transient), slog.String("kind", e.kind.Error()))
}
func classify(cause error, operation ...string) error {
	if cause == nil {
		return nil
	}
	op := "scan"
	if len(operation) > 0 {
		op = operation[0]
	}
	return storageError(cause, op, nil)
}
func storageError(cause error, operation string, kind error) error {
	e := &Error{Operation: operation, Driver: "sql", Code: "unknown", cause: cause, kind: kind}
	inferred := domain.ErrUnavailable
	if errors.Is(cause, sql.ErrNoRows) || ent.IsNotFound(cause) {
		inferred = domain.ErrNotFound
		e.Code = "no_rows"
	}
	var p *pgconn.PgError
	if errors.As(cause, &p) {
		e.Driver, e.Code = "postgres", p.Code
		switch p.Code {
		case "23505":
			inferred = domain.ErrConflict
		case "23503", "23514":
			inferred = domain.ErrInvalid
		case "40P01", "40001", "55P03":
			e.Transient = true
		}
	}
	var m *mysql.MySQLError
	if errors.As(cause, &m) {
		e.Driver, e.Code = "mysql", strconv.Itoa(int(m.Number))
		switch m.Number {
		case 1062:
			inferred = domain.ErrConflict
		case 1451, 1452, 3819:
			inferred = domain.ErrInvalid
		case 1205, 1213:
			e.Transient = true
		}
	}
	if e.kind == nil {
		e.kind = inferred
	}
	return e
}
