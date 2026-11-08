package diagnostic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/jackc/pgx/v5/pgconn"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
)

type cycle struct{}

func (*cycle) Error() string   { panic("must not format") }
func (e *cycle) Unwrap() error { return e }
func TestSafeTreeAndRoles(t *testing.T) {
	c := &pgconn.PgError{Code: "23505", Message: "SECRET-SQL-DSN"}
	e := Wrap(fmt.Errorf("SECRET-TOKEN: %w", c), "storage", "query")
	var got *pgconn.PgError
	if !errors.As(e, &got) || got != c || !errors.Is(e, c) {
		t.Fatal("lost cause")
	}
	for _, tree := range []error{errors.Join(e, context.Canceled), errors.Join(context.Canceled, e), errors.Join(context.DeadlineExceeded, errors.Join(e, context.Canceled))} {
		if Inspect(tree).Kind != "cancelled" {
			t.Fatal("cancel priority")
		}
	}
	for _, tc := range []struct {
		e    error
		kind string
	}{{Combine(domain.ErrInvalid, context.Canceled, false), "invalid"}, {Combine(context.Canceled, c, false), "cancelled"}, {Combine(nil, c, true), "committed_cleanup"}, {Wrap(Combine(domain.ErrInvalid, context.Canceled, false), "runtime", "cleanup"), "invalid"}} {
		if d := Inspect(tc.e); d.Kind != tc.kind {
			t.Fatal(d, tc.kind)
		}
	}
	if !errors.Is(Combine(context.Canceled, c, false), c) {
		t.Fatal("cleanup cause missing")
	}
	Inspect(&cycle{})
	var b bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&b, nil)))
	defer slog.SetDefault(old)
	Log(WithRequest(context.Background(), "req-123"), "storage", "query", e)
	s := b.String()
	for _, v := range []string{"SECRET", "/Users/"} {
		if strings.Contains(s, v) {
			t.Fatal("unsafe log")
		}
	}
	for _, v := range []string{"req-123", "23505", "postgres", "first_application_contact", "error_test.go", "query"} {
		if !strings.Contains(s, v) {
			t.Fatal("missing diagnostic", v, s)
		}
	}
}

func TestForeignFrameworkLoggerNeverResolvesUnsafeFields(t *testing.T) {
	var b bytes.Buffer
	h := Handler{Next: slog.NewJSONHandler(&b, nil)}
	r := slog.NewRecord(time.Now(), slog.LevelError, "CANARY request password", 0)
	r.Add("error", fmt.Errorf("CANARY: %w", context.Canceled), "url", "https://user:CANARY@example/?token=CANARY", "request", map[string]string{"password": "CANARY"})
	if e := h.Handle(WithRequest(context.Background(), "framework-123"), r); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(b.String(), "CANARY") || !strings.Contains(b.String(), "framework-123") || !strings.Contains(b.String(), "cancelled") || !strings.Contains(b.String(), "frames") {
		t.Fatal("foreign log unsafe or empty", b.String())
	}
}

func TestWrappedContactIsCapturedOnceAndRolesSurvive(t *testing.T) {
	first := Wrap(errors.New("secret"), "storage", "query")
	a := Inspect(first)
	b := Inspect(Wrap(first, "http", "request"))
	if len(a.Frames) == 0 || !reflect.DeepEqual(a.Frames, b.Frames) || b.Operation != "storage" {
		t.Fatal("contact replaced by outer wrapper")
	}
	for _, e := range []error{errors.Join(domain.ErrInvalid, domain.ErrUnavailable), errors.Join(domain.ErrUnavailable, domain.ErrInvalid)} {
		if Inspect(e).Kind != "invalid" {
			t.Fatal("classification depends on order")
		}
	}
	if !Has(Combine(domain.ErrInvalid, domain.ErrNotFound, false), domain.ErrInvalid) || Has(Combine(domain.ErrInvalid, domain.ErrNotFound, false), domain.ErrNotFound) {
		t.Fatal("cleanup overrode primary public decision")
	}
}

func TestLeaseAndExpiryKindsRemainUseful(t *testing.T) {
	if Inspect(domain.ErrLeaseLost).Kind != "lease_lost" || Inspect(domain.ErrExpired).Kind != "expired" {
		t.Fatal("lost async classification")
	}
}

func TestTypedNilAndMalformedCauseDoNotHideUnknownCommit(t *testing.T) {
	var c *cycle
	var pg *pgconn.PgError
	for _, e := range []error{errors.Join(c, domain.ErrCommitUnknown, context.Canceled), errors.Join(pg, domain.ErrCommitUnknown)} {
		if Inspect(e).Kind != "outcome_unknown" || !Has(e, domain.ErrCommitUnknown) {
			t.Fatal("nil cause hid commit fact")
		}
	}
}
