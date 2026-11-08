package sqlstore

import (
	"database/sql"
	"errors"
	"github.com/carvebloom/authova/internal/diagnostic"
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

func (e *Error) safeFields() (operation, driver, code, kind string) {
	if e == nil {
		return "unknown", "unknown", "unknown", "dependency unavailable"
	}
	operation = "unknown"
	switch e.Operation {
	case "scan", "query", "execute", "begin", "commit", "upsert":
		operation = e.Operation
	}
	driver = "unknown"
	switch e.Driver {
	case "sql", "postgres", "mysql":
		driver = e.Driver
	}
	code = "unknown"
	if e.Code == "no_rows" {
		code = e.Code
	} else if driver == "postgres" && len(e.Code) == 5 {
		valid := true
		for _, r := range e.Code {
			valid = valid && (r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
		}
		if valid {
			code = e.Code
		}
	} else if driver == "mysql" {
		if n, err := strconv.ParseUint(e.Code, 10, 16); err == nil {
			code = strconv.FormatUint(n, 10)
		}
	}
	kind = "dependency unavailable"
	switch e.kind {
	case domain.ErrNotFound:
		kind = "not found"
	case domain.ErrConflict:
		kind = "conflict"
	case domain.ErrInvalid:
		kind = "invalid input"
	case domain.ErrForbidden:
		kind = "forbidden"
	case domain.ErrUnauthenticated:
		kind = "unauthenticated"
	case domain.ErrCommitUnknown:
		kind = "commit outcome unknown"
	}
	return
}
func (e *Error) Error() string {
	op, driver, code, kind := e.safeFields()
	return "storage " + op + " (" + driver + "/" + code + "): " + kind
}
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}
func (e *Error) Is(target error) bool { return e != nil && errors.Is(e.kind, target) }
func (e *Error) LogValue() slog.Value {
	op, driver, code, kind := e.safeFields()
	return slog.GroupValue(slog.String("operation", op), slog.String("driver", driver), slog.String("code", code), slog.Bool("transient", e != nil && e.Transient), slog.String("kind", kind))
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
	e.cause = diagnostic.Wrap(errors.Join(e.kind, cause), "storage", operation)
	return e
}
