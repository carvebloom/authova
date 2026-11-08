// Package diagnostic separates inspectable causes from bounded, safe diagnostics.
// No arbitrary Error/LogValue/Format method is called while building a record.
package diagnostic

import (
	"context"
	"github.com/carvebloom/authova/internal/domain"
	mysql "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/trace"
	"log/slog"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
)

type Frame struct {
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}
type failure struct {
	cause     error
	op, phase string
	frames    []Frame
}

func (e *failure) Error() string { return "operation failed" }
func (e *failure) Unwrap() error { return e.cause }
func Wrap(err error, op, phase string) error {
	if err == nil {
		return nil
	}
	var frames []Frame
	if len(Inspect(err).Frames) == 0 {
		frames = capture(3)
	}
	return &failure{err, label(op), label(phase), frames}
}
func capture(skip int) []Frame {
	pcs := make([]uintptr, 12)
	n := runtime.Callers(skip, pcs)
	it := runtime.CallersFrames(pcs[:n])
	out := []Frame{}
	for {
		f, more := it.Next()
		name := f.Function
		if i := strings.Index(name, "github.com/carvebloom/authova/"); i >= 0 {
			name = name[i+len("github.com/carvebloom/authova/"):]
		} else {
			name = filepath.Base(name)
		}
		out = append(out, Frame{bound(name, 160), filepath.Base(f.File), f.Line})
		if !more || len(out) == 8 {
			break
		}
	}
	return out
}
func bound(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
func label(s string) string {
	switch s {
	case "http", "protocol", "console", "storage", "sdk", "worker", "runtime", "startup", "panic", "request", "query", "scan", "execute", "begin", "commit", "rollback", "encode", "decode", "connect", "lease", "send", "ack", "cleanup", "shutdown", "serve", "upstream", "response", "recorded_at", "application":
		return s
	}
	return "unknown"
}

// IDs are server generated. Restrict client/upstream IDs before correlation.
func ID(s string) string {
	if len(s) > 128 {
		return ""
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return ""
		}
	}
	return s
}

type idKey struct{}
type request struct {
	id     string
	logged atomic.Bool
}

func WithRequest(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, idKey{}, &request{id: ID(id)})
}
func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(idKey{}).(*request)
	if v != nil {
		return v.id
	}
	return ""
}

type Info struct {
	RetryAfter    int
	Transient     bool
	SecondaryType string
	Committed     bool
	Kind          string
	Type          string
	Driver        string
	Code          string
	Frames        []Frame
	Source        string
	Operation     string
	Phase         string
}

func same(a, b error) bool { return reflect.TypeOf(a).Comparable() && a == b }

