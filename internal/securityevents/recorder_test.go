package securityevents

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type blockedWriter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockedWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return len(p), nil
}
func TestBackpressureAndBoundedShutdown(t *testing.T) {
	w := &blockedWriter{entered: make(chan struct{}), release: make(chan struct{})}
	r := New(slog.New(slog.NewJSONHandler(w, nil)), 1)
	r.Record(Event{Action: "first"})
	select {
	case <-w.entered:
	case <-time.After(time.Second):
		t.Fatal("worker not started")
	}
	r.Record(Event{Action: "second"})
	r.Record(Event{Action: "overflow"})
	dropped := r.Stats().QueueDropped
	if dropped != 1 {
		t.Fatal("queue did not drop overflow", dropped)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if !errors.Is(r.Shutdown(ctx), context.DeadlineExceeded) {
		t.Fatal("shutdown exceeded its caller's bound")
	}
	close(w.release)
	if e := r.Shutdown(context.Background()); e != nil {
		t.Fatal(e)
	}
	r.Record(Event{})
	written, dropped := r.Stats().Written, r.Stats().QueueDropped
	if written != 2 || dropped != 2 {
		t.Fatal("incorrect delivery/drop facts", written, dropped)
	}
}
func TestConcurrentShutdownAccountsForEveryRecord(t *testing.T) {
	r := New(slog.New(slog.NewJSONHandler(io.Discard, nil)), 16)
	var wg sync.WaitGroup
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r.Record(Event{Action: "test"}) }()
	}
	if e := r.Shutdown(context.Background()); e != nil {
		t.Fatal(e)
	}
	wg.Wait()
	written, dropped := r.Stats().Written, r.Stats().QueueDropped
	if written+dropped != 1000 {
		t.Fatal("record lost without a drop fact", written, dropped)
	}
}
func TestStructuredEventsAreBounded(t *testing.T) {
	var out bytes.Buffer
	r := New(slog.New(slog.NewJSONHandler(&out, nil)), 1)
	r.Record(Event{Action: "authentication.password", RequestID: strings.Repeat("x", 1000), Outcome: "rejected", Reason: "invalid_proof"})
	if e := r.Shutdown(context.Background()); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(out.String(), strings.Repeat("x", 129)) || !strings.Contains(out.String(), `"msg":"security_event"`) {
		t.Fatal("event encoding/bounds invalid")
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestOutputFailureIsNotWritten(t *testing.T) {
	r := New(slog.New(slog.NewJSONHandler(failedWriter{}, nil)), 2)
	r.Record(Event{Action: "test"})
	if err := r.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	c := r.Stats()
	if c.Recorded != 1 || c.Attempted != 1 || c.Written != 0 || c.WriteFailed != 1 || c.QueueDropped != 0 {
		t.Fatalf("incorrect failure accounting: %+v", c)
	}
}
