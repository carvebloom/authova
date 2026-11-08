package delivery

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

type diagnosticQueue struct {
	*recordedQueue
	failure  ports.DeliveryFailure
	ackError error
}

func (q *diagnosticQueue) FinishMessageWithFailure(_ context.Context, _ string, _ int, _ int, _ int64, f ports.DeliveryFailure) error {
	q.failure = f
	return q.ackError
}
func TestDeliveryFactsAndAckFailure(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(old)
	for _, tc := range []struct {
		sender Sender
		state  string
	}{{nil, "not_sent"}, {senderFunc(func(context.Context, domain.Message) error { return errors.New("CANARY-SMTP") }), "unknown"}, {senderFunc(func(context.Context, domain.Message) error { return nil }), "accepted"}} {
		q := &diagnosticQueue{recordedQueue: &recordedQueue{}, ackError: errors.New("CANARY-ACK")}
		w := Worker{Queue: q, Email: tc.sender}
		w.processMessage(context.Background(), domain.Message{ID: "message-123", AppID: "app", Kind: "email", Attempts: 1, Created: time.Now().Unix(), Expires: time.Now().Add(time.Minute).Unix()})
		if q.failure.DeliveryState != tc.state {
			t.Fatal(q.failure)
		}
	}
	if strings.Contains(logs.String(), "CANARY") || !strings.Contains(logs.String(), "message-123") || !strings.Contains(logs.String(), "ack") || !strings.Contains(logs.String(), "secondary_type") || !strings.Contains(logs.String(), `"delivery_state":"accepted"`) || !strings.Contains(logs.String(), `"delivery_state":"not_sent"`) || !strings.Contains(logs.String(), `"delivery_state":"unknown"`) || !strings.Contains(logs.String(), `"ack_unknown":true`) {
		t.Fatal("missing/unsafe primary+ack diagnostics")
	}
}
func TestWorkerPanicFailsSupervision(t *testing.T) {
	q := &recordedQueue{pending: []domain.Message{{ID: "panic-message", AppID: "app", Kind: "email", Attempts: 1, Created: time.Now().Unix(), Expires: time.Now().Add(time.Minute).Unix()}}}
	w := Worker{Queue: q, Email: senderFunc(func(context.Context, domain.Message) error { panic("CANARY") })}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if w.Run(ctx) == nil {
		t.Fatal("worker panic silently succeeded")
	}
}

// A real SMTP peer accepts DATA, then drops QUIT. This is not a definite
// pre-send failure and the legacy at-least-once retry policy must remain.
func TestSMTPAcceptedThenQuitFailureIsNotNotSent(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, e := listener.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		r := bufio.NewReader(c)
		fmt.Fprint(c, "220 fixture\r\n")
		for {
			line, e := r.ReadString('\n')
			if e != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"), strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
				fmt.Fprint(c, "250 ok\r\n")
			case strings.HasPrefix(line, "DATA"):
				fmt.Fprint(c, "354 send\r\n")
				for {
					line, e = r.ReadString('\n')
					if e != nil {
						return
					}
					if line == ".\r\n" {
						break
					}
				}
				fmt.Fprint(c, "250 accepted\r\n")
			case strings.HasPrefix(line, "QUIT"):
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	failure := (SMTP{Address: listener.Addr().String(), From: "identity@example.test", Development: true}).Send(ctx, domain.Message{ID: "smtp-123", Kind: "email", Destination: "user@example.test", Body: "CANARY-OTP"})
	<-done
	var sent *sendError
	if !errors.As(failure, &sent) || sent.state != "accepted" {
		t.Fatal("lost provider accepted fact")
	}
	q := &diagnosticQueue{recordedQueue: &recordedQueue{}}
	result := ""
	w := Worker{Queue: q, Email: senderFunc(func(context.Context, domain.Message) error { return failure }), Observe: func(_ string, v string, _ time.Duration) { result = v }}
	w.processMessage(context.Background(), domain.Message{ID: "smtp-123", AppID: "app", Kind: "email", Attempts: 1, Created: time.Now().Unix(), Expires: time.Now().Add(time.Minute).Unix()})
	if q.failure.DeliveryState != "accepted" || q.failure.OutcomeUnknown || result != "retry" {
		t.Fatal("accepted fact or at-least-once compatibility changed", q.failure, result)
	}
}