// Inspect walks both Unwrap forms with a hard node budget, including cycles.
func Inspect(err error) Info {
	d := Info{Kind: "internal", Type: "unknown", Driver: "unknown", Code: "unknown", Source: "recorded_at"}
	stack := []error{err}
	cancel, deadline, unknown := false, false, false
	priority := 100
	for n := 0; len(stack) > 0 && n < 32; n++ {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if nilError(e) {
			continue
		}
		typ := reflect.TypeOf(e)
		d.Type = safeType(typ)
		if same(e, context.Canceled) {
			cancel = true
		}
		if same(e, context.DeadlineExceeded) {
			deadline = true
		}
		if same(e, domain.ErrCommitUnknown) {
			unknown = true
		}
		for _, k := range []struct {
			err  error
			name string
		}{{domain.ErrUnavailable, "dependency"}, {domain.ErrInvalid, "invalid"}, {domain.ErrNotFound, "not_found"}, {domain.ErrConflict, "conflict"}, {domain.ErrForbidden, "forbidden"}, {domain.ErrUnauthenticated, "authentication"}, {domain.ErrRateLimit, "rate_limited"}, {domain.ErrLeaseLost, "lease_lost"}, {domain.ErrExpired, "expired"}} {
			if same(e, k.err) && kindPriority(k.name) < priority {
				priority = kindPriority(k.name)
				d.Kind = k.name
			}
		}
		if contact, ok := e.(interface{ DiagnosticContact() (string, int) }); ok && len(d.Frames) == 0 {
			file, line := contact.DiagnosticContact()
			if file == "client.go" && line > 0 && line < 10000 {
				d.Frames = []Frame{{Function: "sdk.dependency", File: file, Line: line}}
				d.Source = "first_application_contact"
				d.Operation = "sdk"
				d.Phase = "upstream"
			}
		}
		switch v := e.(type) {
		case *Secondary:
			d.Committed = d.Committed || v.Committed
			if v.Cleanup != nil {
				d.SecondaryType = safeType(reflect.TypeOf(v.Cleanup))
			}
			if v.Primary != nil {
				stack = append(stack, v.Primary)
			} else if !v.Committed {
				stack = append(stack, v.Cleanup)
			}
			continue
		case *failure:
			if len(d.Frames) == 0 {
				d.Frames = v.frames
				d.Source = "first_application_contact"
				d.Operation = v.op
				d.Phase = v.phase
			}
		case *domain.RateLimitError:
			d.RetryAfter = min(3600, max(1, v.RetryAfterSeconds))
		case *pgconn.PgError:
			d.Driver = "postgres"
			d.Transient = v.Code == "40P01" || v.Code == "40001" || v.Code == "55P03"
			if len(v.Code) == 5 {
				valid := true
				for _, c := range v.Code {
					valid = valid && (c >= '0' && c <= '9' || c >= 'A' && c <= 'Z')
				}
				if valid {
					d.Code = v.Code
				}
			}
		case *mysql.MySQLError:
			d.Driver = "mysql"
			d.Transient = v.Number == 1205 || v.Number == 1213
			d.Code = itoa(v.Number)
		}
		if a := children(e); len(a) > 0 {
			for i := len(a) - 1; i >= 0 && len(stack) < 32; i-- {
				stack = append(stack, a[i])
			}
		}
	}
	if cancel {
		d.Kind = "cancelled"
	} else if deadline {
		d.Kind = "deadline"
	}
	if d.Committed {
		d.Kind = "committed_cleanup"
	}
	if unknown {
		d.Kind = "outcome_unknown"
	}
	return d
}
func itoa(n uint16) string {
	if n == 0 {
		return "0"
	}
	var b [5]byte
	i := 5
	for n > 0 {
		i--
		b[i] = byte(n%10) + '0'
		n /= 10
	}
	return string(b[i:])
}
func Log(ctx context.Context, op, phase string, err error) {
	if err == nil {
		return
	}
	if r, _ := ctx.Value(idKey{}).(*request); r != nil && !r.logged.CompareAndSwap(false, true) {
		return
	}
	d := Inspect(err)
	if len(d.Frames) == 0 {
		d.Frames = capture(3)
	}
	attrs := []any{"operation", label(op), "phase", label(phase), "kind", d.Kind, "cause_type", d.Type, "driver", d.Driver, "driver_code", d.Code, "request_id", RequestID(ctx), "diagnostic_id", uuid.NewString(), "source", d.Source, "frames", d.Frames, "secondary_type", d.SecondaryType, "committed", d.Committed, "transient", d.Transient, "outcome_unknown", d.Kind == "outcome_unknown"}
	if d.Operation != "" {
		attrs = append(attrs, "first_operation", d.Operation, "first_phase", d.Phase)
	}
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		attrs = append(attrs, "trace_id", sc.TraceID().String())
	}
	if e, ok := err.(*Secondary); ok && e.Cleanup != nil {
		c := Inspect(e.Cleanup)
		attrs = append(attrs, "secondary_kind", c.Kind, "secondary_driver", c.Driver, "secondary_code", c.Code, "secondary_source", c.Source, "secondary_frames", c.Frames)
	}
	if v, ok := ctx.Value(deliveryKey{}).(deliveryFact); ok {
		attrs = append(attrs, "delivery_state", v.state, "ack_unknown", v.ackUnknown, "delivery_code", v.code)
	}
	if attempt, ok := ctx.Value(attemptKey{}).(int); ok {
		attrs = append(attrs, "attempt", attempt)
	}
	if count, ok := ctx.Value(aggregateKey{}).(uint64); ok {
		attrs = append(attrs, "failures_since_log", count)
	}
	if id, _ := ctx.Value(upstreamKey{}).(string); id != "" {
		attrs = append(attrs, "upstream_request_id", id)
	}
	slog.ErrorContext(ctx, "operation failed", attrs...)
}

