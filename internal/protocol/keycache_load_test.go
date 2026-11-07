package protocol

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
)

type keyProbeTx struct {
	ports.AppTx
	calls   atomic.Int64
	entered chan struct{}
	release chan struct{}
	key     domain.SigningKey
	failure error
}

func (s *keyProbeTx) VerificationKeys() ([]domain.SigningKey, error) {
	s.calls.Add(1)
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-s.release
	return []domain.SigningKey{s.key}, s.failure
}

func TestVerificationKeyLoadCoalescesAndSeparatesEpoch(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tx := &keyProbeTx{entered: make(chan struct{}, 1), release: make(chan struct{}), key: domain.SigningKey{ID: "key", Private: x509.MarshalPKCS1PrivateKey(key), Current: true}}
	provider := &Provider{}
	app := domain.App{ID: "test-app"}
	var group sync.WaitGroup
	for n := 0; n < 32; n++ {
		group.Add(1)
		go func() {
			defer group.Done()
			ctx := WithTransaction(context.Background(), tx, app, "", "")
			got, err := provider.verificationKey(ctx, "key", 1)
			if err != nil || got.N.Cmp(key.N) != 0 {
				t.Error("key flight returned wrong authority", err)
			}
		}()
	}
	<-tx.entered
	time.Sleep(20 * time.Millisecond) // Exercise waiters before publishing the key.
	close(tx.release)
	group.Wait()
	if tx.calls.Load() != 1 {
		t.Fatal("key cache stampede", tx.calls.Load())
	}
	ctx := WithTransaction(context.Background(), tx, app, "", "")
	if _, err := provider.verificationKey(ctx, "key", 2); err != nil || tx.calls.Load() != 2 {
		t.Fatal("new epoch reused an old flight/key", err)
	}
	if len(provider.cache.loads) != 0 {
		t.Fatal("key flight leaked")
	}
}

func TestFailedVerificationKeyLoadMarksEveryRequestDependency(t *testing.T) {
	tx := &keyProbeTx{entered: make(chan struct{}, 1), release: make(chan struct{}), failure: domain.ErrUnavailable}
	close(tx.release)
	provider := &Provider{}
	var group sync.WaitGroup
	for n := 0; n < 32; n++ {
		group.Add(1)
		go func() {
			defer group.Done()
			ctx := WithTransaction(context.Background(), tx, domain.App{ID: "test-app"}, "", "")
			_, err := provider.verificationKey(ctx, "key", 1)
			if !errors.Is(err, domain.ErrUnavailable) || !errors.Is(TransactionFailure(ctx), domain.ErrUnavailable) {
				t.Error("key dependency failure became a false inactive result", err)
			}
		}()
	}
	group.Wait()
}

func TestUnknownKidsShareCompleteSetAndAppEpochBoundary(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tx := &keyProbeTx{entered: make(chan struct{}, 1), release: make(chan struct{}), key: domain.SigningKey{ID: "key", Private: x509.MarshalPKCS1PrivateKey(key), Current: true}}
	close(tx.release)
	p := &Provider{}
	for _, app := range []string{"app-one", "app-two"} {
		ctx := WithTransaction(context.Background(), tx, domain.App{ID: app}, "", "")
		for n := 0; n < 100; n++ {
			if _, err = p.verificationKey(ctx, fmt.Sprintf("unknown-%d", n), 1); !errors.Is(err, domain.ErrUnauthenticated) {
				t.Fatal(err)
			}
		}
		if _, err = p.verificationKey(ctx, "key", 1); err != nil {
			t.Fatal(err)
		}
	}
	if tx.calls.Load() != 2 {
		t.Fatalf("random kids drove loads: %d", tx.calls.Load())
	}
	ctx := WithTransaction(context.Background(), tx, domain.App{ID: "app-one"}, "", "")
	if _, err = p.verificationKey(ctx, "key", 2); err != nil || tx.calls.Load() != 3 {
		t.Fatal("new authority generation did not reload", err)
	}
}
