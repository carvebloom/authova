package mcpquery

import (
	"context"
	"github.com/carvebloom/authova/contracts/sdk/query"
	"github.com/google/uuid"
	"log/slog"
	"path/filepath"
	"reflect"
	"runtime"
)

// LogFailure owns the terminal query/CLI diagnostic, not raw stderr formatting.
// SDK errors remain inspectable; no Error/Format/LogValue method is called here.
func LogFailure(ctx context.Context, err error) {
	kind, typ, source, file, line := query.FailureKind(err), "unknown", "recorded_at", "", 0
	reqID := ""
	stack := []error{err}
	cancel, deadline := false, false
	for n := 0; len(stack) > 0 && n < 32; n++ {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if e == nil {
			continue
		}
		t := reflect.TypeOf(e)
		typ = namedType(t)
		if t.Comparable() {
			if e == context.Canceled {
				cancel = true
			}
			if e == context.DeadlineExceeded {
				deadline = true
			}
		}
		if c, ok := e.(interface{ DiagnosticRequestID() string }); ok {
			reqID = safeID(c.DiagnosticRequestID())
		}
		if q, ok := e.(*query.Error); ok && q != nil {
			reqID = safeID(q.RequestID)
		}
		if c, ok := e.(interface{ DiagnosticContact() (string, int) }); ok {
			f, l := c.DiagnosticContact()
			if f == "client.go" && l > 0 && l < 10000 {
				source = "first_application_contact"
				file = f
				line = l
			}
		}
		stack = append(stack, queryChildren(e)...)
		if len(stack) > 32 {
			stack = stack[:32]
		}
	}
	if cancel {
		kind = "cancelled"
	} else if deadline {
		kind = "deadline"
	}
	if file == "" {
		_, f, l, _ := runtime.Caller(1)
		file = filepath.Base(f)
		line = l
	}
	slog.ErrorContext(ctx, "query failed", "operation", "query", "phase", "upstream", "kind", kind, "cause_type", typ, "request_id", reqID, "diagnostic_id", uuid.NewString(), "source", source, "file", file, "line", line)
}
func safeID(s string) string {
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
func queryChildren(e error) (out []error) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	if u, ok := e.(interface{ Unwrap() []error }); ok {
		a := u.Unwrap()
		return a[:min(len(a), 32)]
	}
	if u, ok := e.(interface{ Unwrap() error }); ok {
		return []error{u.Unwrap()}
	}
	return nil
}

func namedType(t reflect.Type) string {
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
	out := prefix + filepath.Base(t.PkgPath()) + "." + name
	if len(out) > 120 {
		out = out[:120]
	}
	return out
}

type responseFailure struct{ cause error }

func (e *responseFailure) Error() string { return "invalid query response" }
func (e *responseFailure) Unwrap() error { return e.cause }
