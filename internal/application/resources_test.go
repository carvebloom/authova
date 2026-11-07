package application

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/domain"
)

func TestKDFAdmissionAndCancellation(t *testing.T) {
	service := &Service{kdf: make(chan struct{}, 1), kdfQueue: make(chan struct{}, 4)}
	service.kdf <- struct{}{} // Hold the only expensive work permit.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wait sync.WaitGroup
	for i := 0; i < 4; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := service.withKDF(ctx, func() error { t.Error("cancelled work executed"); return nil }); !errors.Is(err, domain.ErrUnavailable) {
				t.Error("wrong cancellation error", err)
			}
		}()
	}
	deadline := time.Now().Add(time.Second)
	for len(service.kdfQueue) != 4 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(service.kdfQueue) != 4 {
		t.Fatal("bounded queue did not fill")
	}
	if err := service.withKDF(context.Background(), func() error { t.Error("overloaded work executed"); return nil }); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("overload did not fail promptly")
	}
	cancel()
	wait.Wait()
	<-service.kdf
	if len(service.kdfQueue) != 0 {
		t.Fatal("cancelled waiter retained admission capacity")
	}
	if err := service.withKDF(ctx, func() error { t.Error("cancelled work executed"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("context cause lost")
	}
}
