package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
)

func TestSMSAdapterBoundary(t *testing.T) {
	seen := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-only" || r.Header.Get("Idempotency-Key") != "message-test" {
			t.Error("missing provider authentication/idempotency")
		}
		var data map[string]string
		if json.NewDecoder(r.Body).Decode(&data) != nil || data["app_id"] != "app-test" || data["destination"] != "+15550000001" {
			t.Error("incorrect adapter payload")
		}
		seen = true
		w.WriteHeader(202)
	}))
	defer server.Close()
	sender := SMS{URL: server.URL, Token: "test-only", Client: server.Client()}
	if e := sender.Send(context.Background(), domain.Message{ID: "message-test", AppID: "app-test", Kind: "phone", Destination: "+15550000001", Body: "Test verification message"}); e != nil {
		t.Fatal(e)
	}
	if !seen {
		t.Fatal("provider not called")
	}
}

type recordedQueue struct {
	mu       sync.Mutex
	outcomes map[string]int
	pending  []domain.Message
	ack      chan string
}

func (q *recordedQueue) LeaseMessages(_ context.Context, budget ports.DeliveryLeaseRequest) ([]domain.Message, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	counts := map[string]int{}
	var out, remaining []domain.Message
	for _, m := range q.pending {
		if len(out) < budget.Limit && counts[m.AppID]+budget.InFlight[m.AppID] < budget.AppLimit {
			counts[m.AppID]++
			out = append(out, m)
		} else {
			remaining = append(remaining, m)
		}
	}
	q.pending = remaining
	return out, nil
}
func (q *recordedQueue) FinishMessage(_ context.Context, id string, _ int, outcome int, _ int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.outcomes[id] = outcome
	if q.ack != nil {
		q.ack <- id
	}
	return nil
}

type senderFunc func(context.Context, domain.Message) error

func (f senderFunc) Send(ctx context.Context, m domain.Message) error { return f(ctx, m) }

func TestWorkerExpiration(t *testing.T) {
	q := &recordedQueue{outcomes: map[string]int{}}
	var active, peak, calls atomic.Int32
	w := Worker{Queue: q, Email: senderFunc(func(ctx context.Context, _ domain.Message) error {
		calls.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		select {
		case <-time.After(20 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})}
	now := time.Now().Unix()
	items := []domain.Message{{ID: "expired", Created: now - 100, Expires: now - 1}, {ID: "legacy-expired", Created: now - 1000}, {ID: "exhausted", Created: now, Attempts: 9}}
	for i := 0; i < 12; i++ {
		items = append(items, domain.Message{ID: fmt.Sprint(i), Created: now, Expires: now + 60})
	}
	for _, m := range items {
		w.processMessage(context.Background(), m)
	}
	if calls.Load() != 12 || peak.Load() > 4 || peak.Load() < 1 {
		t.Fatalf("calls=%d concurrency=%d", calls.Load(), peak.Load())
	}
	for _, id := range []string{"expired", "legacy-expired", "exhausted"} {
		if q.outcomes[id] != 2 {
			t.Errorf("%s not permanently expired", id)
		}
	}
	for i := 0; i < 12; i++ {
		if q.outcomes[fmt.Sprint(i)] != 1 {
			t.Errorf("delivery %d not complete", i)
		}
	}
}

func TestWorkerCancellationDrainsInFlight(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	q := &recordedQueue{outcomes: map[string]int{}}
	started := make(chan struct{}, 4)
	w := Worker{Queue: q, Email: senderFunc(func(ctx context.Context, _ domain.Message) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})}
	items := make([]domain.Message, 20)
	for i := range items {
		items[i] = domain.Message{ID: fmt.Sprint(i), AppID: fmt.Sprint(i % 2), Attempts: 1, Created: time.Now().Unix()}
	}
	done := make(chan struct{})
	q.pending = items
	go func() { _ = w.Run(ctx); close(done) }()
	for range 4 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("workers did not start")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not drain cancellation")
	}
}

func TestWorkerSlowAppDoesNotBlockPeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := &recordedQueue{outcomes: map[string]int{}, ack: make(chan string, 32)}
	now := time.Now().Unix()
	for i := 0; i < 12; i++ {
		q.pending = append(q.pending, domain.Message{ID: fmt.Sprint(i), AppID: "slow", Attempts: 1, Created: now})
	}
	started := make(chan string, 16)
	release := make(chan struct{})
	var active, peak atomic.Int32
	w := Worker{Queue: q, Email: senderFunc(func(ctx context.Context, m domain.Message) error {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		started <- m.AppID
		if m.AppID == "slow" {
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})}
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	for range 2 {
		select {
		case app := <-started:
			if app != "slow" {
				t.Fatal(app)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("slow App did not start")
		}
	}
	q.mu.Lock()
	q.pending = append(q.pending, domain.Message{ID: "quiet", AppID: "quiet", Attempts: 1, Created: now})
	q.mu.Unlock()
	select {
	case id := <-q.ack:
		if id != "quiet" {
			t.Fatal("slow App exceeded its budget", id)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("quiet App blocked by slow deliveries")
	}
	if peak.Load() > 4 {
		t.Fatal("global concurrency exceeded")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker failed to drain")
	}
}

func TestWorkerRejectsAdapterBudgetViolation(t *testing.T) {
	budget := ports.DeliveryLeaseRequest{Limit: 2, AppLimit: 2, InFlight: map[string]int{"a": 1}}
	for _, items := range [][]domain.Message{
		{{ID: "1", AppID: "a", Attempts: 1}, {ID: "2", AppID: "a", Attempts: 1}},
		{{ID: "1", AppID: "b", Attempts: 1}, {ID: "1", AppID: "b", Attempts: 1}},
		{{ID: "1", Attempts: 1}}, {{ID: "1", AppID: "b"}},
	} {
		if validLeasedMessages(items, budget) {
			t.Fatal("accepted invalid adapter result")
		}
	}
}

func TestSMTPCancelClosesStalledGreeting(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, e := listener.Accept()
		if e == nil {
			accepted <- conn
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- (SMTP{Address: listener.Addr().String(), From: "identity@example.com", Development: true}).Send(ctx, domain.Message{Kind: "email", Destination: "user@example.com"})
	}()
	select {
	case conn := <-accepted:
		defer conn.Close()
	case <-time.After(time.Second):
		t.Fatal("no SMTP connection")
	}
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatal("cancelled SMTP lost cancellation cause")
		}
	case <-time.After(time.Second):
		t.Fatal("SMTP cancellation ignored")
	}
}
