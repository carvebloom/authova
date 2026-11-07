// Package securityevents records bounded, nonblocking operational security facts.
// It is separate from atomic management audits; saturated logging drops events
// with an explicit counter rather than blocking authentication or growing memory.
package securityevents

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Event struct {
	RequestID  string    `json:"request_id"`
	AppID      string    `json:"app_id"`
	ClientID   string    `json:"client_id,omitempty"`
	SubjectRef string    `json:"subject_ref,omitempty"`
	SourceRef  string    `json:"source_ref"`
	Action     string    `json:"action"`
	Outcome    string    `json:"outcome"`
	Reason     string    `json:"reason"`
	CreatedAt  time.Time `json:"created_at"`
}
type Sink interface {
	Record(Event)
	Stats() Counters
}

// Counters are cumulative. After Shutdown drains, Recorded equals
// Written+WriteFailed+QueueDropped. A live snapshot can include pending records.
type Counters struct{ Recorded, Attempted, Written, WriteFailed, QueueDropped uint64 }
type Recorder struct {
	acceptMu    sync.RWMutex
	logger      *slog.Logger
	queue       chan Event
	stop        chan struct{}
	done        chan struct{}
	once        sync.Once
	closing     atomic.Bool
	recorded    atomic.Uint64
	attempted   atomic.Uint64
	writeFailed atomic.Uint64
	written     atomic.Uint64
	dropped     atomic.Uint64
}

func New(logger *slog.Logger, capacity int) *Recorder {
	if logger == nil {
		logger = slog.Default()
	}
	if capacity < 1 {
		capacity = 1024
	}
	if capacity > 65536 {
		capacity = 65536
	}
	r := &Recorder{logger: logger, queue: make(chan Event, capacity), stop: make(chan struct{}), done: make(chan struct{})}
	go r.run()
	return r
}
func (r *Recorder) Record(e Event) {
	r.recorded.Add(1)
	r.acceptMu.RLock()
	defer r.acceptMu.RUnlock()
	if r.closing.Load() {
		r.dropped.Add(1)
		return
	}
	e.RequestID = bounded(e.RequestID)
	e.AppID = bounded(e.AppID)
	e.ClientID = bounded(e.ClientID)
	e.SubjectRef = bounded(e.SubjectRef)
	e.SourceRef = bounded(e.SourceRef)
	e.Action = bounded(e.Action)
	e.Outcome = bounded(e.Outcome)
	e.Reason = bounded(e.Reason)
	select {
	case r.queue <- e:
	default:
		r.dropped.Add(1)
	}
}
func (r *Recorder) Stats() Counters {
	return Counters{Recorded: r.recorded.Load(), Attempted: r.attempted.Load(), Written: r.written.Load(), WriteFailed: r.writeFailed.Load(), QueueDropped: r.dropped.Load()}
}
func (r *Recorder) Shutdown(ctx context.Context) error {
	r.once.Do(func() { r.acceptMu.Lock(); r.closing.Store(true); close(r.stop); r.acceptMu.Unlock() })
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func bounded(v string) string {
	if len(v) > 128 {
		return strings.Clone(v[:128])
	}
	return v
}
func (r *Recorder) write(e Event) {
	r.attempted.Add(1)
	// Logger.Info discards Handler.Handle errors. Call the handler directly so
	// output failures remain observable without recursively logging to this sink.
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "security_event", 0)
	record.Add("request_id", e.RequestID, "app_id", e.AppID, "client_id", e.ClientID, "subject_ref", e.SubjectRef, "source_ref", e.SourceRef, "action", e.Action, "outcome", e.Outcome, "reason", e.Reason, "created_at", e.CreatedAt.UTC())
	if err := r.logger.Handler().Handle(context.Background(), record); err != nil {
		r.writeFailed.Add(1)
		return
	}
	r.written.Add(1)
}
func (r *Recorder) run() {
	defer close(r.done)
	for {
		select {
		case e := <-r.queue:
			r.write(e)
		case <-r.stop:
			for {
				select {
				case e := <-r.queue:
					r.write(e)
				default:
					return
				}
			}
		}
	}
}
