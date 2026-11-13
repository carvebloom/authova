package sqlstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/carvebloom/authova/internal/diagnostic"
	"log/slog"
	"strings"
	"testing"

	"github.com/carvebloom/authova/internal/domain"
	mysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestClassifiedErrorsPreserveSafeDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		cause        error
		kind         error
		driver, code string
		transient    bool
	}{
		{&pgconn.PgError{Code: "23505", Message: "sensitive-query-and-password"}, domain.ErrConflict, "postgres", "23505", false},
		{&pgconn.PgError{Code: "40P01", Message: "sensitive-query-and-password"}, domain.ErrUnavailable, "postgres", "40P01", true},
		{&mysql.MySQLError{Number: 1213, Message: "sensitive-query-and-password"}, domain.ErrUnavailable, "mysql", "1213", true},
		{&mysql.MySQLError{Number: 1062, Message: "sensitive-query-and-password"}, domain.ErrConflict, "mysql", "1062", false},
		{fmt.Errorf("sensitive-query-and-password: %w", context.DeadlineExceeded), domain.ErrUnavailable, "sql", "unknown", false},
	} {
		err := classify(tc.cause, "execute")
		var structured *Error
		if !errors.Is(err, tc.kind) || !errors.Is(err, tc.cause) || !errors.As(err, &structured) {
			t.Fatal("lost classification/cause")
		}
		if structured.Driver != tc.driver || structured.Code != tc.code || structured.Transient != tc.transient {
			t.Fatal("lost diagnostic fields")
		}
		var out bytes.Buffer
		slog.New(slog.NewJSONHandler(&out, nil)).Error("failed", "error", err)
		if strings.Contains(fmt.Sprintf("%+v %s", err, out.String()), "sensitive-query-and-password") {
			t.Fatal("secret in printable error")
		}
	}
	err := storageError(context.Canceled, "commit", domain.ErrCommitUnknown)
	if !errors.Is(err, domain.ErrCommitUnknown) || !errors.Is(err, context.Canceled) || errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("unknown commit became retryable failure")
	}
}

func TestCommitUnknownDiagnosticOverridesCancelledCause(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.Join(context.Canceled, context.DeadlineExceeded)} {
		err := storageError(cause, "commit", domain.ErrCommitUnknown)
		if !errors.Is(err, domain.ErrCommitUnknown) || diagnostic.Inspect(err).Kind != "outcome_unknown" {
			t.Fatal("commit fact lost")
		}
		var typed *Error
		if !errors.As(err, &typed) || !errors.Is(err, cause) {
			t.Fatal("chain lost")
		}
	}
}

func TestForgedStorageFieldsCannotBypassSafeFormatting(t *testing.T) {
	err := &Error{Operation: "CANARY-OP", Driver: "CANARY-DRIVER", Code: "CANARY-SQL", kind: errors.New("CANARY-CAUSE"), cause: errors.New("CANARY-CAUSE")}
	var out bytes.Buffer
	slog.New(slog.NewJSONHandler(&out, nil)).Error("failed", "error", err)
	if strings.Contains(out.String()+fmt.Sprintf("%+v", err), "CANARY") {
		t.Fatal("same type is not a trust boundary")
	}
}
