package resource

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/carvebloom/authova/internal/domain"
)

func TestNoisyAppCannotConsumePeerOrManagementCapacity(t *testing.T) {
	for _, kind := range []Kind{HTTP, KDF, Database} {
		t.Run(string(kind), func(t *testing.T) {
			m := New(8, 4, 8, func(context.Context, string) (domain.ResourceLimits, error) {
				l := domain.DefaultResourceLimits()
				l.HTTPConcurrency = 3
				l.KDFConcurrency = 2
				l.DBConcurrency = 3
				return l, nil
			})
			ctx := context.Background()
			limit := 3
			if kind == KDF {
				limit = 2
			}
			releases := []func(){}
			for i := 0; i < limit; i++ {
				release, err := m.Acquire(ctx, "app1", kind)
				if err != nil {
					t.Fatal(err)
				}
				releases = append(releases, release)
			}
			if _, err := m.Acquire(ctx, "app1", kind); !errors.Is(err, domain.ErrRateLimit) {
				t.Fatal("App cap not enforced", err)
			}
			peer, err := m.Acquire(ctx, "app2", kind)
			if err != nil {
				t.Fatal("peer starved", err)
			}
			releases = append(releases, peer)
			admin, err := m.Acquire(ctx, "", kind)
			if err != nil {
				t.Fatal("management reserve lost", err)
			}
			releases = append(releases, admin)
			for _, release := range releases {
				release()
				release()
			}
			if len(m.apps) != 0 || m.total[kind] != 0 {
				t.Fatal("permit leaked")
			}
		})
	}
}
func TestSharedBudgetRetainsManagementReserve(t *testing.T) {
	m := New(8, 4, 8, nil)
	ctx := context.Background()
	rs := []func(){}
	for i := 0; i < 7; i++ {
		r, e := m.Acquire(ctx, string(rune('a'+i)), Database)
		if e != nil {
			t.Fatal(e)
		}
		rs = append(rs, r)
	}
	if _, e := m.Acquire(ctx, "other", Database); !errors.Is(e, domain.ErrUnavailable) {
		t.Fatal(e)
	}
	admin, e := m.Acquire(ctx, "", Database)
	if e != nil {
		t.Fatal(e)
	}
	admin()
	for _, r := range rs {
		r()
	}
}
func TestConcurrentAcquireIsBoundedAndReleased(t *testing.T) {
	m := New(32, 4, 32, nil)
	var group sync.WaitGroup
	for i := 0; i < 100; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for j := 0; j < 30; j++ {
				r, e := m.Acquire(context.Background(), "app", KDF)
				if e == nil {
					r()
				} else if !errors.Is(e, domain.ErrRateLimit) && !errors.Is(e, domain.ErrUnavailable) {
					t.Error(e)
				}
			}
		}()
	}
	group.Wait()
	if len(m.apps) != 0 || m.total[KDF] != 0 {
		t.Fatal("leaked permit")
	}
}
func TestPolicyReadSingleFlightFailureAndInvalidation(t *testing.T) {
	var reads atomic.Int64
	ready := make(chan struct{})
	m := New(8, 4, 8, func(ctx context.Context, app string) (domain.ResourceLimits, error) {
		reads.Add(1)
		select {
		case <-ready:
			return domain.DefaultResourceLimits(), nil
		case <-ctx.Done():
			return domain.ResourceLimits{}, ctx.Err()
		}
	})
	var group sync.WaitGroup
	for i := 0; i < 10; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, e := m.Limits(context.Background(), "app"); e != nil {
				t.Error(e)
			}
		}()
	}
	close(ready)
	group.Wait()
	if reads.Load() != 1 {
		t.Fatal("stampede", reads.Load())
	}
	m.Invalidate("app")
	if _, e := m.Limits(context.Background(), "app"); e != nil || reads.Load() != 2 {
		t.Fatal("invalidation failed", e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := m.Acquire(ctx, "app", HTTP); !errors.Is(e, context.Canceled) {
		t.Fatal("cancelled caller admitted")
	}
}
func TestInvalidOrFailedPolicyFailsClosed(t *testing.T) {
	for _, loader := range []Loader{func(context.Context, string) (domain.ResourceLimits, error) { return domain.ResourceLimits{}, nil }, func(context.Context, string) (domain.ResourceLimits, error) {
		return domain.ResourceLimits{}, domain.ErrUnavailable
	}} {
		m := New(8, 4, 8, loader)
		if _, e := m.Acquire(context.Background(), "app", HTTP); e == nil {
			t.Fatal("unavailable policy admitted")
		}
	}
}
