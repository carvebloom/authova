package transport

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
)

type routeProbeStore struct {
	ports.Store
	calls   atomic.Int64
	entered chan struct{}
	release chan struct{}
	failure error
}

func (s *routeProbeStore) RouteApp(ctx context.Context, key string) (domain.App, error) {
	s.calls.Add(1)
	select {
	case s.entered <- struct{}{}:
	default:
	}
	select {
	case <-s.release:
		return domain.App{ID: "app", Key: key}, s.failure
	case <-ctx.Done():
		return domain.App{}, ctx.Err()
	}
}

func TestRoutingCacheCoalescesExpiredMetadata(t *testing.T) {
	store := &routeProbeStore{entered: make(chan struct{}, 1), release: make(chan struct{})}
	server := &Server{Service: &application.Service{Store: store}}
	var group sync.WaitGroup
	for n := 0; n < 32; n++ {
		group.Add(1)
		go func() {
			defer group.Done()
			r := httptest.NewRequest("POST", "/a/key/oauth/introspect", nil)
			r.SetPathValue("app_key", "key")
			app, err := server.routeApp(r)
			if err != nil || app.Key != "key" {
				t.Error("coalesced route failed", err)
			}
		}()
	}
	<-store.entered
	time.Sleep(20 * time.Millisecond) // Keep the leader blocked while concurrent misses arrive.
	close(store.release)
	group.Wait()
	if store.calls.Load() != 1 {
		t.Fatal("routing cache stampede", store.calls.Load())
	}
	server.routeMu.Lock()
	server.routes["key"] = routeEntry{app: domain.App{ID: "app", Key: "key"}, expires: time.Now().Add(-time.Second)}
	server.routeMu.Unlock()
	r := httptest.NewRequest("POST", "/a/key/oauth/introspect", nil)
	r.SetPathValue("app_key", "key")
	if _, err := server.routeApp(r); err != nil || store.calls.Load() != 2 {
		t.Fatal("expired route did not refresh", err)
	}
}

func TestRoutingWaiterCancellationDoesNotCancelLoader(t *testing.T) {
	store := &routeProbeStore{entered: make(chan struct{}, 1), release: make(chan struct{})}
	server := &Server{Service: &application.Service{Store: store}}
	r := httptest.NewRequest("POST", "/a/key/oauth/introspect", nil)
	r.SetPathValue("app_key", "key")
	done := make(chan error, 1)
	go func() { _, err := server.routeApp(r); done <- err }()
	<-store.entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := server.routeApp(r.WithContext(ctx)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(store.release)
	if err := <-done; err != nil || store.calls.Load() != 1 {
		t.Fatal("waiter poisoned metadata loader", err)
	}
	if len(server.routeLoads) != 0 {
		t.Fatal("route flight leaked")
	}
}
