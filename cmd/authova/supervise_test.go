package main

import (
	"context"
	"errors"
	"testing"
)

func TestAllModeWaitsForPeerAndKeepsFailure(t *testing.T) {
	fail := errors.New("worker invariant")
	stopped := make(chan struct{})
	e := supervise(context.Background(), func(context.Context) error { return fail }, func(ctx context.Context) error { <-ctx.Done(); close(stopped); return ctx.Err() })
	if !errors.Is(e, fail) {
		t.Fatal("worker failure lost")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("peer not stopped")
	}
}
