package mcpquery

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"log/slog"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
)

// RecoveredPanic is shared with the CLI boundary; never renders the panic value.
// No server-internal package is required by the standalone query tools.
func RecoveredPanic(ctx context.Context, value any) error {
	typ := "nil"
	if value != nil {
		typ = reflect.TypeOf(value).Kind().String()
	}
	if len(typ) > 120 {
		typ = typ[:120]
	}
	type frame struct {
		Function, File string
		Line           int
	}
	pcs := make([]uintptr, 8)
	n := runtime.Callers(2, pcs)
	it := runtime.CallersFrames(pcs[:n])
	frames := []frame{}
	for {
		f, more := it.Next()
		name := f.Function
		if i := strings.Index(name, "github.com/carvebloom/authova/"); i >= 0 {
			name = name[i+len("github.com/carvebloom/authova/"):]
		} else {
			name = filepath.Base(name)
		}
		frames = append(frames, frame{name, filepath.Base(f.File), f.Line})
		if !more || len(frames) == 6 {
			break
		}
	}
	slog.ErrorContext(ctx, "query panic recovered", "operation", "query", "phase", "panic", "kind", "internal", "diagnostic_id", uuid.NewString(), "panic_type", typ, "source", "panic_unwind", "frames", frames)
	return errors.New("authova query failed")
}
