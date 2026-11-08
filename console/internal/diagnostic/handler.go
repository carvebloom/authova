package diagnostic

import (
	"context"
	"log/slog"
	"runtime"
	"strings"
	"time"
)

// Handler contains framework logging before LogValue resolution. Foreign messages,
// request objects, URLs and metadata are never formatted or forwarded. Our own
// diagnostics use reviewed fields; application code must still use Log/Panic.
type Handler struct {
	Next  slog.Handler
	attrs []slog.Attr
}

func (h Handler) Enabled(ctx context.Context, l slog.Level) bool { return h.Next.Enabled(ctx, l) }
func (h Handler) WithAttrs(a []slog.Attr) slog.Handler {
	return Handler{Next: h.Next, attrs: append(append([]slog.Attr{}, h.attrs...), a...)}
}
func (h Handler) WithGroup(_ string) slog.Handler { return h }
func (h Handler) Handle(ctx context.Context, r slog.Record) error {
	fn := ""
	if f := runtime.FuncForPC(r.PC); f != nil {
		fn = f.Name()
	}
	if strings.HasPrefix(fn, "github.com/carvebloom/authova/") {
		r.AddAttrs(h.attrs...)
		return h.Next.Handle(ctx, r)
	}
	clean := slog.NewRecord(time.Now(), r.Level, "dependency diagnostic", r.PC)
	clean.Add("operation", "protocol", "phase", "upstream", "request_id", RequestID(ctx), "source", "recorded_at")
	inspect := func(a slog.Attr) {
		if e, ok := a.Value.Any().(error); ok {
			d := Inspect(e)
			clean.Add("kind", d.Kind, "cause_type", d.Type, "driver", d.Driver, "driver_code", d.Code)
		}
	}
	count := 0
	for _, a := range h.attrs {
		if count >= 16 {
			break
		}
		count++
		inspect(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		if count >= 16 {
			return false
		}
		count++
		inspect(a)
		return true
	})
	clean.Add("frames", capture(3))
	return h.Next.Handle(ctx, clean)
}
