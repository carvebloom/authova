package resource

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/domain"
)

type databaseAdmission struct {
	release func()
	err     error
}

func queuedDatabaseAcquire(m *Manager, ctx context.Context, app string) <-chan databaseAdmission {
	result := make(chan databaseAdmission, 1)
	go func() {
		r, err := m.Acquire(ctx, app, Database)
		result <- databaseAdmission{r, err}
	}()
	return result
}

func waitDatabaseQueue(t *testing.T, m *Manager, count int) {
	t.Helper()
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		m.mu.Lock()
		n := m.dbQueue.Len()
		m.mu.Unlock()
		if n == count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("database queue did not reach expected state", count)
}

func singlePermitApps() *Manager {
	return New(32, 4, 8, func(context.Context, string) (domain.ResourceLimits, error) {
		limits := domain.DefaultResourceLimits()
		limits.DBConcurrency = 1
		return limits, nil
	})
}

func TestDatabaseQueueBoundsPeerAndManagementReserve(t *testing.T) {
	m := singlePermitApps()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hold, err := m.Acquire(ctx, "noisy", Database)
	if err != nil {
		t.Fatal(err)
	}
	pending := make([]<-chan databaseAdmission, 4)
	for index := range pending {
		pending[index] = queuedDatabaseAcquire(m, ctx, "noisy")
		waitDatabaseQueue(t, m, index+1)
	}
	if _, err := m.Acquire(ctx, "noisy", Database); !errors.Is(err, domain.ErrRateLimit) {
		t.Fatal("per-App waiting bound was not enforced", err)
	}
	peer, err := m.Acquire(ctx, "peer", Database)
	if err != nil {
		t.Fatal("a full App blocked an eligible peer", err)
	}
	admin, err := m.Acquire(WithManagement(ctx), "noisy", Database)
	if err != nil {
		t.Fatal("management joined or lost capacity to a business queue", err)
	}
	cancel()
	for _, waiter := range pending {
		r := <-waiter
		if !errors.Is(r.err, context.Canceled) || r.release != nil {
			t.Fatal("cancelled waiter retained a permit", r.err)
		}
	}
	hold()
	peer()
	admin()
	if m.total[Database] != 0 || m.dbQueue.Len() != 0 || len(m.dbWaiting) != 0 {
		t.Fatal("waiting or active resources leaked")
	}
}

func TestDatabaseQueueFIFOAndInvalidation(t *testing.T) {
	m := singlePermitApps()
	ctx := context.Background()
	hold, _ := m.Acquire(ctx, "app", Database)
	first := queuedDatabaseAcquire(m, ctx, "app")
	waitDatabaseQueue(t, m, 1)
	second := queuedDatabaseAcquire(m, ctx, "app")
	waitDatabaseQueue(t, m, 2)
	hold()
	one := <-first
	if one.err != nil {
		t.Fatal(one.err)
	}
	select {
	case <-second:
		t.Fatal("second waiter bypassed the older request or App cap")
	default:
	}
	m.Invalidate("app")
	two := <-second
	if !errors.Is(two.err, domain.ErrRateLimit) {
		t.Fatal("old queued budget survived a policy invalidation", two.err)
	}
	one.release()
	if m.total[Database] != 0 || m.dbQueue.Len() != 0 {
		t.Fatal("FIFO or policy invalidation leaked a permit")
	}
}

func TestDatabaseQueueGlobalWaitingBound(t *testing.T) {
	m := singlePermitApps()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	holds := make([]func(), m.business[Database])
	for index := range holds {
		var err error
		holds[index], err = m.Acquire(ctx, string(rune('a'+index)), Database)
		if err != nil {
			t.Fatal(err)
		}
	}
	pending := make([]<-chan databaseAdmission, 64)
	for index := range pending {
		pending[index] = queuedDatabaseAcquire(m, ctx, string(rune('a'+index/4)))
		waitDatabaseQueue(t, m, index+1)
	}
	if _, err := m.Acquire(ctx, "another-app", Database); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("global waiting bound was not enforced", err)
	}
	admin, err := m.Acquire(WithManagement(ctx), "", Database)
	if err != nil {
		t.Fatal("full waiting queue consumed management capacity", err)
	}
	admin()
	cancel()
	for _, waiter := range pending {
		out := <-waiter
		if !errors.Is(out.err, context.Canceled) || out.release != nil {
			t.Fatal("cancelled global queue retained a permit", out.err)
		}
	}
	for _, release := range holds {
		release()
	}
	if m.total[Database] != 0 || m.dbQueue.Len() != 0 {
		t.Fatal("global queue leaked resources")
	}
}

func TestDatabaseQueueAbsorbsBoundedBurstWithoutBorrowingControlCapacity(t *testing.T) {
	m := New(1024, 4, 64, func(context.Context, string) (domain.ResourceLimits, error) {
		limits := domain.DefaultResourceLimits()
		limits.DBConcurrency = 56
		return limits, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	holds := make([]func(), 56)
	for index := range holds {
		var err error
		holds[index], err = m.Acquire(ctx, "noisy", Database)
		if err != nil {
			t.Fatal(err)
		}
	}
	// A bounded HTTP worker burst can reach one temporarily stalled replica.
	// Waiting must not create connections or consume the control-plane reserve.
	pending := make([]<-chan databaseAdmission, 200)
	for index := range pending {
		pending[index] = queuedDatabaseAcquire(m, ctx, "noisy")
	}
	waitDatabaseQueue(t, m, len(pending))
	m.mu.Lock()
	active := m.total[Database]
	m.mu.Unlock()
	if active != len(holds) {
		t.Fatal("waiting burst acquired active database permits", active)
	}
	admin, err := m.Acquire(WithManagement(ctx), "noisy", Database)
	if err != nil {
		t.Fatal("waiting burst consumed the management reserve", err)
	}
	admin()
	cancel()
	for _, waiter := range pending {
		out := <-waiter
		if !errors.Is(out.err, context.Canceled) || out.release != nil {
			t.Fatal("cancelled burst retained a permit", out.err)
		}
	}
	for _, release := range holds {
		release()
	}
	if m.total[Database] != 0 || m.dbQueue.Len() != 0 {
		t.Fatal("bounded burst leaked resources")
	}
}

func TestDatabaseQueueTimeoutAndGrantCancellationRace(t *testing.T) {
	m := singlePermitApps()
	hold, _ := m.Acquire(context.Background(), "app", Database)
	pending := queuedDatabaseAcquire(m, context.Background(), "app")
	select {
	case out := <-pending:
		if !errors.Is(out.err, domain.ErrRateLimit) || out.release != nil {
			t.Fatal("bounded wait did not expire", out.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("database queue waited without a bound")
	}
	hold()
	for n := 0; n < 100; n++ {
		ctx, cancel := context.WithCancel(context.Background())
		hold, _ := m.Acquire(context.Background(), "app", Database)
		pending := queuedDatabaseAcquire(m, ctx, "app")
		waitDatabaseQueue(t, m, 1)
		var group sync.WaitGroup
		group.Add(2)
		go func() { defer group.Done(); cancel() }()
		go func() { defer group.Done(); hold() }()
		out := <-pending
		group.Wait()
		if out.err != nil && !errors.Is(out.err, context.Canceled) {
			t.Fatal(out.err)
		}
		if out.release != nil {
			out.release()
			out.release()
		}
		if m.total[Database] != 0 || m.dbQueue.Len() != 0 || len(m.dbWaiting) != 0 {
			t.Fatal("grant/cancellation race leaked a permit")
		}
	}
}
