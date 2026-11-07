package resource

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/domain"
)

func TestMetadataCannotBorrowManagementReserve(t *testing.T) {
	m := New(32, 4, 8, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	metadata, err := m.AcquireMetadata(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var releases []func()
	for n := 0; n < 6; n++ {
		r, err := m.Acquire(ctx, "app", Database)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, r)
	}
	pending := make(chan error, 1)
	go func() { _, err := m.AcquireMetadata(ctx); pending <- err }()
	waitDatabaseQueue(t, m, 1)
	admin, err := m.Acquire(WithManagement(ctx), "", Database)
	if err != nil {
		t.Fatal("routing metadata consumed the management reserve", err)
	}
	select {
	case <-pending:
		t.Fatal("metadata bypassed its own or the business bound")
	default:
	}
	cancel()
	if err := <-pending; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	metadata()
	admin()
	for _, release := range releases {
		release()
	}
	if m.total[Database] != 0 || m.appTotal[Database] != 0 || m.dbQueue.Len() != 0 {
		t.Fatal("metadata budget leaked")
	}
}

func TestMetadataHasIndependentBoundAndDeadline(t *testing.T) {
	m := New(32, 4, 8, func(context.Context, string) (domain.ResourceLimits, error) {
		t.Error("metadata recursively loaded an App policy")
		return domain.ResourceLimits{}, domain.ErrUnavailable
	})
	hold, err := m.AcquireMetadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer hold()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := m.AcquireMetadata(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("metadata wait ignored its caller deadline", err)
	}
}