// Panic never formats the panic value. Its stack is captured while unwinding.
func Panic(ctx context.Context, op string, value any) {
	if r, _ := ctx.Value(idKey{}).(*request); r != nil {
		r.logged.Store(true)
	}
	typ := "nil"
	if value != nil {
		typ = safeType(reflect.TypeOf(value))
	}
	slog.ErrorContext(ctx, "panic recovered", "operation", label(op), "phase", "panic", "kind", "internal", "panic_type", typ, "request_id", RequestID(ctx), "diagnostic_id", uuid.NewString(), "source", "panic_unwind", "frames", capture(3))
}

// Secondary preserves a cleanup failure without redefining a known primary result.
type Secondary struct {
	Primary, Cleanup error
	Committed        bool
}

func (e *Secondary) Error() string   { return "operation and cleanup failed" }
func (e *Secondary) Unwrap() []error { return []error{e.Primary, e.Cleanup} }
func Combine(primary, cleanup error, committed bool) error {
	if cleanup == nil {
		return primary
	}
	return &Secondary{primary, cleanup, committed}
}

type upstreamKey struct{}

func WithUpstream(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, upstreamKey{}, ID(id))
}

// Has is the bounded projection matcher. It does not execute foreign Is methods.
// Internal semantic types expose domain sentinels through their cause trees.
func Has(err, target error) bool {
	stack := []error{err}
	for n := 0; len(stack) > 0 && n < 32; n++ {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if nilError(e) {
			continue
		}
		if v, ok := e.(*Secondary); ok {
			if v.Primary != nil {
				stack = append(stack, v.Primary)
			} else {
				stack = append(stack, v.Cleanup)
			}
			continue
		}
		if same(e, target) {
			return true
		}
		if a := children(e); len(a) > 0 {
			for i := len(a) - 1; i >= 0 && len(stack) < 32; i-- {
				stack = append(stack, a[i])
			}
		}
	}
	return false
}

type deliveryKey struct{}
type deliveryFact struct {
	state      string
	code       string
	ackUnknown bool
}

func WithDelivery(ctx context.Context, state string, ackUnknown bool, codes ...string) context.Context {
	code := "unknown"
	if len(codes) > 0 {
		switch codes[0] {
		case "internal", "dependency", "cancelled", "deadline", "outcome_unknown", "expired", "accepted", "lease_lost", "invalid", "not_found", "conflict", "forbidden", "authentication", "rate_limited", "provider_missing", "provider_rejected", "transport":
			code = codes[0]
		}
	}
	switch state {
	case "accepted", "not_sent", "unknown":
	default:
		state = "unknown"
	}
	return context.WithValue(ctx, deliveryKey{}, deliveryFact{state: state, code: code, ackUnknown: ackUnknown})
}

func kindPriority(kind string) int {
	switch kind {
	case "not_found":
		return 1
	case "invalid":
		return 2
	case "forbidden":
		return 3
	case "authentication":
		return 4
	case "conflict":
		return 5
	case "expired":
		return 6
	case "rate_limited":
		return 7
	case "lease_lost":
		return 8
	case "dependency":
		return 9
	}
	return 100
}

func safeType(t reflect.Type) string {
	prefix := ""
	for i := 0; t.Kind() == reflect.Pointer && i < 4; i++ {
		prefix += "*"
		t = t.Elem()
	}
	name := t.Name()
	if name == "" {
		return prefix + t.Kind().String() + "_anonymous"
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return prefix + "generic_error"
		}
	}
	return bound(prefix+filepath.Base(t.PkgPath())+"."+name, 120)
}

func nilError(e error) bool {
	if e == nil {
		return true
	}
	v := reflect.ValueOf(e)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface:
		return v.IsNil()
	}
	return false
}
func children(e error) (out []error) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	if u, ok := e.(interface{ Unwrap() []error }); ok {
		return u.Unwrap()
	}
	if u, ok := e.(interface{ Unwrap() error }); ok {
		return []error{u.Unwrap()}
	}
	return nil
}

type aggregateKey struct{}

func WithFailureCount(ctx context.Context, count uint64) context.Context {
	return context.WithValue(ctx, aggregateKey{}, min(count, 1000000000))
}

type attemptKey struct{}

func WithAttempt(ctx context.Context, attempt int) context.Context {
	return context.WithValue(ctx, attemptKey{}, min(100000, max(0, attempt)))
}
